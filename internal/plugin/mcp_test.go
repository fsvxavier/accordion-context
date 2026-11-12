package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// mcpEnv builds an MCP server bound to a project that has a session, created
// through the HTTP handler so the two transports share one database.
func mcpEnv(t *testing.T, withSession bool) (*MCP, *testEnv) {
	t.Helper()
	e := newEnv(t)
	if withSession {
		rec := e.do(t, "POST", pathTurn, map[string]any{
			"conversationId": "conv-mcp", "messages": transcript(30),
		}, nil)
		if rec.Code != 200 {
			t.Fatalf("turn status = %d: %s", rec.Code, rec.Body.String())
		}
	}
	m, err := NewMCP(MCPConfig{Store: e.st, DBPath: e.dbPath, Cwd: e.dir, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if m.ProjectID() != e.proj.ID {
		t.Fatalf("mcp project %q != handler project %q", m.ProjectID(), e.proj.ID)
	}
	return m, e
}

// exchange runs the stdio loop over the given lines and returns the output lines.
func exchange(t *testing.T, m *MCP, lines ...string) []string {
	t.Helper()
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	if err := m.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	s := strings.TrimSuffix(out.String(), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func parseOne(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("output line is not JSON: %v: %s", err, line)
	}
	if m["jsonrpc"] != "2.0" {
		t.Fatalf("output without jsonrpc 2.0: %s", line)
	}
	return m
}

func TestMCPInitializeAndPing(t *testing.T) {
	m, _ := mcpEnv(t, false)
	out := exchange(t, m,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":"p","method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
	)
	if len(out) != 3 {
		t.Fatalf("got %d responses, want 3 (notification gets none): %v", len(out), out)
	}
	init := parseOne(t, out[0])
	res := init["result"].(map[string]any)
	if res["protocolVersion"] != "2024-11-05" {
		t.Fatalf("negotiated version = %v", res["protocolVersion"])
	}
	if _, ok := res["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatal("tools capability missing")
	}
	if p := parseOne(t, out[1]); p["id"] != "p" || len(p["result"].(map[string]any)) != 0 {
		t.Fatalf("ping response = %v", p)
	}
	if fallback := parseOne(t, out[2])["result"].(map[string]any)["protocolVersion"]; fallback != "2025-06-18" {
		t.Fatalf("unsupported version answered with %v, want the latest", fallback)
	}
}

func TestMCPToolsListExposesFourTools(t *testing.T) {
	m, _ := mcpEnv(t, false)
	out := exchange(t, m, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := parseOne(t, out[0])["result"].(map[string]any)["tools"].([]any)
	got := map[string]bool{}
	for _, tl := range tools {
		got[tl.(map[string]any)["name"].(string)] = true
	}
	if len(got) != 4 {
		t.Fatalf("tools = %v, want exactly decompress, retrieve, search_context, ctx_status", got)
	}
	for _, want := range []string{"decompress", "retrieve", "search_context", ctxStatusToolName} {
		if !got[want] {
			t.Fatalf("tools/list lacks %s", want)
		}
	}
	if got["compress"] {
		t.Fatal("compress is exposed over MCP; the companion is read and status only")
	}
}

func TestMCPToolCallsReadTheLatestSession(t *testing.T) {
	m, _ := mcpEnv(t, true)
	out := exchange(t, m,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ctx_status"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"retrieve","arguments":{"ref":"m00002"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_context","arguments":{"query":"matcher"}}}`,
	)
	if len(out) != 3 {
		t.Fatalf("got %d responses, want 3", len(out))
	}
	status := toolText(t, out[0])
	if status["messages"].(float64) != 30 {
		t.Fatalf("ctx_status messages = %v, want 30", status["messages"])
	}
	retrieved := toolText(t, out[1])
	if !strings.HasPrefix(retrieved["text"].(string), "message 2 ") {
		t.Fatalf("retrieve text = %v", retrieved["text"])
	}
	// No summary says "matcher" in this session, so the search finds nothing.
	hits := toolText(t, out[2])["hits"].([]any)
	if len(hits) != 0 {
		t.Fatalf("search_context hits = %v, want none", hits)
	}
}

// toolText unwraps a successful tools/call result and decodes its JSON text.
func toolText(t *testing.T, line string) map[string]any {
	t.Helper()
	res := parseOne(t, line)["result"].(map[string]any)
	if res["isError"] != false {
		t.Fatalf("tool result is an error: %v", res)
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("tool text is not JSON: %v: %s", err, text)
	}
	return v
}

func TestMCPWithoutSessionCreatesOneForTheChat(t *testing.T) {
	m, _ := mcpEnv(t, false)
	out := exchange(t, m, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ctx_status"}}`)
	res := parseOne(t, out[0])
	if _, bad := res["error"]; bad {
		t.Fatalf("no session produced a protocol error: %v", res)
	}
	r := res["result"].(map[string]any)
	if r["isError"] != false || !strings.Contains(r["content"].([]any)[0].(map[string]any)["text"].(string), `"sessionId":"mcp-`) {
		t.Fatalf("ctx_status should report the MCP-created session, got %v", r)
	}
}

func TestMCPProtocolErrors(t *testing.T) {
	m, _ := mcpEnv(t, false)
	cases := []struct {
		name string
		line string
		code float64
		id   any
	}{
		{"parse error", `{"jsonrpc":"2.0",`, -32700, nil},
		{"not an object", `42`, -32600, nil},
		{"missing version", `{"id":1,"method":"ping"}`, -32600, float64(1)},
		{"empty method", `{"jsonrpc":"2.0","id":1,"method":""}`, -32600, float64(1)},
		{"object id", `{"jsonrpc":"2.0","id":{},"method":"ping"}`, -32600, nil},
		{"string params", `{"jsonrpc":"2.0","id":1,"method":"ping","params":"x"}`, -32600, float64(1)},
		{"unknown method", `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, -32601, float64(1)},
		{"unknown tool", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"compress"}}`, -32602, float64(1)},
		{"tool without name", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`, -32602, float64(1)},
		{"bad initialize params", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":[1]}`, -32602, float64(1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := exchange(t, m, c.line)
			if len(out) != 1 {
				t.Fatalf("got %d responses: %v", len(out), out)
			}
			res := parseOne(t, out[0])
			errObj, ok := res["error"].(map[string]any)
			if !ok {
				t.Fatalf("no error object: %s", out[0])
			}
			if errObj["code"] != c.code {
				t.Fatalf("code = %v, want %v (%s)", errObj["code"], c.code, out[0])
			}
			if res["id"] != c.id {
				t.Fatalf("id = %v, want %v", res["id"], c.id)
			}
		})
	}
}

func TestMCPNullIDIsARequestNotANotification(t *testing.T) {
	m, _ := mcpEnv(t, false)
	out := exchange(t, m, `{"jsonrpc":"2.0","id":null,"method":"ping"}`)
	if len(out) != 1 {
		t.Fatalf("null-id request got %d responses, want 1", len(out))
	}
	if !strings.Contains(out[0], `"id":null`) {
		t.Fatalf("response does not echo null id: %s", out[0])
	}
}

func TestMCPBatches(t *testing.T) {
	m, _ := mcpEnv(t, false)
	out := exchange(t, m, `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"nope"}]`)
	if len(out) != 1 {
		t.Fatalf("batch produced %d lines, want 1", len(out))
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out[0]), &arr); err != nil {
		t.Fatalf("batch response is not an array: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("batch response has %d entries, want 2 (notification omitted)", len(arr))
	}

	out = exchange(t, m, `[]`, `[{"jsonrpc":"2.0","method":"notifications/x"}]`)
	if len(out) != 1 || !strings.Contains(out[0], `-32600`) {
		t.Fatalf("empty batch output = %v", out)
	}
}

func TestMCPBlankLinesAndOversizeFrames(t *testing.T) {
	m, _ := mcpEnv(t, false)
	out := exchange(t, m, "", "   ", `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if len(out) != 1 {
		t.Fatalf("blank lines produced output: %v", out)
	}

	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("a", maxFrameBytes+10) + `"}}`
	out = exchange(t, m, big, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if len(out) != 2 {
		t.Fatalf("oversize frame: got %d responses, want 2", len(out))
	}
	if !strings.Contains(out[0], "size limit") {
		t.Fatalf("oversize frame response = %s", out[0])
	}
	if !strings.Contains(out[1], `"id":2`) {
		t.Fatalf("frame after the oversize one was not served: %s", out[1])
	}
}

func TestMCPFinalLineWithoutNewline(t *testing.T) {
	m, _ := mcpEnv(t, false)
	var out bytes.Buffer
	err := m.Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"id":1`) {
		t.Fatalf("unterminated final line not served: %q", out.String())
	}
}

func TestMCPStopsOnCancel(t *testing.T) {
	m, _ := mcpEnv(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := m.Serve(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &out); err == nil {
		t.Fatal("cancelled serve returned nil")
	}
}

// signalReader reports every Read call on calls before it reads.
type signalReader struct {
	r     io.Reader
	calls chan struct{}
}

func (s signalReader) Read(p []byte) (int, error) {
	s.calls <- struct{}{}
	return s.r.Read(p)
}

// TestMCPStopsOnCancelWhileIdle covers a client that keeps stdin open and
// sends nothing: cancelling ctx (SIGTERM) must still end Serve.
func TestMCPStopsOnCancelWhileIdle(t *testing.T) {
	m, _ := mcpEnv(t, false)
	pr, pw := io.Pipe()
	defer pw.Close()
	in := signalReader{r: pr, calls: make(chan struct{}, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	errc := make(chan error, 1)
	go func() { errc <- m.Serve(ctx, in, &out) }()
	go func() { _, _ = pw.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")) }()
	// The first Read gets the ping; the second one blocks on the idle pipe.
	for i := 0; i < 2; i++ {
		select {
		case <-in.calls:
		case <-time.After(5 * time.Second):
			t.Fatal("Serve did not read stdin")
		}
	}
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel while stdin was idle")
	}
}

func TestNewMCPRejectsMissingInputs(t *testing.T) {
	e := newEnv(t)
	if _, err := NewMCP(MCPConfig{DBPath: e.dbPath, Cwd: e.dir}); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := NewMCP(MCPConfig{Store: e.st, Cwd: e.dir}); err == nil {
		t.Fatal("empty db path accepted")
	}
	m, err := NewMCP(MCPConfig{Store: e.st, DBPath: e.dbPath})
	if err != nil {
		t.Fatalf("default working directory: %v", err)
	}
	_ = m.Close()
}
