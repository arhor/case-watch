package tracking

import (
	"context"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/data"
)

// Persistence composes tracking's atomic workflows from granular repositories.
// Network requests belong to Coordinator and occur outside these transactions.
type Persistence struct{ DB *data.Store }

var _ Store = Persistence{}

func (p Persistence) Enrollment(ctx context.Context, owner int64) (data.Enrollment, error) {
	return p.DB.Enrollments.FindByOwner(ctx, owner)
}
func (p Persistence) Enroll(ctx context.Context, owner int64, c application.SealedCredentials, o application.Observation) error {
	return p.DB.RunInTx(ctx, func(tx *data.Store) error { return tx.Enrollments.Create(ctx, owner, c, o) })
}
func (p Persistence) Observe(ctx context.Context, owner int64, id string, o application.Observation) error {
	return p.DB.RunInTx(ctx, func(tx *data.Store) error {
		e, err := tx.Enrollments.FindByIdentity(ctx, owner, id)
		if err != nil {
			return err
		}
		if err = tx.Enrollments.UpdateObservation(ctx, owner, id, o); err != nil {
			return err
		}
		if e.Observation.Stage == o.Stage {
			return nil
		}
		sequence, err := tx.History.Append(ctx, id, e.Observation.Stage, o)
		if err != nil {
			return err
		}
		return tx.Pending.Create(ctx, sequence, owner)
	})
}
func (p Persistence) Remove(ctx context.Context, owner int64, id string) error {
	return p.DB.RunInTx(ctx, func(tx *data.Store) error {
		if err := tx.Enrollments.SoftDelete(ctx, owner, id, time.Now().UTC()); err != nil {
			return err
		}
		if err := tx.History.DeleteByEnrollmentID(ctx, id); err != nil {
			return err
		}
		return tx.Guards.DeleteByEnrollmentID(ctx, id)
	})
}
func eligibility(ctx context.Context, tx *data.Store, owner int64, id string, manual bool) (time.Time, error) {
	caseStart, userStart, err := tx.Guards.FindAttemptStartTimes(ctx, owner, id)
	if err != nil {
		return time.Time{}, err
	}
	if manual && userStart.After(caseStart) {
		caseStart = userStart
	}
	if caseStart.IsZero() {
		return time.Time{}, nil
	}
	return caseStart.Add(time.Minute), nil
}
func (p Persistence) CheckEligibility(ctx context.Context, owner int64, id string, manual bool) (time.Time, error) {
	return eligibility(ctx, p.DB, owner, id, manual)
}
func (p Persistence) ReserveCheck(ctx context.Context, owner int64, id string, manual bool, now time.Time) (data.CheckReservation, error) {
	if now.IsZero() {
		return data.CheckReservation{}, data.ErrInvalid
	}
	var result data.CheckReservation
	err := p.DB.RunInTx(ctx, func(tx *data.Store) error {
		next, err := eligibility(ctx, tx, owner, id, manual)
		if err != nil {
			return err
		}
		result.NextEligibleAt = next
		if now.Before(next) {
			return nil
		}
		if err = tx.Guards.RecordCaseStart(ctx, id, now); err != nil {
			return err
		}
		if manual {
			if err = tx.Guards.RecordManualStart(ctx, owner, now); err != nil {
				return err
			}
		}
		result = data.CheckReservation{Reserved: true, NextEligibleAt: now.Add(time.Minute)}
		return nil
	})
	if err != nil {
		return data.CheckReservation{}, err
	}
	return result, nil
}
