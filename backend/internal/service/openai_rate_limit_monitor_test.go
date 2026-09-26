package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type openAIRateMonitorRepo struct {
	AccountRepository
	mu                sync.Mutex
	account           *Account
	clearCalls        int
	changeBeforeClear func(*Account)
}

func (r *openAIRateMonitorRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *r.account
	copy.Extra = cloneOpenAIAutoResetExtra(r.account.Extra)
	return &copy, nil
}

func (r *openAIRateMonitorRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account.Extra == nil {
		r.account.Extra = make(map[string]any)
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	r.account.UpdatedAt = r.account.UpdatedAt.Add(time.Nanosecond)
	return nil
}

func (r *openAIRateMonitorRepo) ClearOpenAIRateLimitIfUnchanged(_ context.Context, _ int64, updatedAt, limitedAt, resetAt time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearCalls++
	if r.changeBeforeClear != nil {
		r.changeBeforeClear(r.account)
	}
	if !r.account.UpdatedAt.Equal(updatedAt) || r.account.RateLimitedAt == nil || r.account.RateLimitResetAt == nil ||
		!r.account.RateLimitedAt.Equal(limitedAt) || !r.account.RateLimitResetAt.Equal(resetAt) {
		return false, nil
	}
	r.account.RateLimitedAt = nil
	r.account.RateLimitResetAt = nil
	return true, nil
}

func (r *openAIRateMonitorRepo) ListWithFilters(_ context.Context, _ pagination.PaginationParams, _, _, _, _ string, _ int64, _ string) ([]Account, *pagination.PaginationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []Account{*r.account}, nil, nil
}

type openAIRateMonitorQuota struct {
	openAIAutoResetQuota
	usage   *OpenAIQuotaUsage
	calls   int
	onQuery func()
}

func (q *openAIRateMonitorQuota) QueryUsage(_ context.Context, _ int64) (*OpenAIQuotaUsage, error) {
	q.calls++
	if q.onQuery != nil {
		q.onQuery()
	}
	return q.usage, nil
}

func recoveredOpenAIQuota(now time.Time) *OpenAIQuotaUsage {
	return &OpenAIQuotaUsage{
		FetchedAt: now.Unix(),
		RateLimit: &OpenAIRateLimit{
			Allowed:         true,
			PrimaryWindow:   &OpenAIRateLimitWindow{UsedPercent: 0, LimitWindowSeconds: 5 * 60 * 60},
			SecondaryWindow: &OpenAIRateLimitWindow{UsedPercent: 0, LimitWindowSeconds: 7 * 24 * 60 * 60},
		},
	}
}

func TestOpenAIQuotaAllowsRequests(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name string
		edit func(*OpenAIQuotaUsage)
		want bool
	}{
		{"both windows recovered", nil, true},
		{"single reported window", func(u *OpenAIQuotaUsage) { u.RateLimit.SecondaryWindow = nil }, true},
		{"upstream disallows", func(u *OpenAIQuotaUsage) { u.RateLimit.Allowed = false }, false},
		{"limit reached", func(u *OpenAIQuotaUsage) { u.RateLimit.LimitReached = true }, false},
		{"five hour exhausted", func(u *OpenAIQuotaUsage) { u.RateLimit.PrimaryWindow.UsedPercent = 100 }, false},
		{"seven day exhausted", func(u *OpenAIQuotaUsage) { u.RateLimit.SecondaryWindow.UsedPercent = 100 }, false},
		{"no windows", func(u *OpenAIQuotaUsage) { u.RateLimit.PrimaryWindow, u.RateLimit.SecondaryWindow = nil, nil }, false},
		{"unknown window length", func(u *OpenAIQuotaUsage) { u.RateLimit.PrimaryWindow.LimitWindowSeconds = 0 }, false},
		{"stale response", func(u *OpenAIQuotaUsage) { u.FetchedAt = now.Add(-time.Hour).Unix() }, false},
		{"missing response time", func(u *OpenAIQuotaUsage) { u.FetchedAt = 0 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := recoveredOpenAIQuota(now)
			if tc.edit != nil {
				tc.edit(usage)
			}
			require.Equal(t, tc.want, openAIQuotaAllowsRequests(usage, now))
		})
	}
}

func TestOpenAIRateLimitMonitorRecoversOnlyObservedCooldown(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	limitedAt, resetAt := now.Add(-time.Hour), now.Add(4*24*time.Hour)
	overload, temporary := now.Add(time.Hour), now.Add(2*time.Hour)
	repo := &openAIRateMonitorRepo{account: &Account{
		ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Schedulable: false, UpdatedAt: now, RateLimitedAt: &limitedAt, RateLimitResetAt: &resetAt,
		OverloadUntil: &overload, TempUnschedulableUntil: &temporary,
		Credentials: map[string]any{"chatgpt_account_id": "same-provider-account"},
	}}
	quota := &openAIRateMonitorQuota{usage: recoveredOpenAIQuota(now)}
	svc := NewOpenAIQuotaAutoResetService(repo, quota, nil, nil, nil, nil, nil)
	require.NoError(t, svc.evaluateAccount(context.Background(), 2))
	require.Equal(t, 1, quota.calls)
	require.Equal(t, 1, repo.clearCalls)
	require.Nil(t, repo.account.RateLimitedAt)
	require.Nil(t, repo.account.RateLimitResetAt)
	require.Equal(t, overload, *repo.account.OverloadUntil)
	require.Equal(t, temporary, *repo.account.TempUnschedulableUntil)
	require.NotEmpty(t, repo.account.Extra[openAIRateLimitCheckKey])
	require.Equal(t, openAIRateLimitGeneration(&Account{RateLimitedAt: &limitedAt, RateLimitResetAt: &resetAt}), repo.account.Extra[openAIRateLimitGenerationKey])
}

func TestOpenAIRateLimitMonitorRechecksNew429AfterOldQuotaResponse(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	limitedAt, resetAt := now.Add(-time.Hour), now.Add(4*24*time.Hour)
	repo := &openAIRateMonitorRepo{account: &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		UpdatedAt: now, RateLimitedAt: &limitedAt, RateLimitResetAt: &resetAt,
	}}
	rearmedAt, rearmedReset := now.Add(time.Second), now.Add(5*24*time.Hour)
	quota := &openAIRateMonitorQuota{usage: recoveredOpenAIQuota(now)}
	quota.onQuery = func() {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		repo.account.UpdatedAt = rearmedAt
		repo.account.RateLimitedAt = &rearmedAt
		repo.account.RateLimitResetAt = &rearmedReset
		quota.onQuery = nil
	}
	svc := NewOpenAIQuotaAutoResetService(repo, quota, nil, nil, nil, nil, nil)
	require.NoError(t, svc.evaluateAccount(context.Background(), 1))
	require.Equal(t, rearmedAt, *repo.account.RateLimitedAt)
	require.True(t, openAIRateLimitCheckStale(repo.account, time.Now()))

	require.NoError(t, svc.evaluateAccount(context.Background(), 1))
	require.Equal(t, 2, quota.calls)
	require.Nil(t, repo.account.RateLimitResetAt)
}

func TestOpenAIRateLimitMonitorKeepsConcurrent429(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	limitedAt, resetAt := now.Add(-time.Hour), now.Add(4*24*time.Hour)
	repo := &openAIRateMonitorRepo{account: &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		UpdatedAt: now, RateLimitedAt: &limitedAt, RateLimitResetAt: &resetAt,
	}}
	rearmedAt, rearmedReset := now.Add(time.Second), now.Add(5*24*time.Hour)
	repo.changeBeforeClear = func(account *Account) {
		account.UpdatedAt = rearmedAt
		account.RateLimitedAt = &rearmedAt
		account.RateLimitResetAt = &rearmedReset
	}
	quota := &openAIRateMonitorQuota{usage: recoveredOpenAIQuota(now)}
	svc := NewOpenAIQuotaAutoResetService(repo, quota, nil, nil, nil, nil, nil)
	require.NoError(t, svc.evaluateAccount(context.Background(), 1))
	require.Equal(t, 1, repo.clearCalls)
	require.Equal(t, rearmedAt, *repo.account.RateLimitedAt)
	require.Equal(t, rearmedReset, *repo.account.RateLimitResetAt)
}

func TestOpenAIRateLimitMonitorSelectsBlockedAccountWithoutResetCredits(t *testing.T) {
	now := time.Now().UTC()
	limitedAt, resetAt := now.Add(-time.Hour), now.Add(time.Hour)
	repo := &openAIRateMonitorRepo{account: &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Schedulable: false, RateLimitedAt: &limitedAt, RateLimitResetAt: &resetAt,
	}}
	svc := NewOpenAIQuotaAutoResetService(repo, nil, nil, nil, nil, nil, nil)
	svc.scanEnabledAccounts(context.Background())
	require.Len(t, svc.queue, 1)
	require.Equal(t, int64(1), <-svc.queue)
}
