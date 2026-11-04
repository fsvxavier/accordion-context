// Package openai adapts the OpenAI Chat Completions API (POST /v1/chat/completions)
// to the protocol-independent kernel model (spec §19.1). It depends only on the
// standard library and internal/kernel, and must not import store or session.
package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

const (
	roleSystem    = "system"
	roleDeveloper = "developer"
	roleUser      = "user"
	roleAssistant = "assistant"
	roleTool      = "tool"
)

// srcMsg is one message of the original request, kept so that Encode can
// reuse its bytes.
type srcMsg struct {
	fields  []field
	role    string
	content json.RawMessage
	calls   [][]byte // original tool_calls entries, in order
}

// piece is the kernel view of one part of a source message. A system, user or
// tool message is one piece. An assistant message is one text piece (when it
// has text) followed by one piece per tool call.
type piece struct {
	src      int
	role     kernel.Role
	kind     kernel.Kind
	toolName string
	toolID   string
	text     string
	call     int // index into srcMsg.calls, -1 for other kinds
}

// Request is a decoded Chat Completions request.
type Request struct {
	fields []field
	msgs   []srcMsg
	pieces map[int64]piece
}

// Decode parses a Chat Completions request body. Seq values are assigned 1..N in
// ledger order; the caller must keep Seq stable for messages it passes back to
// Encode. The roles system, developer, user, assistant and tool are supported.
// Other roles (for example the legacy function role) are rejected.
func Decode(body []byte) (*Request, []kernel.Message, error) {
	fields, err := parseObject(body)
	if err != nil {
		return nil, nil, fmt.Errorf("openai: decode request: %w", err)
	}
	rawMsgs := lookup(fields, "messages")
	if rawMsgs == nil || isNull(rawMsgs) {
		return nil, nil, errors.New("openai: decode request: messages missing")
	}
	var list []json.RawMessage
	if err := json.Unmarshal(rawMsgs, &list); err != nil {
		return nil, nil, fmt.Errorf("openai: decode request: messages: %w", err)
	}

	req := &Request{fields: fields, pieces: make(map[int64]piece)}
	var out []kernel.Message
	var seq int64
	emit := func(p piece) {
		seq++
		req.pieces[seq] = p
		out = append(out, kernel.Message{
			Seq: seq, Ref: kernel.FormatRef(seq), Role: p.role, Kind: p.kind,
			ToolName: p.toolName, ToolCallID: p.toolID, Text: p.text,
		})
	}

	names := make(map[string]string) // tool_call id -> function name
	for i, rm := range list {
		var wm struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			ToolCalls  json.RawMessage `json:"tool_calls"`
			ToolCallID string          `json:"tool_call_id"`
		}
		if err := json.Unmarshal(rm, &wm); err != nil {
			return nil, nil, fmt.Errorf("openai: decode request: message %d: %w", i, err)
		}
		mfields, err := parseObject(rm)
		if err != nil {
			return nil, nil, fmt.Errorf("openai: decode request: message %d: %w", i, err)
		}
		text, err := contentText(wm.Content)
		if err != nil {
			return nil, nil, fmt.Errorf("openai: decode request: message %d: content: %w", i, err)
		}
		src := len(req.msgs)
		req.msgs = append(req.msgs, srcMsg{fields: mfields, role: wm.Role, content: wm.Content})

		switch wm.Role {
		case roleSystem, roleDeveloper:
			emit(piece{src: src, role: kernel.RoleSystem, kind: kernel.KindText, text: text, call: -1})
		case roleUser:
			emit(piece{src: src, role: kernel.RoleUser, kind: kernel.KindText, text: text, call: -1})
		case roleAssistant:
			calls, err := decodeCalls(wm.ToolCalls)
			if err != nil {
				return nil, nil, fmt.Errorf("openai: decode request: message %d: %w", i, err)
			}
			if text != "" || len(calls) == 0 {
				emit(piece{src: src, role: kernel.RoleAssistant, kind: kernel.KindText, text: text, call: -1})
			}
			for ci, c := range calls {
				names[c.id] = c.name
				req.msgs[src].calls = append(req.msgs[src].calls, c.raw)
				emit(piece{src: src, role: kernel.RoleAssistant, kind: kernel.KindToolCall,
					toolName: c.name, toolID: c.id, text: c.args, call: ci})
			}
		case roleTool:
			emit(piece{src: src, role: kernel.RoleTool, kind: kernel.KindToolResult,
				toolName: names[wm.ToolCallID], toolID: wm.ToolCallID, text: text, call: -1})
		default:
			return nil, nil, fmt.Errorf("openai: decode request: message %d: unsupported role %q", i, wm.Role)
		}
	}
	return req, out, nil
}

// contentText flattens a message content, which is a string, an array of parts,
// or absent. Text parts contribute their text. Other parts (images, files) are
// visible only as a placeholder, and their original bytes are kept by Encode.
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
		return "", fmt.Errorf("content must be a string or array: %w", err)
	}
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.Type == "text" && p.Text != nil {
			texts = append(texts, *p.Text)
			continue
		}
		texts = append(texts, "["+p.Type+"]")
	}
	return strings.Join(texts, "\n"), nil
}

// wireCall is one decoded entry of an assistant's tool_calls array.
type wireCall struct {
	id   string
	name string
	args string
	raw  []byte
}

func decodeCalls(raw json.RawMessage) ([]wireCall, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(t, &items); err != nil {
		return nil, fmt.Errorf("tool_calls must be an array: %w", err)
	}
	out := make([]wireCall, 0, len(items))
	for i, it := range items {
		var h struct {
			ID       string `json:"id"`
			Function struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"function"`
		}
		if err := json.Unmarshal(it, &h); err != nil {
			return nil, fmt.Errorf("tool_calls %d: %w", i, err)
		}
		out = append(out, wireCall{
			id: h.ID, name: h.Function.Name, args: argumentsText(h.Function.Arguments), raw: []byte(it),
		})
	}
	return out, nil
}

// argumentsText returns the arguments string of a tool call. The wire form is a
// JSON string. A non-string value is returned compacted.
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

// callEntry is one tool_calls entry of an output message.
type callEntry struct {
	id  string
	raw []byte
}

// outMsg is one message of the rebuilt request.
type outMsg struct {
	fields  []field
	role    string
	hasText bool
	toolID  string
	calls   []callEntry
}

// builder collects the window's pieces into output messages, grouped by their
// source message so that unchanged messages keep their original bytes.
type builder struct {
	out   []*outMsg
	bySrc map[int]*outMsg
}

// Encode rebuilds the request body with its messages replaced by the rendered
// window. Every other top-level field is copied byte for byte. Unchanged messages
// keep their original bytes. Text gets its reference label, and the window is
// made valid for the API: an assistant tool call is kept only when its tool
// result follows it in the window, and a tool result is kept only when its call
// is kept. Assistant messages left without text or calls are dropped.
func Encode(req *Request, window []kernel.Message) ([]byte, error) {
	b := &builder{bySrc: make(map[int]*outMsg)}
	for _, m := range window {
		if p, ok := req.pieces[m.Seq]; ok && p.matches(m) {
			b.addKept(req, p, m)
		} else {
			b.addFresh(m)
		}
	}
	msgs := b.finish()
	if len(msgs) == 0 {
		return nil, errors.New("openai: encode: window has no sendable messages")
	}
	items := make([][]byte, 0, len(msgs))
	for _, om := range msgs {
		items = append(items, om.object())
	}
	return buildObject(setField(req.fields, "messages", joinArray(items))), nil
}

func (p piece) matches(m kernel.Message) bool {
	return p.role == m.Role && p.kind == m.Kind && p.text == m.Text && p.toolID == m.ToolCallID
}

func (b *builder) addKept(req *Request, p piece, m kernel.Message) {
	s := req.msgs[p.src]
	om := b.bySrc[p.src]
	if om == nil {
		om = &outMsg{fields: s.fields, role: s.role}
		b.bySrc[p.src] = om
		b.out = append(b.out, om)
	}
	switch p.kind {
	case kernel.KindText:
		if label := kernel.RefLabel(m); label != "" {
			om.fields = setField(om.fields, "content", labelContent(s.content, label, m.Text))
		}
		om.hasText = true
	case kernel.KindToolCall:
		om.calls = append(om.calls, callEntry{id: p.toolID, raw: s.calls[p.call]})
	case kernel.KindToolResult:
		om.toolID = p.toolID
	}
}

// addFresh adds a message that did not come from the request: a summary block,
// an intent ledger, or a tool call or result that the window rewrote.
func (b *builder) addFresh(m kernel.Message) {
	switch m.Kind {
	case kernel.KindText:
		role := roleUser
		switch m.Role {
		case kernel.RoleSystem:
			role = roleSystem
		case kernel.RoleAssistant:
			role = roleAssistant
		}
		b.out = append(b.out, &outMsg{
			role: role, hasText: true,
			fields: []field{
				{key: "role", raw: mustString(role)},
				{key: "content", raw: mustString(kernel.RefLabel(m) + m.Text)},
			},
		})
	case kernel.KindToolCall:
		entry := callEntry{id: m.ToolCallID, raw: freshCall(m)}
		if n := len(b.out); n > 0 && b.out[n-1].role == roleAssistant {
			b.out[n-1].calls = append(b.out[n-1].calls, entry)
			return
		}
		b.out = append(b.out, &outMsg{
			role:   roleAssistant,
			fields: []field{{key: "role", raw: mustString(roleAssistant)}},
			calls:  []callEntry{entry},
		})
	case kernel.KindToolResult:
		b.out = append(b.out, &outMsg{
			role: roleTool, toolID: m.ToolCallID,
			fields: []field{
				{key: "role", raw: mustString(roleTool)},
				{key: "tool_call_id", raw: mustString(m.ToolCallID)},
				{key: "content", raw: mustString(m.Text)},
			},
		})
	}
	// Reasoning has no Chat Completions form and is not sent back.
}

// finish enforces the pairing rules. A tool result is emitted only directly after
// an assistant message whose tool call it answers, and a tool call is emitted only
// when its result follows it. Results of dropped calls are dropped as well.
func (b *builder) finish() []*outMsg {
	msgs := b.out
	var out []*outMsg
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		switch m.role {
		case roleTool:
			// An orphan result: its call is not in the window.
			continue
		case roleAssistant:
		default:
			out = append(out, m)
			continue
		}
		if len(m.calls) == 0 {
			if m.hasText {
				out = append(out, m)
			}
			continue
		}
		j := i + 1
		answered := make(map[string]bool)
		for ; j < len(msgs) && msgs[j].role == roleTool; j++ {
			answered[msgs[j].toolID] = true
		}
		used := make(map[string]bool)
		var kept []callEntry
		for _, c := range m.calls {
			if answered[c.id] && !used[c.id] {
				used[c.id] = true
				kept = append(kept, c)
			}
		}
		if len(kept) > 0 || m.hasText {
			m.calls = kept
			out = append(out, m)
		}
		for k := i + 1; k < j; k++ {
			if used[msgs[k].toolID] {
				out = append(out, msgs[k])
				delete(used, msgs[k].toolID)
			}
		}
		i = j - 1
	}
	return out
}

func (om *outMsg) object() []byte {
	fields := om.fields
	if om.role == roleAssistant {
		if !om.hasText {
			fields = setField(fields, "content", []byte("null"))
		}
		if len(om.calls) > 0 {
			items := make([][]byte, 0, len(om.calls))
			for _, c := range om.calls {
				items = append(items, c.raw)
			}
			fields = setField(fields, "tool_calls", joinArray(items))
		} else {
			fields = removeField(fields, "tool_calls")
		}
	}
	return buildObject(fields)
}

// labelContent puts the reference label in front of a text content. A string
// becomes one string, and an array of parts gets the label as a leading text part.
// The other parts keep their bytes.
func labelContent(raw json.RawMessage, label, text string) []byte {
	t := bytes.TrimSpace(raw)
	if len(t) > 0 && t[0] == '[' {
		var parts []json.RawMessage
		if json.Unmarshal(t, &parts) == nil {
			lp, _ := marshalNoEscape(struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{"text", label})
			items := [][]byte{lp}
			for _, p := range parts {
				items = append(items, []byte(p))
			}
			return joinArray(items)
		}
	}
	return mustString(label + text)
}

// freshCall builds a tool_calls entry for a call that is not in the request.
func freshCall(m kernel.Message) []byte {
	args := strings.TrimSpace(m.Text)
	if !strings.HasPrefix(args, "{") || !json.Valid([]byte(args)) {
		args = "{}"
	}
	raw, _ := marshalNoEscape(struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}{
		ID:   m.ToolCallID,
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: m.ToolName, Arguments: args},
	})
	return raw
}
