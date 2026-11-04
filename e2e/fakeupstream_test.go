package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// callRecord is what the fake upstream saw in one request and what it answered.
type callRecord struct {
	body      []byte
	dialect   string
	total     int    // tokens of the body: kernel.EstimateTokens over the raw bytes
	cached    int    // simulated cache read
	nudge     bool   // the last user message carried a context_nudge
	answer    string // "compress", "decompress", "read_file", "text"
	fromNudge bool   // the answer was a compress made for a nudge
	msgs      []nmsg
	raw       map[string]json.RawMessage
}

// fakeUpstream stands in for the provider API. It records every request body and
// answers like a coding agent's model (see decide).
type fakeUpstream struct {
	t       *testing.T
	dialect string // "anthropic" or "openai"
	srv     *httptest.Server

	mu     sync.Mutex
	calls  []callRecord
	seq    int
	issued map[string]bool // thinking signatures the fake issued (anthropic)
}

func newFakeUpstream(t *testing.T, dialect string) *fakeUpstream {
	f := &fakeUpstream{t: t, dialect: dialect}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeUpstream) URL() string { return f.srv.URL }

// issuedSig reports whether the fake issued sig.
func (f *fakeUpstream) issuedSig(sig string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issued[sig]
}

// snapshot returns a copy of the recorded calls.
func (f *fakeUpstream) snapshot() []callRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]callRecord(nil), f.calls...)
}

func (f *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	wantPath := "/v1/messages"
	if f.dialect == "openai" {
		wantPath = "/v1/chat/completions"
	}
	if r.URL.Path != wantPath {
		http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		return
	}
	msgs, tools, stream, raw, err := parseRequest(f.dialect, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	if f.issued == nil {
		f.issued = map[string]bool{}
	}
	f.issued[sigFor(fmt.Sprintf("%06d", f.seq+1))] = true
	total := kernel.EstimateTokens(string(body))
	cached := 0
	if prev := f.lastCall(); prev != nil && samePrefix(prev.raw, raw) {
		cached = prev.total * 9 / 10
	}
	act := decide(msgs, tools)
	f.seq++
	id := fmt.Sprintf("%06d", f.seq)
	rec := callRecord{body: body, dialect: f.dialect, total: total, cached: cached,
		nudge: len(msgs) > 0 && hasNudge(msgs[len(msgs)-1]), msgs: msgs, raw: raw}
	rec.answer = "text"
	if act.name != "" {
		rec.answer = act.name
	}
	rec.fromNudge = act.name == "compress" && rec.nudge
	f.calls = append(f.calls, rec)
	f.mu.Unlock()

	if f.dialect == "anthropic" {
		f.answerAnthropic(w, id, act, total, cached, stream)
	} else {
		f.answerOpenAI(w, id, act, total, cached)
	}
}

// lastCall returns the previous record; f.mu must be held.
func (f *fakeUpstream) lastCall() *callRecord {
	if len(f.calls) == 0 {
		return nil
	}
	return &f.calls[len(f.calls)-1]
}

// samePrefix reports whether the system field and the first message are byte-identical.
func samePrefix(a, b map[string]json.RawMessage) bool {
	if !bytes.Equal(a["system"], b["system"]) {
		return false
	}
	fa, fb := firstMsg(a), firstMsg(b)
	return fa != nil && bytes.Equal(fa, fb)
}

func firstMsg(top map[string]json.RawMessage) json.RawMessage {
	var list []json.RawMessage
	if json.Unmarshal(top["messages"], &list) != nil || len(list) == 0 {
		return nil
	}
	return list[0]
}

// parseRequest reads one request of either dialect into the neutral form.
func parseRequest(dialect string, body []byte) ([]nmsg, map[string]bool, bool, map[string]json.RawMessage, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, nil, false, nil, err
	}
	var meta struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &meta)
	tools := map[string]bool{}
	var msgs []nmsg
	if dialect == "anthropic" {
		var req struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, nil, false, nil, err
		}
		for _, t := range req.Tools {
			tools[t.Name] = true
		}
		for _, m := range req.Messages {
			msgs = append(msgs, anthropicMsg(m.Role, m.Content))
		}
		return msgs, tools, meta.Stream, top, nil
	}
	var req struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		Messages []struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			ToolCallID string          `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, nil, false, nil, err
	}
	for _, t := range req.Tools {
		tools[t.Function.Name] = true
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			continue
		case "tool":
			msgs = append(msgs, nmsg{role: "tool", results: []nres{{id: m.ToolCallID, content: flattenContent(m.Content)}}})
		case "assistant":
			nm := nmsg{role: "assistant"}
			if t := flattenContent(m.Content); t != "" {
				nm.texts = []string{t}
			}
			for _, tc := range m.ToolCalls {
				nm.calls = append(nm.calls, ncall{id: tc.ID, name: tc.Function.Name, args: tc.Function.Arguments})
			}
			msgs = append(msgs, nm)
		default:
			msgs = append(msgs, nmsg{role: "user", texts: []string{flattenContent(m.Content)}})
		}
	}
	return msgs, tools, meta.Stream, top, nil
}

// anthropicMsg reads one Messages API message: text, tool_use and tool_result blocks.
func anthropicMsg(role string, content json.RawMessage) nmsg {
	nm := nmsg{role: role}
	var s string
	if json.Unmarshal(content, &s) == nil {
		nm.texts = []string{s}
		return nm
	}
	var blocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
	}
	_ = json.Unmarshal(content, &blocks)
	for _, b := range blocks {
		switch b.Type {
		case "text":
			nm.texts = append(nm.texts, b.Text)
		case "tool_use":
			nm.calls = append(nm.calls, ncall{id: b.ID, name: b.Name, args: string(b.Input)})
		case "tool_result":
			nm.results = append(nm.results, nres{id: b.ToolUseID, content: flattenContent(b.Content)})
		}
	}
	return nm
}

// answerAnthropic writes the answer as an SSE stream, or as one JSON object.
func (f *fakeUpstream) answerAnthropic(w http.ResponseWriter, id string, act action, total, cached int, stream bool) {
	thinking := "Checking the next step of the turn."
	sig := sigFor(id)
	type blk struct {
		kind, text, sig, toolID, name, input string
	}
	blocks := []blk{{kind: "thinking", text: thinking, sig: sig}}
	stop := "end_turn"
	if act.name != "" {
		in, _ := json.Marshal(act.input)
		blocks = append(blocks, blk{kind: "tool_use", toolID: "toolu_" + id, name: act.name, input: string(in)})
		stop = "tool_use"
	} else {
		blocks = append(blocks, blk{kind: "text", text: act.text})
	}
	usage := map[string]any{
		"input_tokens":                total - cached,
		"cache_creation_input_tokens": 0,
		"cache_read_input_tokens":     cached,
		"output_tokens":               1,
	}
	if !stream {
		var content []map[string]any
		for _, b := range blocks {
			switch b.kind {
			case "thinking":
				content = append(content, map[string]any{"type": "thinking", "thinking": b.text, "signature": b.sig})
			case "text":
				content = append(content, map[string]any{"type": "text", "text": b.text})
			case "tool_use":
				content = append(content, map[string]any{"type": "tool_use", "id": b.toolID, "name": b.name,
					"input": json.RawMessage(b.input)})
			}
		}
		usage["output_tokens"] = 40
		writeJSONResp(w, map[string]any{"id": "msg_" + id, "type": "message", "role": "assistant",
			"model": "fake", "content": content, "stop_reason": stop, "stop_sequence": nil, "usage": usage})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	ev := func(name string, v any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	}
	ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_" + id, "type": "message", "role": "assistant", "model": "fake", "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": usage}})
	for i, b := range blocks {
		switch b.kind {
		case "thinking":
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}})
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i,
				"delta": map[string]any{"type": "thinking_delta", "thinking": b.text}})
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i,
				"delta": map[string]any{"type": "signature_delta", "signature": b.sig}})
		case "text":
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "text", "text": ""}})
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i,
				"delta": map[string]any{"type": "text_delta", "text": b.text}})
		case "tool_use":
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "tool_use", "id": b.toolID, "name": b.name, "input": map[string]any{}}})
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": b.input}})
		}
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	ev("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 40}})
	ev("message_stop", map[string]any{"type": "message_stop"})
}

// sigFor is the thinking signature issued with answer id.
func sigFor(id string) string {
	sum := sha256.Sum256([]byte("thinking" + id))
	return "sig-" + hex.EncodeToString(sum[:6])
}

// answerOpenAI writes one Chat Completions JSON object. prompt_tokens includes the cached part.
func (f *fakeUpstream) answerOpenAI(w http.ResponseWriter, id string, act action, total, cached int) {
	msg := map[string]any{"role": "assistant", "content": nil}
	finish := "stop"
	if act.name != "" {
		in, _ := json.Marshal(act.input)
		msg["tool_calls"] = []map[string]any{{"id": "call_" + id, "type": "function",
			"function": map[string]any{"name": act.name, "arguments": string(in)}}}
		finish = "tool_calls"
	} else {
		msg["content"] = act.text
	}
	writeJSONResp(w, map[string]any{"id": "chatcmpl-" + id, "object": "chat.completion", "model": "fake",
		"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": finish}},
		"usage": map[string]any{"prompt_tokens": total, "completion_tokens": 40, "total_tokens": total + 40,
			"prompt_tokens_details": map[string]any{"cached_tokens": cached}}})
}

func writeJSONResp(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
