package proxy

import (
	"github.com/fsvxavier/accordion-context/internal/kernel"
	"net/url"
	"testing"
)

func TestEscapedTailKeepsEncodedSlash(t *testing.T) {
	u, _ := url.Parse("http://x/ctx/https://h/v1/models/a%2Fb:generate")
	got := escapedTail(u, "/v1/models/a/b:generate")
	if want := "/v1/models/a%2Fb:generate"; got != want {
		t.Fatalf("escapedTail=%q, want %q", got, want)
	}
	plain, _ := url.Parse("http://x/v1/messages")
	if got := escapedTail(plain, "/v1/messages"); got != "" {
		t.Fatalf("plain path gave %q, want empty", got)
	}
}

func TestDropHeldAgesOutAfterThreeRequests(t *testing.T) {
	s := &Server{tunes: map[string]*tune{}}
	s.holdSpan("s", 3, 139)
	spans := []kernel.Span{{StartSeq: 3, EndSeq: 139}, {StartSeq: 140, EndSeq: 284}}
	for i := 1; i <= 3; i++ {
		got := dropHeld(spans, s.takeHeld("s"))
		if len(got) != 1 || got[0].StartSeq != 140 {
			t.Fatalf("request %d: spans=%v, want only 140..284", i, got)
		}
	}
	if got := dropHeld(spans, s.takeHeld("s")); len(got) != 2 {
		t.Fatalf("request 4: spans=%v, want both", got)
	}
}
