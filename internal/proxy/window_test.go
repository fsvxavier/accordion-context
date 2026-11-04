package proxy

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsvxavier/accordion-context/internal/config"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// windowServer returns a bare Server with a 123456 fallback window and a store.
func windowServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "acd-ctx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	u, _ := url.Parse("http://127.0.0.1:1")
	s, err := New(Config{Upstream: u, Store: st, ContextLimit: 123_456, GlobalPath: filepath.Join(t.TempDir(), "absent.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestContextWindowPrecedence: window.models (longest prefix) beats
// window.context_limit, which beats the built-in table, which beats the fallback.
func TestContextWindowPrecedence(t *testing.T) {
	s := windowServer(t)
	cases := []struct {
		name  string
		model string
		cfg   func(*config.Config)
		want  int
	}{
		{"built-in table", "claude-x", func(*config.Config) {}, 200_000},
		{"fallback when nothing matches", "mystery-model", func(*config.Config) {}, 123_456},
		{"context_limit beats built-in", "claude-x", func(c *config.Config) { c.WindowLimit = 60_000 }, 60_000},
		{"context_limit beats fallback", "mystery-model", func(c *config.Config) { c.WindowLimit = 60_000 }, 60_000},
		{"models beats built-in", "claude-x", func(c *config.Config) { c.WindowModels = map[string]int{"claude-": 90_000} }, 90_000},
		{"models beats context_limit", "claude-x", func(c *config.Config) {
			c.WindowLimit = 60_000
			c.WindowModels = map[string]int{"claude-x": 200_000}
		}, 200_000},
		{"longest prefix wins", "claude-x-1", func(c *config.Config) {
			c.WindowModels = map[string]int{"claude-": 100_000, "claude-x": 70_000}
		}, 70_000},
		{"shorter prefix for other models", "claude-y", func(c *config.Config) {
			c.WindowModels = map[string]int{"claude-": 100_000, "claude-x": 70_000}
		}, 100_000},
		{"models match case-insensitively", "Claude-X", func(c *config.Config) {
			c.WindowModels = map[string]int{"claude-x": 70_000}
		}, 70_000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Defaults()
			c.cfg(&cfg)
			if got := s.contextWindow(c.model, cfg); got != c.want {
				t.Fatalf("contextWindow(%q) = %d, want %d", c.model, got, c.want)
			}
		})
	}
}

// TestContextLimitConfigShapesNudge: the configured window reaches the trigger
// through the request path, and each source of the limit behaves as specified.
func TestContextLimitConfigShapesNudge(t *testing.T) {
	answer := func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	}
	run := func(yaml string) bool {
		up := newFakeUpstream(t, answer)
		ps, _, _ := newTuned(t, up, tuneOpts{globalYAML: yaml})
		if resp, _ := postMessages(t, ps.URL, transcriptBody(false, 15, nil), nil); resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
		return sawNudge(t, up, 0)
	}
	empty := "cache:\n  ttl: 5m\n"
	cases := []struct {
		name string
		yaml string
		want bool
	}{
		{"empty config keeps the 200K built-in window", empty, false},
		{"window.context_limit 60000 nudges", "window:\n  context_limit: 60000\n", true},
		{"window.models claude-x 60000 nudges", "window:\n  models:\n    claude-x: 60000\n", true},
		{"models 200000 beats context_limit 60000", "window:\n  context_limit: 60000\n  models:\n    claude-x: 200000\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := run(c.yaml); got != c.want {
				t.Fatalf("nudge = %v, want %v", got, c.want)
			}
		})
	}
}

// TestContextLimitChangeReachesExistingSession: a session created under the default
// window takes a window that is configured later. Its stored limit is updated, and
// the next turn is nudged. The other rows of the session are not changed.
func TestContextLimitChangeReachesExistingSession(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, s, dbPath := newTuned(t, up, tuneOpts{clk: clk, globalYAML: "cache:\n  ttl: 5m\n"})
	cfgFile := filepath.Join(filepath.Dir(dbPath), "config.yaml")
	hdr := map[string]string{"X-Acd-Ctx-Conversation": "conv-window"}
	ctx := context.Background()
	sid := sessionID("default", "conv-window", nil)

	// post sends the 15-message transcript plus extra messages. Growth between
	// turns keeps the trigger's growth rule from suppressing the nudge.
	post := func(extra []string) {
		t.Helper()
		if resp, _ := postMessages(t, ps.URL, transcriptBody(false, 15, extra), hdr); resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	post(nil)
	if sawNudge(t, up, 0) {
		t.Fatal("default 200K window: no nudge expected on turn 1")
	}
	if _, lim, err := s.cfg.Store.SessionWindow(ctx, sid); err != nil || lim != 200_000 {
		t.Fatalf("stored limit = %d, %v; want 200000", lim, err)
	}
	msgsBefore := countRows(t, dbPath, "SELECT COUNT(*) FROM messages WHERE session_id = '"+sid+"'")

	if err := os.WriteFile(cfgFile, []byte("window:\n  context_limit: 60000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clk.Advance(31 * time.Second) // past cfgTTL, so the file is read again
	post(bigMessages(10))

	if !sawNudge(t, up, 1) {
		t.Fatal("configured 60K window on an existing session: the turn must be nudged")
	}
	if _, lim, err := s.cfg.Store.SessionWindow(ctx, sid); err != nil || lim != 60_000 {
		t.Fatalf("stored limit = %d, %v; want 60000 after the config change", lim, err)
	}
	// Turn 2 appends its 10 messages to the ledger; the sync adds or drops none.
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM messages WHERE session_id = '"+sid+"'"); n != msgsBefore+10 {
		t.Fatalf("messages = %d, want %d: the sync must touch only the window", n, msgsBefore+10)
	}
	if err := s.cfg.Store.CheckInvariants(ctx, sid); err != nil {
		t.Fatalf("invariants after window change: %v", err)
	}
}

// TestApplyContextLimitFollowsStoredModel: the window is computed from the model the
// session was created with, so a request for another model does not change it.
func TestApplyContextLimitFollowsStoredModel(t *testing.T) {
	s := windowServer(t)
	ctx := context.Background()
	if err := s.cfg.Store.EnsureProject(ctx, "p1", "urn:acd-ctx:project:p1", "global", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.cfg.Store.CreateSession(ctx, store.Session{
		ID: "s1", ProjectID: "p1", Model: "claude-x", ContextLimit: 200_000, Mode: "proxy", Integration: "url",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.WindowModels = map[string]int{"gpt-": 50_000}
	if err := s.applyContextLimit(ctx, "s1", cfg); err != nil {
		t.Fatal(err)
	}
	if _, lim, _ := s.cfg.Store.SessionWindow(ctx, "s1"); lim != 200_000 {
		t.Fatalf("limit = %d, want 200000: a gpt- entry must not change a claude-x session", lim)
	}
	cfg.WindowModels = map[string]int{"claude-": 150_000}
	if err := s.applyContextLimit(ctx, "s1", cfg); err != nil {
		t.Fatal(err)
	}
	if _, lim, _ := s.cfg.Store.SessionWindow(ctx, "s1"); lim != 150_000 {
		t.Fatalf("limit = %d, want 150000 from the stored model", lim)
	}
	if err := s.applyContextLimit(ctx, "missing", cfg); err == nil {
		t.Fatal("unknown session must report an error")
	}
}
