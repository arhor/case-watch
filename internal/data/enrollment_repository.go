package data

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/application"
)

type EnrollmentRepository struct {
	db DBTX
}

func (this *EnrollmentRepository) FindByOwner(ctx context.Context, owner int64) (Enrollment, error) {
	if owner <= 0 {
		return Enrollment{}, ErrInvalid
	}
	return scanEnrollment(
		this.db.QueryRowContext(ctx, `
			SELECT id, owner_id, credential_version, key_id, payload, stage, checked_at
			  FROM enrollments
			 WHERE deleted_at IS NULL
			   AND owner_id = ?
			`, owner,
		),
	)
}

func (this *EnrollmentRepository) FindByIdentity(ctx context.Context, owner int64, id string) (Enrollment, error) {
	if !validID(id) {
		return Enrollment{}, ErrInvalid
	}
	e, err := this.FindByOwner(ctx, owner)
	if err != nil {
		return Enrollment{}, err
	}
	if e.Credentials.ID != id {
		return Enrollment{}, ErrNotFound
	}
	return e, nil
}

func (this *EnrollmentRepository) FindAll(ctx context.Context) ([]Enrollment, error) {
	rows, err := this.db.QueryContext(ctx, `
		SELECT id, owner_id, credential_version, key_id, payload, stage, checked_at
		FROM enrollments
		WHERE deleted_at IS NULL
		ORDER BY owner_id
	`)
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

func (this *EnrollmentRepository) Create(ctx context.Context, owner int64, c application.SealedCredentials, o application.Observation) error {
	if owner <= 0 || !validID(c.ID) || c.FormatVersion != 1 || strings.TrimSpace(c.KeyID) == "" || len(c.Payload) < 28 || !validObservation(o) {
		return ErrInvalid
	}
	var exists bool
	if this.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM enrollments
			 WHERE owner_id = ?
			   AND deleted_at IS NULL
			UNION ALL
			SELECT 1
			  FROM enrollments
			 WHERE id = ?
		)
	`, owner, string(c.ID)).Scan(&exists) != nil {
		return ErrStorage
	}
	if exists {
		return ErrExists
	}
	_, err := this.db.ExecContext(ctx, `
		INSERT INTO enrollments (
			id, owner_id, credential_version, key_id, payload, stage, checked_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, string(c.ID), owner, c.FormatVersion, c.KeyID, c.Payload, o.Stage, stamp(o.CheckedAt))
	if err != nil {
		return ErrStorage
	}
	return nil
}

func (this *EnrollmentRepository) UpdateObservation(ctx context.Context, owner int64, id string, o application.Observation) error {
	if owner <= 0 || !validID(id) || !validObservation(o) {
		return ErrInvalid
	}
	return rowAffected(
		this.db.ExecContext(ctx, `
			UPDATE enrollments
			   SET stage = ?
			     , checked_at = ?
			 WHERE owner_id = ?
			   AND id = ?
			   AND deleted_at IS NULL
			`, o.Stage, stamp(o.CheckedAt), owner, id,
		),
	)
}

func (this *EnrollmentRepository) SoftDelete(ctx context.Context, owner int64, id string, now time.Time) error {
	if owner <= 0 || !validID(id) || now.IsZero() {
		return ErrInvalid
	}
	return rowAffected(
		this.db.ExecContext(ctx, `
			UPDATE enrollments
			   SET deleted_at = ?
			     , credential_version = NULL
				 , key_id = NULL
				 , payload = NULL
				 , stage = NULL
				 , checked_at = NULL
			 WHERE owner_id = ?
			   AND id = ?
			   AND deleted_at IS NULL
			`, stamp(now), owner, id,
		),
	)
}

func (this *EnrollmentRepository) UpdateCredentials(ctx context.Context, c application.SealedCredentials) error {
	if !validID(c.ID) || c.FormatVersion != 1 || c.KeyID == "" || len(c.Payload) == 0 {
		return ErrInvalid
	}
	return rowAffected(
		this.db.ExecContext(ctx, `
			UPDATE enrollments
			   SET credential_version = ?
			     , key_id = ?
			     , payload = ?
			 WHERE id = ?
			   AND deleted_at IS NULL
			`, c.FormatVersion, c.KeyID, c.Payload, c.ID,
		),
	)
}

// ---- Internal implementation --------------------------------------------------------------------

func scanEnrollment(
	row interface {
		Scan(...any) error
	},
) (Enrollment, error) {
	var e Enrollment
	var id, checked string
	err := row.Scan(&id, &e.OwnerID, &e.Credentials.FormatVersion, &e.Credentials.KeyID, &e.Credentials.Payload, &e.Observation.Stage, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	if err != nil {
		return Enrollment{}, ErrStorage
	}
	e.Credentials.ID = id
	e.Observation.CheckedAt, err = time.Parse(time.RFC3339Nano, checked)
	if err != nil {
		return Enrollment{}, ErrStorage
	}
	return e, nil
}

func rowAffected(result sql.Result, err error) error {
	if err != nil {
		return ErrStorage
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ErrStorage
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
