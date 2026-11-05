package notifications

import (
	"context"
	"time"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/repositories"
)

// Persistence owns delivery transactions and retry policy. Sending happens only
// after reservation commits, and before the separate completion transaction.
type Persistence struct{ DB *repositories.Store }

var _ Store = Persistence{}

func (p Persistence) DeliveryHeads(ctx context.Context) ([]repositories.Delivery, error) {
	return p.DB.Pending.FindUndeliveredHeads(ctx)
}
func (p Persistence) RecipientState(ctx context.Context, recipient int64) (repositories.RecipientState, error) {
	return p.DB.Recipients.GetState(ctx, recipient)
}
func (p Persistence) ReserveDelivery(ctx context.Context, recipient int64, now time.Time) (repositories.Delivery, error) {
	if recipient <= 0 || now.IsZero() {
		return repositories.Delivery{}, repositories.ErrInvalid
	}
	var result repositories.Delivery
	err := p.DB.RunInTx(ctx, func(tx *repositories.Store) error {
		r, err := tx.Recipients.GetState(ctx, recipient)
		if err != nil {
			return err
		}
		if r.Pause != "" || r.Blocked {
			return repositories.ErrNotEligible
		}
		d, err := tx.Pending.FindUndeliveredHeadByRecipient(ctx, recipient)
		if err != nil {
			return err
		}
		if d.Status == "reserved" || d.CycleAttempts >= 6 || now.Before(d.NextAttemptAt) {
			return repositories.ErrNotEligible
		}
		if err = tx.Pending.ReserveAttempt(ctx, d, now); err != nil {
			return err
		}
		d.Status = "reserved"
		d.CycleAttempts++
		d.LifetimeAttempts++
		d.ReservedAt = now
		result = d
		return nil
	})
	if err != nil {
		return repositories.Delivery{}, err
	}
	return result, nil
}
func validOutcome(out domain.SendOutcome, now time.Time) bool {
	if now.IsZero() || out.RetryAfter < 0 {
		return false
	}
	switch out.Category {
	case domain.SendAccepted:
		return out.MessageID > 0
	case domain.SendTransient, domain.SendRateLimited, domain.SendBlocked, domain.SendInvalidToken, domain.SendPermanent:
		return true
	default:
		return false
	}
}
func (p Persistence) CompleteDelivery(ctx context.Context, d repositories.Delivery, out domain.SendOutcome, now time.Time) error {
	if !validOutcome(out, now) {
		return repositories.ErrInvalid
	}
	return p.DB.RunInTx(ctx, func(tx *repositories.Store) error {
		current, err := tx.Pending.FindReservedAttempt(ctx, d)
		if err != nil {
			return err
		}
		// ADR 0012: explicit rejection cannot have accepted the message.
		if out.Category == domain.SendInvalidToken {
			return tx.Pending.ReleaseReservation(ctx, current)
		}
		return finishDelivery(ctx, tx, current, out, now, false)
	})
}
func finishDelivery(ctx context.Context, tx *repositories.Store, d repositories.Delivery, out domain.SendOutcome, now time.Time, uncertain bool) error {
	status, failure, pause := "pending", string(out.Category), ""
	var next time.Time
	if out.Category == domain.SendAccepted {
		status = "delivered"
		failure = ""
	} else {
		delay := 30 * time.Second * time.Duration(1<<uint(d.CycleAttempts-1))
		if out.RetryAfter > delay {
			delay = out.RetryAfter
		}
		next = now.Add(delay)
		switch out.Category {
		case domain.SendBlocked:
			pause = "blocked"
		case domain.SendPermanent:
			pause = "permanent"
		}
		if d.CycleAttempts >= 6 && pause == "" {
			pause = "exhausted"
		}
		out.MessageID = 0
	}
	if pause != "" {
		r, err := tx.Recipients.GetState(ctx, d.RecipientID)
		if err != nil {
			return err
		}
		r.Pause = pause
		r.Blocked = r.Blocked || pause == "blocked"
		if err = tx.Recipients.SetState(ctx, d.RecipientID, r); err != nil {
			return err
		}
	}
	if uncertain {
		failure = "uncertain"
	}
	return tx.Pending.FinishAttempt(ctx, d, status, failure, next, out.MessageID)
}

// RecoverDeliveries is startup-only and consumes each interrupted reservation's
// existing attempt. Reserved materializes rows before the first write.
func (p Persistence) RecoverDeliveries(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		return repositories.ErrInvalid
	}
	return p.DB.RunInTx(ctx, func(tx *repositories.Store) error {
		work, err := tx.Pending.FindReserved(ctx)
		if err != nil {
			return err
		}
		for _, d := range work {
			if err = finishDelivery(ctx, tx, d, domain.SendOutcome{Category: domain.SendTransient}, now, true); err != nil {
				return err
			}
		}
		return nil
	})
}
func (p Persistence) ReleaseDelivery(ctx context.Context, d repositories.Delivery) error {
	return p.DB.Pending.ReleaseReservation(ctx, d)
}
func (p Persistence) SetBlocked(ctx context.Context, recipient int64, blocked bool) error {
	if blocked {
		return p.DB.Recipients.SetState(ctx, recipient, repositories.RecipientState{Pause: "blocked", Blocked: true})
	}
	return p.resume(ctx, recipient, true)
}
func (p Persistence) ResumeUnblocked(ctx context.Context, recipient int64) error {
	return p.resume(ctx, recipient, false)
}
func (p Persistence) resume(ctx context.Context, recipient int64, onlyBlocked bool) error {
	return p.DB.RunInTx(ctx, func(tx *repositories.Store) error {
		r, err := tx.Recipients.GetState(ctx, recipient)
		if err != nil {
			return err
		}
		if onlyBlocked && !r.Blocked {
			return nil
		}
		active, err := tx.Pending.HasActiveReservation(ctx, recipient)
		if err != nil {
			return err
		}
		if active {
			return repositories.ErrNotEligible
		}
		if err = tx.Recipients.SetState(ctx, recipient, repositories.RecipientState{}); err != nil {
			return err
		}
		return tx.Pending.ResetHeadForRetry(ctx, recipient)
	})
}
