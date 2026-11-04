package google

import (
	"encoding/json"
	"fmt"
)

// Usage holds the token counters of a generateContent response. PromptTokens
// already includes CachedTokens, as in the OpenAI counters.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CachedTokens     int // usageMetadata.cachedContentTokenCount
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
	PromptTokenCount        *int `json:"promptTokenCount"`
	CandidatesTokenCount    *int `json:"candidatesTokenCount"`
	TotalTokenCount         *int `json:"totalTokenCount"`
	CachedContentTokenCount *int `json:"cachedContentTokenCount"`
}

func (w *usageWire) applyTo(u *Usage) {
	if w == nil {
		return
	}
	if w.PromptTokenCount != nil {
		u.PromptTokens = *w.PromptTokenCount
	}
	if w.CandidatesTokenCount != nil {
		u.CompletionTokens = *w.CandidatesTokenCount
	}
	if w.TotalTokenCount != nil {
		u.TotalTokens = *w.TotalTokenCount
	}
	if w.CachedContentTokenCount != nil {
		u.CachedTokens = *w.CachedContentTokenCount
	}
}

// ParseUsage extracts usageMetadata from a non-streaming generateContent
// response. The bool is false when the response has no usageMetadata object.
func ParseUsage(resp []byte) (Usage, bool, error) {
	var env struct {
		UsageMetadata *usageWire `json:"usageMetadata"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		return Usage{}, false, fmt.Errorf("google: parse usage: %w", err)
	}
	if env.UsageMetadata == nil {
		return Usage{}, false, nil
	}
	var u Usage
	env.UsageMetadata.applyTo(&u)
	return u, true, nil
}
