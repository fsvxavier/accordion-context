package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// stubMCP is an MCP server whose tools never touch a database. The fuzz target
// uses it so each iteration is pure decode and dispatch.
func stubMCP() *MCP {
	return &MCP{backend: func(_ context.Context, name string, _ json.RawMessage) (any, error) {
		if name == "retrieve" {
			return nil, errBadArgs
		}
		return map[string]any{"tool": name}, nil
	}}
}

// FuzzDecodeFrame feeds arbitrary stdio frames through decode, dispatch and
// marshal. Nothing may panic, and every response must be one line of valid
// JSON-RPC 2.0.
func FuzzDecodeFrame(f *testing.F) {
	seeds := []string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":null,"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"ping"}`,
		`{"jsonrpc":"2.0","id":{},"method":"ping"}`,
		`{"jsonrpc":"2.0","id":true,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":42}`,
		`{"jsonrpc":"2.0","id":1,"method":""}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping","params":"str"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"retrieve","arguments":{"ref":"m1"}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x\n"}}`,
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		`[]`,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"},42,null]`,
		`[{"jsonrpc":"2.0","method":"notifications/x"}]`,
		`42`,
		`null`,
		`"string"`,
		`{"jsonrpc":"2.0",`,
		"",
		"   ",
		`{"jsonrpc":"2.0","id":"a\u0000b","method":"ping"}`,
		`{"jsonrpc":"2.0","id":1e400,"method":"ping"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	m := stubMCP()
	f.Fuzz(func(t *testing.T, data []byte) {
		resp := handleFrame(context.Background(), data, m.route)
		if resp == nil {
			return
		}
		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("response does not marshal: %v", err)
		}
		if strings.ContainsAny(string(b), "\n\r") {
			t.Fatalf("response spans lines: %q", b)
		}
		if !json.Valid(b) {
			t.Fatalf("response is not valid JSON: %q", b)
		}
		var check any
		if err := json.Unmarshal(b, &check); err != nil {
			t.Fatal(err)
		}
	})
}

// FuzzServeStdio drives the whole loop, framing included, on arbitrary bytes.
func FuzzServeStdio(f *testing.F) {
	f.Add([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n{\"jsonrpc\":\"2.0\",\"id\":2"))
	f.Add([]byte("\r\n\r\n[]\n"))
	m := stubMCP()
	f.Fuzz(func(t *testing.T, data []byte) {
		var out strings.Builder
		if err := serveStdio(context.Background(), strings.NewReader(string(data)), &out, m.route); err != nil {
			t.Fatalf("serveStdio failed on input: %v", err)
		}
		for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
			if line == "" {
				continue
			}
			if !json.Valid([]byte(line)) {
				t.Fatalf("output line is not JSON: %q", line)
			}
		}
	})
}
