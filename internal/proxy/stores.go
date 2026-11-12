package proxy

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// A project with storage: local keeps its data in <root>/.acd-ctx/acd-ctx.db
// (spec §18.3), also when one serve process handles every project. The project
// comes from the X-Acd-Ctx-Project-Dir header, the only signal that carries the
// root (spec §18.2); a /p/<id>/ prefix or no signal uses Config.Store.
//
// A session lives in the store of its project. session() records the store of
// every session it opens, so code that only has the session id finds it with
// storeOf.

// localDBPath is the database of a project with storage: local.
func localDBPath(root string) string {
	return filepath.Join(root, ".acd-ctx", "acd-ctx.db")
}

// storeForLocked returns the store of project p, opening a local database on
// first use. s.mu must be held.
func (s *Server) storeForLocked(p projectRef) (*store.Store, error) {
	if p.Storage != "local" || s.cfg.OpenStore == nil || !filepath.IsAbs(p.Root) {
		return s.cfg.Store, nil
	}
	path := localDBPath(p.Root)
	if st, ok := s.local[path]; ok {
		return st, nil
	}
	st, err := s.cfg.OpenStore(path)
	if err != nil {
		return nil, err
	}
	s.local[path] = st
	// The epoch it opened at is the baseline, so opening it drops no cache.
	if e, err := st.Epoch(context.Background()); err == nil {
		s.epochs[st] = e
	}
	s.cfg.Logger.Info("project database opened", "project", p.ID, "db", path)
	return st, nil
}

// storeFor is storeForLocked for callers that do not hold s.mu.
func (s *Server) storeFor(p projectRef) (*store.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.storeForLocked(p)
}

// storeOf returns the store holding session sid, or Config.Store for a session
// this process has not opened.
func (s *Server) storeOf(sid string) *store.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.storeOfLocked(sid)
}

// storeOfLocked is storeOf for callers that hold s.mu.
func (s *Server) storeOfLocked(sid string) *store.Store {
	if st, ok := s.sidStore[sid]; ok {
		return st
	}
	return s.cfg.Store
}

// storeHolding finds the store of session id among the open stores, for the
// session endpoints. A local database this process has not opened yet is not
// searched; Config.Store is the answer then.
func (s *Server) storeHolding(ctx context.Context, id string) *store.Store {
	s.mu.Lock()
	st, ok := s.sidStore[id]
	s.mu.Unlock()
	if ok {
		return st
	}
	for _, st := range s.stores() {
		if _, err := st.GetSession(ctx, id); err == nil {
			return st
		}
	}
	return s.cfg.Store
}

// stores lists Config.Store and the open local stores.
func (s *Server) stores() []*store.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*store.Store{s.cfg.Store}
	for _, st := range s.local {
		out = append(out, st)
	}
	return out
}

// Close closes the local databases the proxy opened. Config.Store belongs to
// the caller and stays open.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for path, st := range s.local {
		errs = append(errs, st.Close())
		delete(s.local, path)
	}
	s.sidStore = map[string]*store.Store{}
	s.sessions = map[string]*session.Session{}
	return errors.Join(errs...)
}
