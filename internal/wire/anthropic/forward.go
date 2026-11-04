package anthropic

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// IsInternalTool reports whether name is one of the proxy's own tools. Their
// tool_use blocks never reach the client.
func IsInternalTool(name string) bool {
	return name == ToolCompress || name == ToolDecompress
}

// SSEEvent is one server-sent event as received from upstream.
type SSEEvent struct {
	Name string // value of the event: line, "" when absent
	Data []byte // data lines joined with "\n"
	Raw  []byte // the event exactly as received, blank terminator included
}

// SSEReader splits an event stream into events.
type SSEReader struct {
	br *bufio.Reader
}

// NewSSEReader reads events from r.
func NewSSEReader(r io.Reader) *SSEReader {
	return &SSEReader{br: bufio.NewReader(r)}
}

// Next returns the next event. It returns io.EOF when the stream ends with no
// pending event. A final event without its blank line is returned as is.
func (r *SSEReader) Next() (SSEEvent, error) {
	var name string
	var raw bytes.Buffer
	var data [][]byte
	has := false
	for {
		line, err := r.br.ReadBytes('\n')
		if len(line) > 0 {
			raw.Write(line)
			t := bytes.TrimRight(line, "\r\n")
			switch {
			case len(t) == 0:
				if has {
					return SSEEvent{Name: name, Data: bytes.Join(data, []byte("\n")), Raw: raw.Bytes()}, nil
				}
				raw.Reset() // stray blank line between events
			default:
				has = true
				if rest, ok := bytes.CutPrefix(t, []byte("event:")); ok {
					name = string(bytes.TrimSpace(rest))
				} else if rest, ok := bytes.CutPrefix(t, []byte("data:")); ok {
					data = append(data, bytes.TrimPrefix(rest, []byte(" ")))
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && has {
				return SSEEvent{Name: name, Data: bytes.Join(data, []byte("\n")), Raw: raw.Bytes()}, nil
			}
			return SSEEvent{}, err
		}
	}
}

// Forwarder writes the client's view of one streamed answer that may span
// several server-side rounds (spec §16). Content deltas go out as they arrive.
// Blocks of internal tools are not written, but their input is kept, and every
// block of a round is kept for the continuation. Only the message end (message_delta
// and message_stop) is held, because whether it is final is known at the end of the round.
type Forwarder struct {
	dst     io.Writer
	started bool
	next    int // downstream index of the next forwarded content block
	cur     *round
	held    []SSEEvent
}

// round is the state of one upstream response.
type round struct {
	idx    map[int]int // upstream index -> downstream index, -1 for internal blocks
	byUp   map[int]*streamBlock
	blocks []*streamBlock
	stop   string
	usage  Usage
}

// streamBlock accumulates one content block of a round.
type streamBlock struct {
	typ, id, name string
	internal      bool
	start         struct{ text, thinking, signature string }
	raw           json.RawMessage // content_block as started
	text          strings.Builder
	thinking      strings.Builder
	signature     strings.Builder
	partial       strings.Builder
}

// RoundResult is what one upstream round leaves for the server-side loop.
type RoundResult struct {
	Internal    []ToolUse         // internal tool_use blocks, in order
	ClientTools int               // tool_use blocks the client must run
	Content     []json.RawMessage // every block of the round, as the assistant message needs them
	StopReason  string
	Usage       Usage
}

// ToolUse is a tool_use block of a round.
type ToolUse struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// NewForwarder writes the client stream to dst. Callers flush dst themselves.
func NewForwarder(dst io.Writer) *Forwarder {
	return &Forwarder{dst: dst}
}

// BeginRound starts the events of one upstream round. The message end held from
// an earlier round is discarded, because the answer goes on.
func (f *Forwarder) BeginRound() {
	f.cur = &round{idx: map[int]int{}, byUp: map[int]*streamBlock{}}
	f.held = nil
}

// Feed handles one event of the current round and writes what the client must see.
func (f *Forwarder) Feed(ev SSEEvent) error {
	if f.cur == nil {
		f.BeginRound()
	}
	var head struct {
		Type    string `json:"type"`
		Index   *int   `json:"index"`
		Message *struct {
			Usage *usageWire `json:"usage"`
		} `json:"message"`
		Usage        *usageWire      `json:"usage"`
		ContentBlock json.RawMessage `json:"content_block"`
		Delta        json.RawMessage `json:"delta"`
	}
	if len(ev.Data) == 0 || json.Unmarshal(ev.Data, &head) != nil {
		return f.write(ev.Raw)
	}
	switch head.Type {
	case "message_start":
		if head.Message != nil {
			head.Message.Usage.applyTo(&f.cur.usage)
		}
		if f.started {
			return nil // later rounds continue the message that is already open
		}
		f.started = true
		return f.write(ev.Raw)
	case "message_delta":
		head.Usage.applyTo(&f.cur.usage)
		var d struct {
			StopReason *string `json:"stop_reason"`
		}
		if json.Unmarshal(head.Delta, &d) == nil && d.StopReason != nil {
			f.cur.stop = *d.StopReason
		}
		f.held = append(f.held, ev)
		return nil
	case "message_stop":
		f.held = append(f.held, ev)
		return nil
	case "content_block_start":
		return f.blockStart(ev, head.Index, head.ContentBlock)
	case "content_block_delta":
		return f.blockDelta(ev, head.Index, head.Delta)
	case "content_block_stop":
		return f.blockStop(ev, head.Index)
	default: // ping, error and unknown events pass through
		return f.write(ev.Raw)
	}
}

func (f *Forwarder) blockStart(ev SSEEvent, idx *int, cb json.RawMessage) error {
	if idx == nil {
		return f.write(ev.Raw)
	}
	var c struct {
		Type     string `json:"type"`
		ID       string `json:"id"`
		Name     string `json:"name"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
		Sig      string `json:"signature"`
	}
	_ = json.Unmarshal(cb, &c)
	b := &streamBlock{typ: c.Type, id: c.ID, name: c.Name, raw: cb}
	b.start.text, b.start.thinking, b.start.signature = c.Text, c.Thinking, c.Sig
	f.cur.blocks = append(f.cur.blocks, b)
	f.cur.byUp[*idx] = b
	if c.Type == "tool_use" && IsInternalTool(c.Name) {
		b.internal = true
		f.cur.idx[*idx] = -1
		return nil
	}
	down := f.next
	f.next++
	f.cur.idx[*idx] = down
	return f.emitIndexed(ev, *idx, down)
}

func (f *Forwarder) blockDelta(ev SSEEvent, idx *int, delta json.RawMessage) error {
	b := f.lookup(idx)
	if b == nil {
		return nil
	}
	var d struct {
		Type        string  `json:"type"`
		Text        *string `json:"text"`
		Thinking    *string `json:"thinking"`
		Signature   *string `json:"signature"`
		PartialJSON *string `json:"partial_json"`
	}
	if json.Unmarshal(delta, &d) == nil {
		switch {
		case d.Type == "text_delta" && d.Text != nil:
			b.text.WriteString(*d.Text)
		case d.Type == "thinking_delta" && d.Thinking != nil:
			b.thinking.WriteString(*d.Thinking)
		case d.Type == "signature_delta" && d.Signature != nil:
			b.signature.WriteString(*d.Signature)
		case d.Type == "input_json_delta" && d.PartialJSON != nil:
			b.partial.WriteString(*d.PartialJSON)
		}
	}
	if b.internal {
		return nil
	}
	return f.emitIndexed(ev, *idx, f.cur.idx[*idx])
}

func (f *Forwarder) blockStop(ev SSEEvent, idx *int) error {
	b := f.lookup(idx)
	if b == nil || b.internal {
		return nil
	}
	return f.emitIndexed(ev, *idx, f.cur.idx[*idx])
}

func (f *Forwarder) lookup(idx *int) *streamBlock {
	if idx == nil {
		return nil
	}
	return f.cur.byUp[*idx]
}

// Finish writes the held message end. When the round's internal calls were not
// continued and no client tool is pending, a tool_use stop reason becomes end_turn.
func (f *Forwarder) Finish(endTurn bool) error {
	rewrite := endTurn && f.cur != nil && f.cur.stop == "tool_use"
	for _, ev := range f.held {
		if ev.Name == "message_delta" && rewrite {
			data, err := withStopReason(ev.Data, "end_turn")
			if err != nil {
				return err
			}
			if err := f.writeData(ev.Name, data); err != nil {
				return err
			}
			continue
		}
		if err := f.write(ev.Raw); err != nil {
			return err
		}
	}
	f.held = nil
	return nil
}

// EndRound closes the current round and returns what it contained.
func (f *Forwarder) EndRound() RoundResult {
	var res RoundResult
	if f.cur == nil {
		return res
	}
	res.StopReason = f.cur.stop
	res.Usage = f.cur.usage
	for _, b := range f.cur.blocks {
		switch {
		case b.internal:
			res.Internal = append(res.Internal, ToolUse{ID: b.id, Name: b.name, Input: b.input()})
		case b.typ == "tool_use":
			res.ClientTools++
		}
		if raw := b.content(); raw != nil {
			res.Content = append(res.Content, raw)
		}
	}
	return res
}

// content is the block as a non-streaming response would carry it. Text and
// thinking are the exact concatenation of their deltas.
func (b *streamBlock) content() json.RawMessage {
	var out any
	switch b.typ {
	case "text":
		text := b.start.text + b.text.String()
		if text == "" {
			return nil
		}
		out = map[string]string{"type": "text", "text": text}
	case "thinking":
		sig := b.signature.String()
		if sig == "" {
			sig = b.start.signature
		}
		out = map[string]string{"type": "thinking", "thinking": b.start.thinking + b.thinking.String(), "signature": sig}
	case "tool_use":
		out = toolUseBlock{Type: "tool_use", ID: b.id, Name: b.name, Input: b.input()}
	default:
		return b.raw
	}
	raw, err := marshalNoEscape(out)
	if err != nil {
		return b.raw
	}
	return raw
}

func (b *streamBlock) input() json.RawMessage {
	return json.RawMessage(inputObject(b.partial.String()))
}

func (f *Forwarder) write(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	_, err := f.dst.Write(raw)
	return err
}

// emitIndexed writes ev with its content index changed from up to down. Events
// whose index does not change are written byte for byte.
func (f *Forwarder) emitIndexed(ev SSEEvent, up, down int) error {
	if up == down {
		return f.write(ev.Raw)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(ev.Data, &obj); err != nil {
		return err
	}
	obj["index"] = json.RawMessage(strconv.Itoa(down))
	data, err := marshalNoEscape(obj)
	if err != nil {
		return err
	}
	return f.writeData(ev.Name, data)
}

func (f *Forwarder) writeData(name string, data []byte) error {
	var b bytes.Buffer
	if name != "" {
		fmt.Fprintf(&b, "event: %s\n", name)
	}
	b.WriteString("data: ")
	b.Write(data)
	b.WriteString("\n\n")
	return f.write(b.Bytes())
}

// withStopReason returns a message_delta payload with delta.stop_reason replaced.
func withStopReason(data []byte, reason string) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	var delta map[string]json.RawMessage
	if err := json.Unmarshal(obj["delta"], &delta); err != nil {
		return nil, err
	}
	enc, err := marshalNoEscape(reason)
	if err != nil {
		return nil, err
	}
	delta["stop_reason"] = enc
	d, err := marshalNoEscape(delta)
	if err != nil {
		return nil, err
	}
	obj["delta"] = d
	return marshalNoEscape(obj)
}
