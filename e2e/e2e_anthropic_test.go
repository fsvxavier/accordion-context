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

// TODO(e2e): the config file cannot set the context window. Only the model-prefix
// table (builtinWindows) and the Go API (proxy.Config.ContextLimit, not wired by
// serve) set it. A context_limit or window key would let the scenarios use a
// small window without a model-name workaround. Remove the skip once it exists.
func TestConfigCannotSetContextWindow(t *testing.T) {
	t.Skip("TODO: config has no context_limit/window key (internal/config specs); serve does not wire ContextLimit")
}
