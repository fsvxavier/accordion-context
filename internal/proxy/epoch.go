package proxy

import (
	"context"

	"github.com/fsvxavier/accordion-context/internal/session"
)

// syncEpoch drops the in-memory state of the sessions when the store was
// purged, by this process or by another handle on the same database (store
// epoch, see internal/store/purge.go). Calibration, held spans and the
// project registrations are all derived from rows a purge may have removed.
// The next request for a purged session then creates it again with an empty
// ledger. Configuration is not database state, so it is kept.
//
// A failed read keeps the cache for now: the next request reads the epoch
// again.
func (s *Server) syncEpoch(ctx context.Context) {
	e, err := s.cfg.Store.Epoch(ctx)
	if err != nil {
		s.cfg.Logger.Warn("proxy: read store epoch", "err", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e == s.epoch {
		return
	}
	s.epoch = e
	s.sessions = map[string]*session.Session{}
	s.ensured = map[string]bool{}
	s.roots = map[string]string{}
	s.tunes = map[string]*tune{}
}
