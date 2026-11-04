// Package config loads and merges acd-ctx configuration (spec §18).
//
// Precedence, lowest to highest (§18.5): defaults, global file, matching
// "projects" override in the global file, local <root>/.acd-ctx/config.yaml,
// ACD_CTX_* environment variables, explicit flags. Each key resolves on its
// own. Effective reports which layer supplied each key.
package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// Config is the merged, typed configuration. Field comments give the YAML key.
type Config struct {
	// trigger.* (§8.1, §8.4)
	UFloor         apd.Decimal // trigger.u_floor
	UHigh          apd.Decimal // trigger.u_high
	UEmerg         apd.Decimal // trigger.u_emerg
	StepBase       int         // trigger.step_base
	StepMin        int         // trigger.step_min
	StepMax        int         // trigger.step_max
	StepReadGain   apd.Decimal // trigger.step_read_gain
	MinGrowthFloor int         // trigger.min_growth_floor
	MaxRefusals    int         // trigger.max_refusals
	TiersEnabled   bool        // trigger.tiers_enabled

	// cache.* (§9). Global only: TTL and prices depend on the provider.
	CacheTTL      time.Duration // cache.ttl
	CacheMargin   apd.Decimal   // cache.margin
	PriceUncached apd.Decimal   // cache.price_uncached
	PriceCached   apd.Decimal   // cache.price_cached

	// compress.* (§10, §11)
	PreserveRecentMessages int      // compress.preserve_recent_messages
	PreserveRecentTokens   int      // compress.preserve_recent_tokens
	MinCompressRange       int      // compress.min_compress_range
	MinSummaryChars        int      // compress.min_summary_chars
	MaxSummaryChars        int      // compress.max_summary_chars
	ProtectedTools         []string // compress.protected_tools
	FramingCheck           bool     // compress.framing_check
	UserTurnAfterSummaries bool     // compress.user_turn_after_summaries
	IntentLedger           bool     // compress.intent_ledger

	// ccr.* (§14)
	CCRThresholdTokens int  // ccr.threshold_tokens
	CrushEnabled       bool // ccr.crush

	// recall.* (§13)
	RecallAssist        bool        // recall.assist
	RecallMinAcceptRate apd.Decimal // recall.min_accept_rate

	// window.* (§8.1, §4): the context window the trigger measures against.
	WindowLimit  int            // window.context_limit: 0 derives the window from the model
	WindowModels map[string]int // window.models: model prefix (lower case) -> tokens

	// storage: "global" (default) or "local" (§18.1, §18.3).
	Storage string // storage
}

// Defaults returns the built-in values (spec §18.8 and §8.1).
func Defaults() Config {
	return Config{
		UFloor:         dec("0.30"),
		UHigh:          dec("0.75"),
		UEmerg:         dec("0.95"),
		StepBase:       50_000,
		StepMin:        30_000,
		StepMax:        100_000,
		StepReadGain:   dec("1.0"),
		MinGrowthFloor: 20_000,
		MaxRefusals:    3,
		TiersEnabled:   true,

		CacheTTL:    5 * time.Minute,
		CacheMargin: dec("1.5"),

		PreserveRecentMessages: 3,
		PreserveRecentTokens:   5_000,
		MinCompressRange:       5_000,
		MinSummaryChars:        50,
		MaxSummaryChars:        20_000,
		ProtectedTools:         []string{},
		FramingCheck:           false,
		UserTurnAfterSummaries: true,
		IntentLedger:           true,

		CCRThresholdTokens: 4_000,
		CrushEnabled:       true,

		RecallAssist:        true,
		RecallMinAcceptRate: dec("0.05"),

		WindowModels: map[string]int{},

		Storage: "global",
	}
}

// Kernel maps the trigger settings onto kernel.Config. The kernel package
// does not import this one.
func (c Config) Kernel() kernel.Config {
	return kernel.Config{
		StepBase:       c.StepBase,
		StepMin:        c.StepMin,
		StepMax:        c.StepMax,
		StepReadGain:   c.StepReadGain,
		MinGrowthFloor: c.MinGrowthFloor,
		UFloor:         c.UFloor,
		UHigh:          c.UHigh,
		UEmerg:         c.UEmerg,
		TiersEnabled:   c.TiersEnabled,
	}
}

// keySpec describes one configuration key: its YAML path, whether the local
// project layer may set it (§18.6), and how to reach its field.
type keySpec struct {
	key   string
	local bool
	field func(*Config) any
}

// specs is the complete list of known keys, in display order.
var specs = []keySpec{
	{"trigger.u_floor", true, func(c *Config) any { return &c.UFloor }},
	{"trigger.u_high", true, func(c *Config) any { return &c.UHigh }},
	{"trigger.u_emerg", true, func(c *Config) any { return &c.UEmerg }},
	{"trigger.step_base", true, func(c *Config) any { return &c.StepBase }},
	{"trigger.step_min", true, func(c *Config) any { return &c.StepMin }},
	{"trigger.step_max", true, func(c *Config) any { return &c.StepMax }},
	{"trigger.step_read_gain", true, func(c *Config) any { return &c.StepReadGain }},
	{"trigger.min_growth_floor", true, func(c *Config) any { return &c.MinGrowthFloor }},
	{"trigger.max_refusals", true, func(c *Config) any { return &c.MaxRefusals }},
	{"trigger.tiers_enabled", true, func(c *Config) any { return &c.TiersEnabled }},

	{"cache.ttl", false, func(c *Config) any { return &c.CacheTTL }},
	{"cache.margin", false, func(c *Config) any { return &c.CacheMargin }},
	{"cache.price_uncached", false, func(c *Config) any { return &c.PriceUncached }},
	{"cache.price_cached", false, func(c *Config) any { return &c.PriceCached }},

	{"compress.preserve_recent_messages", true, func(c *Config) any { return &c.PreserveRecentMessages }},
	{"compress.preserve_recent_tokens", true, func(c *Config) any { return &c.PreserveRecentTokens }},
	{"compress.min_compress_range", true, func(c *Config) any { return &c.MinCompressRange }},
	{"compress.min_summary_chars", true, func(c *Config) any { return &c.MinSummaryChars }},
	{"compress.max_summary_chars", true, func(c *Config) any { return &c.MaxSummaryChars }},
	{"compress.protected_tools", true, func(c *Config) any { return &c.ProtectedTools }},
	{"compress.framing_check", true, func(c *Config) any { return &c.FramingCheck }},
	{"compress.user_turn_after_summaries", true, func(c *Config) any { return &c.UserTurnAfterSummaries }},
	{"compress.intent_ledger", true, func(c *Config) any { return &c.IntentLedger }},

	{"ccr.threshold_tokens", true, func(c *Config) any { return &c.CCRThresholdTokens }},
	{"ccr.crush", true, func(c *Config) any { return &c.CrushEnabled }},

	{"recall.assist", true, func(c *Config) any { return &c.RecallAssist }},
	{"recall.min_accept_rate", true, func(c *Config) any { return &c.RecallMinAcceptRate }},

	{"window.context_limit", true, func(c *Config) any { return &c.WindowLimit }},
	{"window.models", true, func(c *Config) any { return &c.WindowModels }},

	{"storage", true, func(c *Config) any { return &c.Storage }},
}

// specByKey indexes specs by YAML path.
var specByKey = func() map[string]keySpec {
	m := make(map[string]keySpec, len(specs))
	for _, s := range specs {
		m[s.key] = s
	}
	return m
}()

// reservedGlobal lists keys that belong to later features (§18.6). They load
// from the global file without error, are ignored, and produce a warning.
// The local layer drops them with a warning too.
var reservedGlobal = []string{"upstream.", "credentials.", "listen.", "tools.external_summary."}

func isReserved(key string) bool {
	for _, p := range reservedGlobal {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// EnvName returns the environment variable that sets key, e.g.
// "trigger.step_base" -> "ACD_CTX_TRIGGER_STEP_BASE".
func EnvName(key string) string {
	return "ACD_CTX_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// parseRaw converts a string from an env var or flag into a layer leaf for the
// field's type.
func parseRaw(field any, raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	switch field.(type) {
	case *int:
		v, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("want integer, got %q", raw)
		}
		return v, nil
	case *apd.Decimal:
		v, err := parseDecimal(raw)
		if err != nil {
			return nil, fmt.Errorf("want number, got %q", raw)
		}
		return v, nil
	case *bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("want boolean, got %q", raw)
		}
		return v, nil
	case *string:
		return raw, nil
	case *time.Duration:
		if _, err := time.ParseDuration(raw); err != nil {
			return nil, fmt.Errorf("want duration like 5m, got %q", raw)
		}
		return raw, nil
	case *[]string:
		out := []any{}
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out, nil
	case *map[string]int:
		// "model-prefix=tokens,model-prefix=tokens"
		out := map[string]any{}
		for _, pair := range strings.Split(raw, ",") {
			if strings.TrimSpace(pair) == "" {
				continue
			}
			k, v, ok := strings.Cut(pair, "=")
			if !ok {
				return nil, fmt.Errorf("want prefix=tokens pairs, got %q", pair)
			}
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("want integer tokens in %q", pair)
			}
			out[strings.TrimSpace(k)] = n
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported field type %T", field)
}

// assign stores a layer leaf (a YAML scalar or []any) into field.
func assign(field any, v any) error {
	switch f := field.(type) {
	case *int:
		if n, ok := v.(int); ok {
			*f = n
			return nil
		}
	case *apd.Decimal:
		switch n := v.(type) {
		case apd.Decimal:
			*f = n
			return nil
		case float64:
			d, err := decimalFromFloat(n)
			if err != nil {
				return err
			}
			*f = d
			return nil
		case int:
			*f = intDec(n)
			return nil
		}
	case *bool:
		if b, ok := v.(bool); ok {
			*f = b
			return nil
		}
	case *string:
		if s, ok := v.(string); ok {
			*f = s
			return nil
		}
	case *time.Duration:
		if s, ok := v.(string); ok {
			d, err := time.ParseDuration(s)
			if err != nil {
				return fmt.Errorf("want duration like 5m, got %q", s)
			}
			*f = d
			return nil
		}
	case *[]string:
		if list, ok := v.([]any); ok {
			out := make([]string, 0, len(list))
			for _, item := range list {
				s, ok := item.(string)
				if !ok {
					return fmt.Errorf("list items must be strings, got %v", item)
				}
				out = append(out, s)
			}
			*f = out
			return nil
		}
	case *map[string]int:
		// Layers merge: a key set by a later layer replaces the earlier value,
		// and other keys stay. Model keys are stored in lower case.
		m, ok := v.(map[string]any)
		if !ok {
			break
		}
		if *f == nil {
			*f = map[string]int{}
		}
		for k, raw := range m {
			n, ok := raw.(int)
			if x, isF := raw.(float64); !ok && isF && x == float64(int(x)) {
				n, ok = int(x), true
			}
			if !ok {
				return fmt.Errorf("window.models %q: want integer tokens, got %v", k, raw)
			}
			(*f)[strings.ToLower(strings.TrimSpace(k))] = n
		}
		return nil
	default:
		return fmt.Errorf("unsupported field type %T", field)
	}
	return fmt.Errorf("wrong type %T for this key", v)
}

// formatValue renders a field for Effective.
func formatValue(field any) string {
	switch f := field.(type) {
	case *int:
		return strconv.Itoa(*f)
	case *apd.Decimal:
		return formatDecimal(*f)
	case *bool:
		return strconv.FormatBool(*f)
	case *string:
		return *f
	case *time.Duration:
		return f.String()
	case *[]string:
		return strings.Join(*f, ",")
	case *map[string]int:
		keys := make([]string, 0, len(*f))
		for k := range *f {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+strconv.Itoa((*f)[k]))
		}
		return strings.Join(parts, ",")
	}
	return ""
}
