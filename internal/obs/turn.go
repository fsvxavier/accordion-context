package obs

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// TurnEvent is the payload of a "turn" event: the inputs of one Plan, which replay
// repeats. Tokens is the calibrated window size the Plan saw, Messages the ledger
// length it ran on, ContextLimit the window in force and Settings the session
// options in force. A replay that restores all four makes the same decisions.
type TurnEvent struct {
	Tokens       int      `json:"tokens"`
	Messages     int      `json:"messages"`
	ContextLimit int      `json:"context_limit"`
	Settings     Settings `json:"settings"`
}

// Settings are the per-session options that decide a Plan (spec §8, §10.2).
type Settings struct {
	RecentKeep       int            `json:"recent_keep"`
	MinCompressRange int            `json:"min_compress_range"`
	MinSummaryChars  int            `json:"min_summary_chars"`
	Kernel           KernelSettings `json:"kernel"`
}

// KernelSettings is kernel.Config with its decimals written as strings, so they
// survive a JSON round trip exactly.
type KernelSettings struct {
	StepBase       int    `json:"step_base"`
	StepMin        int    `json:"step_min"`
	StepMax        int    `json:"step_max"`
	StepReadGain   string `json:"step_read_gain"`
	MinGrowthFloor int    `json:"min_growth_floor"`
	UFloor         string `json:"u_floor"`
	UHigh          string `json:"u_high"`
	UEmerg         string `json:"u_emerg"`
	TiersEnabled   bool   `json:"tiers_enabled"`
}

// SettingsOf captures the options of a session.
func SettingsOf(k kernel.Config, recentKeep, minCompressRange, minSummaryChars int) Settings {
	return Settings{
		RecentKeep: recentKeep, MinCompressRange: minCompressRange, MinSummaryChars: minSummaryChars,
		Kernel: KernelSettings{
			StepBase: k.StepBase, StepMin: k.StepMin, StepMax: k.StepMax,
			StepReadGain: k.StepReadGain.String(), MinGrowthFloor: k.MinGrowthFloor,
			UFloor: k.UFloor.String(), UHigh: k.UHigh.String(), UEmerg: k.UEmerg.String(),
			TiersEnabled: k.TiersEnabled,
		},
	}
}

// Config rebuilds the kernel configuration. It fails when a decimal is malformed.
func (k KernelSettings) Config() (kernel.Config, error) {
	var c kernel.Config
	c.StepBase, c.StepMin, c.StepMax = k.StepBase, k.StepMin, k.StepMax
	c.MinGrowthFloor, c.TiersEnabled = k.MinGrowthFloor, k.TiersEnabled
	for _, f := range []struct {
		s   string
		dst *apd.Decimal
	}{
		{k.StepReadGain, &c.StepReadGain}, {k.UFloor, &c.UFloor},
		{k.UHigh, &c.UHigh}, {k.UEmerg, &c.UEmerg},
	} {
		d, _, err := apd.BaseContext.NewFromString(f.s)
		if err != nil {
			return c, fmt.Errorf("kernel setting %q: %w", f.s, err)
		}
		*f.dst = *d
	}
	return c, nil
}
