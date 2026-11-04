package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Tool names exposed to the model (spec §16).
const (
	ToolCompress   = "compress"
	ToolDecompress = "decompress"
)

const compressToolJSON = `{"name":"compress","description":"Replace already-finished ranges of the conversation with summaries. Each summary is written in the past tense, keeps paths with line numbers, exact errors, decisions with their reasons and exact values, and marks finished user requests as concluded. Pass closeIntents for user requests that the summarized ranges completed. Do not compress the work in progress.","input_schema":{"type":"object","properties":{"ranges":{"type":"array","description":"Ranges of message refs to summarize, for example m00340..m00412.","items":{"type":"object","properties":{"startRef":{"type":"string"},"endRef":{"type":"string"},"summary":{"type":"string"},"topic":{"type":"string"}},"required":["startRef","endRef","summary"]}},"folds":{"type":"array","description":"Folds of tier-2 or tier-3 blocks: the active blocks one tier below, from startBlock to endBlock, become one block of the given tier.","items":{"type":"object","properties":{"tier":{"type":"integer"},"startBlock":{"type":"string"},"endBlock":{"type":"string"},"summary":{"type":"string"},"topic":{"type":"string"}},"required":["tier","startBlock","endBlock","summary"]}},"closeIntents":{"type":"array","description":"Ids of open intents completed by the summarized ranges.","items":{"type":"integer"}}},"required":["ranges"]}}`

const decompressToolJSON = `{"name":"decompress","description":"Recover the original messages behind a summary block. Use mode peek with a query to read only the relevant excerpts, or context to bring the whole block back.","input_schema":{"type":"object","properties":{"blockId":{"type":"string","description":"Block id from a history envelope, for example b12."},"mode":{"type":"string","enum":["context","file","peek"]},"query":{"type":"string","description":"Optional search text, used with mode peek."}},"required":["blockId","mode"]}}`

// ToolDefinitions returns fresh copies of the compress and decompress tool
// definitions in Anthropic tools format.
func ToolDefinitions() []json.RawMessage {
	return []json.RawMessage{json.RawMessage(compressToolJSON), json.RawMessage(decompressToolJSON)}
}

// InjectTools adds the compress and decompress definitions to the request's
// tools array, skipping any tool already declared under the same name. Existing
// tool definitions and every other field are copied byte for byte. When nothing
// is added the body is returned unchanged.
func InjectTools(body []byte) ([]byte, error) {
	fields, err := parseObject(body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: inject tools: %w", err)
	}
	var existing []json.RawMessage
	if raw := lookup(fields, "tools"); raw != nil && !isNull(raw) {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return nil, fmt.Errorf("anthropic: inject tools: tools: %w", err)
		}
	}
	have := make(map[string]bool, len(existing))
	for _, t := range existing {
		if name := toolName(t); name != "" {
			have[name] = true
		}
	}
	added := false
	for _, def := range ToolDefinitions() {
		if have[toolName(def)] {
			continue
		}
		existing = append(existing, def)
		added = true
	}
	if !added {
		return body, nil
	}
	var arr bytes.Buffer
	arr.WriteByte('[')
	for i, t := range existing {
		if i > 0 {
			arr.WriteByte(',')
		}
		arr.Write(t)
	}
	arr.WriteByte(']')
	return buildObject(setField(fields, "tools", arr.Bytes())), nil
}

// HasTool reports whether the request body declares a tool with the given name.
// Malformed bodies report false.
func HasTool(body []byte, name string) bool {
	fields, err := parseObject(body)
	if err != nil {
		return false
	}
	raw := lookup(fields, "tools")
	if raw == nil || isNull(raw) {
		return false
	}
	var tools []json.RawMessage
	if json.Unmarshal(raw, &tools) != nil {
		return false
	}
	for _, t := range tools {
		if toolName(t) == name {
			return true
		}
	}
	return false
}

func toolName(def json.RawMessage) string {
	var h struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(def, &h) != nil {
		return ""
	}
	return h.Name
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// CompressArgs is the normalized input of a compress tool call.
type CompressArgs struct {
	Ranges       []CompressRange
	Folds        []CompressFold
	CloseIntents []int64
}

// CompressRange is one summarized range of ledger sequence numbers, inclusive.
type CompressRange struct {
	StartSeq int64
	EndSeq   int64
	Summary  string
	Topic    string
}

// ParseRef parses a message reference such as "m00042", "[m00042]", "m42", "#42" or "42"
// into its ledger sequence number. The number must be at least 1.
func ParseRef(s string) (int64, error) {
	t := strings.TrimSpace(s)
	// The window shows refs as [m00042]; models often copy the brackets.
	if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
		t = t[1 : len(t)-1]
	}
	t = strings.TrimPrefix(t, "#")
	t = strings.TrimPrefix(strings.TrimPrefix(t, "m"), "M")
	if len(t) == 0 || len(t) > 18 {
		return 0, fmt.Errorf("invalid ref %q", s)
	}
	for i := 0; i < len(t); i++ {
		if t[i] < '0' || t[i] > '9' {
			return 0, fmt.Errorf("invalid ref %q", s)
		}
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid ref %q", s)
	}
	return n, nil
}

// ParseCompressArgs parses the input of a compress tool call. It accepts the
// plain JSON object, JSON wrapped in a Markdown code fence, JSON surrounded by
// prose, JSON encoded again as a string, a bare ranges array, and the alternate
// field names models produce (for example start/end, text/content, title,
// close_intents). Refs may be strings or numbers.
func ParseCompressArgs(input []byte) (CompressArgs, error) {
	var lastErr error
	for _, cand := range candidates(input) {
		v, err := decodeAny(cand, 2)
		if err != nil {
			lastErr = err
			continue
		}
		args, err := normalizeCompress(v)
		if err != nil {
			lastErr = err
			continue
		}
		return args, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no JSON found")
	}
	return CompressArgs{}, fmt.Errorf("anthropic: compress args: %w", lastErr)
}

func candidates(in []byte) []string {
	s := strings.TrimSpace(string(in))
	var out []string
	add := func(c string) {
		c = strings.TrimSpace(c)
		if c == "" {
			return
		}
		for _, o := range out {
			if o == c {
				return
			}
		}
		out = append(out, c)
	}
	add(s)
	if f, ok := fenced(s); ok {
		add(f)
	}
	if b, ok := braced(s); ok {
		add(b)
	}
	return out
}

func fenced(s string) (string, bool) {
	i := strings.Index(s, "```")
	if i < 0 {
		return "", false
	}
	rest := s[i+3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 && !strings.ContainsAny(rest[:nl], "{[") {
		rest = rest[nl+1:] // drop the language tag line, e.g. ```json
	}
	if j := strings.Index(rest, "```"); j >= 0 {
		rest = rest[:j]
	}
	return rest, true
}

func braced(s string) (string, bool) {
	i := strings.IndexAny(s, "{[")
	j := strings.LastIndexAny(s, "}]")
	if i < 0 || j <= i {
		return "", false
	}
	return s[i : j+1], true
}

// decodeAny decodes one JSON value. A JSON string is decoded again, up to depth
// levels, to handle double-encoded arguments.
func decodeAny(c string, depth int) (any, error) {
	dec := json.NewDecoder(strings.NewReader(c))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after JSON value")
	}
	if s, ok := v.(string); ok && depth > 0 {
		return decodeAny(strings.TrimSpace(s), depth-1)
	}
	return v, nil
}

func normalizeCompress(v any) (CompressArgs, error) {
	var args CompressArgs
	if m, ok := v.(map[string]any); ok {
		folds, err := parseFolds(m)
		if err != nil {
			return args, err
		}
		args.Folds = folds
	}
	var ranges any
	var closeList any
	switch x := v.(type) {
	case []any:
		ranges = x
	case map[string]any:
		ranges, _ = firstKey(x, "ranges", "range", "blocks", "compress")
		closeList, _ = firstKey(x, "closeIntents", "close_intents", "closedIntents", "intents")
	default:
		return args, fmt.Errorf("expected object or array, got %T", v)
	}
	list, ok := ranges.([]any)
	if !ok || len(list) == 0 {
		return args, errors.New("ranges must be a non-empty array")
	}
	for i, r := range list {
		m, ok := r.(map[string]any)
		if !ok {
			return args, fmt.Errorf("range %d: expected object", i)
		}
		startV, ok := firstKey(m, "startRef", "start_ref", "startSeq", "start", "from")
		if !ok {
			return args, fmt.Errorf("range %d: missing startRef", i)
		}
		endV, ok := firstKey(m, "endRef", "end_ref", "endSeq", "end", "to")
		if !ok {
			return args, fmt.Errorf("range %d: missing endRef", i)
		}
		start, err := refValue(startV)
		if err != nil {
			return args, fmt.Errorf("range %d: startRef: %w", i, err)
		}
		end, err := refValue(endV)
		if err != nil {
			return args, fmt.Errorf("range %d: endRef: %w", i, err)
		}
		if end < start {
			return args, fmt.Errorf("range %d: endRef %d before startRef %d", i, end, start)
		}
		summary, err := stringValue(m, "summary", "text", "content", "summaryText")
		if err != nil {
			return args, fmt.Errorf("range %d: %w", i, err)
		}
		if strings.TrimSpace(summary) == "" {
			return args, fmt.Errorf("range %d: empty summary", i)
		}
		topic, err := stringValue(m, "topic", "title", "label")
		if err != nil {
			return args, fmt.Errorf("range %d: %w", i, err)
		}
		args.Ranges = append(args.Ranges, CompressRange{
			StartSeq: start, EndSeq: end, Summary: summary, Topic: topic,
		})
	}
	if closeList != nil {
		arr, ok := closeList.([]any)
		if !ok {
			return args, errors.New("closeIntents must be an array")
		}
		for i, c := range arr {
			id, err := refValue(c)
			if err != nil {
				return args, fmt.Errorf("closeIntents %d: %w", i, err)
			}
			args.CloseIntents = append(args.CloseIntents, id)
		}
	}
	return args, nil
}

func firstKey(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

func stringValue(m map[string]any, keys ...string) (string, error) {
	v, ok := firstKey(m, keys...)
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", keys[0])
	}
	return s, nil
}

func refValue(v any) (int64, error) {
	switch x := v.(type) {
	case json.Number:
		return ParseRef(x.String())
	case string:
		return ParseRef(x)
	}
	return 0, fmt.Errorf("unsupported ref type %T", v)
}
