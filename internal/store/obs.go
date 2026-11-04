package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// EventRow is one row of the events log (spec §5.2). Payload is the stored JSON.
type EventRow struct {
	Seq     int64
	At      int64
	Type    string
	Payload json.RawMessage
}

// ListEvents returns the last limit events of a session in event order, oldest
// first. A limit <= 0 returns every event.
func (s *Store) ListEvents(ctx context.Context, sid string, limit int) ([]EventRow, error) {
	q := `SELECT event_seq, at, type, payload FROM events
		WHERE session_id = ? ORDER BY event_seq DESC`
	args := []any{sid}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var r EventRow
		var payload string
		if err := rows.Scan(&r.Seq, &r.At, &r.Type, &payload); err != nil {
			return nil, fmt.Errorf("list events: %w", err)
		}
		r.Payload = json.RawMessage(payload)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// SessionRow is the stored description of one session (spec §5.2).
type SessionRow struct {
	ID           string
	ProjectID    string
	Model        string
	ContextLimit int
	Mode         string
	Integration  string
}

// GetSession reads the row of session sid. sql.ErrNoRows (wrapped) means no such session.
func (s *Store) GetSession(ctx context.Context, sid string) (SessionRow, error) {
	var r SessionRow
	err := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, model, context_limit, mode, integration
		FROM sessions WHERE id = ?`, sid).Scan(
		&r.ID, &r.ProjectID, &r.Model, &r.ContextLimit, &r.Mode, &r.Integration)
	if err != nil {
		return r, fmt.Errorf("session %s: %w", sid, err)
	}
	return r, nil
}

// CacheTotals sums the cache ledger of a session (spec §9.4): the measured cache
// cost of the upstream calls.
type CacheTotals struct {
	Calls         int64
	InputTokens   int64
	CachedTokens  int64
	AfterCompress int64 // calls that were the first after a fold
}

// CacheTotals returns the sums of the session's cache_ledger rows.
func (s *Store) CacheTotals(ctx context.Context, sid string) (CacheTotals, error) {
	var t CacheTotals
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(cached_tokens), 0),
		       COALESCE(SUM(after_compress), 0)
		FROM cache_ledger WHERE session_id = ?`, sid).Scan(
		&t.Calls, &t.InputTokens, &t.CachedTokens, &t.AfterCompress)
	if err != nil {
		return t, fmt.Errorf("cache totals %s: %w", sid, err)
	}
	return t, nil
}
