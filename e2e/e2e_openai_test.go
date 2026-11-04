package e2e

import "testing"

// TestE2EOpenAIMainPath runs the same 80-turn session through Chat Completions
// (non-streamed). The gpt-4o model has a 128000-token window of its own.
func TestE2EOpenAIMainPath(t *testing.T) {
	runScenario(t, scenario{
		dialect:   "openai",
		model:     "gpt-4o-mini",
		restartAt: scenarioTurns * 5 / 8,
	})
}
