package store

import (
	"context"
	"fmt"
)

// SessionWindow returns the model a session was created with and the context
// window stored for it. sql.ErrNoRows (wrapped) means no such session.
func (s *Store) SessionWindow(ctx context.Context, sid string) (model string, limit int, err error) {
	if err := s.db.QueryRowContext(ctx,
		`SELECT model, context_limit FROM sessions WHERE id = ?`, sid).Scan(&model, &limit); err != nil {
		return "", 0, fmt.Errorf("session %s window: %w", sid, err)
	}
	return model, limit, nil
}

// SetContextLimit replaces the stored context window of session sid. It writes
// only that column, so the ledger, blocks and trigger state are left as they are.
func (s *Store) SetContextLimit(ctx context.Context, sid string, limit int) error {
	if limit <= 0 {
		return fmt.Errorf("session %s: context limit must be positive, got %d", sid, limit)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE sessions SET context_limit = ? WHERE id = ?`, limit, sid)
	if err != nil {
		return fmt.Errorf("session %s: set context limit: %w", sid, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("session %s: set context limit: no such session", sid)
	}
	return nil
}
