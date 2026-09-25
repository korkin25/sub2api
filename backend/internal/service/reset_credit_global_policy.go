package service

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// ResetCreditGlobalPolicy is the process-wide automatic reset-credit policy of
// one provider (config key reset_policy.<provider>). It only has an effect while
// Enforce is true; then it replaces the per-account settings of every
// non-shadow OAuth parent account of that provider.
type ResetCreditGlobalPolicy struct {
	Enforce bool

	ExpiryEnabled        bool
	ExpiryHorizon        time.Duration
	ExpiryMinUtilization float64 // 0 means unconditional

	ExhaustionEnabled   bool
	ExhaustionThreshold float64 // used fraction, 0.001..1
	Exhaustion5h        bool
	Exhaustion7d        bool
}

// ResetCreditGlobalPolicyFromConfig converts a validated provider config.
func ResetCreditGlobalPolicyFromConfig(c config.ProviderResetPolicyConfig) ResetCreditGlobalPolicy {
	p := ResetCreditGlobalPolicy{
		Enforce:              c.Enforce,
		ExpiryEnabled:        c.Expiry.Enabled,
		ExpiryHorizon:        c.Expiry.Horizon,
		ExpiryMinUtilization: c.Expiry.MinUtilization,
		ExhaustionEnabled:    c.Exhaustion.Enabled,
		ExhaustionThreshold:  c.Exhaustion.Threshold,
	}
	for _, w := range c.Exhaustion.Windows {
		switch w {
		case config.ResetPolicyWindow5h:
			p.Exhaustion5h = true
		case config.ResetPolicyWindow7d:
			p.Exhaustion7d = true
		}
	}
	return p
}

var resetCreditGlobalPolicies struct {
	openAI    atomic.Pointer[ResetCreditGlobalPolicy]
	anthropic atomic.Pointer[ResetCreditGlobalPolicy]
}

// SetResetCreditGlobalPolicies installs the configured provider policies. A nil
// config clears them, which restores per-account behaviour.
func SetResetCreditGlobalPolicies(cfg *config.Config) {
	if cfg == nil {
		setResetCreditGlobalPolicies(ResetCreditGlobalPolicy{}, ResetCreditGlobalPolicy{})
		return
	}
	openAI := ResetCreditGlobalPolicyFromConfig(cfg.ResetPolicy.OpenAI)
	anthropic := ResetCreditGlobalPolicyFromConfig(cfg.ResetPolicy.Anthropic)
	setResetCreditGlobalPolicies(openAI, anthropic)
	for name, p := range map[string]ResetCreditGlobalPolicy{PlatformOpenAI: openAI, PlatformAnthropic: anthropic} {
		if p.Enforce {
			slog.Info("reset_credit_global_policy_enforced",
				"provider", name,
				"expiry_enabled", p.ExpiryEnabled,
				"expiry_horizon", p.ExpiryHorizon.String(),
				"expiry_min_utilization", p.ExpiryMinUtilization,
				"exhaustion_enabled", p.ExhaustionEnabled,
				"exhaustion_threshold", p.ExhaustionThreshold,
				"exhaustion_5h", p.Exhaustion5h,
				"exhaustion_7d", p.Exhaustion7d,
			)
		}
	}
}

func setResetCreditGlobalPolicies(openAI, anthropic ResetCreditGlobalPolicy) {
	resetCreditGlobalPolicies.openAI.Store(&openAI)
	resetCreditGlobalPolicies.anthropic.Store(&anthropic)
}

func openAIResetCreditGlobalPolicy() ResetCreditGlobalPolicy {
	if p := resetCreditGlobalPolicies.openAI.Load(); p != nil {
		return *p
	}
	return ResetCreditGlobalPolicy{}
}

func anthropicResetCreditGlobalPolicy() ResetCreditGlobalPolicy {
	if p := resetCreditGlobalPolicies.anthropic.Load(); p != nil {
		return *p
	}
	return ResetCreditGlobalPolicy{}
}

// autoResetConfig projects an enforced policy onto the account-level config
// shape used by both workers. Scheduler pause thresholds stay at 100% so the
// enforced policy does not pause an account that still has capacity.
func (p ResetCreditGlobalPolicy) autoResetConfig() OpenAIAutoResetCreditConfig {
	cfg := OpenAIAutoResetCreditConfig{
		Enforced:             true,
		Enabled:              p.ExpiryEnabled || p.ExhaustionEnabled,
		ExpiryHorizonSeconds: int(p.ExpiryHorizon / time.Second),
		ExpiryMinUtilization: p.ExpiryMinUtilization,
		Threshold5h:          openAIAutoResetCreditDefaultThreshold,
		Threshold7d:          openAIAutoResetCreditDefaultThreshold,
		ExhaustionThreshold:  p.ExhaustionThreshold,
		Exhaustion5h:         p.Exhaustion5h,
		Exhaustion7d:         p.Exhaustion7d,
	}
	switch {
	case p.ExpiryEnabled && p.ExhaustionEnabled:
		cfg.Mode = OpenAIAutoResetModeExpiringOrExhausted
	case p.ExpiryEnabled:
		cfg.Mode = OpenAIAutoResetModeExpiring
	default:
		// Also used when both triggers are off (Enabled=false): an enforced
		// policy without triggers disables automatic resets for the provider.
		cfg.Mode = OpenAIAutoResetModeExhausted
	}
	return cfg
}

// exhaustionTriggerEnabled reports whether the resolved mode can spend on cohort exhaustion.
func (c OpenAIAutoResetCreditConfig) exhaustionTriggerEnabled() bool {
	return c.Enabled && (c.Mode == OpenAIAutoResetModeExhausted || c.Mode == OpenAIAutoResetModeExpiringOrExhausted)
}

// validExpiryMinUtilization keeps the legacy per-account range (0.001..1) and
// additionally allows 0 (unconditional) for an enforced global policy.
func (c OpenAIAutoResetCreditConfig) validExpiryMinUtilization() bool {
	if c.Enforced {
		v := c.ExpiryMinUtilization
		return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1
	}
	return isValidOpenAIAutoResetThreshold(c.ExpiryMinUtilization)
}

// expiryUnconditional: redeem an expiring credit regardless of window usage.
func (c OpenAIAutoResetCreditConfig) expiryUnconditional() bool {
	return c.Enforced && c.ExpiryMinUtilization == 0
}

// windowOverThreshold applies the enforced exhaustion rule to one window class.
func (c OpenAIAutoResetCreditConfig) windowOverThreshold(class string, usedFraction float64) bool {
	if math.IsNaN(usedFraction) || math.IsInf(usedFraction, 0) || c.ExhaustionThreshold <= 0 {
		return false
	}
	switch class {
	case config.ResetPolicyWindow5h:
		if !c.Exhaustion5h {
			return false
		}
	case config.ResetPolicyWindow7d:
		if !c.Exhaustion7d {
			return false
		}
	default:
		return false
	}
	return usedFraction >= c.ExhaustionThreshold
}

// Soft exhaustion signals (utilization at or above an enforced threshold below
// 100%) come from the request hot path; each one makes the worker query the
// native usage of the whole cohort, so they are rate limited per account.
const openAIResetSoftNotifyInterval = 30 * time.Second

var openAIResetSoftNotify sync.Map // accountID -> time.Time

func notifyOpenAIAutoResetSoftThreshold(ctx context.Context, accountID int64, now time.Time) {
	if last, ok := openAIResetSoftNotify.Load(accountID); ok {
		if t, ok := last.(time.Time); ok && now.Sub(t) < openAIResetSoftNotifyInterval {
			return
		}
	}
	openAIResetSoftNotify.Store(accountID, now)
	notifyOpenAIAutoResetScoped(ctx, accountID)
}
