package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/wire/google"
)

// googleEndpoint matches the generateContent and streamGenerateContent methods,
// with or without a gateway prefix. The model name is the capture group.
var googleEndpoint = regexp.MustCompile(`(?:^|/)models/([^/:]+):(stream)?[Gg]enerateContent$`)

// googleProtocol adapts the Gemini generateContent and streamGenerateContent
// endpoints. Streaming requires alt=sse, the only form the codec reads. Streams
// are buffered per round.
type googleProtocol struct{}

func (googleProtocol) Name() string { return "google" }

func (googleProtocol) Detect(path string) bool { return googleEndpoint.MatchString(path) }

func (googleProtocol) Meta(path, rawQuery string, _ []byte) (string, bool, error) {
	m := googleEndpoint.FindStringSubmatch(path)
	if m == nil {
		return "", false, fmt.Errorf("google: not a generateContent path")
	}
	stream := m[2] == "stream"
	if stream {
		q, err := url.ParseQuery(rawQuery)
		if err != nil || q.Get("alt") != "sse" {
			// Without alt=sse the answer is a JSON array, which is not compacted.
			return "", false, fmt.Errorf("google: streamGenerateContent without alt=sse")
		}
	}
	return m[1], stream, nil
}

// ClientCompaction marks the client's internal call with the shape of Gemini CLI's
// history compression: a non-streaming :generateContent request with a
// systemInstruction, no tools and no generationConfig (docs/compaction-commands.md,
// second pass). It is not a turn of the conversation, so it passes through
// untouched. The owner narrowed this to that shape: other utility calls carry a
// generationConfig and stay ordinary turns, as do all streaming or tool-carrying
// turns. compactionSignal prefers the text rule's name when the snapshot text matches.
func (googleProtocol) ClientCompaction(rt routed, _ http.Header, body []byte) string {
	if !routedTo(rt, googleProtocol{}.Detect) {
		return ""
	}
	if !geminiCompressionShape(rt.path, body) {
		return ""
	}
	return signalGeminiUtility
}

// geminiHasNoTools reports whether a generateContent body has a systemInstruction
// or contents and no tool declarations. An unreadable body is not a match.
func geminiHasNoTools(body []byte) bool {
	var v map[string]json.RawMessage
	if json.Unmarshal(body, &v) != nil {
		return false
	}
	t := bytes.TrimSpace(v["tools"])
	return len(t) == 0 || string(t) == "null" || string(t) == "[]"
}

// geminiCompressionShape reports the structure of Gemini CLI's history compression
// call: non-streaming, a systemInstruction, no tools and no generationConfig (the
// chat-compression-* aliases do not extend the base config, defaultModelConfigs.ts
// 23-43 against 292-325 at the commit of the doc). It only corroborates the text rule.
func geminiCompressionShape(path string, body []byte) bool {
	if m := googleEndpoint.FindStringSubmatch(path); m == nil || m[2] == "stream" {
		return false
	}
	var v map[string]json.RawMessage
	if json.Unmarshal(body, &v) != nil {
		return false
	}
	_, hasCfg := v["generationConfig"]
	_, hasSys := v["systemInstruction"]
	return hasSys && !hasCfg && geminiHasNoTools(body)
}

func (googleProtocol) Decode(body []byte) (request, []kernel.Message, error) {
	req, msgs, err := google.Decode(body)
	if err != nil {
		return nil, nil, err
	}
	return encoderFunc(func(window []kernel.Message) ([]byte, error) {
		return google.Encode(req, window)
	}), msgs, nil
}

func (googleProtocol) InjectTools(body []byte) ([]byte, error) {
	return google.InjectTools(body)
}

// AppendNudge adds a text part to the last user content, or appends a new user
// content when the last one is from the model.
func (googleProtocol) AppendNudge(body []byte, text string) ([]byte, error) {
	return editArray(body, "contents", func(items []json.RawMessage) ([]json.RawMessage, error) {
		if n := len(items); n > 0 && roleOf(items[n-1]) == "user" {
			m, err := mapOf(items[n-1])
			if err != nil {
				return nil, err
			}
			var parts []json.RawMessage
			if err := json.Unmarshal(m["parts"], &parts); err != nil {
				return nil, err
			}
			part, err := json.Marshal(map[string]string{"text": text})
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
			raw, err := json.Marshal(parts)
			if err != nil {
				return nil, err
			}
			items[n-1], err = setField(items[n-1], "parts", raw)
			return items, err
		}
		fresh, err := json.Marshal(map[string]any{"role": "user", "parts": []map[string]string{{"text": text}}})
		if err != nil {
			return nil, err
		}
		return append(items, fresh), nil
	})
}

func (googleProtocol) ParseTool(name string, input []byte) (toolArgs, error) {
	switch name {
	case toolCompress:
		args, err := google.ParseCompressArgs(input)
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
		args, err := google.ParseDecompressArgs(input)
		if err != nil {
			return toolArgs{}, err
		}
		return toolArgs{block: args.BlockID, mode: args.Mode, query: args.Query}, nil
	case toolSearch:
		args, err := google.ParseSearchArgs(input)
		if err != nil {
			return toolArgs{}, err
		}
		return toolArgs{query: args.Query, limit: args.Limit, scope: args.Scope}, nil
	case toolRetrieve:
		args, err := google.ParseRetrieveArgs(input)
		if err != nil {
			return toolArgs{}, err
		}
		return toolArgs{ref: args.Ref, block: args.BlockID, rng: args.Range}, nil
	}
	return toolArgs{}, fmt.Errorf("unknown tool %q", name)
}

func (googleProtocol) Live() bool { return false }

func (googleProtocol) Read(data []byte, stream bool) reply {
	if stream {
		return googleStreamRead(data)
	}
	rr := reply{final: data, complete: true}
	if u, ok, err := google.ParseUsage(data); err == nil && ok {
		rr.usage = &usage{input: u.PromptTokens, cached: u.CachedTokens}
	}
	m, err := decodeObject(data)
	if err != nil {
		return rr
	}
	sc := scanGoogle(m)
	rr.calls = sc.calls
	rr.clientCalls = sc.client
	rr.assistant = modelTurn(sc.parts)
	if len(sc.calls) > 0 {
		rr.final = encodeObject(m)
	}
	return rr
}

// googleStreamRead reads a buffered SSE stream. Events that only carried internal
// calls are dropped. The model turn is the parts of the first candidate, in order.
func googleStreamRead(data []byte) reply {
	sum, _ := google.StreamPassthrough(io.Discard, bytes.NewReader(data))
	rr := reply{complete: sum.Complete}
	if sum.HasUsage {
		rr.usage = &usage{input: sum.Usage.PromptTokens, cached: sum.Usage.CachedTokens}
	}
	var parts []any
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
		sc := scanGoogle(m)
		rr.calls = append(rr.calls, sc.calls...)
		rr.clientCalls += sc.client
		parts = append(parts, sc.parts...)
		if sc.bare && len(sc.calls) > 0 {
			// The event only carried internal calls, so the client must not see it.
			continue
		}
		it.setPayload(encodeObject(m))
		keep = append(keep, it)
	}
	rr.assistant = modelTurn(parts)
	if len(rr.calls) == 0 {
		rr.final = data
	} else {
		rr.final = renderSSE(keep)
	}
	return rr
}

// googleScan is what one generateContent object says about its functionCall parts.
type googleScan struct {
	calls  []toolCall
	client int
	parts  []any // parts of the first candidate, internal ones included
	bare   bool  // nothing is left to send: no parts, no finish reason, no usage
}

// scanGoogle removes the internal functionCall parts from every candidate of m, in
// place, and reports them. Gemini sends a functionCall whole inside one part.
func scanGoogle(m map[string]any) googleScan {
	var sc googleScan
	cands, _ := m["candidates"].([]any)
	left := 0
	finished := false
	for ci, cv := range cands {
		cm, _ := cv.(map[string]any)
		if _, ok := cm["finishReason"]; ok {
			finished = true
		}
		content, _ := cm["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		kept := make([]any, 0, len(parts))
		for _, p := range parts {
			pm, _ := p.(map[string]any)
			if fc, _ := pm["functionCall"].(map[string]any); fc != nil {
				name, _ := fc["name"].(string)
				if isInternal(name) {
					args, _ := json.Marshal(fc["args"])
					sc.calls = append(sc.calls, toolCall{name: name, input: args})
					if ci == 0 {
						sc.parts = append(sc.parts, p)
					}
					continue
				}
				sc.client++
			}
			kept = append(kept, p)
			if ci == 0 {
				sc.parts = append(sc.parts, p)
			}
		}
		left += len(kept)
		if content != nil && len(kept) != len(parts) {
			content["parts"] = kept
		}
	}
	_, hasUsage := m["usageMetadata"]
	sc.bare = left == 0 && !finished && !hasUsage
	return sc
}

// modelTurn builds the model content that the continuation echoes back.
func modelTurn(parts []any) json.RawMessage {
	if parts == nil {
		parts = []any{}
	}
	b, _ := json.Marshal(map[string]any{"role": "model", "parts": parts})
	return b
}

// Continue echoes the model turn with its raw parts, which keep thought signatures,
// and answers every call in one user content with functionResponse parts.
func (googleProtocol) Continue(body []byte, rr reply, results []toolResult) ([]byte, error) {
	resp := make([]any, 0, len(results))
	for _, res := range results {
		payload := map[string]any{"result": res.text}
		if res.isErr {
			payload = map[string]any{"error": res.text}
		}
		resp = append(resp, map[string]any{"functionResponse": map[string]any{"name": res.name, "response": payload}})
	}
	user, err := json.Marshal(map[string]any{"role": "user", "parts": resp})
	if err != nil {
		return nil, err
	}
	return appendArray(body, "contents", rr.assistant, user)
}
