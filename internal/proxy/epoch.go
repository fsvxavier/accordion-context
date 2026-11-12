package proxy

import (
	"context"

	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// syncEpoch drops the in-memory state of the sessions when a store was purged,
// by this process or by another handle on the same database (store epoch, see
// internal/store/purge.go). Every open store is checked: Config.Store and the
// local project databases (stores.go). Calibration, held spans and the project
// registrations are all derived from rows a purge may have removed. The next
// request for a purged session then creates it again with an empty ledger.
// Configuration is not database state, so it is kept. A change in any store
// drops the state of every session, which is simple and always safe.
//
// A failed read keeps the cache for now: the next request reads the epoch
// again.
func (s *Server) syncEpoch(ctx context.Context) {
	current := map[*store.Store]int64{}
	for _, st := range s.stores() {
		e, err := st.Epoch(ctx)
		if err != nil {
			s.cfg.Logger.Warn("proxy: read store epoch", "err", err)
			return
		}
		current[st] = e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for st, e := range current {
		if prev, ok := s.epochs[st]; !ok || prev != e {
			changed = true
		}
		s.epochs[st] = e
	}
	if !changed {
		return
	}
	s.sessions = map[string]*session.Session{}
	s.sidStore = map[string]*store.Store{}
	s.ensured = map[string]bool{}
	s.roots = map[string]string{}
	s.tunes = map[string]*tune{}
}
