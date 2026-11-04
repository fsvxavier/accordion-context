package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	systemPrompt = "You are a coding agent. Answer briefly and keep the transcript facts exact."
	// smallPayload and bigPayload size the read_file results. Small ones stay under
	// the CCR threshold (ccr.threshold_tokens, 4000 tokens); big ones go through CCR.
	smallPayload = 8_000
	bigPayload   = 48_000
	bigEvery     = 8
	// Turns of the scripted session. The raw transcript must exceed twice the window.
	scenarioTurns = 80
)

var readFileTool = map[string]any{
	"name":        "read_file",
	"description": "Read a source file of the project.",
	"input_schema": map[string]any{
		"type":       "object",
		"properties": map[string]any{"path": map[string]any{"type": "string"}},
		"required":   []string{"path"},
	},
}

var reModPath = regexp.MustCompile(`src/mod(\d{3})\.go`)

// payloadFor is the read_file result of module n: a fact line with a raw value,
// padded with filler. Every eighth module is big.
func payloadFor(n int) string {
	size := smallPayload
	if n%bigEvery == 0 {
		size = bigPayload
	}
	var b strings.Builder
	fmt.Fprintf(&b, "// src/mod%03d.go\n", n)
	fmt.Fprintf(&b, "FACT-%04d VALUE[FACT-%04d]=%s\n", n, n, factValue(n))
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, "line %d of module %d: lorem ipsum dolor sit amet consectetur adipiscing elit\n", i, n)
	}
	return b.String()
}

// client is a scripted coding agent. It keeps the transcript and sends it whole,
// the way Claude Code and other agents do.
type client struct {
	t       *testing.T
	base    string // proxy base including the /p/<project> prefix
	dialect string
	model   string
	msgs    []map[string]any // anthropic messages
	oai     []map[string]any // openai messages, the system message first
	leaks   int              // internal tool calls the client saw
	calls   int              // upstream answers the client received
	lastReq []byte           // last request body, kept for failure messages
}

func newClient(t *testing.T, base, dialect, model string) *client {
	c := &client{t: t, base: base, dialect: dialect, model: model}
	if dialect == "openai" {
		c.oai = []map[string]any{{"role": "system", "content": systemPrompt}}
	}
	return c
}

// turn sends one user message and runs the tool loop until the model answers in text.
// It returns the text answer.
func (c *client) turn(text string) string {
	c.t.Helper()
	if c.dialect == "openai" {
		c.oai = append(c.oai, map[string]any{"role": "user", "content": text})
		return c.turnOpenAI()
	}
	c.msgs = append(c.msgs, map[string]any{"role": "user",
		"content": []map[string]any{{"type": "text", "text": text}}})
	return c.turnAnthropic()
}

func (c *client) post(path string, body any) []byte {
	c.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		c.t.Fatal(err)
	}
	c.lastReq = raw
	req, err := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.dialect == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("x-api-key", "e2e-not-a-secret")
	} else {
		req.Header.Set("Authorization", "Bearer e2e-not-a-secret")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("read answer: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("POST %s: status %d: %s", path, resp.StatusCode, data)
	}
	c.calls++
	return data
}

func (c *client) turnAnthropic() string {
	for round := 0; round < 8; round++ {
		raw := c.post("/v1/messages", map[string]any{
			"model": c.model, "max_tokens": 1024, "system": systemPrompt,
			"tools": []any{readFileTool}, "messages": c.msgs, "stream": true,
		})
		content, stop := parseAnthropicStream(c.t, raw)
		for _, b := range content {
			if b["type"] == "tool_use" {
				if n, _ := b["name"].(string); n == "compress" || n == "decompress" {
					c.leaks++
				}
			}
		}
		c.msgs = append(c.msgs, map[string]any{"role": "assistant", "content": content})
		if stop != "tool_use" {
			var text strings.Builder
			for _, b := range content {
				if b["type"] == "text" {
					text.WriteString(b["text"].(string))
				}
			}
			return text.String()
		}
		var results []map[string]any
		for _, b := range content {
			if b["type"] != "tool_use" {
				continue
			}
			results = append(results, map[string]any{"type": "tool_result", "tool_use_id": b["id"],
				"content": readResult(c.t, b)})
		}
		c.msgs = append(c.msgs, map[string]any{"role": "user", "content": results})
	}
	c.t.Fatal("anthropic: the client's tool loop did not end")
	return ""
}

func (c *client) turnOpenAI() string {
	for round := 0; round < 8; round++ {
		raw := c.post("/v1/chat/completions", map[string]any{
			"model": c.model, "messages": c.oai, "stream": false,
			"tools": []any{map[string]any{"type": "function", "function": map[string]any{
				"name": readFileTool["name"], "description": readFileTool["description"],
				"parameters": readFileTool["input_schema"]}}},
		})
		var out struct {
			Choices []struct {
				Message struct {
					Role      string            `json:"role"`
					Content   *string           `json:"content"`
					ToolCalls []json.RawMessage `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
			c.t.Fatalf("openai answer: %v: %s", err, raw)
		}
		msg := out.Choices[0].Message
		echo := map[string]any{"role": "assistant", "content": msg.Content}
		if len(msg.ToolCalls) > 0 {
			echo["tool_calls"] = msg.ToolCalls
		}
		c.oai = append(c.oai, echo)
		if len(msg.ToolCalls) == 0 {
			if msg.Content == nil {
				return ""
			}
			return *msg.Content
		}
		for _, raw := range msg.ToolCalls {
			var tc struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}
			if err := json.Unmarshal(raw, &tc); err != nil {
				c.t.Fatal(err)
			}
			if tc.Function.Name == "compress" || tc.Function.Name == "decompress" {
				c.leaks++
			}
			c.oai = append(c.oai, map[string]any{"role": "tool", "tool_call_id": tc.ID,
				"content": readResult(c.t, map[string]any{"input": json.RawMessage(tc.Function.Arguments)})})
		}
	}
	c.t.Fatal("openai: the client's tool loop did not end")
	return ""
}

// readResult runs the client tool: read_file returns the module's payload.
func readResult(t *testing.T, b map[string]any) string {
	t.Helper()
	var input struct {
		Path string `json:"path"`
	}
	switch in := b["input"].(type) {
	case json.RawMessage:
		if err := json.Unmarshal(in, &input); err != nil {
			t.Fatalf("read_file input: %v", err)
		}
	case map[string]any:
		raw, _ := json.Marshal(in)
		_ = json.Unmarshal(raw, &input)
	}
	m := reModPath.FindStringSubmatch(input.Path)
	if m == nil {
		t.Fatalf("read_file: unexpected path %q", input.Path)
	}
	n, _ := strconv.Atoi(m[1])
	return payloadFor(n)
}

// parseAnthropicStream rebuilds the content blocks of one streamed answer, in index order.
func parseAnthropicStream(t *testing.T, data []byte) ([]map[string]any, string) {
	t.Helper()
	type acc struct {
		block   map[string]any
		partial strings.Builder
	}
	blocks := map[int]*acc{}
	stop := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Text()
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("stream event: %v", err)
		}
		typ, _ := ev["type"].(string)
		switch typ {
		case "error":
			t.Fatalf("stream error event: %s", payload)
		case "content_block_start":
			idx := int(ev["index"].(float64))
			cb, _ := ev["content_block"].(map[string]any)
			a := &acc{block: map[string]any{}}
			for k, v := range cb {
				a.block[k] = v
			}
			blocks[idx] = a
		case "content_block_delta":
			idx := int(ev["index"].(float64))
			a := blocks[idx]
			d, _ := ev["delta"].(map[string]any)
			switch d["type"] {
			case "text_delta":
				a.block["text"] = a.block["text"].(string) + d["text"].(string)
			case "thinking_delta":
				a.block["thinking"] = a.block["thinking"].(string) + d["thinking"].(string)
			case "signature_delta":
				a.block["signature"] = d["signature"]
			case "input_json_delta":
				a.partial.WriteString(d["partial_json"].(string))
			}
		case "message_delta":
			d, _ := ev["delta"].(map[string]any)
			stop, _ = d["stop_reason"].(string)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("stream read: %v", err)
	}
	if stop == "" {
		t.Fatalf("stream ended without message_delta:\n%s", data)
	}
	var idxs []int
	for i := range blocks {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	var out []map[string]any
	for _, i := range idxs {
		a := blocks[i]
		if a.block["type"] == "tool_use" {
			var in json.RawMessage = json.RawMessage("{}")
			if a.partial.Len() > 0 {
				in = json.RawMessage(a.partial.String())
			}
			a.block["input"] = in
		}
		out = append(out, a.block)
	}
	return out, stop
}
