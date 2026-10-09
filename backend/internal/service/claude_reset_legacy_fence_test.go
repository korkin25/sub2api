package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestClaudeResetLegacyFenceUpgrade(t *testing.T) {
	sum := sha256.Sum256([]byte("grant_next:2:<nil>:<nil>"))
	selection := hex.EncodeToString(sum[:])
	for _, tc := range []struct {
		name, outcome, selection string
		blocked                  bool
	}{
		{"unknown", "unknown", selection, true},
		{"empty outcome", "", selection, true},
		{"corrupt", "corrupt", selection, true},
		{"invalid selection", "reset", "bad", true},
		{"unknown future outcome", "future", selection, true},
		{"prepared without response", "prepared", selection, true},
		{"same consumed selection", "reset", selection, true},
		{"same already used selection", "already_used", selection, true},
		{"different consumed selection", "reset", hex.EncodeToString(make([]byte, 32)), false},
		{"definite cooldown", "cooldown", selection, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &redeemFake{claim: `{"result":"reset"}`}
			s, repo, _ := newRedeemService(t, f)
			ctx := context.Background()
			orgHash := HashIdempotencyKey("claude-org:" + redeemTestOrg)
			row := &IdempotencyRecord{Scope: claudeLegacyResetFenceScope, IdempotencyKeyHash: orgHash, RequestFingerprint: orgHash, Status: IdempotencyStatusProcessing, ExpiresAt: time.Now().AddDate(100, 0, 0)}
			_, err := repo.CreateProcessing(ctx, row)
			require.NoError(t, err)
			body := fmt.Sprintf(`{"selection":%q,"outcome":%q}`, tc.selection, tc.outcome)
			if tc.outcome == "corrupt" {
				body = "{"
			}
			if tc.outcome != "prepared" {
				stored, err := repo.GetByScopeAndKeyHash(ctx, claudeLegacyResetFenceScope, orgHash)
				require.NoError(t, err)
				require.NoError(t, repo.MarkSucceeded(ctx, stored.ID, 200, body, row.ExpiresAt))
			}
			_, err = s.Redeem(ctx, 1, "after-upgrade-1")
			if tc.blocked {
				require.Error(t, err)
				_, err = s.Redeem(ctx, 2, "duplicate-account")
				require.Error(t, err)
				require.Zero(t, f.postCount())
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, f.postCount())
			}
			stored, err := repo.GetByScopeAndKeyHash(ctx, claudeLegacyResetFenceScope, orgHash)
			require.NoError(t, err)
			if tc.outcome != "prepared" {
				require.Equal(t, body, *stored.ResponseBody)
			}
		})
	}
}
