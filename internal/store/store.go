// Package store persists the accordion ledger in SQLite (spec §5, §10).
// It knows nothing about trigger policy or validation rules: those live in
// kernel. Its job is to apply a plan atomically and keep the schema invariants
// (see invariants.go) true inside every write transaction.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

//go:embed schema.sql
var schemaSQL string

// schemaVersion is the newest schema. schema.sql is the v1 base and stays frozen;
// each later version is a step in migrate (see migrate.go).
const schemaVersion = 3

// Store is a single-writer handle over one SQLite database.
type Store struct {
	db  *sql.DB
	enc *zstd.Encoder
	dec *zstd.Decoder
}

// Session describes a row of sessions (spec §5.2).
type Session struct {
	ID           string
	ProjectID    string
	Model        string
	ContextLimit int
	Mode         string // proxy | plugin | library
	Integration  string
}

// NewMessage is a ledger entry before it receives seq and ref.
type NewMessage struct {
	Role       kernel.Role
	Kind       kernel.Kind
	ToolName   string
	ToolCallID string
	Text       string
	Tokens     int
}

// NewBlock is one block produced by a compress call.
type NewBlock struct {
	ID        string
	Tier      int
	Topic     string
	Summary   string
	StartSeq  int64
	EndSeq    int64
	SrcTokens int
	SumTokens int
	Sources   []string // consumed blocks (T2/T3 only)
}

// CompressPlan is the already-validated output of kernel validation (spec §10.1).
type CompressPlan struct {
	NewBlocks    []NewBlock
	Deactivate   []string          // active blocks consumed by NewBlocks
	ParentOf     map[string]string // consumed block -> new parent block
	CloseIntents []int64           // open intents completed by NewBlocks (§11.3)
	TokensAfter  int               // low-water mark after the fold
	Now          int64
}

// CacheCall is one row of cache_ledger (spec §9.4).
type CacheCall struct {
	InputTokens   int
	CachedTokens  int
	EstTokens     int
	AfterCompress bool
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Open opens (or creates) the database at path, applies pragmas and migrates.
// Use "file::memory:?mode=memory&cache=shared" style DSNs for tests.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}

	enc, err := zstd.NewWriter(nil)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("zstd encoder: %w", err)
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		enc.Close()
		db.Close()
		return nil, fmt.Errorf("zstd decoder: %w", err)
	}

	s := &Store{db: db, enc: enc, dec: dec}
	if err := s.migrate(context.Background()); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database and codecs.
func (s *Store) Close() error {
	s.enc.Close()
	s.dec.Close()
	return s.db.Close()
}

// migrate brings the database to schemaVersion, tracked by PRAGMA user_version.
// Fresh databases run every step from 0; older ones run only the missing steps.
// All steps share one transaction, so a failed upgrade leaves the old version.
func (s *Store) migrate(ctx context.Context) error {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if v > schemaVersion {
		return fmt.Errorf("database schema v%d is newer than supported v%d", v, schemaVersion)
	}
	if v == schemaVersion {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if v < 1 {
		if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
	}
	if v < 2 {
		if err := s.migrateV2(ctx, tx); err != nil {
			return fmt.Errorf("migrate to v2: %w", err)
		}
	}
	if v < 3 {
		if err := s.migrateV3(ctx, tx); err != nil {
			return fmt.Errorf("migrate to v3: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}
	return tx.Commit()
}

// EnsureProject inserts the project or refreshes its root and last_seen_at.
func (s *Store) EnsureProject(ctx context.Context, id, root, storage string, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO projects(id, root_path, storage, created_at, last_seen_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			root_path = excluded.root_path,
			last_seen_at = excluded.last_seen_at`,
		id, root, storage, now, now)
	if err != nil {
		return fmt.Errorf("ensure project %s: %w", id, err)
	}
	return nil
}

// CreateSession inserts a session and its zeroed trigger_state row in one tx.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions(id, project_id, created_at, model, context_limit, mode, integration)
		VALUES (?,?,?,?,?,?,?)`,
		sess.ID, sess.ProjectID, now, sess.Model, sess.ContextLimit, sess.Mode, sess.Integration); err != nil {
		return fmt.Errorf("insert session %s: %w", sess.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO trigger_state(session_id, low_water_tokens, nudge_pending, nudge_shown_at)
		VALUES (?,0,0,0)`, sess.ID); err != nil {
		return fmt.Errorf("insert trigger_state %s: %w", sess.ID, err)
	}
	return tx.Commit()
}

// AppendMessage stores the content (deduplicated by sha256) and appends the
// message at the next seq. Returns the ledger view with seq and ref filled.
func (s *Store) AppendMessage(ctx context.Context, sid string, m NewMessage) (kernel.Message, error) {
	return s.appendMessage(ctx, sid, m, false)
}

// AppendMessageWithIntent is AppendMessage that also opens an intent (spec §11.3)
// when m is a user text message. The message and its intent commit together, so
// a crash cannot leave a user turn without its intent.
func (s *Store) AppendMessageWithIntent(ctx context.Context, sid string, m NewMessage) (kernel.Message, error) {
	return s.appendMessage(ctx, sid, m, true)
}

func (s *Store) appendMessage(ctx context.Context, sid string, m NewMessage, withIntent bool) (kernel.Message, error) {
	sum := sha256.Sum256([]byte(m.Text))
	hash := hex.EncodeToString(sum[:])
	data := s.enc.EncodeAll([]byte(m.Text), nil)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return kernel.Message{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO contents(hash, size_bytes, data) VALUES (?,?,?)`,
		hash, len(m.Text), data); err != nil {
		return kernel.Message{}, fmt.Errorf("insert content: %w", err)
	}

	var seq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE session_id = ?`, sid).Scan(&seq); err != nil {
		return kernel.Message{}, fmt.Errorf("next seq: %w", err)
	}
	ref := kernel.FormatRef(seq)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages(session_id, seq, ref, role, kind, tool_name, tool_call_id,
		                     content_hash, tokens, ccr_stored, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,0,?)`,
		sid, seq, ref, string(m.Role), string(m.Kind),
		nullable(m.ToolName), nullable(m.ToolCallID),
		hash, m.Tokens, time.Now().Unix()); err != nil {
		return kernel.Message{}, fmt.Errorf("insert message %s: %w", ref, err)
	}
	if err := indexTx(ctx, tx, sid, indexKindMessage, ref, m.Text); err != nil {
		return kernel.Message{}, err
	}
	if withIntent && m.Role == kernel.RoleUser && m.Kind == kernel.KindText {
		if _, err := addIntentTx(ctx, tx, sid, seq, m.Text); err != nil {
			return kernel.Message{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return kernel.Message{}, err
	}
	return kernel.Message{
		Seq: seq, Ref: ref, Role: m.Role, Kind: m.Kind,
		ToolName: m.ToolName, ToolCallID: m.ToolCallID,
		Text: m.Text, Tokens: m.Tokens,
	}, nil
}

// Snapshot loads the full ledger, blocks, intents and trigger state of a session.
func (s *Store) Snapshot(ctx context.Context, sid string) (kernel.Snapshot, error) {
	snap := kernel.Snapshot{SessionID: sid}
	if err := s.db.QueryRowContext(ctx,
		`SELECT context_limit FROM sessions WHERE id = ?`, sid).Scan(&snap.ContextLimit); err != nil {
		return snap, fmt.Errorf("session %s: %w", sid, err)
	}

	if err := s.loadMessages(ctx, sid, &snap); err != nil {
		return snap, err
	}
	if err := s.loadBlocks(ctx, sid, &snap); err != nil {
		return snap, err
	}
	if err := s.loadIntents(ctx, sid, &snap); err != nil {
		return snap, err
	}
	trig, err := s.GetTrigger(ctx, sid)
	if err != nil {
		return snap, err
	}
	snap.Trigger = trig
	return snap, nil
}

func (s *Store) loadMessages(ctx context.Context, sid string, snap *kernel.Snapshot) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.seq, m.ref, m.role, m.kind, m.tool_name, m.tool_call_id,
		       c.data, m.tokens, m.ccr_stored
		FROM messages m JOIN contents c ON c.hash = m.content_hash
		WHERE m.session_id = ? ORDER BY m.seq`, sid)
	if err != nil {
		return fmt.Errorf("load messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			m                  kernel.Message
			role, kind         string
			toolName, toolCall sql.NullString
			data               []byte
			ccr                int
		)
		if err := rows.Scan(&m.Seq, &m.Ref, &role, &kind, &toolName, &toolCall, &data, &m.Tokens, &ccr); err != nil {
			return fmt.Errorf("scan message: %w", err)
		}
		text, err := s.dec.DecodeAll(data, nil)
		if err != nil {
			return fmt.Errorf("decode content of %s: %w", m.Ref, err)
		}
		m.Role = kernel.Role(role)
		m.Kind = kernel.Kind(kind)
		m.ToolName = toolName.String
		m.ToolCallID = toolCall.String
		m.Text = string(text)
		m.CCRStored = ccr == 1
		snap.Messages = append(snap.Messages, m)
	}
	return rows.Err()
}

func (s *Store) loadBlocks(ctx context.Context, sid string, snap *kernel.Snapshot) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT block_id, tier, COALESCE(topic, ''), summary, start_seq, end_seq,
		       src_tokens, sum_tokens, active
		FROM blocks WHERE session_id = ? ORDER BY start_seq, block_id`, sid)
	if err != nil {
		return fmt.Errorf("load blocks: %w", err)
	}
	index := map[string]int{}
	for rows.Next() {
		var b kernel.Block
		var active int
		if err := rows.Scan(&b.ID, &b.Tier, &b.Topic, &b.Summary, &b.StartSeq, &b.EndSeq,
			&b.SrcTokens, &b.SumTokens, &active); err != nil {
			rows.Close()
			return fmt.Errorf("scan block: %w", err)
		}
		b.Active = active == 1
		index[b.ID] = len(snap.Blocks)
		snap.Blocks = append(snap.Blocks, b)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	src, err := s.db.QueryContext(ctx, `
		SELECT block_id, source_id FROM block_sources
		WHERE session_id = ? ORDER BY block_id, source_id`, sid)
	if err != nil {
		return fmt.Errorf("load block sources: %w", err)
	}
	defer src.Close()
	for src.Next() {
		var blockID, sourceID string
		if err := src.Scan(&blockID, &sourceID); err != nil {
			return fmt.Errorf("scan block source: %w", err)
		}
		if i, ok := index[blockID]; ok {
			snap.Blocks[i].Sources = append(snap.Blocks[i].Sources, sourceID)
		}
	}
	if err := src.Err(); err != nil {
		return err
	}
	// Sources come back in block_id text order ("b10" before "b9"). The kernel
	// reads a block's sources as a run in ledger order, so sort them by seq.
	for i := range snap.Blocks {
		srcs := snap.Blocks[i].Sources
		sort.SliceStable(srcs, func(a, b int) bool {
			return snap.Blocks[index[srcs[a]]].StartSeq < snap.Blocks[index[srcs[b]]].StartSeq
		})
	}
	return nil
}

func (s *Store) loadIntents(ctx context.Context, sid string, snap *kernel.Snapshot) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT intent_id, origin_seq, text, status FROM intents
		WHERE session_id = ? ORDER BY intent_id`, sid)
	if err != nil {
		return fmt.Errorf("load intents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var in kernel.Intent
		if err := rows.Scan(&in.ID, &in.OriginSeq, &in.Text, &in.Status); err != nil {
			return fmt.Errorf("scan intent: %w", err)
		}
		snap.Intents = append(snap.Intents, in)
	}
	return rows.Err()
}

// GetTrigger reads the single trigger_state row of a session.
func (s *Store) GetTrigger(ctx context.Context, sid string) (kernel.TriggerState, error) {
	var t kernel.TriggerState
	var pending int
	err := s.db.QueryRowContext(ctx, `
		SELECT low_water_tokens, nudge_pending, nudge_shown_at, last_shown_t2, last_shown_t3, refusals
		FROM trigger_state WHERE session_id = ?`, sid).Scan(
		&t.LowWater, &pending, &t.NudgeShownAt, &t.LastShownT2, &t.LastShownT3, &t.Refusals)
	if err != nil {
		return t, fmt.Errorf("trigger state %s: %w", sid, err)
	}
	t.NudgePending = pending == 1
	return t, nil
}

// SetTrigger overwrites the trigger_state row of a session.
func (s *Store) SetTrigger(ctx context.Context, sid string, t kernel.TriggerState) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE trigger_state
		SET low_water_tokens = ?, nudge_pending = ?, nudge_shown_at = ?,
		    last_shown_t2 = ?, last_shown_t3 = ?, refusals = ?
		WHERE session_id = ?`,
		t.LowWater, boolInt(t.NudgePending), t.NudgeShownAt,
		t.LastShownT2, t.LastShownT3, t.Refusals, sid)
	if err != nil {
		return fmt.Errorf("set trigger %s: %w", sid, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("set trigger %s: no row updated", sid)
	}
	return nil
}

// AddIntent appends an open intent originating at originSeq and returns its id.
func (s *Store) AddIntent(ctx context.Context, sid string, originSeq int64, text string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := addIntentTx(ctx, tx, sid, originSeq, text)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func addIntentTx(ctx context.Context, tx *sql.Tx, sid string, originSeq int64, text string) (int64, error) {
	var id int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(intent_id), 0) + 1 FROM intents WHERE session_id = ?`, sid).Scan(&id); err != nil {
		return 0, fmt.Errorf("next intent id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO intents(session_id, intent_id, origin_seq, text, status)
		VALUES (?,?,?,?,'open')`, sid, id, originSeq, text); err != nil {
		return 0, fmt.Errorf("insert intent: %w", err)
	}
	return id, nil
}

// CloseIntent moves an open intent to done or superseded.
func (s *Store) CloseIntent(ctx context.Context, sid string, id int64, status string, closedSeq int64) error {
	if status != "done" && status != "superseded" {
		return fmt.Errorf("close intent %d: invalid status %q", id, status)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE intents SET status = ?, closed_seq = ?
		WHERE session_id = ? AND intent_id = ? AND status = 'open'`,
		status, closedSeq, sid, id)
	if err != nil {
		return fmt.Errorf("close intent %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("close intent %d: not open", id)
	}
	return nil
}

// AppendEvent writes one row to the append-only events log.
func (s *Store) AppendEvent(ctx context.Context, sid, typ string, payload any) error {
	return appendEvent(ctx, s.db, sid, typ, payload)
}

// RecordCache appends one cache_ledger row (spec §9.4).
func (s *Store) RecordCache(ctx context.Context, sid string, c CacheCall) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cache_ledger(session_id, call_seq, at, input_tokens, cached_tokens,
		                         est_tokens, after_compress)
		SELECT ?, COALESCE(MAX(call_seq), 0) + 1, ?, ?, ?, ?, ?
		FROM cache_ledger WHERE session_id = ?`,
		sid, time.Now().Unix(), c.InputTokens, c.CachedTokens, c.EstTokens,
		boolInt(c.AfterCompress), sid)
	if err != nil {
		return fmt.Errorf("record cache call: %w", err)
	}
	return nil
}

// CacheRow is one stored cache_ledger row: the upstream sample of one call.
type CacheRow struct {
	CallSeq int64
	At      int64
	CacheCall
}

// ListCacheLedger returns the last limit rows of the session's cache ledger in
// call order, oldest first. A limit <= 0 returns every row. The proxy replays
// the rows to rebuild the token calibration after a restart (spec §15).
func (s *Store) ListCacheLedger(ctx context.Context, sid string, limit int) ([]CacheRow, error) {
	q := `SELECT call_seq, at, input_tokens, cached_tokens, est_tokens, after_compress
		FROM cache_ledger WHERE session_id = ? ORDER BY call_seq DESC`
	args := []any{sid}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list cache ledger: %w", err)
	}
	defer rows.Close()
	var out []CacheRow
	for rows.Next() {
		var r CacheRow
		var after int
		if err := rows.Scan(&r.CallSeq, &r.At, &r.InputTokens, &r.CachedTokens, &r.EstTokens, &after); err != nil {
			return nil, fmt.Errorf("list cache ledger: %w", err)
		}
		r.AfterCompress = after != 0
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list cache ledger: %w", err)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ApplyCompress writes a validated plan in one transaction (spec §10.3).
// Nothing is deleted: messages stay, only blocks.active changes. Invariants
// are checked before commit; a violation rolls everything back.
func (s *Store) ApplyCompress(ctx context.Context, sid string, p CompressPlan) error {
	if len(p.NewBlocks) == 0 {
		return errors.New("apply compress: empty plan")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, b := range p.NewBlocks {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO blocks(session_id, block_id, tier, topic, summary,
			                   start_seq, end_seq, src_tokens, sum_tokens, active, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,1,?)`,
			sid, b.ID, b.Tier, b.Topic, b.Summary,
			b.StartSeq, b.EndSeq, b.SrcTokens, b.SumTokens, p.Now); err != nil {
			return fmt.Errorf("insert block %s: %w", b.ID, err)
		}
		if b.Tier == 1 {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO block_messages(session_id, block_id, seq)
				SELECT session_id, ?, seq FROM messages
				WHERE session_id = ? AND seq BETWEEN ? AND ?`,
				b.ID, sid, b.StartSeq, b.EndSeq); err != nil {
				return fmt.Errorf("link messages of %s: %w", b.ID, err)
			}
		}
		for _, src := range b.Sources {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO block_sources(session_id, block_id, source_id) VALUES (?,?,?)`,
				sid, b.ID, src); err != nil {
				return fmt.Errorf("link source %s of %s: %w", src, b.ID, err)
			}
		}
		if err := indexTx(ctx, tx, sid, indexKindBlock, b.ID, blockBody(b.Topic, b.Summary)); err != nil {
			return err
		}
	}

	for _, id := range p.Deactivate {
		res, err := tx.ExecContext(ctx, `
			UPDATE blocks SET active = 0, parent_block = ?
			WHERE session_id = ? AND block_id = ? AND active = 1`,
			nullable(p.ParentOf[id]), sid, id)
		if err != nil {
			return fmt.Errorf("deactivate %s: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("block %s not active (concurrent change?)", id)
		}
	}

	var closedSeq int64
	for _, b := range p.NewBlocks {
		closedSeq = max(closedSeq, b.EndSeq)
	}
	for _, id := range p.CloseIntents {
		res, err := tx.ExecContext(ctx, `
			UPDATE intents SET status = 'done', closed_seq = ?
			WHERE session_id = ? AND intent_id = ? AND status = 'open'`, closedSeq, sid, id)
		if err != nil {
			return fmt.Errorf("close intent %d: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("close intent %d: not open", id)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE trigger_state
		SET low_water_tokens = ?, nudge_pending = 0, nudge_shown_at = 0, refusals = 0, nudge_forced = 0
		WHERE session_id = ?`, p.TokensAfter, sid); err != nil {
		return fmt.Errorf("reset trigger: %w", err)
	}

	blocks := make([]string, 0, len(p.NewBlocks))
	for _, b := range p.NewBlocks {
		blocks = append(blocks, b.ID)
	}
	if err := appendEvent(ctx, tx, sid, "compress", map[string]any{
		"blocks":        blocks,
		"deactivated":   p.Deactivate,
		"closedIntents": p.CloseIntents,
		"tokensAfter":   p.TokensAfter,
	}); err != nil {
		return err
	}

	if err := checkInvariants(ctx, tx, sid); err != nil {
		return fmt.Errorf("invariant violated, rolling back: %w", err)
	}
	return tx.Commit()
}

// ApplyDecompress writes a decompress plan in one transaction (spec §13.1,
// mode "context"). The target block becomes inactive with no parent, and each
// direct source becomes active again with parent_block cleared. Nothing is
// deleted. Every row update must match exactly one row, so a plan built from a
// stale snapshot fails instead of corrupting the block set. Invariants are
// checked before commit.
func (s *Store) ApplyDecompress(ctx context.Context, sid string, p kernel.DecompressPlan) error {
	if p.Target == "" {
		return errors.New("apply decompress: empty target")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE blocks SET active = 0, parent_block = NULL
		WHERE session_id = ? AND block_id = ? AND active = 1`, sid, p.Target)
	if err != nil {
		return fmt.Errorf("deactivate %s: %w", p.Target, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("decompress %s: block not active (concurrent change?)", p.Target)
	}
	for _, id := range p.Reactivate {
		res, err := tx.ExecContext(ctx, `
			UPDATE blocks SET active = 1, parent_block = NULL
			WHERE session_id = ? AND block_id = ? AND active = 0 AND parent_block = ?`,
			sid, id, p.Target)
		if err != nil {
			return fmt.Errorf("reactivate %s: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("decompress %s: source %s is not folded under it (concurrent change?)", p.Target, id)
		}
	}

	if err := appendEvent(ctx, tx, sid, "decompress", map[string]any{
		"requested":   p.Requested,
		"target":      p.Target,
		"reactivated": p.Reactivate,
		"redirected":  p.Redirected,
	}); err != nil {
		return err
	}
	if err := checkInvariants(ctx, tx, sid); err != nil {
		return fmt.Errorf("invariant violated, rolling back: %w", err)
	}
	return tx.Commit()
}

func appendEvent(ctx context.Context, ex execer, sid, typ string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event %s: %w", typ, err)
	}
	if _, err := ex.ExecContext(ctx, `
		INSERT INTO events(session_id, event_seq, at, type, payload)
		SELECT ?, COALESCE(MAX(event_seq), 0) + 1, ?, ?, ?
		FROM events WHERE session_id = ?`,
		sid, time.Now().Unix(), typ, string(raw), sid); err != nil {
		return fmt.Errorf("append event %s: %w", typ, err)
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
