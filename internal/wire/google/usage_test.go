package google

import "testing"

func TestParseUsage(t *testing.T) {
	u, ok, err := ParseUsage([]byte(`{"candidates":[],"usageMetadata":{"promptTokenCount":1200,"candidatesTokenCount":80,"totalTokenCount":1280,"cachedContentTokenCount":1000}}`))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if u.PromptTokens != 1200 || u.CompletionTokens != 80 || u.TotalTokens != 1280 || u.CachedTokens != 1000 {
		t.Errorf("usage %+v", u)
	}
	if got := u.UncachedPromptTokens(); got != 200 {
		t.Errorf("uncached %d, want 200", got)
	}
	if got := (Usage{PromptTokens: 5, CachedTokens: 9}).UncachedPromptTokens(); got != 0 {
		t.Errorf("uncached clamp %d", got)
	}
}

func TestParseUsageAbsentAndInvalid(t *testing.T) {
	if _, ok, err := ParseUsage([]byte(`{"candidates":[]}`)); ok || err != nil {
		t.Errorf("absent: ok=%v err=%v", ok, err)
	}
	if _, _, err := ParseUsage([]byte(`{bad`)); err == nil {
		t.Error("expected error")
	}
}
