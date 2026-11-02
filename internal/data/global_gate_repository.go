package data

import (
	"context"
	"time"

	"github.com/arhor/case-watch/internal/application"
)

func (s *GlobalGateRepository) ResumeToken(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE delivery_gates SET token_paused=0"); err != nil {
		return ErrStorage
	}
	return nil
}

// CheckEligibility reads existing attempt-start guards without reserving work.
// Cached replies use this to explain eligibility without consuming an attempt.

// RecordGlobalOutcome applies direct-reply token and rate failures.
func (s *GlobalGateRepository) RecordGlobalOutcome(ctx context.Context, out application.SendOutcome, now time.Time) error {
	if now.IsZero() || out.RetryAfter < 0 {
		return ErrInvalid
	}
	switch out.Category {
	case application.SendInvalidToken:
		if _, err := s.db.ExecContext(ctx, "UPDATE delivery_gates SET token_paused=1"); err != nil {
			return ErrStorage
		}
	case application.SendRateLimited:
		if _, err := s.db.ExecContext(ctx, "UPDATE delivery_gates SET rate_until=MAX(rate_until,?)", now.Add(out.RetryAfter).UnixNano()); err != nil {
			return ErrStorage
		}
	}
	return nil
}

// ResumeUnblocked applies explicit authenticated block-to-unblock evidence even
// when the preceding blocked update was missed. Interaction fallback must use
// SetBlocked(false) instead. Lifetime counts and global gates remain intact.
