package cases

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/repositories"
)

const (
	caseCheckCooldown   = "case_check"
	manualCheckCooldown = "manual_check"
)

func (this *Service) saveEnrollment(
	ctx context.Context,
	owner int64,
	id string,
	credentials domain.Credentials,
	observation domain.Observation,
) error {
	return this.db.RunInTx(ctx, func(store *repositories.Store) error {
		return store.Enrollments.Create(ctx, owner, id, credentials, observation)
	})
}

func (this *Service) saveObservation(
	ctx context.Context,
	owner int64,
	id string,
	observation domain.Observation,
) error {
	return this.db.RunInTx(ctx, func(store *repositories.Store) error {
		e, err := store.Enrollments.FindByIdentity(ctx, owner, id)
		if err != nil {
			return err
		}
		if err = store.Enrollments.UpdateObservation(ctx, owner, id, observation); err != nil {
			return err
		}
		if e.Observation.Stage == observation.Stage {
			return nil
		}
		return store.History.Append(ctx, id, e.Observation.Stage, observation)
	})
}

func (this *Service) deleteEnrollment(
	ctx context.Context,
	owner int64,
	id string,
) error {
	return this.db.RunInTx(ctx, func(store *repositories.Store) error {
		if err := store.Enrollments.SoftDelete(ctx, owner, id, time.Now().UTC()); err != nil {
			return err
		}
		if err := store.History.DeleteByEnrollmentID(ctx, id); err != nil {
			return err
		}
		return store.Cooldowns.DeleteBySubjectTypeAndSubjectID(ctx, caseCheckCooldown, id)
	})
}

func (this *Service) checkEligibility(
	ctx context.Context,
	owner int64,
	id string,
	manual bool,
) (time.Time, error) {
	var next time.Time
	err := this.db.RunInTx(ctx, func(store *repositories.Store) error {
		var err error
		next, err = eligibility(ctx, store, owner, id, manual)
		return err
	})
	return next, err
}

func (this *Service) reserveCheck(
	ctx context.Context,
	owner int64,
	id string,
	manual bool,
	now time.Time,
) (repositories.CheckReservation, error) {
	if now.IsZero() {
		return repositories.CheckReservation{}, repositories.ErrInvalid
	}
	var result repositories.CheckReservation
	err := this.db.RunInTx(ctx, func(store *repositories.Store) error {
		next, err := eligibility(ctx, store, owner, id, manual)
		if err != nil {
			return err
		}
		result.NextEligibleAt = next
		if now.Before(next) {
			return nil
		}
		if err = store.Cooldowns.Upsert(ctx,
			repositories.Cooldown{
				SubjectType: caseCheckCooldown,
				SubjectID:   id,
				StartedAt:   now,
			},
		); err != nil {
			return err
		}
		if manual {
			if err = store.Cooldowns.Upsert(ctx, repositories.Cooldown{
				SubjectType: manualCheckCooldown,
				SubjectID:   strconv.FormatInt(owner, 10),
				StartedAt:   now,
			}); err != nil {
				return err
			}
		}
		result = repositories.CheckReservation{
			Reserved:       true,
			NextEligibleAt: now.Add(time.Minute),
		}
		return nil
	})
	if err != nil {
		return repositories.CheckReservation{}, err
	}
	return result, nil
}

func eligibility(
	ctx context.Context,
	store *repositories.Store,
	owner int64,
	id string,
	manual bool,
) (time.Time, error) {
	if _, err := store.Enrollments.FindByIdentity(ctx, owner, id); err != nil {
		return time.Time{}, err
	}
	c, err := store.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, caseCheckCooldown, id)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return time.Time{}, err
	}
	caseStart := c.StartedAt
	if manual {
		c, err = store.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, manualCheckCooldown, strconv.FormatInt(owner, 10))
		if err != nil && !errors.Is(err, repositories.ErrNotFound) {
			return time.Time{}, err
		}
		if c.StartedAt.After(caseStart) {
			caseStart = c.StartedAt
		}
	}
	if caseStart.IsZero() {
		return time.Time{}, nil
	}
	return caseStart.Add(time.Minute), nil
}
