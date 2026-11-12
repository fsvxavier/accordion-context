package proxy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsvxavier/accordion-context/internal/config"
)

// cfgTTL is how long a loaded configuration is reused before the files are read again.
const cfgTTL = 30 * time.Second

// builtinWindows maps model name prefixes to context windows in tokens (spec §4).
// The longest matching prefix wins. Config.ContextWindows adds to or replaces entries.
// The Claude 5 family has a 1M window: upstream accepted requests of up to
// 894k input tokens (claude-sonnet-5-5), 663k (claude-haiku-5-5), 571k
// (claude-opus-5-5) and 534k (claude-fable-5-1). Older Claude models keep 200k.
var builtinWindows = map[string]int{
	"claude-":         200_000,
	"claude-opus-5":   1_000_000,
	"claude-sonnet-5": 1_000_000,
	"claude-haiku-5":  1_000_000,
	"claude-fable-5":  1_000_000,
	"gpt-4o":          128_000,
	"gpt-4.1":         1_047_576,
	"gpt-5":           400_000,
	"o1":              200_000,
	"o3":              200_000,
	"o4":              200_000,
	"gemini-":         1_048_576,
}

// cachedConfig is one loaded configuration and when it was loaded.
type cachedConfig struct {
	cfg config.Config
	at  time.Time
}

// mergedWindows returns builtinWindows overlaid with the configured entries, with
// lower-case keys.
func mergedWindows(extra map[string]int) map[string]int {
	out := make(map[string]int, len(builtinWindows)+len(extra))
	for k, v := range builtinWindows {
		out[k] = v
	}
	for k, v := range extra {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" && v > 0 {
			out[k] = v
		}
	}
	return out
}

// contextWindow returns the context window of model under cfg. The first rule
// that applies wins: window.models (longest prefix), window.context_limit when
// set, the built-in table (builtinWindows plus Config.ContextWindows), and
// finally Config.ContextLimit.
func (s *Server) contextWindow(model string, cfg config.Config) int {
	m := strings.ToLower(model)
	if n, ok := longestPrefix(m, cfg.WindowModels); ok {
		return n
	}
	if cfg.WindowLimit > 0 {
		return cfg.WindowLimit
	}
	if n, ok := longestPrefix(m, s.windows); ok {
		return n
	}
	return s.cfg.ContextLimit
}

// longestPrefix returns the value of the longest key that model starts with.
// ok is false when no key matches.
func longestPrefix(model string, table map[string]int) (n int, ok bool) {
	best, found := "", false
	for k := range table {
		if strings.HasPrefix(model, k) && (!found || len(k) > len(best)) {
			best, found = k, true
		}
	}
	if !found {
		return 0, false
	}
	return table[best], true
}

// applyContextLimit makes an existing session use the window that cfg gives the
// model it was created for. The window comes from the stored model, not from the
// request, so a conversation that switches models keeps one window. A row is
// written only when the value differs, and only context_limit changes.
func (s *Server) applyContextLimit(ctx context.Context, sid string, cfg config.Config) error {
	model, stored, err := s.storeOf(sid).SessionWindow(ctx, sid)
	if err != nil {
		return err
	}
	want := s.contextWindow(model, cfg)
	if want == stored {
		return nil
	}
	if err := s.storeOf(sid).SetContextLimit(ctx, sid, want); err != nil {
		return err
	}
	// Refusals counted against the old window (for example a window too small
	// for the model, with an emergency nudge every turn) would keep nudges
	// suspended in the new one.
	return s.storeOf(sid).ResetRefusals(ctx, sid)
}

// configFor returns the effective configuration of a project root (spec §18.5).
// Only a real directory adds the project layer; synthetic roots and unknown ids
// use the global file alone. A load failure is logged and the defaults are used,
// so configuration problems never stop the traffic.
func (s *Server) configFor(root string) config.Config {
	key := ""
	if filepath.IsAbs(root) && !strings.HasPrefix(root, "urn:") {
		if fi, err := os.Stat(root); err == nil && fi.IsDir() {
			key = filepath.Clean(root)
		}
	}
	now := s.cfg.Now()
	s.mu.Lock()
	e, ok := s.cfgs[key]
	s.mu.Unlock()
	if ok && now.Sub(e.at) < cfgTTL {
		return e.cfg
	}
	c := s.loadConfig(key)
	s.mu.Lock()
	s.cfgs[key] = cachedConfig{cfg: c, at: now}
	s.mu.Unlock()
	return c
}

func (s *Server) loadConfig(root string) config.Config {
	res, err := config.Load(config.Options{
		GlobalPath: s.cfg.GlobalPath,
		Root:       root,
		Flags:      s.cfg.ConfigFlags,
		LookupEnv:  s.cfg.LookupEnv,
	})
	if err != nil {
		s.cfg.Logger.Warn("configuration not loaded; using defaults", "root", root, "err", err)
		return config.Defaults()
	}
	for _, w := range res.Warnings {
		s.cfg.Logger.Warn("configuration", "detail", w)
	}
	return res.Config
}
