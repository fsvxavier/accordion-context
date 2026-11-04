package proxy

import (
	"encoding/json"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/wire/responses"
)

// WebSocket mode of the Responses adapter (spec §19.1). The frame codec is in
// wire/responses (ws.go). This file adapts its results to the shared turn code
// in proxy.go, so both transports compact the same way.

// wsRequest wraps a decoded response.create frame, so that the compaction prelude
// can encode it as it encodes an HTTP body.
func wsRequest(req *responses.Request) request {
	return encoderFunc(func(window []kernel.Message) ([]byte, error) {
		return responses.Encode(req, window)
	})
}

// wsCalls converts the internal calls of a finished round to the turn's calls.
func wsCalls(res responses.Round) []toolCall {
	out := make([]toolCall, 0, len(res.Calls))
	for _, c := range res.Calls {
		out = append(out, toolCall{id: c.CallID, name: c.Name, input: []byte(c.Arguments)})
	}
	return out
}

// wsAssistant returns the output items of a finished round as the assistant
// turn that Continue echoes back, as the SSE path does.
func wsAssistant(res responses.Round) json.RawMessage {
	items := res.Assistant
	if items == nil {
		items = []json.RawMessage{}
	}
	b, _ := json.Marshal(items)
	return b
}

// responsesWSEnd reports whether an upstream event type ends a response that is
// forwarded without compaction.
func responsesWSEnd(typ string) bool {
	switch typ {
	case "response.completed", "response.incomplete", "response.failed", "error":
		return true
	}
	return false
}
