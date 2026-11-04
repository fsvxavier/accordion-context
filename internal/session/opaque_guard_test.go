package session

import (
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// TestHasOpaqueMessage: a compress range may not cover an opaque input item.
func TestHasOpaqueMessage(t *testing.T) {
	msgs := []kernel.Message{
		{Seq: 1, Kind: kernel.KindText, Role: kernel.RoleUser},
		{Seq: 2, Kind: kernel.KindOpaque, Role: kernel.RoleAssistant},
		{Seq: 3, Kind: kernel.KindText, Role: kernel.RoleAssistant},
	}
	for _, tc := range []struct {
		lo, hi int64
		want   bool
	}{{1, 1, false}, {3, 3, false}, {1, 2, true}, {2, 2, true}, {2, 3, true}} {
		if got := hasOpaqueMessage(msgs, tc.lo, tc.hi); got != tc.want {
			t.Errorf("hasOpaqueMessage(%d,%d) = %v, want %v", tc.lo, tc.hi, got, tc.want)
		}
	}
}
