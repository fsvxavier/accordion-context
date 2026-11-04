package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrateV2 runs inside migrate's transaction. It adds the forced-nudge flag
// and backfills search_idx, which before v2 held only block summaries. Messages
// are indexed from now on by appendMessage, and the backfill covers the rows
// written before. The backfill deletes the message and block rows first, so
// running it twice, or on a database whose blocks were already indexed, never
// duplicates a row.
func (s *Store) migrateV2(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE trigger_state ADD COLUMN nudge_forced INTEGER NOT NULL DEFAULT 0
		CHECK (nudge_forced IN (0,1))`); err != nil {
		return fmt.Errorf("add nudge_forced: %w", err)
	}
	if err := clearSearchKinds(ctx, tx, indexKindMessage, indexKindBlock); err != nil {
		return fmt.Errorf("clear search index: %w", err)
	}

	// Messages are read in pages keyed by (session, seq), and each page is closed
	// before its rows are written: the store uses one connection, so a write
	// cannot run while a read cursor is open on it.
	var (
		sid string
		seq int64 = -1
	)
	for {
		page, err := s.messagePage(ctx, tx, sid, seq)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}
		for _, m := range page {
			if err := indexTx(ctx, tx, m.sid, indexKindMessage, m.ref, m.text); err != nil {
				return err
			}
		}
		last := page[len(page)-1]
		sid = last.sid
		seq = last.seq
	}

	blocks, err := s.blockBodies(ctx, tx)
	if err != nil {
		return err
	}
	for _, b := range blocks {
		if err := indexTx(ctx, tx, b.sid, indexKindBlock, b.id, blockBody(b.topic, b.summary)); err != nil {
			return err
		}
	}
	return nil
}

// v2Message is one message row read during the backfill.
type v2Message struct {
	sid, ref, text string
	seq            int64
}

// messagePage returns up to 500 messages after (afterSID, afterSeq) in
// (session, seq) order, decoded.
func (s *Store) messagePage(ctx context.Context, tx *sql.Tx, afterSID string, afterSeq int64) ([]v2Message, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT m.session_id, m.seq, m.ref, c.data
		FROM messages m JOIN contents c ON c.hash = m.content_hash
		WHERE (m.session_id, m.seq) > (?, ?)
		ORDER BY m.session_id, m.seq
		LIMIT 500`, afterSID, afterSeq)
	if err != nil {
		return nil, fmt.Errorf("backfill: read messages: %w", err)
	}
	defer rows.Close()
	var out []v2Message
	for rows.Next() {
		var (
			m    v2Message
			data []byte
		)
		if err := rows.Scan(&m.sid, &m.seq, &m.ref, &data); err != nil {
			return nil, fmt.Errorf("backfill: scan message: %w", err)
		}
		text, err := s.dec.DecodeAll(data, nil)
		if err != nil {
			return nil, fmt.Errorf("backfill: decode %s: %w", m.ref, err)
		}
		m.text = string(text)
		out = append(out, m)
	}
	return out, rows.Err()
}

type v2Block struct{ sid, id, topic, summary string }

func (s *Store) blockBodies(ctx context.Context, tx *sql.Tx) ([]v2Block, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT session_id, block_id, COALESCE(topic, ''), summary FROM blocks`)
	if err != nil {
		return nil, fmt.Errorf("backfill: read blocks: %w", err)
	}
	defer rows.Close()
	var out []v2Block
	for rows.Next() {
		var b v2Block
		if err := rows.Scan(&b.sid, &b.id, &b.topic, &b.summary); err != nil {
			return nil, fmt.Errorf("backfill: scan block: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// migrateV3 runs inside migrate's transaction. It adds the store epoch, a
// single-row counter that every purge increments (purge.go). Processes that
// cache session state compare it to drop the cache after a purge. It also
// indexes messages by content hash, so purges can find unreferenced contents
// without a full scan of messages.
func (s *Store) migrateV3(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE store_epoch (
		   id    INTEGER PRIMARY KEY CHECK (id = 1),
		   epoch INTEGER NOT NULL
		 )`,
		`INSERT INTO store_epoch(id, epoch) VALUES (1, 0)`,
		`CREATE INDEX idx_messages_content ON messages(content_hash)`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("v3 %.40s: %w", q, err)
		}
	}
	return nil
}

// migrateV4 runs inside migrate's transaction, with foreign keys off (see
// migrate). The kind CHECK of messages gains 'opaque', the kind of a Responses
// input item the codec does not model. SQLite cannot alter a CHECK, so the table
// is rebuilt with the same columns, keys and indexes, and every row is copied.
// foreign_key_check then fails the migration if a reference was lost.
func (s *Store) migrateV4(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE messages_v4 (
		   session_id   TEXT NOT NULL REFERENCES sessions(id),
		   seq          INTEGER NOT NULL,
		   ref          TEXT NOT NULL,
		   role         TEXT NOT NULL CHECK (role IN ('system','user','assistant','tool')),
		   kind         TEXT NOT NULL CHECK (kind IN ('text','tool_call','tool_result','reasoning','opaque')),
		   tool_name    TEXT,
		   tool_call_id TEXT,
		   content_hash TEXT NOT NULL REFERENCES contents(hash),
		   tokens       INTEGER NOT NULL,
		   ccr_stored   INTEGER NOT NULL DEFAULT 0,
		   created_at   INTEGER NOT NULL,
		   PRIMARY KEY (session_id, seq),
		   UNIQUE (session_id, ref)
		 )`,
		`INSERT INTO messages_v4 (session_id, seq, ref, role, kind, tool_name, tool_call_id,
		   content_hash, tokens, ccr_stored, created_at)
		 SELECT session_id, seq, ref, role, kind, tool_name, tool_call_id,
		   content_hash, tokens, ccr_stored, created_at FROM messages`,
		`DROP TABLE messages`,
		`ALTER TABLE messages_v4 RENAME TO messages`,
		`CREATE INDEX idx_msgs_tool ON messages(session_id, tool_call_id)`,
		`CREATE INDEX idx_messages_content ON messages(content_hash)`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("v4 %.40s: %w", q, err)
		}
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("v4 foreign_key_check: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("v4 foreign_key_check: references broken")
	}
	return rows.Err()
}
