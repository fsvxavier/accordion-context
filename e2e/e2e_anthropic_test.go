package e2e

import "testing"

// TestE2EAnthropicMainPath runs a 80-turn streamed Messages API session through the
// real binary: folds happen server-side, the proxy restarts mid-run, a folded fact
// comes back by decompress, and the event sequence matches testdata/traces.
// The model name carries the gpt-4o prefix so the proxy uses the 128000-token window.
func TestE2EAnthropicMainPath(t *testing.T) {
	runScenario(t, scenario{
		dialect:   "anthropic",
		model:     "gpt-4o-e2e",
		restartAt: scenarioTurns * 5 / 8,
		golden:    true,
	})
}
