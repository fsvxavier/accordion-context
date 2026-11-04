package responses

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

// StreamCall is a compress or decompress function call found in an SSE stream.
type StreamCall struct {
	OutputIndex int // position of the output item in the response
	ItemID      string
	CallID      string
	Name        string
	Arguments   string        // final arguments string
	Compress    *CompressArgs // set when Name is compress and parsing succeeded
	Decompress  *DecompressArgs
	Search      *SearchArgs   // set when Name is search_context and parsing succeeded
	Retrieve    *RetrieveArgs // set when Name is retrieve and parsing succeeded
	Err         error         // argument parse error
}

// StreamSummary is what StreamPassthrough observed while copying the stream.
type StreamSummary struct {
	Usage    Usage
	HasUsage bool
	Calls    []StreamCall
	// Complete is true when a terminal event was seen: response.completed,
	// response.incomplete or response.failed.
	Complete bool
}

// streamEvent holds the fields of the events this package reads. Fields that
// differ between event types are kept raw and decoded only where they apply.
type streamEvent struct {
	Type        string          `json:"type"`
	OutputIndex *int            `json:"output_index"`
	Delta       json.RawMessage `json:"delta"`
	Arguments   *string         `json:"arguments"`
	Item        *struct {
		Type      string  `json:"type"`
		ID        string  `json:"id"`
		CallID    string  `json:"call_id"`
		Name      string  `json:"name"`
		Arguments *string `json:"arguments"`
	} `json:"item"`
	Response *struct {
		Usage *usageWire `json:"usage"`
	} `json:"response"`
}

// StreamPassthrough copies a Responses SSE stream from src to dst byte for byte,
// line by line, and observes it on the way. Function call items are identified
// by output_index. Their name and call_id come from response.output_item.added.
// Their arguments come from response.function_call_arguments.delta, and the
// final value from response.function_call_arguments.done or the item in
// response.output_item.done. Usage comes from response.completed. A malformed
// data line is copied through and ignored. Argument parse errors are reported
// in StreamCall.Err and do not stop the stream.
func StreamPassthrough(dst io.Writer, src io.Reader) (StreamSummary, error) {
	var sum StreamSummary
	open := make(map[int]*StreamCall)
	br := bufio.NewReader(src)
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			if _, err := dst.Write(line); err != nil {
				return finishStream(sum, open), err
			}
			observeLine(line, &sum, open)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return finishStream(sum, open), rerr
		}
	}
	return finishStream(sum, open), nil
}

func observeLine(line []byte, sum *StreamSummary, open map[int]*StreamCall) {
	t := bytes.TrimRight(line, "\r\n")
	if !bytes.HasPrefix(t, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(t[len("data:"):])
	var ev streamEvent
	if json.Unmarshal(payload, &ev) != nil {
		return
	}
	if ev.Response != nil && ev.Response.Usage != nil {
		ev.Response.Usage.applyTo(&sum.Usage)
		sum.HasUsage = true
	}
	switch ev.Type {
	case "response.completed", "response.incomplete", "response.failed":
		sum.Complete = true
	case "response.output_item.added":
		if ev.Item == nil || ev.Item.Type != "function_call" || ev.OutputIndex == nil {
			return
		}
		c := callAt(open, *ev.OutputIndex)
		c.ItemID = ev.Item.ID
		c.CallID = ev.Item.CallID
		c.Name = ev.Item.Name
		if ev.Item.Arguments != nil {
			c.Arguments = *ev.Item.Arguments
		}
	case "response.function_call_arguments.delta":
		if ev.OutputIndex == nil {
			return
		}
		var d string
		if json.Unmarshal(ev.Delta, &d) == nil {
			callAt(open, *ev.OutputIndex).Arguments += d
		}
	case "response.function_call_arguments.done":
		if ev.OutputIndex == nil || ev.Arguments == nil {
			return
		}
		callAt(open, *ev.OutputIndex).Arguments = *ev.Arguments
	case "response.output_item.done":
		if ev.Item == nil || ev.Item.Type != "function_call" || ev.OutputIndex == nil {
			return
		}
		c := callAt(open, *ev.OutputIndex)
		if c.CallID == "" {
			c.CallID = ev.Item.CallID
		}
		if c.Name == "" {
			c.Name = ev.Item.Name
		}
		if ev.Item.Arguments != nil && *ev.Item.Arguments != "" {
			c.Arguments = *ev.Item.Arguments
		}
	}
}

func callAt(open map[int]*StreamCall, idx int) *StreamCall {
	c := open[idx]
	if c == nil {
		c = &StreamCall{OutputIndex: idx}
		open[idx] = c
	}
	return c
}

// finishStream finalizes the compress and decompress calls, which only complete
// when the stream ends, and orders them by output index. Other function calls
// are not reported.
func finishStream(sum StreamSummary, open map[int]*StreamCall) StreamSummary {
	keys := make([]int, 0, len(open))
	for k := range open {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		c := open[k]
		if !IsInternalTool(c.Name) {
			continue
		}
		finalizeCall(c)
		sum.Calls = append(sum.Calls, *c)
	}
	return sum
}

func finalizeCall(c *StreamCall) {
	args := []byte(strings.TrimSpace(c.Arguments))
	switch c.Name {
	case ToolCompress:
		v, err := ParseCompressArgs(args)
		if err != nil {
			c.Err = err
			return
		}
		c.Compress = &v
	case ToolDecompress:
		v, err := ParseDecompressArgs(args)
		if err != nil {
			c.Err = err
			return
		}
		c.Decompress = &v
	case ToolSearchContext:
		v, err := ParseSearchArgs(args)
		if err != nil {
			c.Err = err
			return
		}
		c.Search = &v
	case ToolRetrieve:
		v, err := ParseRetrieveArgs(args)
		if err != nil {
			c.Err = err
			return
		}
		c.Retrieve = &v
	}
}
