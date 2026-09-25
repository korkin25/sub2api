package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// installResetPolicies sets process-wide policies for one test and restores
// per-account behaviour afterwards. Tests using it must not run in parallel.
func installResetPolicies(t *testing.T, openAI, anthropic config.ProviderResetPolicyConfig) {
	t.Helper()
	cfg := &config.Config{}
	cfg.ResetPolicy.OpenAI = openAI
	cfg.ResetPolicy.Anthropic = anthropic
	require.NoError(t, cfg.ResetPolicy.Validate())
	SetResetCreditGlobalPolicies(cfg)
	t.Cleanup(func() { SetResetCreditGlobalPolicies(nil) })
}

func ownerResetPolicy() config.ProviderResetPolicyConfig {
	return config.ProviderResetPolicyConfig{
		Enforce:    true,
		Expiry:     config.ResetPolicyExpiryConfig{Enabled: true, Horizon: 2 * time.Hour, MinUtilization: 0},
		Exhaustion: config.ResetPolicyExhaustionCfg{Enabled: true, Threshold: 0.95, Windows: []string{"7d"}},
	}
}

func notEnforcedResetPolicy() config.ProviderResetPolicyConfig {
	p := ownerResetPolicy()
	p.Enforce = false
	return p
}

func TestResetGlobalPolicyEnforceOverridesPerAccountOff(t *testing.T) {
	installResetPolicies(t, ownerResetPolicy(), ownerResetPolicy())
	disabled := map[string]any{
		OpenAIAutoResetCreditEnabledExtraKey: false,
		OpenAIAutoResetCreditModeExtraKey:    OpenAIAutoResetModeThreshold,
		"claude_auto_reset_credit_enabled":   false,
	}
	for _, extra := range []map[string]any{nil, disabled} {
		openAI := ResolveOpenAIAutoResetCreditConfig(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra})
		require.True(t, openAI.Enforced)
		require.True(t, openAI.Enabled)
		require.Equal(t, OpenAIAutoResetModeExpiringOrExhausted, openAI.Mode)
		require.Equal(t, 7200, openAI.ExpiryHorizonSeconds)
		require.Equal(t, 1.0, openAI.Threshold5h, "enforced policy must not pause accounts below 100%")
		require.Equal(t, 1.0, openAI.Threshold7d)
		require.True(t, openAI.Exhaustion7d)
		require.False(t, openAI.Exhaustion5h)

		claude := resolveClaudeAutoResetConfig(&Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: extra})
		require.Equal(t, openAI, claude)
	}
	parent := int64(1)
	for _, a := range []*Account{
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent},
		{Platform: PlatformGemini, Type: AccountTypeOAuth},
	} {
		require.False(t, ResolveOpenAIAutoResetCreditConfig(a).Enabled)
	}
	for _, a := range []*Account{
		{Platform: PlatformAnthropic, Type: AccountTypeAPIKey},
		{Platform: PlatformAnthropic, Type: AccountTypeOAuth, ParentAccountID: &parent},
	} {
		require.False(t, resolveClaudeAutoResetConfig(a).Enabled)
	}
}

func TestResetGlobalPolicyIsPerProvider(t *testing.T) {
	installResetPolicies(t, ownerResetPolicy(), notEnforcedResetPolicy())
	require.True(t, ResolveOpenAIAutoResetCreditConfig(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}).Enforced)
	claude := resolveClaudeAutoResetConfig(&Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth})
	require.False(t, claude.Enforced)
	require.False(t, claude.Enabled)

	installResetPolicies(t, notEnforcedResetPolicy(), ownerResetPolicy())
	require.False(t, ResolveOpenAIAutoResetCreditConfig(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}).Enabled)
	require.True(t, resolveClaudeAutoResetConfig(&Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}).Enforced)
}

func TestResetGlobalPolicyEnforcedWithoutTriggersDisablesResets(t *testing.T) {
	p := ownerResetPolicy()
	p.Expiry.Enabled = false
	p.Exhaustion.Enabled = false
	installResetPolicies(t, p, p)
	extra, err := normalizeOpenAIAutoResetCreditExtra(PlatformOpenAI, AccountTypeOAuth, false, map[string]any{OpenAIAutoResetCreditEnabledExtraKey: true, OpenAIAutoResetCreditModeExtraKey: OpenAIAutoResetModeExhausted})
	require.NoError(t, err)
	cfg := ResolveOpenAIAutoResetCreditConfig(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra})
	require.True(t, cfg.Enforced)
	require.False(t, cfg.Enabled)
}

func TestResetGlobalPolicyNotEnforcedKeepsLegacyBehaviour(t *testing.T) {
	extra, err := normalizeOpenAIAutoResetCreditExtra(PlatformOpenAI, AccountTypeOAuth, false, map[string]any{OpenAIAutoResetCreditEnabledExtraKey: true, OpenAIAutoResetCreditModeExtraKey: OpenAIAutoResetModeExpiringOrExhausted})
	require.NoError(t, err)
	claudeExtra, err := normalizeOpenAIAutoResetCreditExtra(PlatformAnthropic, AccountTypeOAuth, false, map[string]any{"claude_auto_reset_credit_enabled": true, "claude_auto_reset_credit_mode": "exhausted"})
	require.NoError(t, err)
	accounts := []*Account{
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra},
	}
	claudeAccounts := []*Account{
		{Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: claudeExtra},
	}
	baseline := func() ([]OpenAIAutoResetCreditConfig, []OpenAIAutoResetCreditConfig) {
		var o, c []OpenAIAutoResetCreditConfig
		for _, a := range accounts {
			o = append(o, ResolveOpenAIAutoResetCreditConfig(a))
		}
		for _, a := range claudeAccounts {
			c = append(c, resolveClaudeAutoResetConfig(a))
		}
		return o, c
	}
	SetResetCreditGlobalPolicies(nil)
	wantOpenAI, wantClaude := baseline()
	installResetPolicies(t, notEnforcedResetPolicy(), notEnforcedResetPolicy())
	gotOpenAI, gotClaude := baseline()
	require.Equal(t, wantOpenAI, gotOpenAI)
	require.Equal(t, wantClaude, gotClaude)
	for _, c := range append(gotOpenAI, gotClaude...) {
		require.False(t, c.Enforced)
	}

	// Legacy exhaustion stays at exactly 100%, including 5h-only exhaustion.
	now := time.Now()
	legacy := gotOpenAI[1]
	u := exhaustedUsage(now)
	require.True(t, openAIResetExhaustedFor(legacy, u, now))
	u.RateLimit.PrimaryWindow.UsedPercent = 99
	require.False(t, openAIResetExhaustedFor(legacy, u, now))
	cu := &ClaudeUsageResponse{}
	cu.SevenDay.Utilization = 99
	cu.SevenDay.ResetsAt = now.Add(time.Hour).Format(time.RFC3339)
	require.False(t, claudeUsageExhaustedFor(gotClaude[1], cu, "claude-opus-4-6", now))
	cu.FiveHour.Utilization = 100
	cu.FiveHour.ResetsAt = now.Add(time.Hour).Format(time.RFC3339)
	require.True(t, claudeUsageExhaustedFor(gotClaude[1], cu, "claude-opus-4-6", now))
	// Legacy expiry still requires a measured benefit.
	expiring := expiringOpenAIUsage(now, 0)
	require.False(t, assessExpiringUsefulReset(expiring, accounts[1], OpenAIAutoResetCreditConfig{Enabled: true, ExpiryHorizonSeconds: 7200, ExpiryMinUtilization: 0}, now))
}

func expiringOpenAIUsage(now time.Time, usedPercent float64) *OpenAIQuotaUsage {
	return &OpenAIQuotaUsage{
		FetchedAt:             now.Unix(),
		RateLimit:             &OpenAIRateLimit{PrimaryWindow: &OpenAIRateLimitWindow{UsedPercent: usedPercent, LimitWindowSeconds: 5 * 3600, ResetAt: now.Add(time.Hour).Unix()}},
		RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1},
		autoResetCandidates:   []openAIAutoResetCreditCandidate{{ID: "credit-a", ExpiresAt: now.Add(90 * time.Minute).Format(time.RFC3339)}},
	}
}

func TestResetGlobalPolicyMinUtilizationZeroIsUnconditional(t *testing.T) {
	installResetPolicies(t, ownerResetPolicy(), ownerResetPolicy())
	now := time.Now()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	cfg := ResolveOpenAIAutoResetCreditConfig(account)
	require.True(t, assessExpiringUsefulReset(expiringOpenAIUsage(now, 0), account, cfg, now))
	// Still bounded by the horizon.
	late := expiringOpenAIUsage(now, 0)
	late.autoResetCandidates[0].ExpiresAt = now.Add(3 * time.Hour).Format(time.RFC3339)
	require.False(t, assessExpiringUsefulReset(late, account, cfg, now))
	// A positive minimum keeps requiring usage.
	cfg.ExpiryMinUtilization = 0.5
	require.False(t, assessExpiringUsefulReset(expiringOpenAIUsage(now, 10), account, cfg, now))
	require.True(t, assessExpiringUsefulReset(expiringOpenAIUsage(now, 60), account, cfg, now))

	claudeCfg := resolveClaudeAutoResetConfig(&Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth})
	expiry := now.Add(90 * time.Minute)
	credit := ClaudeResetCredit{Redeemable: true, ExpiresAt: &expiry, Clears: []string{"five_hour"}, PercentUsed: map[string]float64{"five_hour": 0}}
	require.True(t, claudeExpiringUseful(credit, claudeCfg, now))
	credit.PercentUsed = nil
	require.True(t, claudeExpiringUseful(credit, claudeCfg, now))
	later := now.Add(3 * time.Hour)
	credit.ExpiresAt = &later
	require.False(t, claudeExpiringUseful(credit, claudeCfg, now))
	credit.ExpiresAt = &expiry
	legacy := claudeCfg
	legacy.Enforced = false
	require.False(t, claudeExpiringUseful(credit, legacy, now))
}

func weeklyUsage(now time.Time, weekly, fiveHour float64) *OpenAIQuotaUsage {
	return &OpenAIQuotaUsage{FetchedAt: now.Unix(), RateLimit: &OpenAIRateLimit{
		PrimaryWindow:   &OpenAIRateLimitWindow{UsedPercent: fiveHour, LimitWindowSeconds: 18000, ResetAfterSeconds: 3600},
		SecondaryWindow: &OpenAIRateLimitWindow{UsedPercent: weekly, LimitWindowSeconds: 604800, ResetAfterSeconds: 86400},
	}}
}

func TestResetGlobalPolicyCohortThreshold(t *testing.T) {
	installResetPolicies(t, ownerResetPolicy(), ownerResetPolicy())
	now := time.Now()
	g := int64(7)
	target := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, GroupIDs: []int64{g}}
	peer := target
	peer.ID = 2
	cfg := ResolveOpenAIAutoResetCreditConfig(&target)
	tests := []struct {
		name         string
		target, peer *OpenAIQuotaUsage
		want         bool
	}{
		{"all at or above 95% weekly", weeklyUsage(now, 95, 10), weeklyUsage(now, 99, 0), true},
		{"peer below threshold", weeklyUsage(now, 97, 10), weeklyUsage(now, 94.9, 0), false},
		{"target below threshold", weeklyUsage(now, 90, 10), weeklyUsage(now, 99, 0), false},
		{"5h exhaustion ignored for 7d-only policy", weeklyUsage(now, 10, 100), weeklyUsage(now, 10, 100), false},
		{"peer only 5h exhausted", weeklyUsage(now, 96, 0), weeklyUsage(now, 10, 100), false},
		{"peer quota unknown", weeklyUsage(now, 96, 0), nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &OpenAIQuotaAutoResetService{accountRepo: &cohortRepo{accounts: []Account{target, peer}}, quota: &cohortQuota{usages: map[int64]*OpenAIQuotaUsage{2: tt.peer}}}
			s.scopes.Store(int64(1), openAIResetScope{request: OpenAIAccountScheduleRequest{GroupID: &g, Platform: PlatformOpenAI, RequestedModel: "gpt-5", RequiredTransport: OpenAIUpstreamTransportAny}, expires: now.Add(time.Minute)})
			require.Equal(t, tt.want, s.exhaustedCohort(context.Background(), &target, cfg, tt.target, now))
		})
	}
	// Stale or future snapshots fail closed under the policy as well.
	stale := weeklyUsage(now, 99, 0)
	stale.FetchedAt = now.Add(-24 * time.Hour).Unix()
	require.False(t, openAIResetExhaustedFor(cfg, stale, now))
	elapsed := weeklyUsage(now, 99, 0)
	elapsed.RateLimit.SecondaryWindow.ResetAfterSeconds = 0
	require.False(t, openAIResetExhaustedFor(cfg, elapsed, now))
}

func TestResetGlobalPolicyWindowSelection(t *testing.T) {
	now := time.Now()
	both := ownerResetPolicy()
	both.Exhaustion.Windows = []string{"5h", "7d"}
	installResetPolicies(t, both, both)
	cfg := ResolveOpenAIAutoResetCreditConfig(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	require.True(t, openAIResetExhaustedFor(cfg, weeklyUsage(now, 10, 96), now))
	require.True(t, openAIResetExhaustedFor(cfg, weeklyUsage(now, 96, 10), now))

	installResetPolicies(t, ownerResetPolicy(), ownerResetPolicy())
	cfg = ResolveOpenAIAutoResetCreditConfig(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	require.False(t, openAIResetExhaustedFor(cfg, weeklyUsage(now, 10, 100), now))
	require.True(t, openAIResetExhaustedFor(cfg, weeklyUsage(now, 95, 10), now))
}

func claudeUsageAt(now time.Time, fiveHour, sevenDay, sonnet float64) *ClaudeUsageResponse {
	u := &ClaudeUsageResponse{}
	u.FiveHour.Utilization = fiveHour
	u.FiveHour.ResetsAt = now.Add(time.Hour).Format(time.RFC3339)
	u.SevenDay.Utilization = sevenDay
	u.SevenDay.ResetsAt = now.Add(48 * time.Hour).Format(time.RFC3339)
	u.SevenDaySonnet.Utilization = sonnet
	u.SevenDaySonnet.ResetsAt = now.Add(48 * time.Hour).Format(time.RFC3339)
	return u
}

func TestResetGlobalPolicyClaudeExhaustion(t *testing.T) {
	installResetPolicies(t, notEnforcedResetPolicy(), ownerResetPolicy())
	now := time.Now()
	cfg := resolveClaudeAutoResetConfig(&Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth})
	require.True(t, cfg.Enforced)

	require.False(t, claudeUsageExhaustedFor(cfg, claudeUsageAt(now, 100, 10, 10), "claude-opus-4-6", now), "5h ignored for 7d-only policy")
	require.True(t, claudeUsageExhaustedFor(cfg, claudeUsageAt(now, 0, 95, 0), "claude-opus-4-6", now))
	require.False(t, claudeUsageExhaustedFor(cfg, claudeUsageAt(now, 0, 94, 0), "claude-opus-4-6", now))
	// The 7-day Sonnet window is 7d-class, only for Sonnet models.
	require.True(t, claudeUsageExhaustedFor(cfg, claudeUsageAt(now, 0, 10, 96), "claude-sonnet-4-6", now))
	require.False(t, claudeUsageExhaustedFor(cfg, claudeUsageAt(now, 0, 10, 96), "claude-opus-4-6", now))
	expired := claudeUsageAt(now, 0, 99, 0)
	expired.SevenDay.ResetsAt = now.Add(-time.Minute).Format(time.RFC3339)
	require.False(t, claudeUsageExhaustedFor(cfg, expired, "claude-opus-4-6", now))
	require.False(t, claudeUsageExhaustedFor(cfg, nil, "claude-opus-4-6", now))

	// The credit must clear a configured window above the threshold.
	credit := ClaudeResetCredit{Clears: []string{"five_hour"}, PercentUsed: map[string]float64{"five_hour": 100}}
	require.False(t, claudeCreditUsefulForExhaustion(cfg, credit, "claude-opus-4-6"))
	credit = ClaudeResetCredit{Clears: []string{"seven_day"}, PercentUsed: map[string]float64{"seven_day": 96}}
	require.True(t, claudeCreditUsefulForExhaustion(cfg, credit, "claude-opus-4-6"))
	credit.PercentUsed["seven_day"] = 90
	require.False(t, claudeCreditUsefulForExhaustion(cfg, credit, "claude-opus-4-6"))
	credit = ClaudeResetCredit{Clears: []string{"seven_day_sonnet"}, PercentUsed: map[string]float64{"seven_day_sonnet": 97}}
	require.True(t, claudeCreditUsefulForExhaustion(cfg, credit, "claude-sonnet-4-6"))
	require.False(t, claudeCreditUsefulForExhaustion(cfg, credit, "claude-opus-4-6"))
	legacy := cfg
	legacy.Enforced = false
	credit = ClaudeResetCredit{Clears: []string{"seven_day"}, PercentUsed: map[string]float64{"seven_day": 96}}
	require.False(t, claudeCreditUsefulForExhaustion(legacy, credit, "claude-opus-4-6"))
	credit.PercentUsed["seven_day"] = 100
	require.True(t, claudeCreditUsefulForExhaustion(legacy, credit, "claude-opus-4-6"))

	// A grant must still clear every window at its limit and at least one
	// configured window over the threshold.
	u := claudeUsageAt(now, 100, 96, 0)
	require.False(t, claudeGrantClearsAllBlocksFor(cfg, u, []string{"seven_day"}, "claude-opus-4-6", now))
	require.True(t, claudeGrantClearsAllBlocksFor(cfg, u, []string{"five_hour", "seven_day"}, "claude-opus-4-6", now))
	require.False(t, claudeGrantClearsAllBlocksFor(cfg, u, []string{"five_hour"}, "claude-opus-4-6", now))
	require.True(t, claudeGrantClearsAllBlocksFor(cfg, claudeUsageAt(now, 20, 96, 0), []string{"seven_day"}, "claude-opus-4-6", now))
	require.False(t, claudeGrantClearsAllBlocks(claudeUsageAt(now, 20, 96, 0), []string{"seven_day"}, "claude-opus-4-6", now), "legacy rule unchanged")
	require.False(t, claudeGrantClearsAllBlocksFor(legacy, claudeUsageAt(now, 20, 96, 0), []string{"seven_day"}, "claude-opus-4-6", now))
}

func TestResetGlobalPolicySchedulerSoftNotifyWithoutPause(t *testing.T) {
	installResetPolicies(t, ownerResetPolicy(), ownerResetPolicy())
	openAIResetSoftNotify.Delete(int64(41))
	t.Cleanup(func() { openAIResetSoftNotify.Delete(int64(41)) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &OpenAIQuotaAutoResetService{ctx: ctx, queue: make(chan int64, 4)}
	setOpenAIAutoResetNotifier(worker)
	t.Cleanup(func() { clearOpenAIAutoResetNotifier(worker) })

	now := time.Now()
	account := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		"codex_7d_used_percent":  96.0,
		"codex_7d_reset_at":      now.Add(24 * time.Hour).Format(time.RFC3339),
		"codex_usage_updated_at": now.Format(time.RFC3339),
	}}
	paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
	require.False(t, paused, "an account with capacity left must stay schedulable")
	select {
	case id := <-worker.queue:
		require.Equal(t, int64(41), id)
	default:
		t.Fatal("expected a cohort check notification at the enforced threshold")
	}
	worker.pending.Delete(int64(41))
	paused, _ = shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
	require.False(t, paused)
	require.Len(t, worker.queue, 0, "soft notifications are rate limited")

	// A 5h-only value over the threshold does not notify a 7d-only policy.
	openAIResetSoftNotify.Delete(int64(41))
	account.Extra = map[string]any{
		"codex_5h_used_percent":  97.0,
		"codex_5h_reset_at":      now.Add(time.Hour).Format(time.RFC3339),
		"codex_usage_updated_at": now.Format(time.RFC3339),
	}
	paused, _ = shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
	require.False(t, paused)
	require.Len(t, worker.queue, 0)
}
