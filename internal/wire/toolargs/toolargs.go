// Package toolargs parses the arguments of the two read-side internal tools,
// search_context and retrieve (spec §13.1, §16). Every dialect codec hands over
// the arguments JSON of its own call shape, and the parsing rules are the same
// for all of them: fences, braces around the value, double-encoded strings and
// the alternate field names that models produce.
package toolargs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Scopes of search_context.
const (
	ScopeSession = "session" // this conversation only (the default)
	ScopeProject = "project" // every conversation of the project
)

// Search is a normalized search_context call.
type Search struct {
	Query string
	Limit int    // 0 when the model gave none
	Scope string // ScopeSession or ScopeProject
}

// Retrieve is a normalized retrieve call. Exactly one of Ref and BlockID is set.
type Retrieve struct {
	Ref     string // message ref, for example m00042
	BlockID string // block id, for example b12
	Range   string // optional message range, for example m00340..m00412
}

// ParseSearch parses the arguments of a search_context call.
func ParseSearch(input []byte) (Search, error) {
	m, err := decodeObject(input)
	if err != nil {
		return Search{}, fmt.Errorf("search_context args: %w", err)
	}
	var out Search
	if out.Query, err = stringField(m, "query", "q", "text", "search"); err != nil {
		return Search{}, fmt.Errorf("search_context args: %w", err)
	}
	if out.Query = strings.TrimSpace(out.Query); out.Query == "" {
		return Search{}, errors.New("search_context args: query is required")
	}
	if out.Limit, err = intField(m, "limit", "max", "top_k"); err != nil {
		return Search{}, fmt.Errorf("search_context args: %w", err)
	}
	scope, err := stringField(m, "scope")
	if err != nil {
		return Search{}, fmt.Errorf("search_context args: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", ScopeSession:
		out.Scope = ScopeSession
	case ScopeProject:
		out.Scope = ScopeProject
	default:
		return Search{}, fmt.Errorf("search_context args: unknown scope %q", scope)
	}
	return out, nil
}

// ParseRetrieve parses the arguments of a retrieve call. Refs may be strings or
// numbers. Giving both a ref and a block id is an error.
func ParseRetrieve(input []byte) (Retrieve, error) {
	m, err := decodeObject(input)
	if err != nil {
		return Retrieve{}, fmt.Errorf("retrieve args: %w", err)
	}
	var out Retrieve
	if out.Ref, err = stringField(m, "ref", "messageRef", "message_ref"); err != nil {
		return Retrieve{}, fmt.Errorf("retrieve args: %w", err)
	}
	if out.BlockID, err = stringField(m, "blockId", "block_id", "block"); err != nil {
		return Retrieve{}, fmt.Errorf("retrieve args: %w", err)
	}
	if out.Range, err = stringField(m, "range"); err != nil {
		return Retrieve{}, fmt.Errorf("retrieve args: %w", err)
	}
	out.Ref = strings.TrimSpace(out.Ref)
	out.BlockID = strings.TrimSpace(out.BlockID)
	out.Range = strings.TrimSpace(out.Range)
	switch {
	case out.Ref == "" && out.BlockID == "":
		return Retrieve{}, errors.New("retrieve args: ref or blockId is required")
	case out.Ref != "" && out.BlockID != "":
		return Retrieve{}, errors.New("retrieve args: give ref or blockId, not both")
	}
	return out, nil
}

// decodeObject returns the first candidate substring that decodes to a JSON object.
func decodeObject(input []byte) (map[string]any, error) {
	var lastErr error
	for _, cand := range candidates(input) {
		v, err := decodeAny(cand, 2)
		if err != nil {
			lastErr = err
			continue
		}
		if m, ok := v.(map[string]any); ok {
			return m, nil
		}
		lastErr = fmt.Errorf("expected object, got %T", v)
	}
	if lastErr == nil {
		lastErr = errors.New("no JSON found")
	}
	return nil, lastErr
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

// firstKey returns the value of the first present, non-null key.
func firstKey(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

// stringField returns the first present key as text. Numbers are accepted as
// their decimal form, so a numeric ref works. An absent key gives "".
func stringField(m map[string]any, keys ...string) (string, error) {
	v, ok := firstKey(m, keys...)
	if !ok {
		return "", nil
	}
	switch x := v.(type) {
	case string:
		return x, nil
	case json.Number:
		return x.String(), nil
	}
	return "", fmt.Errorf("%s must be a string", keys[0])
}

// intField returns the first present key as an integer. Strings that hold a
// number are accepted. An absent key gives 0.
func intField(m map[string]any, keys ...string) (int, error) {
	v, ok := firstKey(m, keys...)
	if !ok {
		return 0, nil
	}
	var s string
	switch x := v.(type) {
	case json.Number:
		s = x.String()
	case string:
		s = strings.TrimSpace(x)
	default:
		return 0, fmt.Errorf("%s must be an integer", keys[0])
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", keys[0])
	}
	return n, nil
}
