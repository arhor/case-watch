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

func (s *Service) saveEnrollment(ctx context.Context, owner int64, id string, c domain.Credentials, o domain.Observation) error {
	return s.db.RunInTx(ctx, func(tx *repositories.Store) error {
		return tx.Enrollments.Create(ctx, owner, id, c, o)
	})
}

func (s *Service) saveObservation(ctx context.Context, owner int64, id string, o domain.Observation) error {
	return s.db.RunInTx(ctx, func(tx *repositories.Store) error {
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
		return tx.History.Append(ctx, id, e.Observation.Stage, o)
	})
}

func (s *Service) deleteEnrollment(ctx context.Context, owner int64, id string) error {
	return s.db.RunInTx(ctx, func(tx *repositories.Store) error {
		if err := tx.Enrollments.SoftDelete(ctx, owner, id, time.Now().UTC()); err != nil {
			return err
		}
		if err := tx.History.DeleteByEnrollmentID(ctx, id); err != nil {
			return err
		}
		return tx.Cooldowns.DeleteBySubjectTypeAndSubjectID(ctx, caseCheckCooldown, id)
	})
}

func eligibility(ctx context.Context, tx *repositories.Store, owner int64, id string, manual bool) (time.Time, error) {
	if _, err := tx.Enrollments.FindByIdentity(ctx, owner, id); err != nil {
		return time.Time{}, err
	}
	c, err := tx.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, caseCheckCooldown, id)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return time.Time{}, err
	}
	caseStart := c.StartedAt
	if manual {
		c, err = tx.Cooldowns.FindBySubjectTypeAndSubjectID(ctx, manualCheckCooldown, strconv.FormatInt(owner, 10))
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

func (s *Service) checkEligibility(ctx context.Context, owner int64, id string, manual bool) (time.Time, error) {
	var next time.Time
	err := s.db.RunInTx(ctx, func(tx *repositories.Store) error {
		var err error
		next, err = eligibility(ctx, tx, owner, id, manual)
		return err
	})
	return next, err
}

func (s *Service) reserveCheck(ctx context.Context, owner int64, id string, manual bool, now time.Time) (repositories.CheckReservation, error) {
	if now.IsZero() {
		return repositories.CheckReservation{}, repositories.ErrInvalid
	}
	var result repositories.CheckReservation
	err := s.db.RunInTx(ctx, func(tx *repositories.Store) error {
		next, err := eligibility(ctx, tx, owner, id, manual)
		if err != nil {
			return err
		}
		result.NextEligibleAt = next
		if now.Before(next) {
			return nil
		}
		if err = tx.Cooldowns.Upsert(ctx,
			repositories.Cooldown{
				SubjectType: caseCheckCooldown,
				SubjectID:   id,
				StartedAt:   now,
			},
		); err != nil {
			return err
		}
		if manual {
			if err = tx.Cooldowns.Upsert(ctx, repositories.Cooldown{
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
