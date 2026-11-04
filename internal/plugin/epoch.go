package plugin

import (
	"context"

	"github.com/fsvxavier/accordion-context/internal/session"
)

// syncEpoch drops the cached sessions when the store was purged, by this
// process or by another handle on the same database (store epoch, see
// internal/store/purge.go). The project row may be gone too, and a session
// cannot be created without it, so it is written again first. A purged
// conversation is then created again on its next request with an empty ledger.
//
// The epoch is recorded only after the project row is restored, so a failed
// restore is retried on the next request.
func (h *Handler) syncEpoch(ctx context.Context) {
	e, err := h.cfg.Store.Epoch(ctx)
	if err != nil {
		h.cfg.Logger.Warn("plugin: read store epoch", "err", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if e == h.epoch {
		return
	}
	if err := h.cfg.Store.EnsureProject(ctx, h.cfg.Project.ID, h.cfg.Project.Root,
		h.cfg.Project.Storage, nowUnix()); err != nil {
		h.cfg.Logger.Warn("plugin: restore project row", "err", err)
		return
	}
	h.epoch = e
	h.sessions = map[string]*session.Session{}
}
