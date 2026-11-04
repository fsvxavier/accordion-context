package anthropic

import (
	"encoding/json"
	"fmt"
)

// Usage holds the token counters reported by the Messages API. InputTokens
// excludes cached tokens; the cache counters are separate (see TotalInput).
type Usage struct {
	InputTokens              int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
	OutputTokens             int
}

// TotalInput returns the prompt size: uncached input plus cache reads and writes.
func (u Usage) TotalInput() int {
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

// usageWire uses pointers so that absent and zero fields can be told apart when
// stream events are merged.
type usageWire struct {
	InputTokens              *int `json:"input_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
}

func (w *usageWire) applyTo(u *Usage) {
	if w == nil {
		return
	}
	if w.InputTokens != nil {
		u.InputTokens = *w.InputTokens
	}
	if w.CacheReadInputTokens != nil {
		u.CacheReadInputTokens = *w.CacheReadInputTokens
	}
	if w.CacheCreationInputTokens != nil {
		u.CacheCreationInputTokens = *w.CacheCreationInputTokens
	}
	if w.OutputTokens != nil {
		u.OutputTokens = *w.OutputTokens
	}
}

// ParseUsage extracts usage from a non-streaming Messages response. It reads the
// top-level "usage" object, or "message.usage" as found in a message_start event.
// The bool is false when the response has no usage object.
func ParseUsage(resp []byte) (Usage, bool, error) {
	var env struct {
		Usage   *usageWire `json:"usage"`
		Message *struct {
			Usage *usageWire `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		return Usage{}, false, fmt.Errorf("anthropic: parse usage: %w", err)
	}
	w := env.Usage
	if w == nil && env.Message != nil {
		w = env.Message.Usage
	}
	if w == nil {
		return Usage{}, false, nil
	}
	var u Usage
	w.applyTo(&u)
	return u, true, nil
}
