package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// fakeClock is the proxy clock. Tests move it between requests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// tuneOpts configures newTuned.
type tuneOpts struct {
	windows    map[string]int
	globalYAML string // written to the global config file; empty means no file
	clk        *fakeClock
}

// newTuned starts the proxy with a temp global config, the given context windows
// and the fake clock. It returns the HTTP server, the Server and the DB path.
func newTuned(t *testing.T, up *fakeUpstream, o tuneOpts) (*httptest.Server, *Server, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "acd-ctx.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	globalPath := filepath.Join(dir, "absent.yaml")
	if o.globalYAML != "" {
		globalPath = filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(globalPath, []byte(o.globalYAML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	u, _ := url.Parse(up.srv.URL)
	cfg := Config{Upstream: u, Store: st, Version: "test", GlobalPath: globalPath, ContextWindows: o.windows}
	if o.clk != nil {
		cfg.Now = o.clk.Now
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ps := httptest.NewServer(s)
	t.Cleanup(ps.Close)
	return ps, s, dbPath
}

// answerWithInput is a plain end_turn answer that reports n input tokens.
func answerWithInput(n int) string {
	return fmt.Sprintf(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-x",`+
		`"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",`+
		`"usage":{"input_tokens":%d,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":3}}`, n)
}

// bigMessages returns n alternating messages of about 3.6K tokens each, starting
// with an assistant turn (transcriptBody ends with a user turn).
func bigMessages(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		role := "assistant"
		if i%2 == 1 {
			role = "user"
		}
		out = append(out, `{"role":"`+role+`","content":"`+strings.Repeat("reading the parser source and noting details ", 300)+` #`+strconv.Itoa(i)+`"}`)
	}
	return out
}

// plainBody builds a model request of plain text messages, alternating from user.
func plainBody(t *testing.T, texts []string) string {
	t.Helper()
	var msgs []string
	for i, tx := range texts {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		b, err := json.Marshal(map[string]string{"role": role, "content": tx})
		if err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, string(b))
	}
	return `{"model":"claude-x","max_tokens":64,"messages":[` + strings.Join(msgs, ",") + `]}`
}

func sawNudge(t *testing.T, up *fakeUpstream, i int) bool {
	t.Helper()
	raw, _ := json.Marshal(up.request(i))
	return strings.Contains(string(raw), "context_nudge")
}

// TestContextLimitFromConfigChangesNudge: the same transcript is firm-nudged when
// the configured window is small, and not nudged when the window is the default.
func TestContextLimitFromConfigChangesNudge(t *testing.T) {
	answer := func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	}

	small := newFakeUpstream(t, answer)
	ps, _, _ := newTuned(t, small, tuneOpts{windows: map[string]int{"claude-x": 60_000}})
	if resp, _ := postMessages(t, ps.URL, transcriptBody(false, 15, nil), nil); resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !sawNudge(t, small, 0) {
		t.Fatal("60K window: the transcript is above 75% of it, so a firm nudge must reach the model")
	}

	big := newFakeUpstream(t, answer)
	ps2, _, _ := newTuned(t, big, tuneOpts{})
	if resp, _ := postMessages(t, ps2.URL, transcriptBody(false, 15, nil), nil); resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if sawNudge(t, big, 0) {
		t.Fatal("default 200K window: the transcript is about 27% of it, nudge must not appear")
	}
}

// TestCalibrationConverges: the provider reports twice the local estimate. The
// factor converges to 2, and the trigger's TokenCount follows it.
func TestCalibrationConverges(t *testing.T) {
	var mu sync.Mutex
	var est []int
	up := newFakeUpstream(t, func(n int, _ map[string]json.RawMessage) (int, string, string) {
		mu.Lock()
		defer mu.Unlock()
		return 200, "application/json", answerWithInput(2 * est[n])
	})
	ps, s, _ := newTuned(t, up, tuneOpts{})
	hdr := map[string]string{"X-Acd-Ctx-Conversation": "conv-calib"}

	var texts []string
	for turn := 0; turn < 25; turn++ {
		texts = append(texts, fmt.Sprintf("turn %d: %s", turn, strings.Repeat("alpha beta gamma delta ", 40)), "ack")
		// Each request ends with a user message: drop the trailing assistant answer.
		req := texts[:len(texts)-1]
		e := 0
		for _, tx := range req {
			e += kernel.EstimateTokens(tx)
		}
		mu.Lock()
		est = append(est, e)
		mu.Unlock()
		if resp, _ := postMessages(t, ps.URL, plainBody(t, req), hdr); resp.StatusCode != 200 {
			t.Fatalf("turn %d: status %d", turn, resp.StatusCode)
		}
	}
	sid := sessionID("default", "conv-calib", nil)
	factor, samples := s.calibFactor(sid)
	f, err := strconv.ParseFloat(factor, 64)
	if err != nil {
		t.Fatal(err)
	}
	if samples < 20 || f < 1.95 || f > 2.05 {
		t.Fatalf("factor %s after %d samples, want about 2", factor, samples)
	}
	if got := s.calibrate(sid, 1000); got < 1900 || got > 2100 {
		t.Fatalf("calibrated 1000 = %d, want about 2000", got)
	}
}

// TestCrushPlaceholderIsStable: a large tool result is replaced by the CCR
// placeholder with a decompress hint, and the same bytes go out on every later turn.
func TestCrushPlaceholderIsStable(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, _ := newTuned(t, up, tuneOpts{})
	hdr := map[string]string{"X-Acd-Ctx-Conversation": "conv-ccr"}

	log := strings.Repeat("compiling package component ok\n", 700)
	msgs := []any{
		map[string]any{"role": "user", "content": "Run the build and report."},
		map[string]any{"role": "assistant", "content": []any{map[string]any{
			"type": "tool_use", "id": "toolu_a", "name": "bash", "input": map[string]any{"command": "make"}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": "toolu_a", "content": log}}},
		map[string]any{"role": "assistant", "content": "Build output received."},
		map[string]any{"role": "user", "content": "Next?"},
	}
	send := func(m []any) {
		b, err := json.Marshal(map[string]any{"model": "claude-x", "max_tokens": 64, "messages": m})
		if err != nil {
			t.Fatal(err)
		}
		if resp, _ := postMessages(t, ps.URL, string(b), hdr); resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	send(msgs)
	send(append(msgs, map[string]any{"role": "assistant", "content": "Working."}, map[string]any{"role": "user", "content": "Continue."}))

	first := toolResultText(t, up, 0)
	second := toolResultText(t, up, 1)
	if first == log || !strings.Contains(first, "[stored m00003") {
		t.Fatalf("tool result was not replaced by a CCR placeholder: %.200s", first)
	}
	if !strings.Contains(first, `decompress(blockId="m00003", mode="context")`) || strings.Contains(first, "retrieve(") {
		t.Fatalf("placeholder lacks the decompress hint: %s", first)
	}
	if first != second {
		t.Fatal("placeholder bytes changed between turns: the prompt cache would miss")
	}
}

// exactUsage answers with the local estimate of the request's messages as its
// input count, so the calibration factor stays at 1.
func exactUsage(_ int, body map[string]json.RawMessage) (int, string, string) {
	var req struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	raw, _ := json.Marshal(body)
	_ = json.Unmarshal(raw, &req)
	n := 0
	for _, m := range req.Messages {
		n += kernel.EstimateTokens(m.Content)
	}
	return 200, "application/json", answerWithInput(n)
}

// toolResultText returns the content of the tool_result block of upstream request i.
func toolResultText(t *testing.T, up *fakeUpstream, i int) string {
	t.Helper()
	var body struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	raw, _ := json.Marshal(up.request(i))
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	for _, m := range body.Messages {
		var blocks []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if json.Unmarshal(m.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" {
				return b.Content
			}
		}
	}
	t.Fatalf("request %d has no tool_result", i)
	return ""
}

// TestGateDefersThenInjects: a gentle nudge is deferred while the cache is warm,
// the deferral does not count as shown, and it is injected once the cache is cold.
func TestGateDefersThenInjects(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	up := newFakeUpstream(t, exactUsage)
	// 150K window: 15 messages are about 36% of it and 27 about 65%, the gentle band.
	// Only the messages outside the 10-message recent zone count as pending (p1 >= S).
	ps, s, dbPath := newTuned(t, up, tuneOpts{windows: map[string]int{"claude-x": 150_000}, clk: clk})
	hdr := map[string]string{"X-Acd-Ctx-Conversation": "conv-gate"}
	ctx := context.Background()
	sid := sessionID("default", "conv-gate", nil)

	post := func(body string) {
		t.Helper()
		if resp, _ := postMessages(t, ps.URL, body, hdr); resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	post(transcriptBody(false, 15, nil)) // sets the low-water mark only
	post(transcriptBody(false, 15, bigMessages(12)))
	if sawNudge(t, up, 1) {
		t.Fatal("warm cache: the gentle nudge must be deferred")
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM events WHERE type = 'nudge_deferred'"); n != 1 {
		t.Fatalf("nudge_deferred events = %d, want 1", n)
	}
	tr, err := s.cfg.Store.GetTrigger(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	if tr.NudgePending || tr.Refusals != 0 {
		t.Fatalf("deferred nudge counted as shown: pending=%v refusals=%d", tr.NudgePending, tr.Refusals)
	}

	clk.Advance(10 * time.Minute) // past the 5 minute default TTL
	post(transcriptBody(false, 15, bigMessages(12)))
	if !sawNudge(t, up, 2) {
		t.Fatal("cold cache: the deferred nudge must be injected")
	}
}

// TestCacheTTLFromConfigChangesGate: cache.ttl from the global file moves the
// point where a deferred nudge is injected.
func TestCacheTTLFromConfigChangesGate(t *testing.T) {
	run := func(yaml string) bool {
		clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
		up := newFakeUpstream(t, exactUsage)
		ps, _, _ := newTuned(t, up, tuneOpts{windows: map[string]int{"claude-x": 150_000}, clk: clk, globalYAML: yaml})
		hdr := map[string]string{"X-Acd-Ctx-Conversation": "conv-ttl"}
		post := func(body string) {
			if resp, _ := postMessages(t, ps.URL, body, hdr); resp.StatusCode != 200 {
				t.Fatalf("status %d", resp.StatusCode)
			}
		}
		post(transcriptBody(false, 15, nil))
		post(transcriptBody(false, 15, bigMessages(12)))
		clk.Advance(2 * time.Minute)
		post(transcriptBody(false, 15, bigMessages(12)))
		return sawNudge(t, up, 2)
	}
	if run("") {
		t.Fatal("default TTL 5m: 2 minutes idle must keep the nudge deferred")
	}
	if !run("cache:\n  ttl: 1m\n") {
		t.Fatal("cache.ttl 1m: 2 minutes idle must inject the deferred nudge")
	}
}

// TestReduceWindowIsPure: the reduction is deterministic, leaves its input alone,
// and skips results below the threshold.
func TestReduceWindowIsPure(t *testing.T) {
	big := strings.Repeat("compiling package component ok\n", 700)
	in := []kernel.Message{
		{Seq: 3, Ref: "m00003", Role: kernel.RoleUser, Kind: kernel.KindToolResult, ToolName: "bash", Text: big, Tokens: kernel.EstimateTokens(big)},
		{Seq: 4, Ref: "m00004", Role: kernel.RoleUser, Kind: kernel.KindToolResult, Text: "short", Tokens: 1},
	}
	a := reduceWindow(in, 4000, true)
	b := reduceWindow(in, 4000, true)
	if a[0].Text != b[0].Text || a[0].Text == big {
		t.Fatal("reduction is not stable or did not apply")
	}
	if in[0].Text != big {
		t.Fatal("input message was modified")
	}
	if a[1].Text != "short" {
		t.Fatal("small tool result was changed")
	}
	if off := reduceWindow(in, 4000, false); off[0].Text != big {
		t.Fatal("ccr disabled: the window must pass through unchanged")
	}
}
