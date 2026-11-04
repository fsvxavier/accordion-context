package google

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// StreamCall is a compress or decompress functionCall found in an SSE stream.
// Gemini sends a functionCall whole inside one part, so each call is complete
// when it is observed. A streamed call has no id, because the id is synthesized
// by Decode from the position in the history, and the caller assigns it when the
// call comes back in the next request.
type StreamCall struct {
	Candidate  int
	Part       int // index of the part within the candidate, over the whole stream
	Name       string
	Arguments  string          // compact JSON object
	Compress   *CompressArgs   // set when Name is compress and parsing succeeded
	Decompress *DecompressArgs // set when Name is decompress and parsing succeeded
	Search     *SearchArgs     // set when Name is search_context and parsing succeeded
	Retrieve   *RetrieveArgs   // set when Name is retrieve and parsing succeeded
	Err        error           // argument parse error
}

// StreamSummary is what StreamPassthrough observed while copying the stream.
type StreamSummary struct {
	Usage    Usage
	HasUsage bool
	Calls    []StreamCall
	// Complete is true when a candidate reported a finishReason.
	Complete bool
}

type streamChunk struct {
	Candidates []struct {
		Index   int `json:"index"`
		Content struct {
			Parts []json.RawMessage `json:"parts"`
		} `json:"content"`
		FinishReason *string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *usageWire `json:"usageMetadata"`
}

// StreamPassthrough copies a streamGenerateContent SSE stream (alt=sse) from src
// to dst byte for byte, line by line, and observes it on the way. It records the
// compress and decompress functionCall parts and collects usageMetadata from the
// chunks that carry it. A malformed data line is copied through and ignored.
// Argument parse errors are reported in StreamCall.Err and do not stop the stream.
func StreamPassthrough(dst io.Writer, src io.Reader) (StreamSummary, error) {
	var sum StreamSummary
	parts := make(map[int]int) // candidate index -> parts seen so far
	br := bufio.NewReader(src)
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			if _, err := dst.Write(line); err != nil {
				return sum, err
			}
			observeLine(line, &sum, parts)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return sum, rerr
		}
	}
	return sum, nil
}

func observeLine(line []byte, sum *StreamSummary, parts map[int]int) {
	t := bytes.TrimRight(line, "\r\n")
	if !bytes.HasPrefix(t, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(t[len("data:"):])
	var ch streamChunk
	if json.Unmarshal(payload, &ch) != nil {
		return
	}
	if ch.UsageMetadata != nil {
		ch.UsageMetadata.applyTo(&sum.Usage)
		sum.HasUsage = true
	}
	for _, c := range ch.Candidates {
		if c.FinishReason != nil && *c.FinishReason != "" {
			sum.Complete = true
		}
		for _, raw := range c.Content.Parts {
			pos := parts[c.Index]
			parts[c.Index]++
			var p struct {
				FunctionCall *wireCall `json:"functionCall"`
			}
			if json.Unmarshal(raw, &p) != nil || p.FunctionCall == nil {
				continue
			}
			if name := p.FunctionCall.Name; !IsInternalTool(name) {
				continue
			}
			call := StreamCall{
				Candidate: c.Index, Part: pos, Name: p.FunctionCall.Name,
				Arguments: compactJSON(p.FunctionCall.Args, "{}"),
			}
			finalizeCall(&call)
			sum.Calls = append(sum.Calls, call)
		}
	}
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
