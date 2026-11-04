package openai

import (
	"encoding/json"
	"fmt"
)

// Usage holds the token counters of a Chat Completions response. PromptTokens
// already includes CachedTokens, unlike the Anthropic counters.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CachedTokens     int // prompt_tokens_details.cached_tokens
}

// UncachedPromptTokens returns the prompt tokens that were not served from the
// cache. It never returns a negative number.
func (u Usage) UncachedPromptTokens() int {
	if n := u.PromptTokens - u.CachedTokens; n > 0 {
		return n
	}
	return 0
}

// usageWire uses pointers so that absent and zero fields can be told apart when
// stream chunks are merged.
type usageWire struct {
	PromptTokens     *int `json:"prompt_tokens"`
	CompletionTokens *int `json:"completion_tokens"`
	TotalTokens      *int `json:"total_tokens"`
	PromptDetails    *struct {
		CachedTokens *int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (w *usageWire) applyTo(u *Usage) {
	if w == nil {
		return
	}
	if w.PromptTokens != nil {
		u.PromptTokens = *w.PromptTokens
	}
	if w.CompletionTokens != nil {
		u.CompletionTokens = *w.CompletionTokens
	}
	if w.TotalTokens != nil {
		u.TotalTokens = *w.TotalTokens
	}
	if w.PromptDetails != nil && w.PromptDetails.CachedTokens != nil {
		u.CachedTokens = *w.PromptDetails.CachedTokens
	}
}

// ParseUsage extracts the top-level usage object of a non-streaming Chat
// Completions response. The bool is false when the response has no usage object.
func ParseUsage(resp []byte) (Usage, bool, error) {
	var env struct {
		Usage *usageWire `json:"usage"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		return Usage{}, false, fmt.Errorf("openai: parse usage: %w", err)
	}
	if env.Usage == nil {
		return Usage{}, false, nil
	}
	var u Usage
	env.Usage.applyTo(&u)
	return u, true, nil
}
