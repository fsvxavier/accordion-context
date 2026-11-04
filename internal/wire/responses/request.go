// Package responses adapts the OpenAI Responses API (POST /v1/responses, spec
// §19.1) to the protocol-independent kernel model. It depends only on the
// standard library and internal/kernel, and must not import store, session or
// other wire packages.
//
// Stateful requests are not compacted. A request with previous_response_id or
// conversation refers to items the server keeps, so Decode returns ErrStateful
// and the caller forwards the request unchanged.
package responses

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// ErrStateful is returned by Decode when the request refers to server-side state
// through previous_response_id or conversation. The server then holds history
// that the proxy cannot see, so rewriting the window would make the two copies
// diverge. The caller must forward such a request unchanged and must not
// compress it.
var ErrStateful = errors.New("responses: request uses server-side state; forward unchanged")

// Sentinel sources for pieces that do not come from an input item.
const (
	srcInstructions = -1 // the top-level instructions field
	srcString       = -2 // a plain string input, one user message
)

// srcItem is one element of the input array, kept so that Encode can reuse its
// bytes when it is unchanged.
type srcItem struct {
	raw    []byte
	fields []field
}

// piece is the kernel view of one source item. Each input item yields exactly
// one kernel message.
type piece struct {
	src      int // index into Request.items, or srcInstructions or srcString
	role     kernel.Role
	kind     kernel.Kind
	toolName string
	callID   string
	text     string
	tokens   int // set for opaque items, whose text is only a fingerprint
}

// Request is a decoded Responses request.
type Request struct {
	fields []field
	items  []srcItem
	pieces map[int64]piece
}

// Decode parses a Responses request body. The instructions field, when present,
// becomes the first message (role system, seq 1). Each input item follows in
// order. A string input becomes one user message. Seq values are assigned 1..N;
// the caller must keep them stable for messages it passes back to Encode.
//
// Item types message, function_call, function_call_output and reasoning are
// modelled. A message item may omit "type", which is how the API accepts role
// and content shorthand. Any other item type (compaction, compaction_trigger,
// local_shell_call, web_search_call, ...) is opaque: it becomes one KindOpaque
// message, is never paired or summarized, and Encode sends its original bytes
// back in the same place.
//
// Decode returns ErrStateful, unwrapped, when previous_response_id or
// conversation is set to a non-empty value.
func Decode(body []byte) (*Request, []kernel.Message, error) {
	fields, err := parseObject(body)
	if err != nil {
		return nil, nil, fmt.Errorf("responses: decode request: %w", err)
	}
	if stateful(fields) {
		return nil, nil, ErrStateful
	}
	req := &Request{fields: fields, pieces: make(map[int64]piece)}
	var out []kernel.Message
	var seq int64
	emit := func(p piece) {
		seq++
		req.pieces[seq] = p
		out = append(out, kernel.Message{
			Seq: seq, Ref: kernel.FormatRef(seq), Role: p.role, Kind: p.kind,
			ToolName: p.toolName, ToolCallID: p.callID, Text: p.text, Tokens: p.tokens,
		})
	}

	if raw := lookup(fields, "instructions"); raw != nil && !isNull(raw) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, nil, fmt.Errorf("responses: decode request: instructions must be a string: %w", err)
		}
		emit(piece{src: srcInstructions, role: kernel.RoleSystem, kind: kernel.KindText, text: s})
	}

	raw := lookup(fields, "input")
	if raw == nil || isNull(raw) {
		return nil, nil, errors.New("responses: decode request: input missing")
	}
	if t := bytes.TrimSpace(raw); len(t) > 0 && t[0] == '"' {
		var s string
		if err := json.Unmarshal(t, &s); err != nil {
			return nil, nil, fmt.Errorf("responses: decode request: input: %w", err)
		}
		emit(piece{src: srcString, role: kernel.RoleUser, kind: kernel.KindText, text: s})
		return req, out, nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, nil, fmt.Errorf("responses: decode request: input must be a string or an array: %w", err)
	}

	req.items = make([]srcItem, len(list))
	names := make(map[string]string) // call_id -> function name
	for i, it := range list {
		ifields, err := parseObject(it)
		if err != nil {
			return nil, nil, fmt.Errorf("responses: decode request: input %d: %w", i, err)
		}
		req.items[i] = srcItem{raw: []byte(it), fields: ifields}

		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(it, &head); err != nil {
			return nil, nil, fmt.Errorf("responses: decode request: input %d: %w", i, err)
		}
		if !modelledItem(head.Type) {
			emit(piece{src: i, role: kernel.RoleAssistant, kind: kernel.KindOpaque,
				text: opaqueText(head.Type, it), tokens: opaqueTokens(it)})
			continue
		}

		var h struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			CallID    string          `json:"call_id"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Output    json.RawMessage `json:"output"`
			Summary   []struct {
				Text *string `json:"text"`
			} `json:"summary"`
		}
		if err := json.Unmarshal(it, &h); err != nil {
			return nil, nil, fmt.Errorf("responses: decode request: input %d: %w", i, err)
		}
		typ := h.Type
		if typ == "" {
			typ = "message"
		}
		switch typ {
		case "message":
			role, err := messageRole(h.Role)
			if err != nil {
				return nil, nil, fmt.Errorf("responses: decode request: input %d: %w", i, err)
			}
			text, err := contentText(h.Content)
			if err != nil {
				return nil, nil, fmt.Errorf("responses: decode request: input %d: content: %w", i, err)
			}
			emit(piece{src: i, role: role, kind: kernel.KindText, text: text})
		case "function_call":
			names[h.CallID] = h.Name
			emit(piece{src: i, role: kernel.RoleAssistant, kind: kernel.KindToolCall,
				toolName: h.Name, callID: h.CallID, text: argumentsText(h.Arguments)})
		case "function_call_output":
			text, err := contentText(h.Output)
			if err != nil {
				return nil, nil, fmt.Errorf("responses: decode request: input %d: output: %w", i, err)
			}
			emit(piece{src: i, role: kernel.RoleTool, kind: kernel.KindToolResult,
				toolName: names[h.CallID], callID: h.CallID, text: text})
		case "reasoning":
			emit(piece{src: i, role: kernel.RoleAssistant, kind: kernel.KindReasoning,
				text: summaryText(h.Summary)})
		}
	}
	return req, out, nil
}

// modelledItem reports whether the codec models an input item of this type. The
// empty type is a message, which may omit "type".
func modelledItem(typ string) bool {
	switch typ {
	case "", "message", "function_call", "function_call_output", "reasoning":
		return true
	}
	return false
}

// opaqueText is the text of an opaque item in the ledger: its type and a digest
// of its bytes. The digest tells two items of the same type apart, which the
// ledger needs when it compares a stored transcript with a new request. The item
// itself is never rendered from this text; Encode sends its original bytes.
func opaqueText(typ string, raw []byte) string {
	if len(typ) > 64 {
		typ = typ[:64]
	}
	sum := sha256.Sum256(raw)
	return "opaque " + typ + " sha256:" + hex.EncodeToString(sum[:16])
}

// opaqueTokens estimates the size of an opaque item from its bytes. Encrypted
// content is dense, so it is counted at about three bytes per token.
func opaqueTokens(raw []byte) int {
	return max(1, (len(raw)+2)/3)
}

// stateful reports whether the request names server-side state.
func stateful(fields []field) bool {
	for _, key := range []string{"previous_response_id", "conversation"} {
		raw := lookup(fields, key)
		if raw == nil || isNull(raw) {
			continue
		}
		if string(bytes.TrimSpace(raw)) == `""` {
			continue
		}
		return true
	}
	return false
}

func messageRole(role string) (kernel.Role, error) {
	switch role {
	case "system", "developer":
		return kernel.RoleSystem, nil
	case "user":
		return kernel.RoleUser, nil
	case "assistant":
		return kernel.RoleAssistant, nil
	}
	return "", fmt.Errorf("unsupported role %q", role)
}

// contentText flattens message content, which is a string, an array of parts, or
// absent. A part with text contributes its text. Other parts (images, files,
// refusals) are visible only as a placeholder. Their bytes are kept by Encode.
func contentText(raw json.RawMessage) (string, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return "", nil
	}
	if t[0] == '"' {
		var s string
		if err := json.Unmarshal(t, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	var parts []struct {
		Type string  `json:"type"`
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(t, &parts); err != nil {
		return "", fmt.Errorf("must be a string or an array: %w", err)
	}
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.Text != nil {
			texts = append(texts, *p.Text)
			continue
		}
		texts = append(texts, "["+p.Type+"]")
	}
	return strings.Join(texts, "\n"), nil
}

// argumentsText returns the arguments string of a function call. The wire form
// is a JSON string. A non-string value is returned compacted.
func argumentsText(raw json.RawMessage) string {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return ""
	}
	if t[0] == '"' {
		var s string
		if json.Unmarshal(t, &s) == nil {
			return s
		}
		return string(t)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, t); err != nil {
		return string(t)
	}
	return buf.String()
}

// summaryText joins the summary texts of a reasoning item.
func summaryText(summary []struct {
	Text *string `json:"text"`
}) string {
	var parts []string
	for _, s := range summary {
		if s.Text != nil {
			parts = append(parts, *s.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// Encode rebuilds the request body with its input replaced by the rendered
// window. Every other top-level field, including instructions, is copied byte
// for byte. Unchanged items keep their original bytes. Text gets its reference
// label, and the window is made valid for the API. A function call is kept only
// when its output follows it in the window, and an output is kept only when its
// call precedes it. Reasoning items are sent back only when they are in the
// window. A fresh summary is sent as a user or assistant message.
//
// The input is always written as an array. The instructions message is not
// written to input, because instructions is passed through unchanged.
func Encode(req *Request, window []kernel.Message) ([]byte, error) {
	if req == nil {
		return nil, errors.New("responses: encode: nil request")
	}
	var items []outItem
	for _, m := range window {
		p, ok := req.pieces[m.Seq]
		if ok && p.src == srcInstructions {
			continue
		}
		if ok && p.src >= 0 && p.matches(m) {
			items = append(items, keptItem(req, p, m))
			continue
		}
		if it, ok := freshItem(m); ok {
			items = append(items, it)
			continue
		}
		if m.Kind == kernel.KindOpaque {
			// An opaque item is only ever sent back as the request carried it. A window
			// that names one the request does not have is refused, not silently cut.
			return nil, fmt.Errorf("responses: encode: opaque item %s is not in the request", m.Ref)
		}
	}
	items = dropOrphanReasoning(pairCalls(items))
	if len(items) == 0 {
		return nil, errors.New("responses: encode: window has no sendable input")
	}
	raws := make([][]byte, len(items))
	for i, it := range items {
		raws[i] = it.raw
	}
	return buildObject(setField(req.fields, "input", joinArray(raws))), nil
}

// outItem is one item of the rebuilt input. src is the index of the source item
// it came from, or -1 for a fresh item.
type outItem struct {
	raw    []byte
	kind   kernel.Kind
	callID string
	src    int
}

func (p piece) matches(m kernel.Message) bool {
	return p.role == m.Role && p.kind == m.Kind && p.text == m.Text && p.callID == m.ToolCallID
}

// keptItem returns a source item, with its label added when it is a text message.
func keptItem(req *Request, p piece, m kernel.Message) outItem {
	src := req.items[p.src]
	out := outItem{raw: src.raw, kind: p.kind, callID: p.callID, src: p.src}
	if p.kind != kernel.KindText {
		return out
	}
	label := kernel.RefLabel(m)
	if label == "" {
		return out
	}
	content := labelContent(lookup(src.fields, "content"), partType(p.role), label, m.Text)
	out.raw = buildObject(setField(src.fields, "content", content))
	return out
}

// partType is the content part type the API expects for a role's text.
func partType(role kernel.Role) string {
	if role == kernel.RoleAssistant {
		return "output_text"
	}
	return "input_text"
}

// labelContent puts the reference label in front of a text content. A string
// becomes one string, and an array of parts gets the label as a leading part. The
// other parts keep their bytes.
func labelContent(raw json.RawMessage, typ, label, text string) []byte {
	t := bytes.TrimSpace(raw)
	if len(t) > 0 && t[0] == '[' {
		var parts []json.RawMessage
		if json.Unmarshal(t, &parts) == nil {
			lp, _ := marshalNoEscape(struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{typ, label})
			items := make([][]byte, 0, len(parts)+1)
			items = append(items, lp)
			for _, p := range parts {
				items = append(items, []byte(p))
			}
			return joinArray(items)
		}
	}
	return mustString(label + text)
}

// freshItem builds an input item for a message that did not come from the
// request. Reasoning has no portable form, so it is not sent back.
func freshItem(m kernel.Message) (outItem, bool) {
	switch m.Kind {
	case kernel.KindText:
		role := "user"
		switch m.Role {
		case kernel.RoleSystem:
			role = "system"
		case kernel.RoleAssistant:
			role = "assistant"
		}
		raw := buildObject([]field{
			{key: "role", raw: mustString(role)},
			{key: "content", raw: mustString(kernel.RefLabel(m) + m.Text)},
		})
		return outItem{raw: raw, kind: kernel.KindText, src: -1}, true
	case kernel.KindToolCall:
		raw := buildObject([]field{
			{key: "type", raw: mustString("function_call")},
			{key: "call_id", raw: mustString(m.ToolCallID)},
			{key: "name", raw: mustString(m.ToolName)},
			{key: "arguments", raw: mustString(freshArgs(m.Text))},
		})
		return outItem{raw: raw, kind: kernel.KindToolCall, callID: m.ToolCallID, src: -1}, true
	case kernel.KindToolResult:
		raw := buildObject([]field{
			{key: "type", raw: mustString("function_call_output")},
			{key: "call_id", raw: mustString(m.ToolCallID)},
			{key: "output", raw: mustString(m.Text)},
		})
		return outItem{raw: raw, kind: kernel.KindToolResult, callID: m.ToolCallID, src: -1}, true
	}
	return outItem{}, false
}

// dropOrphanReasoning keeps a reasoning item only when the item that followed it
// in the input is the next item of the output. The API expects a reasoning item
// to be sent with the item it precedes, so a reasoning item whose follower was
// dropped, or was replaced by something else, is dropped too. The check runs
// backwards so that a reasoning item chained to a dropped reasoning item goes
// as well. The rule is the conservative reading of the API's requirement and
// has not been checked against the live API.
func dropOrphanReasoning(items []outItem) []outItem {
	keep := make([]bool, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		if it.kind != kernel.KindReasoning {
			keep[i] = true
			continue
		}
		keep[i] = it.src >= 0 && i+1 < len(items) && keep[i+1] && items[i+1].src == it.src+1
	}
	var out []outItem
	for i, it := range items {
		if keep[i] {
			out = append(out, it)
		}
	}
	return out
}

// freshArgs returns arguments that are a JSON object, or {} when the text is not one.
func freshArgs(text string) string {
	args := strings.TrimSpace(text)
	if !strings.HasPrefix(args, "{") || !json.Valid([]byte(args)) {
		return "{}"
	}
	return args
}

// pairCalls enforces the pairing rules. A call is kept only when a result for it
// appears after it, and a result is kept only when its call was kept before it.
// Only the first result of a call is kept.
func pairCalls(items []outItem) []outItem {
	firstResult := make(map[string]int)
	for i, it := range items {
		if it.kind != kernel.KindToolResult {
			continue
		}
		if _, ok := firstResult[it.callID]; !ok {
			firstResult[it.callID] = i
		}
	}
	open := make(map[string]bool)
	var out []outItem
	for i, it := range items {
		switch it.kind {
		case kernel.KindToolCall:
			if j, ok := firstResult[it.callID]; !ok || j <= i {
				continue
			}
			open[it.callID] = true
		case kernel.KindToolResult:
			if !open[it.callID] {
				continue
			}
			delete(open, it.callID)
		}
		out = append(out, it)
	}
	return out
}
