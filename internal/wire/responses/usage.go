package responses

import (
	"encoding/json"
	"fmt"
)

// Usage holds the token counters of a Responses reply. InputTokens already
// includes CachedTokens, as in Chat Completions.
type Usage struct {
	InputTokens     int
	OutputTokens    int
	TotalTokens     int
	CachedTokens    int // input_tokens_details.cached_tokens
	ReasoningTokens int // output_tokens_details.reasoning_tokens
}

// UncachedInputTokens returns the input tokens that were not served from the
// cache. It never returns a negative number.
func (u Usage) UncachedInputTokens() int {
	if n := u.InputTokens - u.CachedTokens; n > 0 {
		return n
	}
	return 0
}

// usageWire uses pointers so that absent and zero fields can be told apart when
// stream events are merged.
type usageWire struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
	TotalTokens  *int `json:"total_tokens"`
	InputDetails *struct {
		CachedTokens *int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputDetails *struct {
		ReasoningTokens *int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func (w *usageWire) applyTo(u *Usage) {
	if w == nil {
		return
	}
	if w.InputTokens != nil {
		u.InputTokens = *w.InputTokens
	}
	if w.OutputTokens != nil {
		u.OutputTokens = *w.OutputTokens
	}
	if w.TotalTokens != nil {
		u.TotalTokens = *w.TotalTokens
	}
	if w.InputDetails != nil && w.InputDetails.CachedTokens != nil {
		u.CachedTokens = *w.InputDetails.CachedTokens
	}
	if w.OutputDetails != nil && w.OutputDetails.ReasoningTokens != nil {
		u.ReasoningTokens = *w.OutputDetails.ReasoningTokens
	}
}

// ParseUsage extracts usage from a Responses reply. It reads the top-level usage
// object, or response.usage as carried by a response.completed event. The bool is
// false when the reply has no usage object.
func ParseUsage(resp []byte) (Usage, bool, error) {
	var env struct {
		Usage    *usageWire `json:"usage"`
		Response *struct {
			Usage *usageWire `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		return Usage{}, false, fmt.Errorf("responses: parse usage: %w", err)
	}
	w := env.Usage
	if w == nil && env.Response != nil {
		w = env.Response.Usage
	}
	if w == nil {
		return Usage{}, false, nil
	}
	var u Usage
	w.applyTo(&u)
	return u, true, nil
}
