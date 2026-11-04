// Package google adapts the Google Gemini generateContent and
// streamGenerateContent APIs to the protocol-independent kernel model (spec
// §19.1). It depends only on the standard library and internal/kernel, and must
// not import store or session.
//
// Gemini has no tool call ids. Decode synthesizes one for every functionCall
// part, "gc:<name>:<n>", where n counts the calls with that name from the start
// of the contents. A functionResponse is linked to the oldest unanswered call with
// the same name (FIFO), so ids are stable as long as Decode sees the full
// history, which the proxy always sends. The ids are never sent upstream.
package google

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// srcContent is one content object of the original request, kept so that Encode
// can reuse the bytes of its parts.
type srcContent struct {
	fields []field
	role   string // normalized: user or model; empty for systemInstruction
	parts  [][]byte
}

// piece is the kernel view of one part of a source content.
type piece struct {
	src      int
	part     int
	role     kernel.Role
	kind     kernel.Kind
	toolName string
	toolID   string
	text     string
}

// Request is a decoded generateContent request.
type Request struct {
	fields []field
	srcs   []srcContent
	pieces map[int64]piece
	sysSrc int // index into srcs of systemInstruction, -1 when absent
}

// partInfo is the classified form of one part.
type partInfo struct {
	kind kernel.Kind
	role kernel.Role
	name string
	id   string
	text string
}

// Decode parses a generateContent request body. Seq values are assigned 1..N in
// ledger order (systemInstruction first, then the contents, part by part). The
// caller must keep Seq stable for the messages it passes back to Encode.
func Decode(body []byte) (*Request, []kernel.Message, error) {
	fields, err := parseObject(body)
	if err != nil {
		return nil, nil, fmt.Errorf("google: decode request: %w", err)
	}
	req := &Request{fields: fields, pieces: make(map[int64]piece), sysSrc: -1}
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

	if raw := lookup(fields, "systemInstruction"); raw != nil && !isNull(raw) {
		c, err := parseContent(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("google: decode request: systemInstruction: %w", err)
		}
		src := len(req.srcs)
		req.srcs = append(req.srcs, c)
		req.sysSrc = src
		for pi, part := range c.parts {
			emit(piece{src: src, part: pi, role: kernel.RoleSystem, kind: kernel.KindText, text: partText(part)})
		}
	}

	rawContents := lookup(fields, "contents")
	if rawContents == nil || isNull(rawContents) {
		return nil, nil, errors.New("google: decode request: contents missing")
	}
	var list []json.RawMessage
	if err := json.Unmarshal(rawContents, &list); err != nil {
		return nil, nil, fmt.Errorf("google: decode request: contents: %w", err)
	}

	pending := make(map[string][]string) // function name -> unanswered call ids, oldest first
	ordinal := make(map[string]int)      // function name -> calls seen so far
	for ci, rc := range list {
		c, err := parseContent(rc)
		if err != nil {
			return nil, nil, fmt.Errorf("google: decode request: content %d: %w", ci, err)
		}
		src := len(req.srcs)
		req.srcs = append(req.srcs, c)
		for pi, part := range c.parts {
			p, err := classifyPart(c.role, part)
			if err != nil {
				return nil, nil, fmt.Errorf("google: decode request: content %d part %d: %w", ci, pi, err)
			}
			switch p.kind {
			case kernel.KindToolCall:
				ordinal[p.name]++
				p.id = fmt.Sprintf("gc:%s:%d", p.name, ordinal[p.name])
				pending[p.name] = append(pending[p.name], p.id)
			case kernel.KindToolResult:
				if q := pending[p.name]; len(q) > 0 {
					p.id = q[0]
					pending[p.name] = q[1:]
				}
			}
			emit(piece{src: src, part: pi, role: p.role, kind: p.kind,
				toolName: p.name, toolID: p.id, text: p.text})
		}
	}
	return req, out, nil
}

// parseContent reads one content object (or systemInstruction) and its raw parts.
func parseContent(raw []byte) (srcContent, error) {
	var c srcContent
	fields, err := parseObject(raw)
	if err != nil {
		return c, err
	}
	var h struct {
		Role  string          `json:"role"`
		Parts json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return c, err
	}
	if c.role, err = normalizeRole(h.Role); err != nil {
		return c, err
	}
	c.fields = fields
	if len(h.Parts) > 0 && !isNull(h.Parts) {
		var parts []json.RawMessage
		if err := json.Unmarshal(h.Parts, &parts); err != nil {
			return c, fmt.Errorf("parts must be an array: %w", err)
		}
		for i, p := range parts {
			if _, err := parseObject(p); err != nil {
				return c, fmt.Errorf("part %d: %w", i, err)
			}
			c.parts = append(c.parts, []byte(p))
		}
	}
	return c, nil
}

// normalizeRole maps the Gemini roles to user or model. The empty role and the
// legacy function role are user turns. A system instruction has no role, so it
// normalizes to empty, which the caller never checks.
func normalizeRole(r string) (string, error) {
	switch r {
	case "", "user", "function":
		return "user", nil
	case "model":
		return "model", nil
	}
	return "", fmt.Errorf("unsupported role %q", r)
}

// classifyPart maps one part to its kernel kind. A text part is text, or
// reasoning when thought is true. A functionCall is a tool call and a
// functionResponse is a tool result. Other parts (inlineData, fileData and the
// like) are visible only as a placeholder, and their bytes are kept by Encode.
func classifyPart(contentRole string, part []byte) (partInfo, error) {
	var w struct {
		Text             json.RawMessage `json:"text"`
		Thought          bool            `json:"thought"`
		FunctionCall     *wireCall       `json:"functionCall"`
		FunctionResponse *wireResponse   `json:"functionResponse"`
	}
	if err := json.Unmarshal(part, &w); err != nil {
		return partInfo{}, err
	}
	textRole := kernel.RoleUser
	if contentRole == "model" {
		textRole = kernel.RoleAssistant
	}
	switch {
	case w.FunctionCall != nil:
		return partInfo{kind: kernel.KindToolCall, role: kernel.RoleAssistant,
			name: w.FunctionCall.Name, text: compactJSON(w.FunctionCall.Args, "{}")}, nil
	case w.FunctionResponse != nil:
		return partInfo{kind: kernel.KindToolResult, role: kernel.RoleTool,
			name: w.FunctionResponse.Name, text: compactJSON(w.FunctionResponse.Response, "{}")}, nil
	}
	if s, ok := rawString(w.Text); ok {
		if w.Thought {
			return partInfo{kind: kernel.KindReasoning, role: kernel.RoleAssistant, text: s}, nil
		}
		return partInfo{kind: kernel.KindText, role: textRole, text: s}, nil
	}
	return partInfo{kind: kernel.KindText, role: textRole, text: placeholder(part)}, nil
}

type wireCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type wireResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

// partText is the text of a system instruction part.
func partText(part []byte) string {
	var w struct {
		Text json.RawMessage `json:"text"`
	}
	if json.Unmarshal(part, &w) == nil {
		if s, ok := rawString(w.Text); ok {
			return s
		}
	}
	return placeholder(part)
}

// rawString decodes a JSON string. The bool is false for absent, null and
// non-string values.
func rawString(raw json.RawMessage) (string, bool) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(t, &s); err != nil {
		return "", false
	}
	return s, true
}

// placeholder names a part that has no text, for example "[inlineData]".
func placeholder(part []byte) string {
	fields, err := parseObject(part)
	if err != nil || len(fields) == 0 {
		return "[part]"
	}
	return "[" + fields[0].key + "]"
}

// matches reports whether a window message is still the piece it was decoded from.
func (p piece) matches(m kernel.Message) bool {
	return p.role == m.Role && p.kind == m.Kind && p.text == m.Text &&
		p.toolID == m.ToolCallID && p.toolName == m.ToolName
}

// outContent is one content of the rebuilt request. A content that came from the
// request keeps its fields. A fresh content is built from the window.
type outContent struct {
	fields []field
	role   string
	fresh  bool
	parts  [][]byte
}

type builder struct {
	out   []*outContent
	bySrc map[int]*outContent
	sys   *outContent
}

// Encode rebuilds the request body with its contents replaced by the rendered
// window. Every other top-level field is copied byte for byte, and so are the
// parts that were not changed, including unknown part members such as
// thoughtSignature. Text gets its reference label.
//
// The window is made valid for the API: a functionCall is kept only when its
// functionResponse appears later in the window, and a functionResponse only when
// its call is kept. Duplicate calls and responses are dropped. Reasoning has no
// wire form when fresh and is not sent back. Contents left without parts are
// dropped. A window message that did not come from the request (a summary block,
// an intent ledger, or a tool call or result the window rewrote) is sent as a
// user or model part. A fresh system message is sent as a user part, because
// Gemini contents have no system role.
func Encode(req *Request, window []kernel.Message) ([]byte, error) {
	keep := pairKeep(window)
	b := &builder{bySrc: make(map[int]*outContent)}
	for i, m := range window {
		if (m.Kind == kernel.KindToolCall || m.Kind == kernel.KindToolResult) && !keep[i] {
			continue
		}
		if p, ok := req.pieces[m.Seq]; ok && p.matches(m) {
			b.addKept(req, p, m)
		} else {
			b.addFresh(m)
		}
	}
	var items [][]byte
	for _, c := range b.out {
		if len(c.parts) == 0 {
			continue
		}
		items = append(items, c.object())
	}
	if len(items) == 0 {
		return nil, errors.New("google: encode: window has no sendable messages")
	}
	fields := setField(req.fields, "contents", joinArray(items))
	if req.sysSrc >= 0 && b.sys != nil && len(b.sys.parts) > 0 {
		sys := setField(req.srcs[req.sysSrc].fields, "parts", joinArray(b.sys.parts))
		fields = setField(fields, "systemInstruction", buildObject(sys))
	} else if req.sysSrc >= 0 {
		fields = removeField(fields, "systemInstruction")
	}
	return buildObject(fields), nil
}

// pairKeep marks the tool call and result messages that Encode sends. A call is
// kept when a result with the same id follows it in the window. A result is kept
// when its call is kept, and only the first result and the first call of an id
// are used.
func pairKeep(window []kernel.Message) []bool {
	keep := make([]bool, len(window))
	firstCall := make(map[string]int)
	answered := make(map[string]bool)
	for i, m := range window {
		switch m.Kind {
		case kernel.KindToolCall:
			if m.ToolCallID == "" {
				continue
			}
			if _, ok := firstCall[m.ToolCallID]; !ok {
				firstCall[m.ToolCallID] = i
			}
		case kernel.KindToolResult:
			ci, ok := firstCall[m.ToolCallID]
			if m.ToolCallID == "" || !ok || answered[m.ToolCallID] {
				continue
			}
			answered[m.ToolCallID] = true
			keep[ci] = true
			keep[i] = true
		}
	}
	return keep
}

func (b *builder) addKept(req *Request, p piece, m kernel.Message) {
	s := req.srcs[p.src]
	var target *outContent
	if p.src == req.sysSrc {
		if b.sys == nil {
			b.sys = &outContent{}
		}
		target = b.sys
	} else {
		target = b.bySrc[p.src]
		if target == nil {
			target = &outContent{fields: s.fields, role: s.role}
			b.bySrc[p.src] = target
			b.out = append(b.out, target)
		}
	}
	part := s.parts[p.part]
	if m.Kind == kernel.KindText {
		if label := kernel.RefLabel(m); label != "" {
			target.parts = append(target.parts, labelPart(part, label, m.Text)...)
			return
		}
	}
	target.parts = append(target.parts, part)
}

// labelPart puts the reference label in front of a text part. A part without
// text (a placeholder such as inlineData) gets the label as a text part of its own.
func labelPart(part []byte, label, text string) [][]byte {
	var w struct {
		Text json.RawMessage `json:"text"`
	}
	if json.Unmarshal(part, &w) == nil {
		if _, ok := rawString(w.Text); ok {
			fields, err := parseObject(part)
			if err == nil {
				return [][]byte{buildObject(setField(fields, "text", mustString(label+text)))}
			}
		}
	}
	lp := buildObject([]field{{key: "text", raw: mustString(label)}})
	return [][]byte{lp, part}
}

// addFresh adds a message that did not come from the request.
func (b *builder) addFresh(m kernel.Message) {
	var role string
	var part []byte
	switch m.Kind {
	case kernel.KindText:
		role = "user"
		if m.Role == kernel.RoleAssistant {
			role = "model"
		}
		part = buildObject([]field{{key: "text", raw: mustString(kernel.RefLabel(m) + m.Text)}})
	case kernel.KindToolCall:
		role = "model"
		part = buildObject([]field{{key: "functionCall", raw: buildObject([]field{
			{key: "name", raw: mustString(m.ToolName)},
			{key: "args", raw: []byte(freshArgs(m.Text))},
		})}})
	case kernel.KindToolResult:
		role = "user"
		part = buildObject([]field{{key: "functionResponse", raw: buildObject([]field{
			{key: "name", raw: mustString(m.ToolName)},
			{key: "response", raw: []byte(freshResponse(m.Text))},
		})}})
	default:
		// Reasoning has no wire form for fresh messages and is not sent back.
		return
	}
	if n := len(b.out); n > 0 && b.out[n-1].fresh && b.out[n-1].role == role {
		b.out[n-1].parts = append(b.out[n-1].parts, part)
		return
	}
	b.out = append(b.out, &outContent{role: role, fresh: true, parts: [][]byte{part}})
}

// freshArgs returns the args of a fresh functionCall: the text when it is a JSON
// object, otherwise an empty object.
func freshArgs(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "{") && json.Valid([]byte(t)) {
		return compactJSON([]byte(t), "{}")
	}
	return "{}"
}

// freshResponse returns the response of a fresh functionResponse: the text when
// it is a JSON object, otherwise the text wrapped as {"content": text}.
func freshResponse(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "{") && json.Valid([]byte(t)) {
		return compactJSON([]byte(t), "{}")
	}
	return string(buildObject([]field{{key: "content", raw: mustString(text)}}))
}

func (c *outContent) object() []byte {
	parts := joinArray(c.parts)
	if c.fresh {
		return buildObject([]field{
			{key: "role", raw: mustString(c.role)},
			{key: "parts", raw: parts},
		})
	}
	return buildObject(setField(c.fields, "parts", parts))
}
