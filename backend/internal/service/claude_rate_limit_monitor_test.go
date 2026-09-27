package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func recoveredClaudeUsage(now time.Time) *ClaudeUsageResponse {
	u := &ClaudeUsageResponse{}
	u.FiveHour.ResetsAt = now.Add(4 * time.Hour).UTC().Format(time.RFC3339)
	u.SevenDay.ResetsAt = now.Add(4 * 24 * time.Hour).UTC().Format(time.RFC3339)
	return u
}

func TestClaudeUsageAllowsCooldownClear(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name  string
		edit  func(*ClaudeUsageResponse)
		reset time.Time
		want  bool
	}{
		{"healthy 5h cooldown", nil, now.Add(4 * time.Hour), true},
		{"healthy 7d cooldown", nil, now.Add(4 * 24 * time.Hour), true},
		{"unrelated 429", nil, now.Add(2 * time.Hour), false},
		{"missing 5h", func(u *ClaudeUsageResponse) { u.FiveHour.ResetsAt = "" }, now.Add(4 * 24 * time.Hour), false},
		{"missing 7d", func(u *ClaudeUsageResponse) { u.SevenDay.ResetsAt = "" }, now.Add(4 * time.Hour), false},
		{"malformed reset", func(u *ClaudeUsageResponse) { u.SevenDay.ResetsAt = "bad" }, now.Add(4 * time.Hour), false},
		{"exhausted 5h", func(u *ClaudeUsageResponse) { u.FiveHour.Utilization = 100 }, now.Add(4 * time.Hour), false},
		{"exhausted 7d", func(u *ClaudeUsageResponse) { u.SevenDay.Utilization = 100 }, now.Add(4 * time.Hour), false},
		{"invalid utilization", func(u *ClaudeUsageResponse) { u.FiveHour.Utilization = math.NaN() }, now.Add(4 * time.Hour), false},
		{"model window independent", func(u *ClaudeUsageResponse) { u.SevenDaySonnet.Utilization = 100 }, now.Add(4 * time.Hour), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := recoveredClaudeUsage(now)
			if tc.edit != nil {
				tc.edit(u)
			}
			require.Equal(t, tc.want, claudeUsageAllowsCooldownClear(u, tc.reset, now))
		})
	}
	require.False(t, claudeUsageAllowsCooldownClear(nil, now.Add(4*time.Hour), now))
}

type claudeRateMonitorRepo struct {
	*claudeAutoIntegrationRepo
	clearCalls        int
	getCalls          int
	changeOnSecondGet bool
}

func (r *claudeRateMonitorRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	r.getCalls++
	if r.changeOnSecondGet && r.getCalls == 2 {
		newLimit := *r.account.RateLimitResetAt
		newLimit = newLimit.Add(time.Hour)
		r.account.RateLimitResetAt = &newLimit
		r.account.UpdatedAt = r.account.UpdatedAt.Add(time.Second)
	}
	copy := *r.account
	return &copy, nil
}

func (r *claudeRateMonitorRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	r.account.UpdatedAt = r.account.UpdatedAt.Add(time.Nanosecond)
	return nil
}

func (r *claudeRateMonitorRepo) ClearClaudeRateLimitIfUnchanged(_ context.Context, _ int64, updatedAt, limitedAt, resetAt time.Time) (bool, error) {
	r.clearCalls++
	if !r.account.UpdatedAt.Equal(updatedAt) || !r.account.RateLimitedAt.Equal(limitedAt) || !r.account.RateLimitResetAt.Equal(resetAt) {
		return false, nil
	}
	r.account.RateLimitedAt = nil
	r.account.RateLimitResetAt = nil
	r.account.UpdatedAt = r.account.UpdatedAt.Add(time.Nanosecond)
	return true, nil
}

func TestClaudeRateLimitMonitorScansWithoutAutoResetAndThrottles(t *testing.T) {
	w, _ := newClaudeAutoIntegration(t, false, 0)
	base, ok := w.accounts.(*claudeAutoIntegrationRepo)
	require.True(t, ok)
	repo := &claudeRateMonitorRepo{claudeAutoIntegrationRepo: base}
	w.accounts, w.service.accounts = repo, repo
	now := time.Now().UTC().Truncate(time.Second)
	limited, reset := now.Add(-time.Minute), now.Add(4*time.Hour)
	repo.account.RateLimitedAt, repo.account.RateLimitResetAt = &limited, &reset
	repo.account.UpdatedAt = now
	repo.account.Schedulable = false
	w.fetcher = claudeRecoveryUsage{usage: recoveredClaudeUsage(now)}
	w.scan(context.Background())
	require.Equal(t, 1, repo.clearCalls)
	require.Nil(t, repo.account.RateLimitResetAt)
	require.False(t, repo.account.Schedulable)
	require.NotEmpty(t, repo.account.Extra[claudeRateLimitCheckKey])

	repo.account.RateLimitedAt, repo.account.RateLimitResetAt = &limited, &reset
	w.scan(context.Background())
	require.Equal(t, 1, repo.clearCalls)
}

func TestClaudeRateLimitMonitorPreservesConcurrent429(t *testing.T) {
	w, _ := newClaudeAutoIntegration(t, false, 0)
	base, ok := w.accounts.(*claudeAutoIntegrationRepo)
	require.True(t, ok)
	repo := &claudeRateMonitorRepo{claudeAutoIntegrationRepo: base, changeOnSecondGet: true}
	w.accounts, w.service.accounts = repo, repo
	now := time.Now().UTC().Truncate(time.Second)
	limited, reset := now.Add(-time.Minute), now.Add(4*time.Hour)
	repo.account.RateLimitedAt, repo.account.RateLimitResetAt = &limited, &reset
	repo.account.UpdatedAt = now
	w.fetcher = claudeRecoveryUsage{usage: recoveredClaudeUsage(now)}
	w.scan(context.Background())
	require.Zero(t, repo.clearCalls)
	require.Equal(t, reset.Add(time.Hour), *repo.account.RateLimitResetAt)
}
