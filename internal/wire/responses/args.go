package responses

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CompressArgs is the normalized input of a compress function call.
type CompressArgs struct {
	Ranges       []CompressRange
	CloseIntents []int64
}

// CompressRange is one summarized range of ledger sequence numbers, inclusive.
type CompressRange struct {
	StartSeq int64
	EndSeq   int64
	Summary  string
	Topic    string
}

// DecompressArgs is the normalized input of a decompress function call.
type DecompressArgs struct {
	BlockID string
	Mode    string // context, file or peek
	Query   string
}

// ParseCompressArgs parses the arguments string of a compress function call. The
// arguments arrive as a JSON string, so the parser accepts the plain JSON object,
// JSON in a Markdown code fence, JSON surrounded by prose, JSON encoded again as
// a string (up to two levels), a bare ranges array, and the alternate field names
// that models produce (for example start/end, text/content, title,
// close_intents). Refs may be strings or numbers.
func ParseCompressArgs(arguments []byte) (CompressArgs, error) {
	var lastErr error
	for _, cand := range candidates(arguments) {
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
	return CompressArgs{}, fmt.Errorf("responses: compress args: %w", lastErr)
}

// ParseDecompressArgs parses the arguments string of a decompress function call.
// The same recovery rules as ParseCompressArgs apply. A missing mode defaults to
// context, and an unknown mode is an error.
func ParseDecompressArgs(arguments []byte) (DecompressArgs, error) {
	var lastErr error
	for _, cand := range candidates(arguments) {
		v, err := decodeAny(cand, 2)
		if err != nil {
			lastErr = err
			continue
		}
		obj, ok := v.(map[string]any)
		if !ok {
			lastErr = fmt.Errorf("expected object, got %T", v)
			continue
		}
		args, err := normalizeDecompress(obj)
		if err != nil {
			lastErr = err
			continue
		}
		return args, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no JSON found")
	}
	return DecompressArgs{}, fmt.Errorf("responses: decompress args: %w", lastErr)
}

func normalizeDecompress(m map[string]any) (DecompressArgs, error) {
	var args DecompressArgs
	id, err := stringValue(m, "blockId", "block_id", "id")
	if err != nil {
		return args, err
	}
	if strings.TrimSpace(id) == "" {
		return args, errors.New("missing blockId")
	}
	mode, err := stringValue(m, "mode")
	if err != nil {
		return args, err
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "context"
	}
	switch mode {
	case "context", "file", "peek":
	default:
		return args, fmt.Errorf("unknown mode %q", mode)
	}
	query, err := stringValue(m, "query")
	if err != nil {
		return args, err
	}
	args.BlockID = strings.TrimSpace(id)
	args.Mode = mode
	args.Query = query
	return args, nil
}

// candidates lists the substrings that may hold the JSON value, most literal
// first: the whole input, the body of a Markdown fence, and the span from the
// first opening bracket to the last closing one.
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

// stringValue returns the first present key as a string. An absent key gives "".
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

// refValue reads a message reference given as a string or a number.
func refValue(v any) (int64, error) {
	switch x := v.(type) {
	case json.Number:
		return parseRef(x.String())
	case string:
		return parseRef(x)
	}
	return 0, fmt.Errorf("unsupported ref type %T", v)
}

// parseRef parses a message reference such as "m00042", "m42", "#42" or "42" into
// its ledger sequence number. The number must be at least 1.
func parseRef(s string) (int64, error) {
	t := strings.TrimSpace(s)
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
