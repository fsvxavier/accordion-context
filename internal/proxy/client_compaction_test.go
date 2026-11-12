package proxy

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/fsvxavier/accordion-context/internal/obs"
)

// rawUpstream records the exact bytes of every request body and paths.
type rawUpstream struct {
	mu     sync.Mutex
	paths  []string
	bodies [][]byte
	srv    *httptest.Server
}

func newRawUpstream(t *testing.T) *rawUpstream {
	t.Helper()
	u := &rawUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.paths = append(u.paths, r.URL.Path)
		u.bodies = append(u.bodies, raw)
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == pathMessages {
			io.WriteString(w, plainAnswer)
			return
		}
		io.WriteString(w, `{"id":"resp_1","object":"response","output":[]}`)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *rawUpstream) last() ([]byte, string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	n := len(u.bodies)
	return u.bodies[n-1], u.paths[n-1]
}

func (u *rawUpstream) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.bodies)
}

// clientCompactionProxy starts the proxy against a raw upstream.
func clientCompactionProxy(t *testing.T) (*httptest.Server, *Server, string, *rawUpstream) {
	t.Helper()
	up := newRawUpstream(t)
	s, _, dbPath := newServer(t, &fakeUpstream{srv: up.srv}, "")
	ps := httptest.NewServer(s)
	t.Cleanup(ps.Close)
	return ps, s, dbPath, up
}

// requestCount reads one acd_ctx_requests_total series.
func requestCount(t *testing.T, s *Server, protocol, outcome string) float64 {
	t.Helper()
	c, err := s.telemetry.m.Requests.GetMetricWithLabelValues(protocol, outcome)
	if err != nil {
		t.Fatal(err)
	}
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

func queryStrings(t *testing.T, dbPath, query string, args ...any) []string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestClientCompactionDetection(t *testing.T) {
	claude := http.Header{}
	claude.Set("X-Claude-Code-Compaction", "auto")
	manual := http.Header{}
	manual.Set("x-claude-code-compaction", "manual")
	reactive := http.Header{}
	reactive.Set("X-Claude-Code-Compaction", "reactive")
	class := http.Header{}
	class.Set("X-Claude-Code-Request-Class", "compaction")
	mainClass := http.Header{}
	mainClass.Set("X-Claude-Code-Request-Class", "main")
	bogus := http.Header{}
	bogus.Set("X-Claude-Code-Compaction", "maybe")
	none := http.Header{}

	tests := []struct {
		name string
		p    protocol
		rt   routed
		h    http.Header
		want string
	}{
		{"anthropic auto", anthropicProtocol{}, routed{path: "/v1/messages"}, claude, signalClaudeCodeHeader},
		{"anthropic manual", anthropicProtocol{}, routed{path: "/v1/messages"}, manual, signalClaudeCodeHeader},
		{"anthropic reactive", anthropicProtocol{}, routed{path: "/v1/messages"}, reactive, signalClaudeCodeHeader},
		{"anthropic request class", anthropicProtocol{}, routed{path: "/v1/messages"}, class, signalClaudeCodeClass},
		{"anthropic via ctx", anthropicProtocol{}, routed{path: "/gw/v1/messages", viaCtx: true}, claude, signalClaudeCodeHeader},
		{"anthropic main class", anthropicProtocol{}, routed{path: "/v1/messages"}, mainClass, ""},
		{"anthropic unknown value", anthropicProtocol{}, routed{path: "/v1/messages"}, bogus, ""},
		{"anthropic no header", anthropicProtocol{}, routed{path: "/v1/messages"}, none, ""},
		{"anthropic other path", anthropicProtocol{}, routed{path: "/v1/messages/count_tokens"}, claude, ""},
		{"anthropic gateway prefix without ctx", anthropicProtocol{}, routed{path: "/gw/v1/messages"}, claude, ""},
		{"responses compact", responsesProtocol{}, routed{path: "/v1/responses/compact"}, none, signalCodexCompactPath},
		{"responses compact via ctx", responsesProtocol{}, routed{path: "/gw/v1/responses/compact", viaCtx: true}, none, signalCodexCompactPath},
		{"responses compact no ctx prefix", responsesProtocol{}, routed{path: "/gw/v1/responses/compact"}, none, ""},
		{"responses collection", responsesProtocol{}, routed{path: "/v1/responses"}, none, ""},
		{"responses by id", responsesProtocol{}, routed{path: "/v1/responses/resp_1"}, none, ""},
		{"responses ignores claude header", responsesProtocol{}, routed{path: "/v1/responses"}, claude, ""},
		{"openai chat", openaiProtocol{}, routed{path: "/v1/chat/completions"}, claude, ""},
		{"google generate", googleProtocol{}, routed{path: "/v1beta/models/gemini:generateContent"}, claude, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.ClientCompaction(tc.rt, tc.h, nil); got != tc.want {
				t.Fatalf("ClientCompaction(%+v) = %q, want %q", tc.rt, got, tc.want)
			}
		})
	}
}

// transcript builds an Anthropic request body from alternating turns.
func transcript(t *testing.T, msgs ...string) string {
	t.Helper()
	var list []map[string]any
	for i, m := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		list = append(list, map[string]any{"role": role, "content": m})
	}
	b, err := json.Marshal(map[string]any{"model": "claude-x", "max_tokens": 64, "messages": list})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const compactionPrompt = "Your task is to create a detailed summary of the conversation so far."

func TestClientCompactionForwardedUnchanged(t *testing.T) {
	ps, s, dbPath, up := clientCompactionProxy(t)

	// A normal turn creates the session and its ledger.
	normal := transcript(t, "first question")
	postMessages(t, ps.URL, normal, nil)
	msgsBefore := countRows(t, dbPath, "SELECT COUNT(*) FROM messages")
	if countRows(t, dbPath, "SELECT COUNT(*) FROM sessions") != 1 {
		t.Fatal("normal turn did not create one session")
	}
	if got := countRows(t, dbPath, "SELECT COUNT(*) FROM events WHERE type = 'client_compaction'"); got != 0 {
		t.Fatalf("client_compaction events before any compaction = %d", got)
	}

	// The client's summarizing request: same transcript plus its instruction.
	compaction := transcript(t, "first question", "a long answer", compactionPrompt)
	before := up.count()
	resp, _ := postMessages(t, ps.URL, compaction, map[string]string{"X-Claude-Code-Compaction": "manual"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("compaction status = %d", resp.StatusCode)
	}
	if up.count() != before+1 {
		t.Fatalf("upstream calls = %d, want one more", up.count()-before)
	}
	sent, path := up.last()
	if path != pathMessages {
		t.Fatalf("upstream path = %q", path)
	}
	if !bytes.Equal(sent, []byte(compaction)) {
		t.Fatalf("upstream body changed:\n got %s\nwant %s", sent, compaction)
	}
	if strings.Contains(string(sent), `"compress"`) || strings.Contains(string(sent), "[m") {
		t.Fatal("tools or refs were injected into the compaction request")
	}
	if got := countRows(t, dbPath, "SELECT COUNT(*) FROM messages"); got != msgsBefore {
		t.Fatalf("ledger messages = %d, want %d (no ingest)", got, msgsBefore)
	}
	if got := countRows(t, dbPath, "SELECT COUNT(*) FROM sessions"); got != 1 {
		t.Fatalf("sessions = %d, want 1", got)
	}
	if got := countRows(t, dbPath, "SELECT COUNT(*) FROM events WHERE type = 'client_compaction'"); got != 1 {
		t.Fatalf("client_compaction events = %d, want 1", got)
	}
	if got := requestCount(t, s, "anthropic", obs.OutcomeClientCompaction); got != 1 {
		t.Fatalf("client_compaction counter = %v, want 1", got)
	}
	if got := requestCount(t, s, "anthropic", obs.OutcomeFailOpen); got != 0 {
		t.Fatalf("failopen counter = %v, want 0 for a client compaction", got)
	}

	// The next normal turn is compacted as usual: tools are injected and the ledger grows.
	next := transcript(t, "first question", "a long answer", compactionPrompt, "a summary", "next question")
	postMessages(t, ps.URL, next, nil)
	sent, _ = up.last()
	if !strings.Contains(string(sent), `"compress"`) {
		t.Fatal("normal turn after a compaction did not get the compress tool")
	}
	if got := countRows(t, dbPath, "SELECT COUNT(*) FROM messages"); got <= msgsBefore {
		t.Fatalf("ledger did not grow on the normal turn: %d", got)
	}
	if got := requestCount(t, s, "anthropic", obs.OutcomeClientCompaction); got != 1 {
		t.Fatalf("client_compaction counter after a normal turn = %v, want 1", got)
	}
}

func TestClientCompactionWithoutSessionCountsOnly(t *testing.T) {
	ps, s, dbPath, up := clientCompactionProxy(t)
	compaction := transcript(t, "first question", "answer", compactionPrompt)
	postMessages(t, ps.URL, compaction, map[string]string{"X-Claude-Code-Request-Class": "compaction"})

	sent, _ := up.last()
	if !bytes.Equal(sent, []byte(compaction)) {
		t.Fatal("compaction body was not forwarded unchanged")
	}
	if got := countRows(t, dbPath, "SELECT COUNT(*) FROM sessions"); got != 0 {
		t.Fatalf("sessions = %d, want 0", got)
	}
	if got := countRows(t, dbPath, "SELECT COUNT(*) FROM events"); got != 0 {
		t.Fatalf("events = %d, want 0", got)
	}
	if got := requestCount(t, s, "anthropic", obs.OutcomeClientCompaction); got != 1 {
		t.Fatalf("client_compaction counter = %v, want 1", got)
	}
}

func TestClientCompactionUndecodableBodyIsNotFailOpen(t *testing.T) {
	ps, s, _, up := clientCompactionProxy(t)
	body := `{"model":"claude-x","messages":"not a list"}`
	postMessages(t, ps.URL, body, map[string]string{"X-Claude-Code-Compaction": "auto"})
	sent, _ := up.last()
	if string(sent) != body {
		t.Fatalf("body changed: %s", sent)
	}
	if got := requestCount(t, s, "anthropic", obs.OutcomeClientCompaction); got != 1 {
		t.Fatalf("client_compaction counter = %v, want 1", got)
	}
	if got := requestCount(t, s, "anthropic", obs.OutcomeFailOpen); got != 0 {
		t.Fatalf("failopen counter = %v, want 0", got)
	}
}

func TestResponsesCompactPassesThrough(t *testing.T) {
	ps, s, dbPath, up := clientCompactionProxy(t)
	body := `{"model":"gpt-x","input":[{"type":"message","role":"user","content":"hi"},` +
		`{"type":"compaction","encrypted_content":"opaque"}]}`

	// The launcher's shape is /p/<id>/ctx/<upstream>; the bare and /ctx/ forms are pinned too.
	for _, base := range []string{ps.URL, ps.URL + "/ctx/" + up.srv.URL, ps.URL + "/p/proj-1/ctx/" + up.srv.URL} {
		before := up.count()
		req, _ := http.NewRequest(http.MethodPost, base+"/v1/responses/compact", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d via %s", resp.StatusCode, base)
		}
		if up.count() != before+1 {
			t.Fatalf("upstream calls via %s = %d", base, up.count()-before)
		}
		sent, path := up.last()
		if path != "/v1/responses/compact" {
			t.Fatalf("upstream path = %q", path)
		}
		if string(sent) != body {
			t.Fatalf("body changed via %s: %s", base, sent)
		}
	}
	if got := countRows(t, dbPath, "SELECT COUNT(*) FROM sessions"); got != 0 {
		t.Fatalf("sessions = %d, want 0", got)
	}
	if got := requestCount(t, s, "responses", obs.OutcomeClientCompaction); got != 3 {
		t.Fatalf("client_compaction counter = %v, want 3", got)
	}
	if got := requestCount(t, s, "responses", obs.OutcomeFailOpen); got != 0 {
		t.Fatalf("failopen counter = %v, want 0", got)
	}
}

func TestClientCompactionParentLinkOnFirstRequestAfter(t *testing.T) {
	ps, _, dbPath, _ := clientCompactionProxy(t)

	postMessages(t, ps.URL, transcript(t, "first question"), nil)
	postMessages(t, ps.URL, transcript(t, "first question", "answer", compactionPrompt),
		map[string]string{"X-Claude-Code-Compaction": "auto"})
	parents := queryStrings(t, dbPath, "SELECT session_id FROM events WHERE type = 'client_compaction'")
	if len(parents) != 1 {
		t.Fatalf("client_compaction events = %d, want 1", len(parents))
	}
	old := parents[0]

	// After the compaction the transcript starts with the summary, a new session.
	postMessages(t, ps.URL, transcript(t, "Summary of the earlier conversation.", "ok", "continue"),
		map[string]string{"X-Claude-Code-Context-Compacted": "auto"})
	links := queryStrings(t, dbPath, "SELECT payload FROM events WHERE type = 'client_compaction_parent'")
	if len(links) != 1 {
		t.Fatalf("parent link events = %d, want 1", len(links))
	}
	if !strings.Contains(links[0], old) {
		t.Fatalf("parent link payload %s does not name %s", links[0], old)
	}
	// The link is consumed: a second marked request writes nothing new.
	postMessages(t, ps.URL, transcript(t, "Summary of the earlier conversation.", "ok", "continue", "ok", "more"),
		map[string]string{"X-Claude-Code-Context-Compacted": "auto"})
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM events WHERE type = 'client_compaction_parent'"); n != 1 {
		t.Fatalf("parent link events after second marker = %d, want 1", n)
	}
}

// TestClaudeSideRequestsOpenNoSession: Claude Code requests of class auxiliary
// (the auto-mode classifier) or side_reply are forwarded unchanged and open no
// session; classes main and subagent are compacted as conversations.
func TestClaudeSideRequestsOpenNoSession(t *testing.T) {
	ps, s, dbPath, up := clientCompactionProxy(t)
	body := transcript(t, "Respond with <severity>N</severity> ONLY.")
	for _, class := range []string{"auxiliary", "side_reply", "Auxiliary"} {
		postMessages(t, ps.URL, body, map[string]string{"X-Claude-Code-Request-Class": class})
		if sent, _ := up.last(); !bytes.Equal(sent, []byte(body)) {
			t.Fatalf("class %s: body was not forwarded unchanged", class)
		}
	}
	if got := countRows(t, dbPath, "SELECT COUNT(*) FROM sessions"); got != 0 {
		t.Fatalf("sessions = %d, want 0 after side requests", got)
	}
	if got := requestCount(t, s, "anthropic", obs.OutcomeSideRequest); got != 3 {
		t.Fatalf("side_request counter = %v, want 3", got)
	}
	for _, class := range []string{"main", "subagent"} {
		postMessages(t, ps.URL, transcript(t, "question for "+class), map[string]string{"X-Claude-Code-Request-Class": class})
		if sent, _ := up.last(); !strings.Contains(string(sent), `"compress"`) {
			t.Fatalf("class %s: not compacted (no compress tool)", class)
		}
	}
	if got := countRows(t, dbPath, "SELECT COUNT(*) FROM sessions"); got != 2 {
		t.Fatalf("sessions = %d, want 2 (main and subagent)", got)
	}
}
