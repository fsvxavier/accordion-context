package proxy

import (
	"testing"

	"github.com/fsvxavier/accordion-context/internal/config"
)

// TestClaude5WindowIsOneMillion: Claude 5 models get the 1M window, older Claude
// models keep 200k, and window.models still wins.
func TestClaude5WindowIsOneMillion(t *testing.T) {
	s := &Server{windows: mergedWindows(nil)}
	s.cfg.ContextLimit = 200_000
	for model, want := range map[string]int{
		"claude-sonnet-5-5": 1_000_000, "claude-opus-5-5": 1_000_000, "claude-haiku-5-5": 1_000_000,
		"claude-fable-5-2": 1_000_000, "claude-3-5-sonnet": 200_000, "claude-sonnet-4": 200_000,
	} {
		if got := s.contextWindow(model, config.Config{}); got != want {
			t.Errorf("contextWindow(%q) = %d, want %d", model, got, want)
		}
	}
	cfg := config.Config{WindowModels: map[string]int{"claude-sonnet-5": 300_000}}
	if got := s.contextWindow("claude-sonnet-5-5", cfg); got != 300_000 {
		t.Errorf("window.models override = %d, want 300000", got)
	}
}
