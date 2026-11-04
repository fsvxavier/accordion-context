package responses

import "github.com/fsvxavier/accordion-context/internal/kernel"

// FrameCodec is the seam for WebSocket mode of the Responses API (spec §19.1,
// §21 phase 8). It is a stub: no type implements it yet, and the package has no
// WebSocket code.
//
// What remains to implement:
//   - the connection itself: the WebSocket handshake and per-connection framing,
//     reading one client frame at a time and writing server events back as they
//     arrive;
//   - mapping the client's response.create frames onto Decode and Encode. The
//     request fields come from the frame, not from an HTTP body, so the frame
//     must be parsed the same way Decode parses a body;
//   - connection-local state. A connection can chain responses through
//     previous_response_id without resending history. Such requests must return
//     ErrStateful, as they do in SSE mode, so they are forwarded unchanged;
//   - the server events, which use the same types as SSE mode. The event handling
//     in stream.go can be reused for them. Each frame is forwarded unchanged;
//   - error and close frames, and what happens when the connection drops.
//
// The WebSocket protocol details must be checked against the current API
// reference before this is implemented.
type FrameCodec interface {
	// ReadCreate returns the decoded request carried by the next response.create
	// client frame, along with its kernel messages.
	ReadCreate() (*Request, []kernel.Message, error)
	// WriteEvent forwards one server event frame to the client unchanged.
	WriteEvent(frame []byte) error
}
