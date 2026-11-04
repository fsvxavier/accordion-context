package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/wire/openai"
)

// pathChat is the Chat Completions endpoint (spec §19.1).
const pathChat = "/v1/chat/completions"

// openaiProtocol adapts POST /v1/chat/completions. Streams are buffered per round.
type openaiProtocol struct{}

func (openaiProtocol) Name() string { return "openai" }

func (openaiProtocol) Detect(path string) bool { return path == pathChat }

func (openaiProtocol) Meta(_, _ string, body []byte) (string, bool, error) {
	var meta struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return "", false, err
	}
	return meta.Model, meta.Stream, nil
}

func (openaiProtocol) Decode(body []byte) (request, []kernel.Message, error) {
	req, msgs, err := openai.Decode(body)
	if err != nil {
		return nil, nil, err
	}
	return encoderFunc(func(window []kernel.Message) ([]byte, error) {
		return openai.Encode(req, window)
	}), msgs, nil
}

func (openaiProtocol) InjectTools(body []byte) ([]byte, error) {
	return openai.InjectTools(body)
}

// AppendNudge adds a text part to the last user message. When the last message
// is not from the user (typically a tool result), a new user message is appended,
// so the nudge is never dropped.
func (openaiProtocol) AppendNudge(body []byte, text string) ([]byte, error) {
	return editArray(body, "messages", func(msgs []json.RawMessage) ([]json.RawMessage, error) {
		if n := len(msgs); n > 0 && roleOf(msgs[n-1]) == "user" {
			m, err := mapOf(msgs[n-1])
			if err != nil {
				return nil, err
			}
			content, err := appendPart(m["content"], "text", text)
			if err != nil {
				return nil, err
			}
			msgs[n-1], err = setField(msgs[n-1], "content", content)
			return msgs, err
		}
		fresh, err := json.Marshal(map[string]string{"role": "user", "content": text})
		if err != nil {
			return nil, err
		}
		return append(msgs, fresh), nil
	})
}

func (openaiProtocol) ParseTool(name string, input []byte) (toolArgs, error) {
	switch name {
	case toolCompress:
		args, err := openai.ParseCompressArgs(input)
		if err != nil {
			return toolArgs{}, err
		}
		out := toolArgs{}
		for _, r := range args.Ranges {
			out.ranges = append(out.ranges, compressRange(r.StartSeq, r.EndSeq, r.Summary, r.Topic))
		}
		for _, f := range args.Folds {
			out.folds = append(out.folds, session.FoldRange{
				Tier: f.Tier, StartBlock: f.StartBlock, EndBlock: f.EndBlock, Summary: f.Summary, Topic: f.Topic,
			})
		}
		out.closeIntents = args.CloseIntents
		return out, nil
	case toolDecompress:
		args, err := openai.ParseDecompressArgs(input)
		if err != nil {
			return toolArgs{}, err
		}
		return toolArgs{block: args.BlockID, mode: args.Mode, query: args.Query}, nil
	}
	return toolArgs{}, fmt.Errorf("unknown tool %q", name)
}

func (openaiProtocol) Live() bool { return false }

func (openaiProtocol) Read(data []byte, stream bool) reply {
	if stream {
		return openaiStreamRead(data)
	}
	rr := reply{final: data, complete: true}
	if u, ok, err := openai.ParseUsage(data); err == nil && ok {
		rr.usage = &usage{input: u.PromptTokens, cached: u.CachedTokens}
	}
	m, err := decodeObject(data)
	if err != nil {
		return rr
	}
	choices, _ := m["choices"].([]any)
	if len(choices) == 0 {
		return rr
	}
	for ci, cv := range choices {
		msg := choiceMessage(cv)
		tcs, _ := msg["tool_calls"].([]any)
		for _, tv := range tcs {
			name, id, args := callFields(tv)
			if !isInternal(name) {
				rr.clientCalls++
				continue
			}
			rr.calls = append(rr.calls, toolCall{id: id, name: name, input: []byte(args)})
			if ci != 0 {
				// Only the first choice can be continued; the others are stripped.
				rr.clientCalls++
			}
		}
	}
	if len(rr.calls) == 0 {
		return rr
	}
	first, _ := json.Marshal(choiceMessage(choices[0]))
	rr.assistant = first
	for _, cv := range choices {
		ch, _ := cv.(map[string]any)
		msg := choiceMessage(cv)
		stripCalls(msg, ch)
	}
	rr.final = encodeObject(m)
	return rr
}

// choiceMessage returns the message object of a choice, or nil.
func choiceMessage(cv any) map[string]any {
	ch, _ := cv.(map[string]any)
	msg, _ := ch["message"].(map[string]any)
	return msg
}

// callFields reads the name, id and arguments of one tool_calls entry.
func callFields(tv any) (name, id, args string) {
	tc, _ := tv.(map[string]any)
	fn, _ := tc["function"].(map[string]any)
	name, _ = fn["name"].(string)
	id, _ = tc["id"].(string)
	args, _ = fn["arguments"].(string)
	return name, id, args
}

// stripCalls removes internal entries from a message. When none is left, the
// choice finishes as stop, not tool_calls, because the client has nothing to run.
func stripCalls(msg, ch map[string]any) {
	if msg == nil {
		return
	}
	tcs, _ := msg["tool_calls"].([]any)
	kept := make([]any, 0, len(tcs))
	for _, tv := range tcs {
		if name, _, _ := callFields(tv); !isInternal(name) {
			kept = append(kept, tv)
		}
	}
	if len(kept) == len(tcs) {
		return
	}
	if len(kept) == 0 {
		delete(msg, "tool_calls")
		if ch != nil && ch["finish_reason"] == "tool_calls" {
			ch["finish_reason"] = "stop"
		}
		return
	}
	msg["tool_calls"] = kept
}

// openaiStreamRead reads a buffered Chat Completions stream. The codec reports the
// internal calls, usage and completion. A second pass removes the internal deltas
// and counts the client calls, which the codec does not report.
func openaiStreamRead(data []byte) reply {
	sum, _ := openai.StreamPassthrough(io.Discard, bytes.NewReader(data))
	rr := reply{complete: sum.Complete}
	if sum.HasUsage {
		rr.usage = &usage{input: sum.Usage.PromptTokens, cached: sum.Usage.CachedTokens}
	}
	type key struct{ choice, index int }
	internal := map[key]bool{}
	var text []byte
	var toolCalls []any
	for _, c := range sum.Calls {
		internal[key{c.Choice, c.Index}] = true
		rr.calls = append(rr.calls, toolCall{id: c.ID, name: c.Name, input: []byte(c.Arguments)})
		if c.Choice == 0 {
			toolCalls = append(toolCalls, map[string]any{
				"id": c.ID, "type": "function",
				"function": map[string]any{"name": c.Name, "arguments": c.Arguments},
			})
		} else {
			rr.clientCalls++
		}
	}
	if len(rr.calls) == 0 {
		rr.final = data
		return rr
	}

	items := parseSSE(data)
	objs := make([]map[string]any, len(items))
	for i := range items {
		if items[i].payload == nil || string(items[i].payload) == "[DONE]" {
			continue
		}
		if m, err := decodeObject(items[i].payload); err == nil {
			objs[i] = m
		}
	}
	clientKeys := map[key]bool{}
	changed := make([]bool, len(items))
	for i, m := range objs {
		if m == nil {
			continue
		}
		choices, _ := m["choices"].([]any)
		for _, cv := range choices {
			ch, _ := cv.(map[string]any)
			ci, _ := intField(ch, "index")
			delta, _ := ch["delta"].(map[string]any)
			if s, ok := delta["content"].(string); ok && ci == 0 {
				text = append(text, s...)
			}
			tcs, _ := delta["tool_calls"].([]any)
			if len(tcs) == 0 {
				continue
			}
			kept := make([]any, 0, len(tcs))
			for pos, tv := range tcs {
				tc, _ := tv.(map[string]any)
				idx, ok := intField(tc, "index")
				if !ok {
					idx = pos
				}
				k := key{ci, idx}
				if internal[k] {
					changed[i] = true
					continue
				}
				clientKeys[k] = true
				kept = append(kept, tv)
			}
			if len(kept) != len(tcs) {
				changed[i] = true
				if len(kept) == 0 {
					delete(delta, "tool_calls")
				} else {
					delta["tool_calls"] = kept
				}
			}
		}
	}
	if len(clientKeys) == 0 && rr.clientCalls == 0 {
		// The round only has internal calls, so it must not finish as tool_calls.
		for i, m := range objs {
			if m == nil {
				continue
			}
			choices, _ := m["choices"].([]any)
			for _, cv := range choices {
				if ch, _ := cv.(map[string]any); ch["finish_reason"] == "tool_calls" {
					ch["finish_reason"] = "stop"
					changed[i] = true
				}
			}
		}
	}
	rr.clientCalls += len(clientKeys)

	msg := map[string]any{"role": "assistant", "content": nil, "tool_calls": toolCalls}
	if len(text) > 0 {
		msg["content"] = string(text)
	}
	rr.assistant = encodeObject(msg)

	for i := range items {
		if changed[i] {
			items[i].setPayload(encodeObject(objs[i]))
		}
	}
	rr.final = renderSSE(items)
	return rr
}

func (openaiProtocol) Continue(body []byte, rr reply, results []toolResult) ([]byte, error) {
	items := []json.RawMessage{rr.assistant}
	for _, res := range results {
		raw, err := json.Marshal(map[string]string{"role": "tool", "tool_call_id": res.id, "content": res.text})
		if err != nil {
			return nil, err
		}
		items = append(items, raw)
	}
	return appendArray(body, "messages", items...)
}
