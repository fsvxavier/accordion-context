package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/wire/responses"
)

// pathResponses is the Responses endpoint (spec §19.1). Only the collection path
// is matched, so /v1/responses/{id} passes through.
const pathResponses = "/v1/responses"

// pathResponsesCompact is the standalone compaction endpoint of the Responses API.
// It is not compacted: its POST is the client's own compaction call and is
// forwarded unchanged (see ClientCompaction).
const pathResponsesCompact = "/v1/responses/compact"

// responsesProtocol adapts POST /v1/responses. Requests that refer to server-side
// state are forwarded unchanged (responses.ErrStateful). Streams are buffered.
type responsesProtocol struct{}

func (responsesProtocol) Name() string { return "responses" }

func (responsesProtocol) Detect(path string) bool { return path == pathResponses }

func (responsesProtocol) Meta(_, _ string, body []byte) (string, bool, error) {
	var meta struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return "", false, err
	}
	return meta.Model, meta.Stream, nil
}

func (responsesProtocol) Decode(body []byte) (request, []kernel.Message, error) {
	req, msgs, err := responses.Decode(body)
	if err != nil {
		return nil, nil, err
	}
	return encoderFunc(func(window []kernel.Message) ([]byte, error) {
		return responses.Encode(req, window)
	}), msgs, nil
}

// ClientCompaction matches a POST to /v1/responses/compact, with or without a /ctx/
// gateway prefix. The endpoint is stateless and its output must not be pruned, so
// the request is never changed.
func (responsesProtocol) ClientCompaction(rt routed, _ http.Header, _ []byte) bool {
	return routedTo(rt, func(path string) bool { return path == pathResponsesCompact })
}

func (responsesProtocol) InjectTools(body []byte) ([]byte, error) {
	return responses.InjectTools(body)
}

// AppendNudge adds an input_text part to the last user message item, or appends
// a new user message when the last item is from someone else.
func (responsesProtocol) AppendNudge(body []byte, text string) ([]byte, error) {
	return editArray(body, "input", func(items []json.RawMessage) ([]json.RawMessage, error) {
		if n := len(items); n > 0 && roleOf(items[n-1]) == "user" {
			m, err := mapOf(items[n-1])
			if err != nil {
				return nil, err
			}
			content, err := appendPart(m["content"], "input_text", text)
			if err != nil {
				return nil, err
			}
			items[n-1], err = setField(items[n-1], "content", content)
			return items, err
		}
		fresh, err := json.Marshal(map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]string{{"type": "input_text", "text": text}},
		})
		if err != nil {
			return nil, err
		}
		return append(items, fresh), nil
	})
}

func (responsesProtocol) ParseTool(name string, input []byte) (toolArgs, error) {
	switch name {
	case toolCompress:
		args, err := responses.ParseCompressArgs(input)
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
		args, err := responses.ParseDecompressArgs(input)
		if err != nil {
			return toolArgs{}, err
		}
		return toolArgs{block: args.BlockID, mode: args.Mode, query: args.Query}, nil
	case toolSearch:
		args, err := responses.ParseSearchArgs(input)
		if err != nil {
			return toolArgs{}, err
		}
		return toolArgs{query: args.Query, limit: args.Limit, scope: args.Scope}, nil
	case toolRetrieve:
		args, err := responses.ParseRetrieveArgs(input)
		if err != nil {
			return toolArgs{}, err
		}
		return toolArgs{ref: args.Ref, block: args.BlockID, rng: args.Range}, nil
	}
	return toolArgs{}, fmt.Errorf("unknown tool %q", name)
}

func (responsesProtocol) Live() bool { return false }

func (responsesProtocol) Read(data []byte, stream bool) reply {
	if stream {
		return responsesStreamRead(data)
	}
	rr := reply{final: data, complete: true}
	if u, ok, err := responses.ParseUsage(data); err == nil && ok {
		rr.usage = &usage{input: u.InputTokens, cached: u.CachedTokens}
	}
	m, err := decodeObject(data)
	if err != nil {
		return rr
	}
	out, _ := m["output"].([]any)
	rr.assistant = encodeArray(out)
	kept := make([]any, 0, len(out))
	for _, it := range out {
		im, _ := it.(map[string]any)
		if im["type"] == "function_call" {
			name, _ := im["name"].(string)
			if isInternal(name) {
				rr.calls = append(rr.calls, callOf(im))
				continue
			}
			rr.clientCalls++
		}
		kept = append(kept, it)
	}
	if len(rr.calls) > 0 {
		m["output"] = kept
		rr.final = encodeObject(m)
	}
	return rr
}

// callOf reads a function_call output item.
func callOf(im map[string]any) toolCall {
	name, _ := im["name"].(string)
	id, _ := im["call_id"].(string)
	args, _ := im["arguments"].(string)
	return toolCall{id: id, name: name, input: []byte(args)}
}

// responsesStreamRead reads a buffered Responses stream. The codec reports the
// internal calls by output index, plus usage and completion. A second pass drops
// their events, removes their items from the terminal response, and keeps the
// done items as the assistant turn that Continue echoes back.
func responsesStreamRead(data []byte) reply {
	sum, _ := responses.StreamPassthrough(io.Discard, bytes.NewReader(data))
	rr := reply{complete: sum.Complete}
	if sum.HasUsage {
		rr.usage = &usage{input: sum.Usage.InputTokens, cached: sum.Usage.CachedTokens}
	}
	internal := map[int]bool{}
	for _, c := range sum.Calls {
		internal[c.OutputIndex] = true
		rr.calls = append(rr.calls, toolCall{id: c.CallID, name: c.Name, input: []byte(c.Arguments)})
	}
	if len(rr.calls) == 0 {
		rr.final = data
		return rr
	}

	var done []any
	var keep []sseItem
	for _, it := range parseSSE(data) {
		if it.payload == nil {
			keep = append(keep, it)
			continue
		}
		m, err := decodeObject(it.payload)
		if err != nil {
			keep = append(keep, it)
			continue
		}
		typ, _ := m["type"].(string)
		if typ == "response.output_item.done" {
			// Every done item is kept, internal calls included: Continue echoes them
			// back, and the function_call_output refers to them.
			done = append(done, m["item"])
		}
		if idx, ok := intField(m, "output_index"); ok && internal[idx] {
			continue
		}
		switch typ {
		case "response.output_item.added":
			if im, _ := m["item"].(map[string]any); im["type"] == "function_call" {
				if name, _ := im["name"].(string); !isInternal(name) {
					rr.clientCalls++
				}
			}
		case "response.completed", "response.incomplete", "response.failed":
			if resp, _ := m["response"].(map[string]any); resp != nil {
				if out, ok := resp["output"].([]any); ok {
					kept := make([]any, 0, len(out))
					for i, o := range out {
						if !internal[i] {
							kept = append(kept, o)
						}
					}
					resp["output"] = kept
				}
			}
		}
		it.setPayload(encodeObject(m))
		keep = append(keep, it)
	}
	if len(done) == 0 {
		// Without done events, rebuild the function_call items that the outputs refer to.
		for _, c := range rr.calls {
			done = append(done, map[string]any{"type": "function_call", "call_id": c.id, "name": c.name, "arguments": string(c.input)})
		}
	}
	rr.assistant = encodeArray(done)
	rr.final = renderSSE(keep)
	return rr
}

// encodeArray marshals a list, with an empty list rather than null.
func encodeArray(items []any) json.RawMessage {
	if items == nil {
		items = []any{}
	}
	b, _ := json.Marshal(items)
	return b
}

func (responsesProtocol) Continue(body []byte, rr reply, results []toolResult) ([]byte, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(rr.assistant, &items); err != nil {
		return nil, err
	}
	for _, res := range results {
		raw, err := json.Marshal(map[string]string{"type": "function_call_output", "call_id": res.id, "output": res.text})
		if err != nil {
			return nil, err
		}
		items = append(items, raw)
	}
	return appendArray(body, "input", items...)
}
