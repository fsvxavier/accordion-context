package responses

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// field is one top-level member of a JSON object, kept as raw bytes so that
// unknown members survive re-encoding unchanged.
type field struct {
	key string
	raw []byte
}

// parseObject splits a JSON object into its members, keeping each value's raw
// bytes exactly as they appeared in the input.
func parseObject(body []byte) ([]field, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	var out []field
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("object key is not a string")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		out = append(out, field{key: key, raw: []byte(raw)})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after JSON object")
	}
	return out, nil
}

// buildObject writes the members back in order, as {"key":raw,...}.
func buildObject(fields []field) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := marshalNoEscape(f.key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(f.raw)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// setField returns a copy of fields with key replaced, or appended if absent.
func setField(fields []field, key string, raw []byte) []field {
	out := make([]field, len(fields), len(fields)+1)
	copy(out, fields)
	for i := range out {
		if out[i].key == key {
			out[i].raw = raw
			return out
		}
	}
	return append(out, field{key: key, raw: raw})
}

// lookup returns the raw value of key, or nil when it is absent.
func lookup(fields []field, key string) []byte {
	for _, f := range fields {
		if f.key == key {
			return f.raw
		}
	}
	return nil
}

// joinArray joins raw JSON values into an array. The items are copied verbatim.
func joinArray(items [][]byte) []byte {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(it)
	}
	b.WriteByte(']')
	return b.Bytes()
}

func isNull(raw []byte) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// mustString encodes a Go string as a JSON string. Encoding a string cannot fail.
func mustString(s string) []byte {
	b, _ := marshalNoEscape(s)
	return b
}
