// Package anthropic adapts the Anthropic Messages API (POST /v1/messages) to the
// protocol-independent kernel model (spec §19.1). It is stdlib-only and must not
// import store or session.
package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

const (
	roleUser      = "user"
	roleAssistant = "assistant"
)

// field is one top-level member of the request object, kept as raw bytes so
// unknown fields and the system prompt survive re-encoding unchanged.
type field struct {
	key string
	raw json.RawMessage
}

// keptBlock is the original wire form of a decoded block. Encode reuses it only
// while the kernel message at the same Seq still has the same role, kind, text
// and tool id, so thinking signatures, images and cache_control survive.
type keptBlock struct {
	role   string
	kind   kernel.Kind
	text   string
	toolID string
	raw    json.RawMessage
}

// Request is a decoded Messages API request.
type Request struct {
	fields []field
	kept   map[int64]keptBlock
}

// block is one decoded content block before sequence numbers are assigned.
type block struct {
	msg  kernel.Message
	role string
	raw  json.RawMessage // nil when the content was a bare JSON string
}

// Decode parses a Messages API request body. Seq values are assigned 1..N in
// ledger order (system first, then each content block in message order); the
// caller must keep Seq stable for messages it passes back to Encode.
func Decode(body []byte) (*Request, []kernel.Message, error) {
	fields, err := parseObject(body)
	if err != nil {
		return nil, nil, fmt.Errorf("anthropic: decode request: %w", err)
	}
	req := &Request{fields: fields, kept: make(map[int64]keptBlock)}
	var out []kernel.Message
	var seq int64

	if raw := lookup(fields, "system"); raw != nil {
		text, err := systemText(raw)
		if err != nil {
			return nil, nil, err
		}
		if text != "" {
			seq++
			out = append(out, kernel.Message{
				Seq: seq, Ref: kernel.FormatRef(seq),
				Role: kernel.RoleSystem, Kind: kernel.KindText, Text: text,
			})
		}
	}

	rawMsgs := lookup(fields, "messages")
	if rawMsgs == nil {
		return nil, nil, errors.New("anthropic: decode request: messages missing")
	}
	var list []json.RawMessage
	if err := json.Unmarshal(rawMsgs, &list); err != nil {
		return nil, nil, fmt.Errorf("anthropic: decode request: messages: %w", err)
	}
	names := make(map[string]string) // tool_use id -> tool name
	for i, rm := range list {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(rm, &m); err != nil {
			return nil, nil, fmt.Errorf("anthropic: decode request: message %d: %w", i, err)
		}
		if m.Role != roleUser && m.Role != roleAssistant {
			return nil, nil, fmt.Errorf("anthropic: decode request: message %d: unknown role %q", i, m.Role)
		}
		blocks, err := decodeContent(m.Role, m.Content, names)
		if err != nil {
			return nil, nil, fmt.Errorf("anthropic: decode request: message %d: %w", i, err)
		}
		for _, b := range blocks {
			seq++
			b.msg.Seq = seq
			b.msg.Ref = kernel.FormatRef(seq)
			out = append(out, b.msg)
			if b.raw != nil {
				req.kept[seq] = keptBlock{
					role: b.role, kind: b.msg.Kind, text: b.msg.Text,
					toolID: b.msg.ToolCallID, raw: b.raw,
				}
			}
		}
	}
	return req, out, nil
}

func decodeContent(role string, content json.RawMessage, names map[string]string) ([]block, error) {
	t := bytes.TrimSpace(content)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return nil, nil
	}
	if t[0] == '"' {
		var s string
		if err := json.Unmarshal(t, &s); err != nil {
			return nil, err
		}
		return []block{{role: role, msg: kernel.Message{Role: roleOf(role), Kind: kernel.KindText, Text: s}}}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(t, &items); err != nil {
		return nil, fmt.Errorf("content must be a string or array: %w", err)
	}
	var out []block
	for _, raw := range items {
		var h struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Thinking  string          `json:"thinking"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &h); err != nil {
			return nil, err
		}
		var m kernel.Message
		switch h.Type {
		case "text":
			m = kernel.Message{Role: roleOf(role), Kind: kernel.KindText, Text: h.Text}
		case "tool_use":
			names[h.ID] = h.Name
			m = kernel.Message{Role: kernel.RoleAssistant, Kind: kernel.KindToolCall,
				ToolName: h.Name, ToolCallID: h.ID, Text: compactInput(h.Input)}
		case "tool_result":
			m = kernel.Message{Role: kernel.RoleTool, Kind: kernel.KindToolResult,
				ToolName: names[h.ToolUseID], ToolCallID: h.ToolUseID, Text: flattenResult(h.Content)}
		case "thinking", "redacted_thinking":
			m = kernel.Message{Role: kernel.RoleAssistant, Kind: kernel.KindReasoning, Text: h.Thinking}
		default:
			// image, document, server tool blocks, ...: visible only as a placeholder.
			// The original bytes are kept, so they are re-sent verbatim when the Seq matches.
			m = kernel.Message{Role: roleOf(role), Kind: kernel.KindText, Text: "[" + h.Type + "]"}
		}
		out = append(out, block{msg: m, role: role, raw: raw})
	}
	return out, nil
}

func roleOf(apiRole string) kernel.Role {
	if apiRole == roleAssistant {
		return kernel.RoleAssistant
	}
	return kernel.RoleUser
}

func systemText(raw json.RawMessage) (string, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return "", nil
	}
	if t[0] == '"' {
		var s string
		if err := json.Unmarshal(t, &s); err != nil {
			return "", fmt.Errorf("anthropic: decode request: system: %w", err)
		}
		return s, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(t, &parts); err != nil {
		return "", fmt.Errorf("anthropic: decode request: system: %w", err)
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n\n"), nil
}

func compactInput(raw json.RawMessage) string {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return "{}"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, t); err != nil {
		return string(t)
	}
	return buf.String()
}

func flattenResult(raw json.RawMessage) string {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return ""
	}
	switch t[0] {
	case '"':
		var s string
		if json.Unmarshal(t, &s) == nil {
			return s
		}
	case '[':
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(t, &parts) == nil {
			var texts []string
			for _, p := range parts {
				if p.Type == "text" {
					texts = append(texts, p.Text)
				}
			}
			return strings.Join(texts, "\n")
		}
	}
	return string(t)
}

// Encode rebuilds the request body with its messages replaced by the rendered
// window. Every other top-level field, the system prompt included, is copied
// byte for byte. System messages in the window are skipped because the system
// field is never rewritten. Reasoning blocks not kept from the original request
// are dropped, since Anthropic needs a signature to replay them.
func Encode(req *Request, window []kernel.Message) ([]byte, error) {
	var items []item
	for _, m := range window {
		if m.Role == kernel.RoleSystem {
			continue
		}
		if it, ok := req.blockFor(m); ok {
			items = append(items, it)
		}
	}
	runs := assemble(items)
	if len(runs) == 0 {
		return nil, errors.New("anthropic: encode: window has no sendable messages")
	}
	var msgs bytes.Buffer
	msgs.WriteByte('[')
	for i, r := range runs {
		if i > 0 {
			msgs.WriteByte(',')
		}
		role, err := marshalNoEscape(r.role)
		if err != nil {
			return nil, err
		}
		msgs.WriteString(`{"role":`)
		msgs.Write(role)
		msgs.WriteString(`,"content":[`)
		for j, it := range r.items {
			if j > 0 {
				msgs.WriteByte(',')
			}
			msgs.Write(it.raw)
		}
		msgs.WriteString("]}")
	}
	msgs.WriteByte(']')
	return buildObject(setField(req.fields, "messages", msgs.Bytes())), nil
}

// item is one content block ready to be placed in a message.
type item struct {
	role string
	kind kernel.Kind
	id   string
	raw  json.RawMessage
}

// run is a maximal sequence of items sharing one API role.
type run struct {
	role  string
	items []item
}

func (r *Request) blockFor(m kernel.Message) (item, bool) {
	role := roleUser
	if m.Role == kernel.RoleAssistant {
		role = roleAssistant
	}
	if k, ok := r.kept[m.Seq]; ok && k.role == role && k.kind == m.Kind &&
		k.text == m.Text && k.toolID == m.ToolCallID {
		return item{role: role, kind: m.Kind, id: m.ToolCallID, raw: k.raw}, true
	}
	switch m.Kind {
	case kernel.KindText:
		raw, err := marshalNoEscape(map[string]string{"type": "text", "text": m.Text})
		if err != nil {
			return item{}, false
		}
		return item{role: role, kind: kernel.KindText, raw: raw}, true
	case kernel.KindToolCall:
		raw, err := marshalNoEscape(toolUseBlock{
			Type: "tool_use", ID: m.ToolCallID, Name: m.ToolName, Input: json.RawMessage(inputObject(m.Text)),
		})
		if err != nil {
			return item{}, false
		}
		return item{role: roleAssistant, kind: kernel.KindToolCall, id: m.ToolCallID, raw: raw}, true
	case kernel.KindToolResult:
		raw, err := marshalNoEscape(toolResultBlock{
			Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Text,
		})
		if err != nil {
			return item{}, false
		}
		return item{role: roleUser, kind: kernel.KindToolResult, id: m.ToolCallID, raw: raw}, true
	}
	return item{}, false
}

type toolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type toolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

func inputObject(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "{") && json.Valid([]byte(t)) {
		return t
	}
	return "{}"
}

// assemble enforces the API rules on a flat item list. A tool_use is kept only
// if the next user run carries its tool_result, and a tool_result only if the
// previous assistant run carries its tool_use, so pairs are never split. Empty
// runs are dropped, neighbours of the same role are merged, and tool_result
// blocks are moved to the front of their user message as the API requires.
func assemble(items []item) []run {
	var runs []run
	for _, it := range items {
		if n := len(runs); n > 0 && runs[n-1].role == it.role {
			runs[n-1].items = append(runs[n-1].items, it)
		} else {
			runs = append(runs, run{role: it.role, items: []item{it}})
		}
	}

	allowed := make([]map[string]bool, len(runs))
	for r := range runs {
		if runs[r].role == roleAssistant {
			results := map[string]bool{}
			if r+1 < len(runs) {
				results = resultIDs(runs[r+1].items)
			}
			paired := map[string]bool{}
			kept := runs[r].items[:0]
			for _, it := range runs[r].items {
				if it.kind == kernel.KindToolCall {
					if results[it.id] && !paired[it.id] {
						paired[it.id] = true
						kept = append(kept, it)
					}
					continue
				}
				kept = append(kept, it)
			}
			runs[r].items = kept
			if r+1 < len(runs) {
				allowed[r+1] = paired
			}
			continue
		}
		paired := allowed[r]
		kept := runs[r].items[:0]
		for _, it := range runs[r].items {
			if it.kind == kernel.KindToolResult {
				if paired[it.id] {
					delete(paired, it.id)
					kept = append(kept, it)
				}
				continue
			}
			kept = append(kept, it)
		}
		runs[r].items = kept
	}

	var out []run
	for _, r := range runs {
		if len(r.items) == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].role == r.role {
			out[n-1].items = append(out[n-1].items, r.items...)
			continue
		}
		out = append(out, r)
	}
	for i := range out {
		if out[i].role == roleUser {
			out[i].items = resultsFirst(out[i].items)
		}
	}
	return out
}

func resultIDs(items []item) map[string]bool {
	ids := make(map[string]bool)
	for _, it := range items {
		if it.kind == kernel.KindToolResult {
			ids[it.id] = true
		}
	}
	return ids
}

func resultsFirst(items []item) []item {
	out := make([]item, 0, len(items))
	for _, it := range items {
		if it.kind == kernel.KindToolResult {
			out = append(out, it)
		}
	}
	for _, it := range items {
		if it.kind != kernel.KindToolResult {
			out = append(out, it)
		}
	}
	return out
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
		return nil, errors.New("body is not a JSON object")
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
		out = append(out, field{key: key, raw: raw})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return out, nil
}

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
	out := make([]field, len(fields))
	copy(out, fields)
	for i := range out {
		if out[i].key == key {
			out[i].raw = raw
			return out
		}
	}
	return append(out, field{key: key, raw: raw})
}

func lookup(fields []field, key string) json.RawMessage {
	for _, f := range fields {
		if f.key == key {
			return f.raw
		}
	}
	return nil
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
