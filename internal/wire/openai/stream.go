package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

// StreamCall is a compress or decompress tool call found in an SSE stream.
type StreamCall struct {
	Choice     int
	Index      int // tool_call index within the choice
	ID         string
	Name       string
	Arguments  string          // accumulated arguments string
	Compress   *CompressArgs   // set when Name is compress and parsing succeeded
	Decompress *DecompressArgs // set when Name is decompress and parsing succeeded
	Err        error           // argument parse error
}

// StreamSummary is what StreamPassthrough observed while copying the stream.
type StreamSummary struct {
	Usage    Usage
	HasUsage bool
	Calls    []StreamCall
	// Complete is true when the [DONE] sentinel or a finish_reason was seen.
	Complete bool
}

type streamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			ToolCalls []struct {
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Function *struct {
					Name      string  `json:"name"`
					Arguments *string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageWire `json:"usage"`
}

type callKey struct {
	choice int
	index  int
}

// StreamPassthrough copies a Chat Completions SSE stream from src to dst byte for
// byte, line by line, and observes it on the way. It accumulates the tool_call
// deltas of each choice, keyed by their index, and records the compress and
// decompress calls at the end of the stream. It also collects usage from chunks
// that carry it (stream_options.include_usage). A malformed data line is copied
// through and ignored. Argument parse errors are reported in StreamCall.Err and
// do not stop the stream.
func StreamPassthrough(dst io.Writer, src io.Reader) (StreamSummary, error) {
	var sum StreamSummary
	open := make(map[callKey]*StreamCall)
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

func observeLine(line []byte, sum *StreamSummary, open map[callKey]*StreamCall) {
	t := bytes.TrimRight(line, "\r\n")
	if !bytes.HasPrefix(t, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(t[len("data:"):])
	if string(payload) == "[DONE]" {
		sum.Complete = true
		return
	}
	var ch streamChunk
	if json.Unmarshal(payload, &ch) != nil {
		return
	}
	if ch.Usage != nil {
		ch.Usage.applyTo(&sum.Usage)
		sum.HasUsage = true
	}
	for _, c := range ch.Choices {
		if c.FinishReason != nil && *c.FinishReason != "" {
			sum.Complete = true
		}
		for pos, tc := range c.Delta.ToolCalls {
			idx := pos
			if tc.Index != nil {
				idx = *tc.Index
			}
			k := callKey{choice: c.Index, index: idx}
			call := open[k]
			if call == nil {
				call = &StreamCall{Choice: c.Index, Index: idx}
				open[k] = call
			}
			if tc.ID != "" {
				call.ID = tc.ID
			}
			if tc.Function != nil {
				if tc.Function.Name != "" {
					call.Name = tc.Function.Name
				}
				if tc.Function.Arguments != nil {
					call.Arguments += *tc.Function.Arguments
				}
			}
		}
	}
}

// finishStream finalizes the compress and decompress calls, which only complete
// when the stream ends, and orders them by choice and index. Other tool calls are
// not reported.
func finishStream(sum StreamSummary, open map[callKey]*StreamCall) StreamSummary {
	keys := make([]callKey, 0, len(open))
	for k := range open {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].choice != keys[b].choice {
			return keys[a].choice < keys[b].choice
		}
		return keys[a].index < keys[b].index
	})
	for _, k := range keys {
		c := open[k]
		if c.Name != ToolCompress && c.Name != ToolDecompress {
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
	}
}
