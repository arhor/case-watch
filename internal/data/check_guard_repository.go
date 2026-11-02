package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/arhor/case-watch/internal/application"
)

// Starts returns persisted case and owner attempt times for an active identity.
func (s *CheckGuardRepository) Starts(ctx context.Context, owner int64, id application.EnrollmentID) (time.Time, time.Time, error) {
	if owner <= 0 || !validID(id) {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	var caseStart, userStart int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT started FROM case_guards WHERE enrollment_id=e.id),0),COALESCE((SELECT started FROM manual_guards WHERE owner_id=e.owner_id),0) FROM enrollments e WHERE e.owner_id=? AND e.id=? AND e.deleted_at IS NULL`, owner, string(id)).Scan(&caseStart, &userStart)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, time.Time{}, ErrStorage
	}
	return instant(caseStart), instant(userStart), nil
}
func (s *CheckGuardRepository) StartCase(ctx context.Context, id application.EnrollmentID, now time.Time) error {
	if !validID(id) || now.IsZero() {
		return ErrInvalid
	}
	return affected(s.db.ExecContext(ctx, "INSERT INTO case_guards(enrollment_id,started) SELECT id,? FROM enrollments WHERE id=? AND deleted_at IS NULL ON CONFLICT(enrollment_id) DO UPDATE SET started=excluded.started", now.UnixNano(), string(id)))
}
func (s *CheckGuardRepository) StartManual(ctx context.Context, owner int64, now time.Time) error {
	if owner <= 0 || now.IsZero() {
		return ErrInvalid
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO manual_guards VALUES(?,?) ON CONFLICT(owner_id) DO UPDATE SET started=excluded.started", owner, now.UnixNano()); err != nil {
		return ErrStorage
	}
	return nil
}
func (s *CheckGuardRepository) DeleteEnrollment(ctx context.Context, id application.EnrollmentID) error {
	if !validID(id) {
		return ErrInvalid
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM case_guards WHERE enrollment_id=?", string(id)); err != nil {
		return ErrStorage
	}
	return nil
}
