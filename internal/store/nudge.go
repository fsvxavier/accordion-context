package store

import (
	"context"
	"fmt"
)

// RequestNudge marks a session for forced compaction (spec §19.4 compact). It
// raises nudge_pending and nudge_forced together and logs a "compact" event
// with payload, in one transaction. The next Plan of the session nudges, and
// the flag is cleared by that Plan.
func (s *Store) RequestNudge(ctx context.Context, sid string, payload any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE trigger_state SET nudge_pending = 1, nudge_forced = 1 WHERE session_id = ?`, sid)
	if err != nil {
		return fmt.Errorf("request nudge %s: %w", sid, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("request nudge %s: no trigger state", sid)
	}
	if err := appendEvent(ctx, tx, sid, "compact", payload); err != nil {
		return err
	}
	return tx.Commit()
}

// ForcedNudge reports whether a forced nudge is waiting for the next Plan.
func (s *Store) ForcedNudge(ctx context.Context, sid string) (bool, error) {
	var forced int
	if err := s.db.QueryRowContext(ctx,
		`SELECT nudge_forced FROM trigger_state WHERE session_id = ?`, sid).Scan(&forced); err != nil {
		return false, fmt.Errorf("forced nudge %s: %w", sid, err)
	}
	return forced == 1, nil
}

// ClearForcedNudge drops the forced flag of a session. NudgePending is left as
// it is: the nudge it caused is still unanswered until a compress.
func (s *Store) ClearForcedNudge(ctx context.Context, sid string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE trigger_state SET nudge_forced = 0 WHERE session_id = ?`, sid); err != nil {
		return fmt.Errorf("clear forced nudge %s: %w", sid, err)
	}
	return nil
}
