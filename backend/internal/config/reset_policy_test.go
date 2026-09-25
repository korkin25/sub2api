//go:build unit

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResetPolicyDefaultsKeepPerAccountBehaviour(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	for _, p := range []ProviderResetPolicyConfig{cfg.ResetPolicy.OpenAI, cfg.ResetPolicy.Anthropic} {
		require.False(t, p.Enforce)
		require.False(t, p.Expiry.Enabled)
		require.Equal(t, time.Hour, p.Expiry.Horizon)
		require.Equal(t, 0.25, p.Expiry.MinUtilization)
		require.False(t, p.Exhaustion.Enabled)
		require.Equal(t, 1.0, p.Exhaustion.Threshold)
		require.Equal(t, []string{"5h", "7d"}, p.Exhaustion.Windows)
	}
}

func TestResetPolicyLoadsFromEnv(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("RESET_POLICY_OPENAI_ENFORCE", "true")
	t.Setenv("RESET_POLICY_OPENAI_EXPIRY_ENABLED", "true")
	t.Setenv("RESET_POLICY_OPENAI_EXPIRY_HORIZON", "2h")
	t.Setenv("RESET_POLICY_OPENAI_EXPIRY_MIN_UTILIZATION", "0")
	t.Setenv("RESET_POLICY_OPENAI_EXHAUSTION_ENABLED", "true")
	t.Setenv("RESET_POLICY_OPENAI_EXHAUSTION_THRESHOLD", "0.95")
	t.Setenv("RESET_POLICY_OPENAI_EXHAUSTION_WINDOWS", "7d")
	t.Setenv("RESET_POLICY_ANTHROPIC_ENFORCE", "true")
	t.Setenv("RESET_POLICY_ANTHROPIC_EXPIRY_ENABLED", "true")
	t.Setenv("RESET_POLICY_ANTHROPIC_EXPIRY_HORIZON", "90m")
	t.Setenv("RESET_POLICY_ANTHROPIC_EXPIRY_MIN_UTILIZATION", "0.5")
	t.Setenv("RESET_POLICY_ANTHROPIC_EXHAUSTION_ENABLED", "true")
	t.Setenv("RESET_POLICY_ANTHROPIC_EXHAUSTION_THRESHOLD", "0.95")
	t.Setenv("RESET_POLICY_ANTHROPIC_EXHAUSTION_WINDOWS", "7D, 5h,7d")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, ProviderResetPolicyConfig{
		Enforce:    true,
		Expiry:     ResetPolicyExpiryConfig{Enabled: true, Horizon: 2 * time.Hour, MinUtilization: 0},
		Exhaustion: ResetPolicyExhaustionCfg{Enabled: true, Threshold: 0.95, Windows: []string{"7d"}},
	}, cfg.ResetPolicy.OpenAI)
	require.Equal(t, ProviderResetPolicyConfig{
		Enforce:    true,
		Expiry:     ResetPolicyExpiryConfig{Enabled: true, Horizon: 90 * time.Minute, MinUtilization: 0.5},
		Exhaustion: ResetPolicyExhaustionCfg{Enabled: true, Threshold: 0.95, Windows: []string{"7d", "5h"}},
	}, cfg.ResetPolicy.Anthropic)
}

func TestResetPolicyRejectsInvalidValues(t *testing.T) {
	cases := map[string]string{
		"RESET_POLICY_OPENAI_EXPIRY_HORIZON":             "30s",
		"RESET_POLICY_ANTHROPIC_EXPIRY_HORIZON":          "169h",
		"RESET_POLICY_OPENAI_EXPIRY_MIN_UTILIZATION":     "1.5",
		"RESET_POLICY_ANTHROPIC_EXPIRY_MIN_UTILIZATION":  "-0.1",
		"RESET_POLICY_OPENAI_EXHAUSTION_THRESHOLD":       "0",
		"RESET_POLICY_ANTHROPIC_EXHAUSTION_THRESHOLD":    "1.01",
		"RESET_POLICY_OPENAI_EXHAUSTION_WINDOWS":         "1d",
		"RESET_POLICY_ANTHROPIC_EXHAUSTION_WINDOWS":      ",",
		"RESET_POLICY_OPENAI_EXHAUSTION_THRESHOLD#nan":   "NaN",
		"RESET_POLICY_ANTHROPIC_EXPIRY_HORIZON#fraction": "90500ms",
	}
	for key, value := range cases {
		t.Run(key, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			env := key
			for i := range key {
				if key[i] == '#' {
					env = key[:i]
					break
				}
			}
			t.Setenv(env, value)
			_, err := Load()
			require.Error(t, err)
			require.Contains(t, err.Error(), "reset_policy.")
		})
	}
}

func TestResetPolicyValidatedEvenWhenNotEnforced(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("RESET_POLICY_OPENAI_ENFORCE", "false")
	t.Setenv("RESET_POLICY_OPENAI_EXHAUSTION_WINDOWS", "5h,30d")
	_, err := Load()
	require.ErrorContains(t, err, "reset_policy.openai.exhaustion.windows")
}
