package e2e

import (
	"strings"
	"testing"
)

// TestBugToolRoundKeepsTurnOrder reproduces the window order bug. Once a turn has
// started a tool round, the next request renders its user message after the round
// (kernel.Render appends the last user message last, render.go), so the model sees
// its own tool_call and tool result before the request that caused them.
func TestBugToolRoundKeepsTurnOrder(t *testing.T) {
	reproToolOrder(t, "openai", "gpt-4o-mini")
}

// reproToolOrder is the body of TestBugToolRoundKeepsTurnOrder, kept apart so the bug
// can be run without the skip. The dialect is a parameter for that reason. It was run
// on both dialects: anthropic and openai both fail (the test itself runs openai only).
func reproToolOrder(t *testing.T, dialect, model string) {
	t.Helper()
	bin := buildBinary(t)
	up := newFakeUpstream(t, dialect)
	fx := newFixture(t, bin, up.URL(), configYAML)
	srv := fx.start(t)
	c := newClient(t, srv.base+"/p/e2e", dialect, model)
	c.turn("Turn 1: read module 1 and record its facts.")
	c.turn("Turn 2: read module 2 and record its facts.")

	bad := 0
	seen := false
	for i, rec := range up.snapshot() {
		userAt, callAt := -1, -1
		for j, m := range rec.msgs {
			for _, tx := range m.texts {
				if strings.HasPrefix(stripLabel(tx), "Turn 2:") {
					userAt = j
				}
			}
			for _, c := range m.calls {
				if strings.Contains(c.args, "mod002") {
					callAt = j
				}
			}
		}
		if callAt >= 0 {
			seen = true
		}
		if userAt >= 0 && callAt >= 0 && callAt < userAt {
			bad++
			t.Errorf("request %d: tool call for module 2 (message %d) precedes the turn that asked for it (message %d)", i, callAt, userAt)
		}
	}
	if !seen {
		t.Fatal("the model never issued the module 2 call")
	}
	if bad == 0 {
		t.Log("order kept")
	}
}
