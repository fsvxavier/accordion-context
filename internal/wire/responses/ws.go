package responses

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// WebSocket mode of the Responses API (spec §19.1, §21 phase 8). The client sends
// one response.create frame per response, with the body fields of the POST
// request. The server answers with the same events that SSE mode sends, one per
// frame. A connection carries many responses, each one independent.
//
// FrameCodec is pure: it takes frames and returns frames. The transport (the
// WebSocket connection, the loop of rounds and the tool calls) lives in
// internal/proxy. One FrameCodec serves one connection. ReadCreate starts a new
// client-visible response, and the rounds of that response follow: StartRound,
// ServerEvent for each upstream event, Result, then FinishRound when the response
// is over. A round that continues with a compress or decompress result is never
// shown to the client as an end. Its terminal frame is not sent, and the next
// round continues the same response, so the client sees one response.

// CreateType is the type of the client frame that starts a response.
const CreateType = "response.create"

// ErrNotCreate reports a client frame that is not response.create, such as
// response.cancel. The caller forwards such a frame unchanged.
var ErrNotCreate = errors.New("responses: frame is not response.create")

// Terminal classifies a server event for the caller of ServerEvent.
type Terminal int

const (
	// NotTerminal means the round goes on.
	NotTerminal Terminal = iota
	// TerminalHeld means the round ended with response.completed, incomplete or
	// failed. Its frame is held, and FinishRound returns it rewritten for the client.
	TerminalHeld
	// TerminalError means an error event ended the response. It is returned for
	// forwarding, and no terminal frame follows.
	TerminalError
)

// Round summarizes one upstream round once its events are in.
type Round struct {
	// Complete is true when response.completed, incomplete or failed was seen.
	Complete bool
	Usage    Usage
	HasUsage bool
	// Calls are the internal compress and decompress calls, in output order.
	Calls []StreamCall
	// ClientCalls counts the function calls the client must run. A round with
	// them cannot continue on the server.
	ClientCalls int
	// Assistant holds the output items of the round, in order. A continuation sends
	// them back as the assistant turn, as the SSE path does.
	Assistant []json.RawMessage
}

// FrameCodec holds the state of one WebSocket connection.
type FrameCodec struct {
	// Response state, reset by ReadCreate.
	clientID string            // response id shown to the client
	seq      int64             // last sequence_number sent to the client
	rounds   int               // rounds started for this response
	offset   int               // added to the output_index of the current round
	outputs  []json.RawMessage // client-visible output items of all rounds
	held     []byte            // terminal frame of the current round, not yet sent

	// Round state, reset by StartRound.
	roundMax    int // highest upstream output_index of the round, -1 when none
	internal    map[int]bool
	sum         StreamSummary
	open        map[int]*StreamCall
	assistant   []json.RawMessage
	clientCalls int
}

// NewFrameCodec returns the state of a new connection.
func NewFrameCodec() *FrameCodec { return &FrameCodec{} }

// EventType returns the type field of a frame. A frame without one returns "".
func EventType(frame []byte) (string, error) {
	var h struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(frame, &h); err != nil {
		return "", fmt.Errorf("responses: frame: %w", err)
	}
	return h.Type, nil
}

// ReadCreate decodes a client response.create frame and starts a new response.
// It returns ErrNotCreate for other frame types. A stateful request returns
// ErrStateful, as Decode does, and the caller forwards the frame unchanged.
func (c *FrameCodec) ReadCreate(frame []byte) (*Request, []kernel.Message, error) {
	typ, err := EventType(frame)
	if err != nil {
		return nil, nil, err
	}
	if typ != CreateType {
		return nil, nil, ErrNotCreate
	}
	req, msgs, err := Decode(frame)
	if err != nil {
		return nil, nil, err
	}
	*c = FrameCodec{}
	return req, msgs, nil
}

// StartRound begins an upstream round of the current response. Output indexes
// of a continuation round follow the items already shown to the client.
func (c *FrameCodec) StartRound() {
	if c.rounds > 0 {
		c.offset += c.roundMax + 1
	}
	c.rounds++
	c.roundMax = -1
	c.internal = map[int]bool{}
	c.sum = StreamSummary{}
	c.open = map[int]*StreamCall{}
	c.assistant = nil
	c.clientCalls = 0
	c.held = nil
}

// ServerEvent processes one upstream event of the current round. The returned
// frame goes to the client. It is nil when the event is withheld: an internal
// compress or decompress call, or the created and in_progress events of a
// continuation round, which the client has already seen. A terminal
// response.completed, incomplete or failed event is held. FinishRound sends it.
// An error event is returned for forwarding and reports TerminalError.
//
// Malformed frames are returned unchanged, as the SSE path copies them.
func (c *FrameCodec) ServerEvent(frame []byte) ([]byte, Terminal, error) {
	if c.open == nil {
		return nil, NotTerminal, errors.New("responses: server event before StartRound")
	}
	var ev struct {
		Type        string          `json:"type"`
		OutputIndex *int            `json:"output_index"`
		Item        json.RawMessage `json:"item"`
		Response    *struct {
			ID string `json:"id"`
		} `json:"response"`
	}
	if err := json.Unmarshal(frame, &ev); err != nil {
		return frame, NotTerminal, nil
	}
	observeLine(append([]byte("data: "), frame...), &c.sum, c.open)

	idx := -1
	if ev.OutputIndex != nil {
		idx = *ev.OutputIndex
		if idx > c.roundMax {
			c.roundMax = idx
		}
	}
	var item struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if len(ev.Item) > 0 && string(ev.Item) != "null" {
		_ = json.Unmarshal(ev.Item, &item)
	}
	isCall := item.Type == "function_call"

	switch ev.Type {
	case "response.output_item.added":
		if isCall && idx >= 0 {
			if isInternalCall(item.Name) {
				c.internal[idx] = true
			} else {
				c.clientCalls++
			}
		}
	case "response.output_item.done":
		if isCall && idx >= 0 && isInternalCall(item.Name) {
			c.internal[idx] = true
		}
		if len(ev.Item) > 0 && string(ev.Item) != "null" {
			raw := append(json.RawMessage(nil), ev.Item...)
			c.assistant = append(c.assistant, raw)
			if idx < 0 || !c.internal[idx] {
				c.outputs = append(c.outputs, raw)
			}
		}
	case "response.completed", "response.incomplete", "response.failed":
		c.held = append([]byte(nil), frame...)
		return nil, TerminalHeld, nil
	case "error":
		return frame, TerminalError, nil
	case "response.created", "response.in_progress":
		if c.rounds > 1 {
			return nil, NotTerminal, nil
		}
		if ev.Type == "response.created" && ev.Response != nil && c.clientID == "" {
			c.clientID = ev.Response.ID
		}
	}
	if idx >= 0 && c.internal[idx] {
		return nil, NotTerminal, nil
	}
	out, err := c.stamp(frame)
	return out, NotTerminal, err
}

// Result summarizes the current round after its last event. Call it once, when
// ServerEvent reports a terminal event or the upstream ends.
func (c *FrameCodec) Result() Round {
	sum := finishStream(c.sum, c.open)
	res := Round{
		Complete:    sum.Complete,
		Usage:       sum.Usage,
		HasUsage:    sum.HasUsage,
		Calls:       sum.Calls,
		ClientCalls: c.clientCalls,
		Assistant:   c.assistant,
	}
	if len(res.Assistant) == 0 {
		// Without done items, rebuild the calls, as the SSE path does.
		for _, call := range res.Calls {
			raw, _ := json.Marshal(map[string]string{
				"type": "function_call", "call_id": call.CallID, "name": call.Name, "arguments": call.Arguments,
			})
			res.Assistant = append(res.Assistant, raw)
		}
	}
	return res
}

// FinishRound returns the held terminal frame of the round, rewritten for the
// client. Internal items are removed from its output. A response that spans
// several rounds shows every visible item of all rounds. It returns nil when no
// terminal frame was held. The caller calls it only when the response ends
// here, because a continuing round sends no terminal frame.
func (c *FrameCodec) FinishRound() ([]byte, error) {
	if c.held == nil {
		return nil, nil
	}
	frame := c.held
	c.held = nil
	var m map[string]json.RawMessage
	if err := json.Unmarshal(frame, &m); err != nil {
		return frame, nil
	}
	rawResp, ok := m["response"]
	if !ok {
		return c.stamp(frame)
	}
	var r map[string]json.RawMessage
	if err := json.Unmarshal(rawResp, &r); err != nil {
		return c.stamp(frame)
	}
	if c.rounds > 1 {
		out := c.outputs
		if out == nil {
			out = []json.RawMessage{}
		}
		r["output"] = mustRaw(out)
	} else if raw, ok := r["output"]; ok {
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) == nil {
			kept := make([]json.RawMessage, 0, len(items))
			for i, it := range items {
				if !c.internal[i] {
					kept = append(kept, it)
				}
			}
			r["output"] = mustRaw(kept)
		}
	}
	m["response"] = mustRaw(r)
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("responses: finish round: %w", err)
	}
	return c.stamp(out)
}

// stamp rewrites the frame for the client: sequence_number continues the
// client's numbering, output_index follows the offset of a continuation round,
// and response.id is the id the client saw first. Frames without these fields
// pass through unchanged. A malformed frame is returned as it is.
func (c *FrameCodec) stamp(frame []byte) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(frame, &m); err != nil {
		return frame, nil
	}
	changed := false
	if _, ok := m["sequence_number"]; ok {
		c.seq++
		m["sequence_number"] = mustRaw(c.seq)
		changed = true
	}
	if c.offset != 0 {
		if raw, ok := m["output_index"]; ok {
			var n int
			if json.Unmarshal(raw, &n) == nil {
				m["output_index"] = mustRaw(n + c.offset)
				changed = true
			}
		}
	}
	if c.clientID != "" {
		if raw, ok := m["response"]; ok {
			var r map[string]json.RawMessage
			if json.Unmarshal(raw, &r) == nil && r["id"] != nil {
				r["id"] = mustRaw(c.clientID)
				m["response"] = mustRaw(r)
				changed = true
			}
		}
	}
	if !changed {
		return frame, nil
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("responses: stamp frame: %w", err)
	}
	return out, nil
}

// isInternalCall reports whether a function call is one of the proxy's tools.
func isInternalCall(name string) bool {
	return IsInternalTool(name)
}

func mustRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
