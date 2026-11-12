package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// JSON-RPC 2.0 error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// maxFrameBytes bounds one stdio message. Larger lines get an error response.
const maxFrameBytes = 4 << 20

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// decoded is one element of a frame: a request, or the error that makes it invalid.
type decoded struct {
	req rpcRequest
	err *rpcError
}

// decodeFrame parses one stdio line. It returns the requests of a batch, or a
// single request (batch false). fatal is set when the whole frame is unusable.
func decodeFrame(data []byte) (items []decoded, batch bool, fatal *rpcError) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, false, &rpcError{codeInvalidRequest, "empty message"}
	}
	if !json.Valid(trimmed) {
		return nil, false, &rpcError{codeParseError, "parse error"}
	}
	if trimmed[0] == '[' {
		var raws []json.RawMessage
		if err := json.Unmarshal(trimmed, &raws); err != nil {
			return nil, false, &rpcError{codeInvalidRequest, "batch must be an array"}
		}
		if len(raws) == 0 {
			return nil, false, &rpcError{codeInvalidRequest, "empty batch"}
		}
		items = make([]decoded, len(raws))
		for i, raw := range raws {
			items[i] = decodeOne(raw)
		}
		return items, true, nil
	}
	return []decoded{decodeOne(trimmed)}, false, nil
}

// decodeOne validates one request object. The id is kept even when the request
// is invalid, so the error response can echo it.
func decodeOne(raw json.RawMessage) decoded {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		// Best effort: recover the id of a request that is otherwise malformed.
		var idOnly struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(raw, &idOnly) == nil && validRPCID(idOnly.ID) {
			return decoded{req: rpcRequest{ID: idOnly.ID}, err: &rpcError{codeInvalidRequest, "invalid request object"}}
		}
		return decoded{err: &rpcError{codeInvalidRequest, "invalid request object"}}
	}
	if !validRPCID(req.ID) {
		// The id is not echoed: an unusable id yields a null-id error response.
		return decoded{err: &rpcError{codeInvalidRequest, "id must be a string, number or null"}}
	}
	if req.JSONRPC != "2.0" {
		return decoded{req: req, err: &rpcError{codeInvalidRequest, `jsonrpc must be "2.0"`}}
	}
	if req.Method == "" {
		return decoded{req: req, err: &rpcError{codeInvalidRequest, "method is required"}}
	}
	if p := firstByte(req.Params); p != 0 && p != '{' && p != '[' {
		return decoded{req: req, err: &rpcError{codeInvalidRequest, "params must be an object or array"}}
	}
	return decoded{req: req}
}

// validRPCID reports whether a raw id is a string, a number or null. Objects,
// arrays and booleans are invalid. A number must fit in a float64, so a client
// that parses the echoed id cannot fail on it.
func validRPCID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	switch id[0] {
	case '"', 'n':
		return true
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		var f float64
		return json.Unmarshal(id, &f) == nil
	}
	return false
}

func firstByte(raw json.RawMessage) byte {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || string(t) == "null" {
		return 0
	}
	return t[0]
}

// readLine reads one newline-terminated frame. A line over max is reported as
// tooLong and its remainder is discarded.
func readLine(br *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	var buf []byte
	for {
		chunk, e := br.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(chunk) > max {
				tooLong = true
				buf = nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case e == nil:
			return bytes.TrimRight(buf, "\r\n"), tooLong, nil
		case errors.Is(e, bufio.ErrBufferFull):
			continue
		default:
			return bytes.TrimRight(buf, "\r\n"), tooLong, e
		}
	}
}

// stdioFrame is one line read by the serveStdio reader goroutine.
type stdioFrame struct {
	line    []byte
	tooLong bool
	err     error
}

// serveStdio runs the newline-delimited JSON-RPC loop until in ends or ctx is done.
// Only framed responses are written to out.
//
// The blocking read runs in its own goroutine, so a cancelled ctx (SIGTERM,
// Ctrl-C) ends the loop even while the client sends nothing. That goroutine
// stays blocked in Read until in returns; for os.Stdin the process exits first.
func serveStdio(ctx context.Context, in io.Reader, out io.Writer, dispatch dispatcher) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	frames := make(chan stdioFrame)
	done := make(chan struct{})
	defer close(done)
	go func() {
		br := bufio.NewReaderSize(in, 64<<10)
		for {
			line, tooLong, err := readLine(br, maxFrameBytes)
			select {
			case frames <- stdioFrame{line, tooLong, err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		var f stdioFrame
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f = <-frames:
		}
		if f.tooLong {
			if werr := writeFrame(out, &rpcResponse{JSONRPC: "2.0", ID: nil,
				Error: &rpcError{codeInvalidRequest, "message exceeds size limit"}}); werr != nil {
				return werr
			}
		} else if len(bytes.TrimSpace(f.line)) > 0 {
			if resp := handleFrame(ctx, f.line, dispatch); resp != nil {
				if werr := writeFrame(out, resp); werr != nil {
					return werr
				}
			}
		}
		if f.err != nil {
			if errors.Is(f.err, io.EOF) {
				return nil
			}
			return f.err
		}
	}
}

// handleFrame decodes a frame and dispatches it. It returns nil when nothing
// must be sent back (notifications only). The result is a single response
// object or a batch array, marshaled to one line.
func handleFrame(ctx context.Context, line []byte, dispatch dispatcher) any {
	items, batch, fatal := decodeFrame(line)
	if fatal != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: nil, Error: fatal}
	}
	if !batch {
		// Return nil explicitly: a nil *rpcResponse inside any is not a nil interface.
		if r := dispatchOne(ctx, items[0], dispatch); r != nil {
			return r
		}
		return nil
	}
	out := make([]*rpcResponse, 0, len(items))
	for _, it := range items {
		if r := dispatchOne(ctx, it, dispatch); r != nil {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func dispatchOne(ctx context.Context, it decoded, dispatch dispatcher) *rpcResponse {
	if it.err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: idOrNull(it.req.ID), Error: it.err}
	}
	isNotification := it.req.ID == nil
	result, rerr := dispatch(ctx, it.req)
	if isNotification {
		return nil
	}
	if rerr != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: it.req.ID, Error: rerr}
	}
	if result == nil {
		result = map[string]any{}
	}
	return &rpcResponse{JSONRPC: "2.0", ID: it.req.ID, Result: result}
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

// writeFrame marshals v as one compact line. encoding/json escapes newlines in
// strings, so a message never spans two lines.
func writeFrame(out io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = out.Write(b)
	return err
}

// dispatcher handles one well-formed request. It returns a result, or an rpcError.
type dispatcher func(ctx context.Context, req rpcRequest) (any, *rpcError)

// isNotificationMethod reports whether method is a notifications/* name, which
// the server ignores.
func isNotificationMethod(method string) bool {
	return strings.HasPrefix(method, "notifications/")
}
