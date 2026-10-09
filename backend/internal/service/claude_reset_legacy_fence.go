package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// The deployed fork stored organization fences under this historical name.
// They have no timestamp: an ambiguous outcome cannot inherit upstream's new
// time-based expiry. Retain them until explicitly reconciled; never rewrite them.
const claudeLegacyResetFenceScope = "claude_reset_account_fence"

func (s *ClaudeResetCreditService) checkLegacyResetFence(ctx context.Context, orgHash string, grant *claudeResetGrant) error {
	row, err := s.idempotency.repo.GetByScopeAndKeyHash(ctx, claudeLegacyResetFenceScope, orgHash)
	if err != nil {
		return ErrIdempotencyStoreUnavail
	}
	if row == nil {
		return nil
	}
	unresolved := func() error {
		return infraerrors.Conflict("CLAUDE_RESET_UNRESOLVED", "legacy reset requires reconciliation")
	}
	var prior struct {
		Selection string `json:"selection"`
		Outcome   string `json:"outcome"`
	}
	if row.ResponseBody == nil || json.Unmarshal([]byte(*row.ResponseBody), &prior) != nil {
		return unresolved()
	}
	selection, err := hex.DecodeString(prior.Selection)
	if err != nil || len(selection) != sha256.Size {
		return unresolved()
	}
	switch prior.Outcome {
	case "reset", "already_used":
		if grant != nil {
			// Preserve the old fence's protection against a stale status response
			// advertising the same already consumed grant/count/validity combination.
			sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%v:%v", grant.ID, grant.ResetsLeft, grant.StartsAt, grant.EndsAt)))
			if hex.EncodeToString(sum[:]) == prior.Selection {
				return unresolved()
			}
		}
		return nil
	case "not_limited", "cooldown", "ineligible":
		return nil
	default:
		return unresolved()
	}
}
