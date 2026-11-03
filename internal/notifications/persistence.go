package notifications

import (
	"context"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/data"
)

// Persistence owns delivery transactions and retry policy. Sending happens only
// after reservation commits, and before the separate completion transaction.
type Persistence struct{ DB *data.Store }

var _ Store = Persistence{}

func (p Persistence) DeliveryHeads(ctx context.Context) ([]data.Delivery, error) {
	return p.DB.Pending.FindUndeliveredHeads(ctx)
}
func gates(ctx context.Context, s *data.Store, recipient int64) (data.RecipientState, data.GlobalState, error) {
	r, err := s.Recipients.GetState(ctx, recipient)
	if err != nil {
		return r, data.GlobalState{}, err
	}
	g, err := s.Gates.GetState(ctx)
	return r, g, err
}
func (p Persistence) DeliveryGates(ctx context.Context, recipient int64) (data.RecipientState, data.GlobalState, error) {
	var r data.RecipientState
	var g data.GlobalState
	err := p.DB.RunInTx(ctx, func(tx *data.Store) error {
		var err error
		r, g, err = gates(ctx, tx, recipient)
		return err
	})
	return r, g, err
}
func (p Persistence) ReserveDelivery(ctx context.Context, recipient int64, now time.Time) (data.Delivery, error) {
	if recipient <= 0 || now.IsZero() {
		return data.Delivery{}, data.ErrInvalid
	}
	var result data.Delivery
	err := p.DB.RunInTx(ctx, func(tx *data.Store) error {
		r, g, err := gates(ctx, tx, recipient)
		if err != nil {
			return err
		}
		if r.Pause != "" || r.Blocked || now.Before(g.RateUntil) {
			return data.ErrNotEligible
		}
		d, err := tx.Pending.FindUndeliveredHeadByRecipient(ctx, recipient)
		if err != nil {
			return err
		}
		if d.Status == "reserved" || d.CycleAttempts >= 6 || now.Before(d.NextAttemptAt) {
			return data.ErrNotEligible
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
		return data.Delivery{}, err
	}
	return result, nil
}
func validOutcome(out application.SendOutcome, now time.Time) bool {
	if now.IsZero() || out.RetryAfter < 0 {
		return false
	}
	switch out.Category {
	case application.SendAccepted:
		return out.MessageID > 0
	case application.SendTransient, application.SendRateLimited, application.SendBlocked, application.SendInvalidToken, application.SendPermanent:
		return true
	default:
		return false
	}
}
func (p Persistence) CompleteDelivery(ctx context.Context, d data.Delivery, out application.SendOutcome, now time.Time) error {
	if !validOutcome(out, now) {
		return data.ErrInvalid
	}
	return p.DB.RunInTx(ctx, func(tx *data.Store) error {
		current, err := tx.Pending.FindReservedAttempt(ctx, d)
		if err != nil {
			return err
		}
		// ADR 0012: explicit rejection cannot have accepted the message.
		if out.Category == application.SendInvalidToken {
			return tx.Pending.ReleaseReservation(ctx, current)
		}
		return finishDelivery(ctx, tx, current, out, now, false)
	})
}
func recordGlobal(ctx context.Context, tx *data.Store, out application.SendOutcome, now time.Time) error {
	switch out.Category {
	case application.SendRateLimited:
		return tx.Gates.ExtendRateUntil(ctx, now.Add(out.RetryAfter))
	}
	return nil
}
func finishDelivery(ctx context.Context, tx *data.Store, d data.Delivery, out application.SendOutcome, now time.Time, uncertain bool) error {
	status, failure, pause := "pending", string(out.Category), ""
	var next time.Time
	if out.Category == application.SendAccepted {
		status = "delivered"
		failure = ""
	} else {
		delay := 30 * time.Second * time.Duration(1<<uint(d.CycleAttempts-1))
		if out.RetryAfter > delay {
			delay = out.RetryAfter
		}
		next = now.Add(delay)
		switch out.Category {
		case application.SendBlocked:
			pause = "blocked"
		case application.SendPermanent:
			pause = "permanent"
		}
		if d.CycleAttempts >= 6 && pause == "" {
			pause = "exhausted"
		}
		if err := recordGlobal(ctx, tx, out, now); err != nil {
			return err
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
		return data.ErrInvalid
	}
	return p.DB.RunInTx(ctx, func(tx *data.Store) error {
		work, err := tx.Pending.FindReserved(ctx)
		if err != nil {
			return err
		}
		for _, d := range work {
			if err = finishDelivery(ctx, tx, d, application.SendOutcome{Category: application.SendTransient}, now, true); err != nil {
				return err
			}
		}
		return nil
	})
}
func (p Persistence) ReleaseDelivery(ctx context.Context, d data.Delivery) error {
	return p.DB.Pending.ReleaseReservation(ctx, d)
}
func (p Persistence) SetBlocked(ctx context.Context, recipient int64, blocked bool) error {
	if blocked {
		return p.DB.Recipients.SetState(ctx, recipient, data.RecipientState{Pause: "blocked", Blocked: true})
	}
	return p.resume(ctx, recipient, true)
}
func (p Persistence) ResumeUnblocked(ctx context.Context, recipient int64) error {
	return p.resume(ctx, recipient, false)
}
func (p Persistence) resume(ctx context.Context, recipient int64, onlyBlocked bool) error {
	return p.DB.RunInTx(ctx, func(tx *data.Store) error {
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
			return data.ErrNotEligible
		}
		if err = tx.Recipients.SetState(ctx, recipient, data.RecipientState{}); err != nil {
			return err
		}
		return tx.Pending.ResetHeadForRetry(ctx, recipient)
	})
}
func (p Persistence) RecordGlobalOutcome(ctx context.Context, out application.SendOutcome, now time.Time) error {
	if now.IsZero() || out.RetryAfter < 0 {
		return data.ErrInvalid
	}
	return recordGlobal(ctx, p.DB, out, now)
}
