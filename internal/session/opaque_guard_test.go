package session

import (
	"context"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// TestBarrierInside: a compress range may not cover a barrier opaque item, but a
// carried one (a client system message) does not stop it.
func TestBarrierInside(t *testing.T) {
	msgs := []kernel.Message{
		{Seq: 1, Ref: "m00001", Kind: kernel.KindText, Role: kernel.RoleUser},
		{Seq: 2, Ref: "m00002", Kind: kernel.KindOpaque, Role: kernel.RoleAssistant},
		{Seq: 3, Ref: "m00003", Kind: kernel.KindText, Role: kernel.RoleAssistant},
		{Seq: 4, Ref: "m00004", Kind: kernel.KindOpaque, Role: kernel.RoleUser},
		{Seq: 5, Ref: "m00005", Kind: kernel.KindText, Role: kernel.RoleAssistant},
	}
	for _, tc := range []struct {
		lo, hi int64
		want   bool
	}{{1, 1, false}, {3, 3, false}, {1, 2, true}, {2, 2, true}, {2, 3, true}, {3, 5, false}} {
		ref, got := barrierInside(msgs, tc.lo, tc.hi)
		if got != tc.want {
			t.Errorf("barrierInside(%d,%d) = %v, want %v", tc.lo, tc.hi, got, tc.want)
		}
		if got && ref != "m00002" {
			t.Errorf("barrierInside(%d,%d) ref = %q, want m00002", tc.lo, tc.hi, ref)
		}
	}
}

// TestHandleCompressCarriesClientSystemMessages: a range over client system
// messages (carried opaque items) is accepted, their tokens do not count as
// replaced, and the window keeps them verbatim after the envelope. A range that
// ends on one, or covers a barrier, is refused naming the item.
func TestHandleCompressCarriesClientSystemMessages(t *testing.T) {
	ctx := context.Background()
	s, st := newSession(t)
	msgs := transcript()
	for _, i := range []int{3, 6} { // m00004 and m00007
		msgs[i].Role, msgs[i].Kind, msgs[i].Text = kernel.RoleUser, kernel.KindOpaque, "reminder "+msgs[i].Text
	}
	msgs[10].Role, msgs[10].Kind = kernel.RoleAssistant, kernel.KindOpaque // m00011: barrier
	if _, err := s.Ingest(ctx, msgs); err != nil {
		t.Fatal(err)
	}

	if _, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{{"m00002", "m00007", okSummary, ""}}}); err == nil ||
		!strings.Contains(err.Error(), "client system message") {
		t.Fatalf("range ending on a carried item: err = %v", err)
	}
	if _, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{{"m00008", "m00012", okSummary, ""}}}); err == nil ||
		!strings.Contains(err.Error(), "m00011") {
		t.Fatalf("range over a barrier: err = %v", err)
	}
	if _, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{{"m00002", "m00008", okSummary, ""}}}); err != nil {
		t.Fatal(err)
	}
	snap, err := st.Snapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if b := snap.Blocks[0]; b.SrcTokens != 5000 {
		t.Fatalf("SrcTokens = %d, want 5000 (carried items excluded)", b.SrcTokens)
	}
	var order []string
	for _, m := range kernel.Render(snap) {
		switch {
		case strings.Contains(m.Text, `<history block="b1"`):
			order = append(order, "b1")
		case m.Seq >= 2 && m.Seq <= 8:
			order = append(order, m.Ref)
		}
	}
	if got := strings.Join(order, " "); got != "b1 m00004 m00007" {
		t.Fatalf("window order = %q, want envelope then the carried items", got)
	}
}
