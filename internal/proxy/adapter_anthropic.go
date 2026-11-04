package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/wire/anthropic"
)

// pathMessages is the Anthropic Messages endpoint (spec §19.1).
const pathMessages = "/v1/messages"

// anthropicProtocol adapts POST /v1/messages. It is the only dialect whose
// streams are live: the Forwarder withholds internal tool_use blocks as they pass.
type anthropicProtocol struct{}

func (anthropicProtocol) Name() string { return "anthropic" }

func (anthropicProtocol) Detect(path string) bool { return path == pathMessages }

func (anthropicProtocol) Meta(_, _ string, body []byte) (string, bool, error) {
	var meta struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return "", false, err
	}
	return meta.Model, meta.Stream, nil
}

func (anthropicProtocol) Decode(body []byte) (request, []kernel.Message, error) {
	req, msgs, err := anthropic.Decode(body)
	if err != nil {
		return nil, nil, err
	}
	return encoderFunc(func(window []kernel.Message) ([]byte, error) {
		return anthropic.Encode(req, window)
	}), msgs, nil
}

func (anthropicProtocol) InjectTools(body []byte) ([]byte, error) {
	return anthropic.InjectTools(body)
}

func (anthropicProtocol) AppendNudge(body []byte, text string) ([]byte, error) {
	return appendToLastUser(body, text)
}

func (anthropicProtocol) ParseTool(name string, input []byte) (toolArgs, error) {
	switch name {
	case toolCompress:
		args, err := anthropic.ParseCompressArgs(input)
		if err != nil {
			return toolArgs{}, err
		}
		out := toolArgs{}
		for _, r := range args.Ranges {
			out.ranges = append(out.ranges, compressRange(r.StartSeq, r.EndSeq, r.Summary, r.Topic))
		}
		return out, nil
	case toolDecompress:
		var a struct {
			BlockID string `json:"blockId"`
			Mode    string `json:"mode"`
			Query   string `json:"query"`
		}
		if err := json.Unmarshal(input, &a); err != nil {
			return toolArgs{}, fmt.Errorf("decompress args: %w", err)
		}
		return toolArgs{block: a.BlockID, mode: a.Mode, query: a.Query}, nil
	}
	return toolArgs{}, fmt.Errorf("unknown tool %q", name)
}

func (anthropicProtocol) Live() bool { return true }

// Read handles a complete JSON answer. Live streams go through streamRound, so
// stream is never true here.
func (anthropicProtocol) Read(data []byte, _ bool) reply {
	rr := reply{final: data, complete: true}
	if u, ok, err := anthropic.ParseUsage(data); err == nil && ok {
		rr.usage = &usage{input: u.TotalInput(), cached: u.CacheReadInputTokens}
	}
	var msg struct {
		Content []json.RawMessage `json:"content"`
	}
	if json.Unmarshal(data, &msg) != nil {
		return rr
	}
	rr.assistant, _ = json.Marshal(msg.Content)
	for _, raw := range msg.Content {
		var b toolUseWire
		if json.Unmarshal(raw, &b) != nil || b.Type != "tool_use" {
			continue
		}
		if isInternal(b.Name) {
			rr.calls = append(rr.calls, toolCall{id: b.ID, name: b.Name, input: b.Input})
		} else {
			rr.clientCalls++
		}
	}
	if len(rr.calls) > 0 {
		if out, err := stripInternal(data); err == nil {
			rr.final = out
		}
	}
	return rr
}

func (anthropicProtocol) Continue(body []byte, rr reply, results []toolResult) ([]byte, error) {
	blocks := make([]json.RawMessage, 0, len(results))
	for _, res := range results {
		raw, err := json.Marshal(toolResultWire{Type: "tool_result", ToolUseID: res.id, Content: res.text, IsError: res.isErr})
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, raw)
	}
	a, err := json.Marshal(map[string]any{"role": "assistant", "content": rr.assistant})
	if err != nil {
		return nil, err
	}
	u, err := json.Marshal(map[string]any{"role": "user", "content": blocks})
	if err != nil {
		return nil, err
	}
	return appendArray(body, "messages", a, u)
}

// toolUseWire is the wire form of a tool_use block (also used for replay).
type toolUseWire struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

type toolResultWire struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// stripInternal removes internal tool_use blocks from a JSON message and turns
// stop_reason tool_use into end_turn when nothing else is pending.
func stripInternal(data []byte) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, err
	}
	var content []json.RawMessage
	if err := json.Unmarshal(top["content"], &content); err != nil {
		return nil, err
	}
	kept := make([]json.RawMessage, 0, len(content))
	pending := false
	for _, raw := range content {
		var b toolUseWire
		if json.Unmarshal(raw, &b) == nil && b.Type == "tool_use" {
			if isInternal(b.Name) {
				continue
			}
			pending = true
		}
		kept = append(kept, raw)
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return nil, err
	}
	top["content"] = out
	if !pending {
		var stop string
		if json.Unmarshal(top["stop_reason"], &stop) == nil && stop == "tool_use" {
			top["stop_reason"] = json.RawMessage(`"end_turn"`)
		}
	}
	return json.Marshal(top)
}

// appendToLastUser adds a text block to the last user message (upstream only).
func appendToLastUser(body []byte, text string) ([]byte, error) {
	return editArray(body, "messages", func(msgs []json.RawMessage) ([]json.RawMessage, error) {
		if len(msgs) == 0 {
			return nil, errors.New("no messages")
		}
		var last struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(msgs[len(msgs)-1], &last); err != nil {
			return nil, err
		}
		if last.Role != "user" {
			return nil, errors.New("last message is not user")
		}
		block, err := json.Marshal(map[string]string{"type": "text", "text": text})
		if err != nil {
			return nil, err
		}
		last.Content = append(last.Content, block)
		out, err := json.Marshal(last)
		if err != nil {
			return nil, err
		}
		msgs[len(msgs)-1] = out
		return msgs, nil
	})
}

// streamRound forwards one upstream SSE response as it arrives. Internal blocks
// are withheld and their calls run after the round. When the round continues
// server-side, the client keeps receiving the same message, and only the
// message end waits for the last round.
func (s *Server) streamRound(w http.ResponseWriter, resp *http.Response, t *turn, round int, fw *anthropic.Forwarder) bool {
	defer resp.Body.Close()
	if !t.begun {
		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(http.StatusOK)
		t.begun = true
	}
	fw.BeginRound()
	sse := anthropic.NewSSEReader(resp.Body)
	ended := false
	for {
		ev, err := sse.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.cfg.Logger.Error("upstream stream broken", "session", t.sid, "err", err)
			s.abort(w, t, "upstream stream broken", http.StatusBadGateway)
			return true
		}
		if ev.Name == "message_stop" || ev.Name == "error" {
			// An upstream error event is terminal too, so no second one is added.
			ended = true
		}
		if err := fw.Feed(ev); err != nil {
			s.cfg.Logger.Warn("client stream write failed", "session", t.sid, "err", err)
			return true
		}
	}

	if !ended {
		// The upstream closed the stream before message_stop: the round is cut
		// short and must not be finished as if it were complete.
		s.cfg.Logger.Error("upstream stream truncated", "session", t.sid, "round", round)
		s.abort(w, t, "upstream stream truncated", http.StatusBadGateway)
		return true
	}

	res := fw.EndRound()
	s.recordCache(t, usage{input: res.Usage.TotalInput(), cached: res.Usage.CacheReadInputTokens})
	if len(res.Internal) == 0 {
		if err := fw.Finish(false); err != nil {
			s.cfg.Logger.Warn("client stream write failed", "session", t.sid, "err", err)
		}
		return true
	}

	results := make([]toolResult, 0, len(res.Internal))
	for _, u := range res.Internal {
		results = append(results, s.runTool(t, toolCall{id: u.ID, name: u.Name, input: []byte(u.Input)}))
	}
	if res.ClientTools > 0 || round+1 >= s.cfg.MaxLoop {
		// The round cannot continue. The internal blocks were already withheld from the client.
		s.cfg.Logger.Warn("internal tool_use not continued", "session", t.sid,
			"client_tools", res.ClientTools, "round", round)
		if err := fw.Finish(res.ClientTools == 0); err != nil {
			s.cfg.Logger.Warn("client stream write failed", "session", t.sid, "err", err)
		}
		return true
	}

	content, err := json.Marshal(res.Content)
	if err != nil {
		s.cfg.Logger.Error("append tool round failed", "session", t.sid, "err", err)
		_ = fw.Finish(true)
		return true
	}
	next, err := t.proto.Continue(t.body, reply{assistant: content}, results)
	if err != nil {
		s.cfg.Logger.Error("append tool round failed", "session", t.sid, "err", err)
		_ = fw.Finish(true)
		return true
	}
	t.body = next
	return false
}
