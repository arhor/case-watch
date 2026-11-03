package data

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// FindAttemptStartTimes returns persisted case and owner attempt times for an active identity.
func (this *CheckGuardRepository) FindAttemptStartTimes(ctx context.Context, owner int64, id string) (time.Time, time.Time, error) {
	if owner <= 0 || !validID(id) {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	var caseStart, userStart int64
	err := this.db.QueryRowContext(ctx, `
		SELECT COALESCE((
			       SELECT started
			         FROM case_guards
			        WHERE enrollment_id = e.id
		       ), 0),
		       COALESCE((
			       SELECT started
			         FROM manual_guards
			        WHERE owner_id = e.owner_id
		       ), 0)
		  FROM enrollments AS e
		 WHERE e.owner_id = ?
		   AND e.id = ?
		   AND e.deleted_at IS NULL
	`, owner, id).Scan(&caseStart, &userStart)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, time.Time{}, ErrStorage
	}
	return instant(caseStart), instant(userStart), nil
}

func (this *CheckGuardRepository) RecordCaseStart(ctx context.Context, id string, now time.Time) error {
	if !validID(id) || now.IsZero() {
		return ErrInvalid
	}
	return rowAffected(
		this.db.ExecContext(ctx, `
			INSERT INTO case_guards (enrollment_id, started)
			SELECT id, ?
			  FROM enrollments
			 WHERE id = ?
			   AND deleted_at IS NULL
			ON CONFLICT (enrollment_id)
			DO UPDATE SET started = excluded.started
		`, now.UnixNano(), id),
	)
}

func (this *CheckGuardRepository) RecordManualStart(ctx context.Context, owner int64, now time.Time) error {
	if owner <= 0 || now.IsZero() {
		return ErrInvalid
	}
	if _, err := this.db.ExecContext(ctx, `
		INSERT INTO manual_guards (owner_id, started)
		VALUES (?, ?)
		ON CONFLICT (owner_id)
		DO UPDATE SET started = excluded.started
	`, owner, now.UnixNano()); err != nil {
		return ErrStorage
	}
	return nil
}

func (this *CheckGuardRepository) DeleteByEnrollmentID(ctx context.Context, id string) error {
	if !validID(id) {
		return ErrInvalid
	}
	if _, err := this.db.ExecContext(ctx, `
		DELETE FROM case_guards
		WHERE enrollment_id = ?
	`, string(id)); err != nil {
		return ErrStorage
	}
	return nil
}
