package responses

import "testing"

func TestParseUsageTopLevel(t *testing.T) {
	resp := []byte(`{"id":"resp_1","usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":40},"output_tokens":9,"output_tokens_details":{"reasoning_tokens":4},"total_tokens":109}}`)
	u, ok, err := ParseUsage(resp)
	if err != nil || !ok {
		t.Fatalf("ParseUsage ok=%v err=%v", ok, err)
	}
	want := Usage{InputTokens: 100, OutputTokens: 9, TotalTokens: 109, CachedTokens: 40, ReasoningTokens: 4}
	if u != want {
		t.Fatalf("got %+v, want %+v", u, want)
	}
	if got := u.UncachedInputTokens(); got != 60 {
		t.Errorf("UncachedInputTokens = %d, want 60", got)
	}
}

func TestParseUsageFromCompletedEvent(t *testing.T) {
	resp := []byte(`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":7,"output_tokens":2,"total_tokens":9}}}`)
	u, ok, err := ParseUsage(resp)
	if err != nil || !ok {
		t.Fatalf("ParseUsage ok=%v err=%v", ok, err)
	}
	if u.InputTokens != 7 || u.CachedTokens != 0 || u.OutputTokens != 2 {
		t.Fatalf("got %+v", u)
	}
}

func TestParseUsageAbsentAndInvalid(t *testing.T) {
	if _, ok, err := ParseUsage([]byte(`{"id":"resp_1"}`)); ok || err != nil {
		t.Errorf("absent usage: ok=%v err=%v", ok, err)
	}
	if _, _, err := ParseUsage([]byte(`{`)); err == nil {
		t.Error("malformed body accepted")
	}
}

func TestUncachedNeverNegative(t *testing.T) {
	if got := (Usage{InputTokens: 5, CachedTokens: 9}).UncachedInputTokens(); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}
