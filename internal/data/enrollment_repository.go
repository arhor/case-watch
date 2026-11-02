package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/arhor/case-watch/internal/application"
)

func scanEnrollment(row interface{ Scan(...any) error }) (Enrollment, error) {
	var e Enrollment
	var id, checked string
	err := row.Scan(&e.OwnerID, &id, &e.Credentials.FormatVersion, &e.Credentials.KeyID, &e.Credentials.Payload, &e.Observation.Stage, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	if err != nil {
		return Enrollment{}, ErrStorage
	}
	e.Credentials.ID = application.EnrollmentID(id)
	e.Observation.CheckedAt, err = time.Parse(time.RFC3339Nano, checked)
	if err != nil {
		return Enrollment{}, ErrStorage
	}
	return e, nil
}

const enrollmentColumns = "owner_id,id,credential_version,key_id,payload,stage,checked_at"

func (s *EnrollmentRepository) Enrollment(ctx context.Context, owner int64) (Enrollment, error) {
	if owner <= 0 {
		return Enrollment{}, ErrInvalid
	}
	return scanEnrollment(s.db.QueryRowContext(ctx, "SELECT "+enrollmentColumns+" FROM enrollments WHERE owner_id=?", owner))
}

// Enrollments is for startup authentication and policy-filtered background work.
// Callers must apply current authorization before decrypting or starting work.

func (s *EnrollmentRepository) Enrollments(ctx context.Context) ([]Enrollment, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+enrollmentColumns+" FROM enrollments ORDER BY owner_id")
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	result := []Enrollment{}
	for rows.Next() {
		e, err := scanEnrollment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return result, nil
}
