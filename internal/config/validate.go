package config

import (
	"fmt"
	"strings"
)

// problem is one failed rule and the keys it involves.
type problem struct {
	keys []string
	msg  string
}

// validate applies the §18.8 rules plus basic range checks.
func validate(c Config) []problem {
	var ps []problem
	add := func(keys []string, format string, args ...any) {
		ps = append(ps, problem{keys: keys, msg: fmt.Sprintf(format, args...)})
	}

	if c.UFloor < 0 {
		add([]string{"trigger.u_floor"}, "must be >= 0")
	}
	if c.UFloor > c.UHigh {
		add([]string{"trigger.u_floor", "trigger.u_high"}, "u_floor must be <= u_high")
	}
	if c.UHigh > c.UEmerg {
		add([]string{"trigger.u_high", "trigger.u_emerg"}, "u_high must be <= u_emerg")
	}
	if c.UEmerg > 1 {
		add([]string{"trigger.u_emerg"}, "must be <= 1 (fraction of the context window)")
	}
	if c.StepMin <= 0 {
		add([]string{"trigger.step_min"}, "must be > 0")
	}
	if c.StepMin > c.StepBase {
		add([]string{"trigger.step_min", "trigger.step_base"}, "step_min must be <= step_base")
	}
	if c.StepBase > c.StepMax {
		add([]string{"trigger.step_base", "trigger.step_max"}, "step_base must be <= step_max")
	}
	if c.StepReadGain < 0 {
		add([]string{"trigger.step_read_gain"}, "must be >= 0")
	}
	if c.MinGrowthFloor < 0 {
		add([]string{"trigger.min_growth_floor"}, "must be >= 0")
	}
	if c.MaxRefusals < 0 {
		add([]string{"trigger.max_refusals"}, "must be >= 0")
	}
	if c.CacheTTL < 0 {
		add([]string{"cache.ttl"}, "must be >= 0")
	}
	if c.CacheMargin < 0 {
		add([]string{"cache.margin"}, "must be >= 0")
	}
	if c.PriceUncached < 0 {
		add([]string{"cache.price_uncached"}, "must be >= 0")
	}
	if c.PriceCached < 0 {
		add([]string{"cache.price_cached"}, "must be >= 0")
	}
	for _, f := range []struct {
		key string
		v   int
	}{
		{"compress.preserve_recent_messages", c.PreserveRecentMessages},
		{"compress.preserve_recent_tokens", c.PreserveRecentTokens},
		{"compress.min_compress_range", c.MinCompressRange},
		{"compress.min_summary_chars", c.MinSummaryChars},
		{"compress.max_summary_chars", c.MaxSummaryChars},
		{"ccr.threshold_tokens", c.CCRThresholdTokens},
	} {
		if f.v < 0 {
			add([]string{f.key}, "must be >= 0")
		}
	}
	if c.MaxSummaryChars < c.MinSummaryChars {
		add([]string{"compress.min_summary_chars", "compress.max_summary_chars"}, "min_summary_chars must be <= max_summary_chars")
	}
	if c.RecallMinAcceptRate < 0 || c.RecallMinAcceptRate > 1 {
		add([]string{"recall.min_accept_rate"}, "must be in [0, 1]")
	}
	if c.Storage != "global" && c.Storage != "local" {
		add([]string{"storage"}, "must be \"global\" or \"local\", got %q", c.Storage)
	}
	return ps
}

// Validate checks a merged configuration and returns one error listing every
// violated rule. It does not know which layer a value came from. Load reports
// sources.
func Validate(c Config) error {
	ps := validate(c)
	if len(ps) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(ps))
	for _, p := range ps {
		msgs = append(msgs, strings.Join(p.keys, "/")+": "+p.msg)
	}
	return fmt.Errorf("invalid configuration: %s", strings.Join(msgs, "; "))
}
