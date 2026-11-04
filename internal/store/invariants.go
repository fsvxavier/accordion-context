package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// queryer is satisfied by both *sql.DB and *sql.Tx.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// invariantQueries are the SQL checks from spec §17.1. Each must return zero rows.
// They are run inside every compress transaction (checkInvariants) and can be
// run on demand via Store.CheckInvariants.
var invariantQueries = []struct {
	id    string
	query string
}{
	{"I1", `
		SELECT bm.seq FROM block_messages bm
		JOIN blocks b ON b.session_id = bm.session_id AND b.block_id = bm.block_id
		WHERE bm.session_id = ?1 AND b.active = 1
		GROUP BY bm.seq HAVING COUNT(*) > 1`},
	{"I2", `
		SELECT a.block_id FROM blocks a JOIN blocks b
		  ON a.session_id = b.session_id AND a.block_id < b.block_id
		WHERE a.session_id = ?1 AND a.active = 1 AND b.active = 1
		  AND a.start_seq <= b.end_seq AND b.start_seq <= a.end_seq`},
	{"I3", `
		SELECT m1.tool_call_id FROM messages m1
		JOIN messages m2 ON m1.session_id = m2.session_id
		  AND m1.tool_call_id = m2.tool_call_id AND m1.seq < m2.seq
		JOIN blocks b ON b.session_id = m1.session_id AND b.active = 1
		WHERE m1.session_id = ?1
		  AND ((m1.seq BETWEEN b.start_seq AND b.end_seq)
		       <> (m2.seq BETWEEN b.start_seq AND b.end_seq))`},
	// I6: a T2/T3 block must consume at least one source, and every source
	// must be inactive with parent_block pointing back at it.
	{"I6", `
		SELECT b.block_id FROM blocks b
		WHERE b.session_id = ?1 AND b.tier > 1
		  AND NOT EXISTS (SELECT 1 FROM block_sources s
		                  WHERE s.session_id = b.session_id AND s.block_id = b.block_id)
		UNION ALL
		SELECT s.source_id FROM block_sources s
		JOIN blocks src ON src.session_id = s.session_id AND src.block_id = s.source_id
		WHERE s.session_id = ?1
		  AND (src.active = 1 OR src.parent_block IS NOT s.block_id)`},
	// I7: every message stays recoverable. A message covered by a block is
	// recoverable if an active block covers it, or an inactive T1 block that
	// covers it has a parent (decompress walks up from there).
	{"I7", `
		SELECT m.seq FROM messages m
		WHERE m.session_id = ?1
		  AND EXISTS (SELECT 1 FROM blocks b WHERE b.session_id = m.session_id
		              AND m.seq BETWEEN b.start_seq AND b.end_seq)
		  AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.session_id = m.session_id
		              AND b.active = 1 AND m.seq BETWEEN b.start_seq AND b.end_seq)
		  AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.session_id = m.session_id
		              AND b.active = 0 AND b.parent_block IS NOT NULL
		              AND m.seq BETWEEN b.start_seq AND b.end_seq)`},
}

// CheckInvariants runs I1, I2, I3, I6, I7 for a session against the committed state.
func (s *Store) CheckInvariants(ctx context.Context, sid string) error {
	return checkInvariants(ctx, s.db, sid)
}

func checkInvariants(ctx context.Context, q queryer, sid string) error {
	var errs []error
	for _, inv := range invariantQueries {
		rows, err := q.QueryContext(ctx, inv.query, sid)
		if err != nil {
			return fmt.Errorf("invariant %s query: %w", inv.id, err)
		}
		n := 0
		for rows.Next() {
			n++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fmt.Errorf("invariant %s scan: %w", inv.id, err)
		}
		if n > 0 {
			errs = append(errs, fmt.Errorf("invariant %s violated by %d row(s)", inv.id, n))
		}
	}
	return errors.Join(errs...)
}
