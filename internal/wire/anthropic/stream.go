package anthropic

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

// StreamCall is a compress or decompress tool_use found in an SSE stream.
type StreamCall struct {
	Index    int
	ID       string
	Name     string
	Input    string        // accumulated input JSON
	Compress *CompressArgs // set when Name is compress and parsing succeeded
	Err      error         // compress argument parse error
}

// StreamSummary is what StreamPassthrough observed while copying the stream.
type StreamSummary struct {
	Usage    Usage
	Calls    []StreamCall
	Complete bool // message_stop was seen
}

type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage *usageWire `json:"usage"`
	} `json:"message"`
	Usage        *usageWire `json:"usage"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta *struct {
		Type        string  `json:"type"`
		PartialJSON *string `json:"partial_json"`
	} `json:"delta"`
}

// StreamPassthrough copies an Anthropic SSE stream from src to dst byte for byte,
// line by line, and observes it on the way. It collects usage from message_start
// and message_delta, and collects compress and decompress tool_use blocks by
// accumulating their input_json_delta fragments. A malformed data line is copied
// through and ignored. Parse errors of compress arguments are reported in
// StreamCall.Err and do not stop the stream.
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
	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			ev.Message.Usage.applyTo(&sum.Usage)
		}
	case "message_delta":
		ev.Usage.applyTo(&sum.Usage)
	case "message_stop":
		sum.Complete = true
	case "content_block_start":
		if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" &&
			IsInternalTool(ev.ContentBlock.Name) {
			open[ev.Index] = &StreamCall{Index: ev.Index, ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name}
		}
	case "content_block_delta":
		if c, ok := open[ev.Index]; ok && ev.Delta != nil && ev.Delta.Type == "input_json_delta" && ev.Delta.PartialJSON != nil {
			c.Input += *ev.Delta.PartialJSON
		}
	case "content_block_stop":
		if c, ok := open[ev.Index]; ok {
			delete(open, ev.Index)
			finalizeCall(c)
			sum.Calls = append(sum.Calls, *c)
		}
	}
}

func finalizeCall(c *StreamCall) {
	if c.Name != ToolCompress {
		return
	}
	args, err := ParseCompressArgs([]byte(strings.TrimSpace(c.Input)))
	if err != nil {
		c.Err = err
		return
	}
	c.Compress = &args
}

// finishStream closes calls whose content_block_stop never arrived and orders the
// calls by block index.
func finishStream(sum StreamSummary, open map[int]*StreamCall) StreamSummary {
	idx := make([]int, 0, len(open))
	for i := range open {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		c := open[i]
		finalizeCall(c)
		sum.Calls = append(sum.Calls, *c)
	}
	sort.SliceStable(sum.Calls, func(a, b int) bool { return sum.Calls[a].Index < sum.Calls[b].Index })
	return sum
}
