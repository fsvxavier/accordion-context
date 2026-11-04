package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/session"
)

// Internal tool names (spec §16). Every dialect exposes the same two tools under
// the same names; only their declaration and wire shape differ.
const (
	toolCompress   = "compress"
	toolDecompress = "decompress"
)

func isInternal(name string) bool {
	return name == toolCompress || name == toolDecompress
}

// protocol is one API dialect the proxy compacts (spec §19.1). An adapter wraps
// one wire codec and owns the shape of its tool calls, tool results and streams.
// The server-side loop in proxy.go is written once against this interface.
type protocol interface {
	// Name identifies the dialect in logs.
	Name() string
	// Detect reports whether path, with the /p/ and /ctx/ prefixes removed, is an
	// endpoint of this dialect. The router has already checked the method.
	Detect(path string) bool
	// Meta returns the model and whether the answer is streamed. An error means the
	// request is not compacted and is forwarded unchanged.
	Meta(path, rawQuery string, body []byte) (model string, stream bool, err error)
	// Decode parses the client request into ledger messages.
	Decode(body []byte) (request, []kernel.Message, error)
	// InjectTools adds the compress and decompress tools in the dialect's format.
	InjectTools(body []byte) ([]byte, error)
	// AppendNudge adds text to the newest user turn of an encoded body.
	AppendNudge(body []byte, text string) ([]byte, error)
	// ParseTool normalizes the arguments of one internal call.
	ParseTool(name string, input []byte) (toolArgs, error)
	// Live reports whether a streamed answer can reach the client as it arrives.
	// Only Anthropic can do this, because its stream shows which blocks are
	// internal. Other dialects are read in full by Read before anything is sent.
	Live() bool
	// Read interprets one complete upstream answer: a JSON body, or the whole SSE
	// stream when stream is true. It never fails. A stream that did not finish
	// reports complete false.
	Read(data []byte, stream bool) reply
	// Continue appends one round and its tool results to an encoded body. rr.assistant
	// is the dialect's assistant turn, as Read produced it.
	Continue(body []byte, rr reply, results []toolResult) ([]byte, error)
}

// request is a decoded client request that can be rendered again.
type request interface {
	encode(window []kernel.Message) ([]byte, error)
}

// encoderFunc adapts a codec Encode call, which closes over its decoded request.
type encoderFunc func(window []kernel.Message) ([]byte, error)

func (f encoderFunc) encode(window []kernel.Message) ([]byte, error) { return f(window) }

// protocols lists the adapters in the order the router tries them.
var protocols = []protocol{anthropicProtocol{}, openaiProtocol{}, responsesProtocol{}, googleProtocol{}}

// protocolFor returns the adapter of a routed request, or nil when the path is not
// a compacted endpoint. Under /ctx/ a gateway prefix is allowed in front of the
// endpoint, so a suffix that starts at a path segment is tried too.
func protocolFor(rt routed) protocol {
	for _, p := range protocols {
		if p.Detect(rt.path) {
			return p
		}
		if rt.viaCtx {
			for i := 1; i < len(rt.path); i++ {
				if rt.path[i] == '/' && p.Detect(rt.path[i:]) {
					return p
				}
			}
		}
	}
	return nil
}

// toolCall is one internal call found in an upstream answer.
type toolCall struct {
	id    string
	name  string
	input []byte // arguments: a JSON object, or the text of a string-encoded object
}

// toolResult is what an internal call produced, before the dialect formats it.
type toolResult struct {
	id    string
	name  string
	text  string
	isErr bool
}

// toolArgs is a normalized internal call. Each adapter fills it from its codec.
type toolArgs struct {
	ranges []session.CompressRange // compress
	block  string                  // decompress: block id
	mode   string
	query  string
}

// usage is the prompt size of one upstream call, as the cache ledger counts it.
// input includes the cached part.
type usage struct {
	input  int
	cached int
}

// reply is one upstream answer as the server-side loop sees it.
type reply struct {
	calls       []toolCall
	clientCalls int             // calls the client must run; the loop cannot continue past them
	usage       *usage          // nil when the answer carried no usage
	complete    bool            // false for a stream that ended before its terminal event
	final       []byte          // the answer with internal calls removed, as the client gets it
	assistant   json.RawMessage // the assistant turn in the dialect's shape, for Continue
}

// compressRange builds the session form of one range from codec sequence numbers.
func compressRange(start, end int64, summary, topic string) session.CompressRange {
	return session.CompressRange{
		StartRef: kernel.FormatRef(start),
		EndRef:   kernel.FormatRef(end),
		Summary:  summary,
		Topic:    topic,
	}
}

// mapOf decodes a JSON object into raw fields, so that unknown fields survive a rewrite.
func mapOf(raw []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("not a JSON object")
	}
	return m, nil
}

// editArray rewrites the array under key of a top-level JSON object. Every other
// field is carried over.
func editArray(body []byte, key string, fn func([]json.RawMessage) ([]json.RawMessage, error)) ([]byte, error) {
	top, err := mapOf(body)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(top[key], &items); err != nil {
		return nil, fmt.Errorf("field %s: %w", key, err)
	}
	items, err = fn(items)
	if err != nil {
		return nil, err
	}
	arr, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	top[key] = arr
	return json.Marshal(top)
}

// appendArray adds items to the end of the array under key.
func appendArray(body []byte, key string, items ...json.RawMessage) ([]byte, error) {
	return editArray(body, key, func(cur []json.RawMessage) ([]json.RawMessage, error) {
		return append(cur, items...), nil
	})
}

// setField returns item with one field replaced.
func setField(item json.RawMessage, key string, value json.RawMessage) (json.RawMessage, error) {
	m, err := mapOf(item)
	if err != nil {
		return nil, err
	}
	m[key] = value
	return json.Marshal(m)
}

// roleOf returns the role field of a message object, or "".
func roleOf(item json.RawMessage) string {
	var v struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(item, &v)
	return v.Role
}

// appendPart adds a text part to a message content. The content is a string or an
// array of parts, and a string becomes its first part. partType is the part type
// that the dialect expects ("text" or "input_text").
func appendPart(content json.RawMessage, partType, text string) (json.RawMessage, error) {
	var parts []json.RawMessage
	trimmed := bytes.TrimSpace(content)
	switch {
	case len(trimmed) == 0 || string(trimmed) == "null":
	case trimmed[0] == '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, err
		}
		if s != "" {
			first, err := json.Marshal(map[string]string{"type": partType, "text": s})
			if err != nil {
				return nil, err
			}
			parts = append(parts, first)
		}
	default:
		if err := json.Unmarshal(trimmed, &parts); err != nil {
			return nil, err
		}
	}
	part, err := json.Marshal(map[string]string{"type": partType, "text": text})
	if err != nil {
		return nil, err
	}
	return json.Marshal(append(parts, part))
}

// sseItem is one event of a server-sent stream: its lines, and the joined value
// of its data lines (nil when it has none).
type sseItem struct {
	lines   []string
	payload []byte
}

// parseSSE splits an SSE body into events. CRLF is read as LF. A trailing event
// that lacks its blank line is kept, so a truncated stream stays visible.
func parseSSE(data []byte) []sseItem {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	var out []sseItem
	var cur []string
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			if len(cur) > 0 {
				out = append(out, newSSEItem(cur))
				cur = nil
			}
			continue
		}
		cur = append(cur, line)
	}
	if len(cur) > 0 {
		out = append(out, newSSEItem(cur))
	}
	return out
}

func newSSEItem(lines []string) sseItem {
	it := sseItem{lines: lines}
	var data []string
	for _, l := range lines {
		if v, ok := strings.CutPrefix(l, "data:"); ok {
			data = append(data, strings.TrimPrefix(v, " "))
		}
	}
	if len(data) > 0 {
		it.payload = []byte(strings.Join(data, "\n"))
	}
	return it
}

// setPayload replaces the data lines of an event with one line that carries p.
func (it *sseItem) setPayload(p []byte) {
	var out []string
	placed := false
	for _, l := range it.lines {
		if strings.HasPrefix(l, "data:") {
			if !placed {
				out = append(out, "data: "+string(p))
				placed = true
			}
			continue
		}
		out = append(out, l)
	}
	it.lines = out
	it.payload = p
}

// renderSSE writes events back as an SSE body, each followed by a blank line.
func renderSSE(items []sseItem) []byte {
	var b strings.Builder
	for _, it := range items {
		for _, l := range it.lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// decodeObject parses a JSON object while keeping numbers exact.
func decodeObject(p []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("not a JSON object")
	}
	return m, nil
}

func encodeObject(m map[string]any) []byte {
	b, _ := json.Marshal(m)
	return b
}

// intField reads an integer field that decodeObject kept as a json.Number.
func intField(m map[string]any, key string) (int, bool) {
	n, ok := m[key].(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	if err != nil {
		return 0, false
	}
	return int(i), true
}
