package config

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Reset-credit window identifiers accepted by reset_policy.<provider>.exhaustion.windows.
const (
	ResetPolicyWindow5h = "5h"
	ResetPolicyWindow7d = "7d"
)

// ResetPolicyConfig holds the global automatic reset-credit policy for each
// provider that supports reset credits on OAuth parent accounts.
//
// A provider policy takes effect only while its `enforce` flag is true. When
// enforced it replaces the per-account auto reset settings of every non-shadow
// OAuth parent account of that provider. When `enforce` is false the remaining
// provider settings are ignored and per-account behaviour is unchanged.
type ResetPolicyConfig struct {
	OpenAI    ProviderResetPolicyConfig `mapstructure:"openai"`
	Anthropic ProviderResetPolicyConfig `mapstructure:"anthropic"`
}

// ProviderResetPolicyConfig is the policy for one provider.
type ProviderResetPolicyConfig struct {
	// Enforce applies this policy to every eligible parent account of the
	// provider, overriding per-account settings (including "disabled").
	Enforce    bool                     `mapstructure:"enforce"`
	Expiry     ResetPolicyExpiryConfig  `mapstructure:"expiry"`
	Exhaustion ResetPolicyExhaustionCfg `mapstructure:"exhaustion"`
}

// ResetPolicyExpiryConfig redeems a credit that would otherwise expire unused.
type ResetPolicyExpiryConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// Horizon: a credit expiring within this duration is eligible (1m..168h).
	Horizon time.Duration `mapstructure:"horizon"`
	// MinUtilization: minimum used fraction (0..1) of an active 5h/7d window
	// required to redeem an expiring credit. 0 redeems unconditionally.
	MinUtilization float64 `mapstructure:"min_utilization"`
}

// ResetPolicyExhaustionCfg redeems a credit when the whole eligible
// group/model cohort is at or above Threshold in at least one of Windows.
type ResetPolicyExhaustionCfg struct {
	Enabled bool `mapstructure:"enabled"`
	// Threshold: used fraction (0.001..1) at which a window counts as exhausted.
	Threshold float64 `mapstructure:"threshold"`
	// Windows: window classes that count toward exhaustion ("5h", "7d").
	// Claude's 7-day Sonnet window belongs to the "7d" class.
	Windows []string `mapstructure:"windows"`
}

func setResetPolicyDefaults() {
	for _, provider := range []string{"openai", "anthropic"} {
		prefix := "reset_policy." + provider + "."
		viper.SetDefault(prefix+"enforce", false)
		viper.SetDefault(prefix+"expiry.enabled", false)
		viper.SetDefault(prefix+"expiry.horizon", time.Hour)
		viper.SetDefault(prefix+"expiry.min_utilization", 0.25)
		viper.SetDefault(prefix+"exhaustion.enabled", false)
		viper.SetDefault(prefix+"exhaustion.threshold", 1.0)
		viper.SetDefault(prefix+"exhaustion.windows", []string{ResetPolicyWindow5h, ResetPolicyWindow7d})
	}
}

// Validate normalizes window names and rejects out-of-range values. It runs
// regardless of `enforce` so that a typo fails at startup instead of silently
// changing behaviour the day the policy is enforced.
func (c *ResetPolicyConfig) Validate() error {
	if err := c.OpenAI.validate("reset_policy.openai"); err != nil {
		return err
	}
	return c.Anthropic.validate("reset_policy.anthropic")
}

func (p *ProviderResetPolicyConfig) validate(prefix string) error {
	if p.Expiry.Horizon < time.Minute || p.Expiry.Horizon > 7*24*time.Hour || p.Expiry.Horizon%time.Second != 0 {
		return fmt.Errorf("%s.expiry.horizon must be whole seconds between 1m and 168h, got %s", prefix, p.Expiry.Horizon)
	}
	if v := p.Expiry.MinUtilization; math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
		return fmt.Errorf("%s.expiry.min_utilization must be between 0 and 1, got %v", prefix, v)
	}
	if v := p.Exhaustion.Threshold; math.IsNaN(v) || math.IsInf(v, 0) || v < 0.001 || v > 1 {
		return fmt.Errorf("%s.exhaustion.threshold must be between 0.001 and 1, got %v", prefix, v)
	}
	windows, err := normalizeResetPolicyWindows(p.Exhaustion.Windows)
	if err != nil {
		return fmt.Errorf("%s.exhaustion.windows: %w", prefix, err)
	}
	p.Exhaustion.Windows = windows
	return nil
}

func normalizeResetPolicyWindows(raw []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, 2)
	for _, entry := range raw {
		// A single env value may carry "5h,7d" or "5h 7d".
		for _, part := range strings.FieldsFunc(entry, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
			name := strings.ToLower(strings.TrimSpace(part))
			if name != ResetPolicyWindow5h && name != ResetPolicyWindow7d {
				return nil, fmt.Errorf("unknown window %q (allowed: 5h, 7d)", part)
			}
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one window (5h, 7d) is required")
	}
	return out, nil
}
