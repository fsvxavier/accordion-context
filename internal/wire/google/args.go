package google

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

// DecompressArgs is the normalized input of a decompress function call.
type DecompressArgs struct {
	BlockID string
	Mode    string // context, file or peek
	Query   string
}

// ParseRef parses a message reference such as "m00042", "m42", "#42" or "42"
// into its ledger sequence number. The number must be at least 1.
func ParseRef(s string) (int64, error) {
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

// ParseCompressArgs parses the args of a compress functionCall. Gemini sends args
// as a JSON object, so that is the normal input. A JSON string that holds the
// object (double encoding) is accepted once. The alternate field names that
// models produce are accepted too (for example start/end, text/content, title,
// close_intents), and refs may be strings or numbers.
func ParseCompressArgs(args []byte) (CompressArgs, error) {
	v, err := decodeArgs(args)
	if err != nil {
		return CompressArgs{}, fmt.Errorf("google: compress args: %w", err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return CompressArgs{}, fmt.Errorf("google: compress args: expected object, got %T", v)
	}
	out, err := normalizeCompress(obj)
	if err != nil {
		return CompressArgs{}, fmt.Errorf("google: compress args: %w", err)
	}
	return out, nil
}

// ParseDecompressArgs parses the args of a decompress functionCall. The same
// rules as ParseCompressArgs apply. A missing mode defaults to context, and an
// unknown mode is an error.
func ParseDecompressArgs(args []byte) (DecompressArgs, error) {
	v, err := decodeArgs(args)
	if err != nil {
		return DecompressArgs{}, fmt.Errorf("google: decompress args: %w", err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return DecompressArgs{}, fmt.Errorf("google: decompress args: expected object, got %T", v)
	}
	out, err := normalizeDecompress(obj)
	if err != nil {
		return DecompressArgs{}, fmt.Errorf("google: decompress args: %w", err)
	}
	return out, nil
}

// decodeArgs decodes one JSON value. A JSON string is decoded once more.
func decodeArgs(in []byte) (any, error) {
	v, err := decodeOne(in)
	if err != nil {
		return nil, err
	}
	if s, ok := v.(string); ok {
		return decodeOne([]byte(strings.TrimSpace(s)))
	}
	return v, nil
}

func decodeOne(in []byte) (any, error) {
	dec := json.NewDecoder(strings.NewReader(string(in)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after JSON value")
	}
	return v, nil
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

func normalizeCompress(m map[string]any) (CompressArgs, error) {
	var args CompressArgs
	folds, err := parseFolds(m)
	if err != nil {
		return args, err
	}
	args.Folds = folds
	ranges, _ := firstKey(m, "ranges", "range", "blocks")
	list, ok := ranges.([]any)
	if !ok || len(list) == 0 {
		return args, errors.New("ranges must be a non-empty array")
	}
	for i, r := range list {
		rm, ok := r.(map[string]any)
		if !ok {
			return args, fmt.Errorf("range %d: expected object", i)
		}
		startV, ok := firstKey(rm, "startRef", "start_ref", "startSeq", "start", "from")
		if !ok {
			return args, fmt.Errorf("range %d: missing startRef", i)
		}
		endV, ok := firstKey(rm, "endRef", "end_ref", "endSeq", "end", "to")
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
		summary, err := stringValue(rm, "summary", "text", "content", "summaryText")
		if err != nil {
			return args, fmt.Errorf("range %d: %w", i, err)
		}
		if strings.TrimSpace(summary) == "" {
			return args, fmt.Errorf("range %d: empty summary", i)
		}
		topic, err := stringValue(rm, "topic", "title", "label")
		if err != nil {
			return args, fmt.Errorf("range %d: %w", i, err)
		}
		args.Ranges = append(args.Ranges, CompressRange{StartSeq: start, EndSeq: end, Summary: summary, Topic: topic})
	}
	if closeV, ok := firstKey(m, "closeIntents", "close_intents", "closedIntents", "intents"); ok {
		arr, ok := closeV.([]any)
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

func refValue(v any) (int64, error) {
	switch x := v.(type) {
	case json.Number:
		return ParseRef(x.String())
	case string:
		return ParseRef(x)
	}
	return 0, fmt.Errorf("unsupported ref type %T", v)
}
