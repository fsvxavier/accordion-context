package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/project"
	"github.com/fsvxavier/accordion-context/internal/store"
)

const testToken = "0123456789abcdef0123456789abcdef0123"

// testEnv is one store, one project directory and one handler on top of them.
type testEnv struct {
	st      *store.Store
	dbPath  string
	dir     string
	exports string
	proj    project.Resolved
	handler *Handler
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "acd.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dir := t.TempDir()
	proj, err := project.ResolveWith(dir, nil, project.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	exports := t.TempDir()
	h, err := New(Config{Store: st, Project: proj, Token: testToken, Version: "test", ExportDir: exports})
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{st: st, dbPath: dbPath, dir: dir, exports: exports, proj: proj, handler: h}
}

// do sends one request through the guards and the router.
func (e *testEnv) do(t *testing.T, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("response is not a JSON object: %v: %s", err, rec.Body.String())
	}
	return m
}

// transcript returns n text messages of 1000 tokens each, alternating roles.
func transcript(n int) []map[string]any {
	out := make([]map[string]any, 0, n)
	for i := range n {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		out = append(out, map[string]any{
			"role": role, "kind": "text", "tokens": 1000,
			"text": fmt.Sprintf("message %d %s", i+1, strings.Repeat("x", 200)),
		})
	}
	return out
}

func TestGuardsRefuseBadRequests(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name    string
		method  string
		path    string
		host    string
		origin  bool
		token   string
		want    int
		headers map[string]string
	}{
		{name: "foreign host", method: "GET", path: pathManifest, host: "evil.example:8787", want: 403},
		{name: "dns rebinding host with port", method: "GET", path: pathManifest, host: "attacker.test", want: 403},
		{name: "origin header", method: "GET", path: pathManifest, host: "127.0.0.1:8787", origin: true, want: 403},
		{name: "preflight", method: "OPTIONS", path: pathManifest, host: "127.0.0.1:8787", want: 403},
		{name: "no token", method: "GET", path: pathManifest, host: "127.0.0.1:8787", token: "-", want: 401},
		{name: "wrong token", method: "GET", path: pathManifest, host: "127.0.0.1:8787", token: "nope", want: 401},
		{name: "wrong method", method: "GET", path: pathTurn, host: "127.0.0.1:8787", want: 405},
		{name: "unknown endpoint", method: "GET", path: pathPrefix + "nope", host: "127.0.0.1:8787", want: 404},
		{name: "outside prefix", method: "GET", path: "/ctx/x", host: "127.0.0.1:8787", want: 404},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "http://"+c.host+c.path, nil)
			switch c.token {
			case "":
				req.Header.Set("Authorization", "Bearer "+testToken)
			case "-":
			default:
				req.Header.Set("Authorization", "Bearer "+c.token)
			}
			if c.origin {
				req.Header.Set("Origin", "https://evil.example")
			}
			rec := httptest.NewRecorder()
			e.handler.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

func TestNewRejectsWeakConfig(t *testing.T) {
	e := newEnv(t)
	base := Config{Store: e.st, Project: e.proj, Token: testToken}
	short := base
	short.Token = "short"
	if _, err := New(short); err == nil {
		t.Fatal("short token accepted")
	}
	noStore := base
	noStore.Store = nil
	if _, err := New(noStore); err == nil {
		t.Fatal("nil store accepted")
	}
	noProj := base
	noProj.Project = project.Resolved{}
	if _, err := New(noProj); err == nil {
		t.Fatal("empty project accepted")
	}
}

func TestManifestListsSpecTools(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "GET", pathManifest, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	m := decodeMap(t, rec)
	if m["protocol"] != protocolName || m["protocolVersion"].(float64) != protocolVersion {
		t.Fatalf("manifest protocol = %v", m)
	}
	tools := m["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"compress", "decompress", "retrieve", "search_context", ctxStatusToolName} {
		if !names[want] {
			t.Fatalf("manifest lacks %s", want)
		}
	}
}

func TestTurnIngestsAndRejectsDivergence(t *testing.T) {
	e := newEnv(t)
	conv := "conv-turn"
	msgs := transcript(12)
	rec := e.do(t, "POST", pathRuntime, map[string]any{
		"conversationId": conv, "model": "m-test", "contextWindow": 200000, "cwd": e.dir,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("runtime-info status = %d: %s", rec.Code, rec.Body.String())
	}
	if created := decodeMap(t, rec)["created"]; created != true {
		t.Fatalf("runtime-info created = %v, want true", created)
	}

	rec = e.do(t, "POST", pathTurn, map[string]any{"conversationId": conv, "messages": msgs}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("turn status = %d: %s", rec.Code, rec.Body.String())
	}
	got := decodeMap(t, rec)
	if got["ingested"].(float64) != 12 {
		t.Fatalf("ingested = %v, want 12", got["ingested"])
	}
	// The window carries the 12 messages plus kernel additions (intents line).
	if n := len(got["visible"].([]any)); n < 12 {
		t.Fatalf("visible = %d messages, want at least 12", n)
	}

	// The same transcript plus one message ingests exactly one message.
	msgs = append(msgs, map[string]any{"role": "user", "kind": "text", "text": "one more", "tokens": 5})
	rec = e.do(t, "POST", pathTurn, map[string]any{"conversationId": conv, "messages": msgs}, nil)
	if got := decodeMap(t, rec)["ingested"].(float64); got != 1 {
		t.Fatalf("second turn ingested = %v, want 1", got)
	}

	// A rewritten history is refused and writes nothing.
	msgs[0]["text"] = "rewritten"
	rec = e.do(t, "POST", pathTurn, map[string]any{"conversationId": conv, "messages": msgs}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("divergent turn status = %d, want 409", rec.Code)
	}
}

func TestTurnRejectsBadMessages(t *testing.T) {
	e := newEnv(t)
	cases := []map[string]any{
		{"role": "wizard", "kind": "text", "text": "x"},
		{"role": "user", "kind": "video", "text": "x"},
		{"role": "user", "kind": "text", "text": "x", "tokens": -1},
	}
	for _, m := range cases {
		rec := e.do(t, "POST", pathTurn, map[string]any{
			"conversationId": "conv-bad", "messages": []any{m},
		}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("message %v: status = %d, want 400", m, rec.Code)
		}
	}
	rec := e.do(t, "POST", pathTurn, map[string]any{"conversationId": "", "messages": []any{}}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty conversation id: status = %d, want 400", rec.Code)
	}
}

func TestBodyLimitAnd413(t *testing.T) {
	e := newEnv(t)
	big := `{"conversationId":"c","messages":[{"role":"user","kind":"text","text":"` +
		strings.Repeat("a", maxBodyBytes+16) + `"}]}`
	req := httptest.NewRequest("POST", "http://127.0.0.1:8787"+pathTurn, strings.NewReader(big))
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", rec.Code)
	}
}

func TestCompactUnknownConversationIs404(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "POST", pathCompact, map[string]any{"conversationId": "c"}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("compact status = %d, want 404", rec.Code)
	}
}

// TestCompactArmsNextNudge: compact offers the spans and the next turn nudges
// with tier 1 even though the window is far from the usage bands.
func TestCompactArmsNextNudge(t *testing.T) {
	e := newEnv(t)
	conv := "conv-compact"
	msgs := transcript(30)
	if rec := e.do(t, "POST", pathTurn, map[string]any{"conversationId": conv, "messages": msgs}, nil); rec.Code != http.StatusOK {
		t.Fatalf("turn status = %d: %s", rec.Code, rec.Body.String())
	}
	rec := e.do(t, "POST", pathCompact, map[string]any{"conversationId": conv}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("compact status = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeMap(t, rec)
	if body["nudgePending"] != true {
		t.Fatalf("nudgePending = %v, want true", body["nudgePending"])
	}
	spans := body["spans"].([]any)
	if len(spans) != 1 || spans[0].(map[string]any)["start"] != "m00002" {
		t.Fatalf("spans = %v, want one span from m00002", spans)
	}

	next := append(msgs, transcript(31)[30])
	rec = e.do(t, "POST", pathTurn, map[string]any{"conversationId": conv, "messages": next}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("next turn status = %d: %s", rec.Code, rec.Body.String())
	}
	dec := decodeMap(t, rec)["decision"].(map[string]any)
	if dec["nudge"] != true || dec["tier"] != float64(1) || dec["row"] != "forced" {
		t.Fatalf("decision after compact = %v, want forced tier-1 nudge", dec)
	}
}

func TestToolFlowCompressRetrieveSearchStatus(t *testing.T) {
	e := newEnv(t)
	conv := "conv-tools"
	rec := e.do(t, "POST", pathTurn, map[string]any{"conversationId": conv, "messages": transcript(30)}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("turn status = %d: %s", rec.Code, rec.Body.String())
	}

	summary := "Decided to keep the matcher in kernel and log the reason, path internal/kernel/x.go:42, " +
		"error literal 'bad rate', exact value 0.30."
	rec = e.do(t, "POST", pathTool, map[string]any{
		"conversationId": conv, "name": "compress",
		"arguments": map[string]any{"ranges": []any{map[string]any{
			"startRef": "m00002", "endRef": "m00007", "summary": summary, "topic": "matcher",
		}}},
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("compress status = %d: %s", rec.Code, rec.Body.String())
	}
	res := decodeMap(t, rec)["result"].(map[string]any)
	blockIDs := res["blockIds"].([]any)
	if len(blockIDs) != 1 {
		t.Fatalf("blockIds = %v, want one block", blockIDs)
	}
	blockID := blockIDs[0].(string)

	// A range below the 5000-token minimum is refused with 422 and writes nothing.
	rec = e.do(t, "POST", pathTool, map[string]any{
		"conversationId": conv, "name": "compress",
		"arguments": map[string]any{"ranges": []any{map[string]any{
			"startRef": "m00012", "endRef": "m00012", "summary": summary,
		}}},
	}, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("short range status = %d, want 422", rec.Code)
	}

	rec = e.do(t, "POST", pathTool, map[string]any{
		"conversationId": conv, "name": "retrieve", "arguments": map[string]any{"ref": "m00003"},
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("retrieve status = %d: %s", rec.Code, rec.Body.String())
	}
	if text := decodeMap(t, rec)["result"].(map[string]any)["text"].(string); !strings.HasPrefix(text, "message 3 ") {
		t.Fatalf("retrieve text = %q", text)
	}

	// search_context finds the block summary; the messages do not say "matcher".
	rec = e.do(t, "POST", pathTool, map[string]any{
		"conversationId": conv, "name": "search_context",
		"arguments": map[string]any{"query": "matcher kernel", "limit": 5},
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search_context status = %d: %s", rec.Code, rec.Body.String())
	}
	hits := decodeMap(t, rec)["result"].(map[string]any)["hits"].([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["key"] != blockID || hits[0].(map[string]any)["kind"] != "block" {
		t.Fatalf("search hits = %v, want the block %s", hits, blockID)
	}
	rec = e.do(t, "POST", pathTool, map[string]any{
		"conversationId": conv, "name": "search_context", "arguments": map[string]any{"query": "*"},
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("search without terms status = %d, want 400", rec.Code)
	}

	rec = e.do(t, "GET", pathStatus+"?conversationId="+conv, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d: %s", rec.Code, rec.Body.String())
	}
	st := decodeMap(t, rec)
	if n := len(st["activeBlocks"].([]any)); n != 1 {
		t.Fatalf("activeBlocks = %d, want 1", n)
	}

	// decompress context expands the block: it is no longer active.
	rec = e.do(t, "POST", pathTool, map[string]any{
		"conversationId": conv, "name": "decompress", "arguments": map[string]any{"blockId": blockID},
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("decompress status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "GET", pathStatus+"?conversationId="+conv, nil, nil)
	if n := len(decodeMap(t, rec)["activeBlocks"].([]any)); n != 0 {
		t.Fatalf("after decompress activeBlocks = %d, want 0", n)
	}

	// peek returns only the matching original lines of the block.
	rec = e.do(t, "POST", pathTool, map[string]any{
		"conversationId": conv, "name": "decompress",
		"arguments": map[string]any{"blockId": blockID, "mode": "peek", "query": "message 4 "},
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("peek status = %d: %s", rec.Code, rec.Body.String())
	}
	text := decodeMap(t, rec)["result"].(map[string]any)["text"].(string)
	if !strings.Contains(text, "[m00004]") || strings.Contains(text, "[m00005]") {
		t.Fatalf("peek text = %q", text)
	}

	// mode file writes the block's original messages owner-only and returns the path.
	rec = e.do(t, "POST", pathTool, map[string]any{
		"conversationId": conv, "name": "decompress",
		"arguments": map[string]any{"blockId": blockID, "mode": "file"},
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("mode file status = %d: %s", rec.Code, rec.Body.String())
	}
	exported := decodeMap(t, rec)["result"].(map[string]any)
	path := exported["path"].(string)
	if filepath.Dir(path) != e.exports || exported["messages"] != float64(6) {
		t.Fatalf("export result = %v, want a file in %s holding 6 messages", exported, e.exports)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode = %v, want 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "== m00002 ") || !strings.Contains(string(raw), "== m00007 ") {
		t.Fatalf("export text lacks the block's messages: %q", raw[:min(len(raw), 120)])
	}
	rec = e.do(t, "POST", pathTool, map[string]any{
		"conversationId": conv, "name": "decompress",
		"arguments": map[string]any{"blockId": "../../etc", "mode": "file"},
	}, nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("traversal block id was accepted: %s", rec.Body.String())
	}
}

func TestToolErrorsAndUnknowns(t *testing.T) {
	e := newEnv(t)
	conv := "conv-err"
	e.do(t, "POST", pathTurn, map[string]any{"conversationId": conv, "messages": transcript(3)}, nil)

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"unknown tool", map[string]any{"conversationId": conv, "name": "rm_rf"}, http.StatusBadRequest},
		{"bad args type", map[string]any{"conversationId": conv, "name": "retrieve",
			"arguments": json.RawMessage(`"not an object"`)}, http.StatusBadRequest},
		{"retrieve without ref", map[string]any{"conversationId": conv, "name": "retrieve"}, http.StatusBadRequest},
		{"unknown ref refused", map[string]any{"conversationId": conv, "name": "retrieve",
			"arguments": map[string]any{"ref": "m99999"}}, http.StatusUnprocessableEntity},
		{"compress without ranges", map[string]any{"conversationId": conv, "name": "compress"}, http.StatusBadRequest},
		{"unknown conversation", map[string]any{"conversationId": "nobody", "name": ctxStatusToolName}, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := e.do(t, "POST", pathTool, c.body, nil)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.want, rec.Body.String())
			}
		})
	}

	rec := e.do(t, "GET", pathStatus+"?conversationId=nobody", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status of unknown conversation = %d, want 404", rec.Code)
	}
	rec = e.do(t, "POST", pathTurn, map[string]any{"conversationId": conv, "messages": "not-a-list"}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed turn = %d, want 400", rec.Code)
	}
}

func TestReaderCannotWrite(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", pathTurn, map[string]any{"conversationId": "conv-ro", "messages": transcript(2)}, nil)
	rd, err := openReader(e.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	id, err := rd.latestSession(t.Context(), e.proj.ID)
	if err != nil {
		t.Fatalf("latest session through reader: %v", err)
	}
	if id == "" {
		t.Fatal("reader found no session")
	}
	if _, err := rd.db.Exec(`DELETE FROM sessions`); err == nil {
		t.Fatal("read-only handle accepted a write")
	}
}

func TestWriteTokenFileIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "token")
	tok, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 64 {
		t.Fatalf("token length = %d, want 64", len(tok))
	}
	if err := WriteTokenFile(path, tok); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestSessionIDIsStableAndProjectScoped(t *testing.T) {
	a := sessionID("p1", "c")
	if a != sessionID("p1", "c") {
		t.Fatal("session id not stable")
	}
	if a == sessionID("p2", "c") || a == sessionID("p1", "d") {
		t.Fatal("session id not scoped by project and conversation")
	}
	if !strings.HasPrefix(a, "plg-") {
		t.Fatalf("session id %q lacks the plg- prefix", a)
	}
}
