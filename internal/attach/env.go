package attach

import (
	"os"
	"slices"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/launcher"
)

// noProxyExtra keeps loopback traffic (the base URL degree) away from a user's
// HTTP_PROXY or HTTPS_PROXY.
const noProxyExtra = "127.0.0.1,localhost"

// buildEnv applies overrides to base (os.Environ when nil) and makes the
// loopback addresses bypass any user proxy.
func buildEnv(base []string, overrides []kv) []string {
	if base == nil {
		base = os.Environ()
	}
	env := slices.Clone(base)
	for _, o := range overrides {
		env = setEnv(env, o.k, o.v)
	}
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		env = setEnv(env, key, joinNoProxy(envGet(env, key)))
	}
	return env
}

// joinNoProxy appends the loopback entries to an existing NO_PROXY value.
func joinNoProxy(cur string) string {
	if strings.Contains(cur, "127.0.0.1") && strings.Contains(cur, "localhost") {
		return cur
	}
	if strings.TrimSpace(cur) == "" {
		return noProxyExtra
	}
	return cur + "," + noProxyExtra
}

// envGet returns the value of key in env, the last assignment wins.
func envGet(env []string, key string) string {
	prefix := key + "="
	val := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			val = strings.TrimPrefix(kv, prefix)
		}
	}
	return val
}

// setEnv replaces key in env, or appends it when absent.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	return append(out, prefix+value)
}

// uniqueSorted sorts in place, drops empty values and duplicates.
func uniqueSorted(in []string) []string {
	var out []string
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func summarize(attempts []Attempt) string {
	if len(attempts) == 0 {
		return "no degree attempted"
	}
	parts := make([]string, 0, len(attempts))
	for _, a := range attempts {
		parts = append(parts, a.Degree+": "+a.Reason)
	}
	return strings.Join(parts, "; ")
}

// BaseURLVars lists the base URL variables of every profile, for console mode
// and for the doctor. Duplicates are removed; order follows the profiles.
func BaseURLVars(profiles []launcher.Profile) []string {
	var out []string
	for _, p := range profiles {
		if p.BaseURLEnv != "" && !slices.Contains(out, p.BaseURLEnv) {
			out = append(out, p.BaseURLEnv)
		}
	}
	return out
}
