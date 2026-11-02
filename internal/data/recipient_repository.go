package data

import (
	"context"
)

func (s *RecipientRepository) DeliveryGates(ctx context.Context, recipient int64) (RecipientState, GlobalState, error) {
	if recipient <= 0 {
		return RecipientState{}, GlobalState{}, ErrInvalid
	}
	var r RecipientState
	var g GlobalState
	var rate int64
	if s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT pause FROM recipients WHERE recipient_id=?),''),COALESCE((SELECT blocked FROM recipients WHERE recipient_id=?),0),token_paused,rate_until FROM delivery_gates`, recipient, recipient).Scan(&r.Pause, &r.Blocked, &g.TokenPaused, &rate) != nil {
		return r, g, ErrStorage
	}
	g.RateUntil = instant(rate)
	return r, g, nil
}

// ResumeRecipient is explicit operator recovery. It preserves lifetime counts and
// refuses to invalidate a live reservation. Ordinary private interactions should
// call SetBlocked(false), which only clears evidence-backed block pauses.

func (s *RecipientRepository) ResumeRecipient(ctx context.Context, recipient int64) error {
	return s.resume(ctx, recipient, false)
}

func (s *RecipientRepository) resume(ctx context.Context, recipient int64, onlyBlocked bool) error {
	if recipient <= 0 {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	var blocked, active int
	if tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT blocked FROM recipients WHERE recipient_id=?),0),EXISTS(SELECT 1 FROM pending WHERE recipient_id=? AND status='reserved')", recipient, recipient).Scan(&blocked, &active) != nil {
		return ErrStorage
	}
	if onlyBlocked && blocked == 0 {
		return nil
	}
	if active != 0 {
		return ErrNotEligible
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO recipients(recipient_id) VALUES(?) ON CONFLICT(recipient_id) DO UPDATE SET pause='',blocked=0", recipient); err != nil {
		return ErrStorage
	}
	if _, err = tx.ExecContext(ctx, "UPDATE pending SET cycle_attempts=0,next_attempt=0,failure='' WHERE sequence=(SELECT sequence FROM pending WHERE recipient_id=? AND status<>'delivered' ORDER BY sequence LIMIT 1)", recipient); err != nil {
		return ErrStorage
	}
	if tx.Commit() != nil {
		return ErrStorage
	}
	return nil
}

func (s *RecipientRepository) SetBlocked(ctx context.Context, recipient int64, blocked bool) error {
	if recipient <= 0 {
		return ErrInvalid
	}
	if !blocked {
		return s.resume(ctx, recipient, true)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO recipients(recipient_id,pause,blocked) VALUES(?,'blocked',1) ON CONFLICT(recipient_id) DO UPDATE SET pause='blocked',blocked=1", recipient); err != nil {
		return ErrStorage
	}
	return nil
}

// ResumeToken is explicit maintenance after correcting credentials. Rate limits
// remain untouched and cannot be bypassed by token or recipient recovery.

func (s *RecipientRepository) ResumeUnblocked(ctx context.Context, recipient int64) error {
	return s.resume(ctx, recipient, false)
}
