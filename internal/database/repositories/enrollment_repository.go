package repositories

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/models"
)

type EnrollmentRepository struct {
	executor QueryExecutor
}

func NewEnrollmentRepository(executor QueryExecutor) *EnrollmentRepository {
	return &EnrollmentRepository{executor: executor}
}

func (this *EnrollmentRepository) FindByOwner(ctx context.Context, owner int64) (models.Enrollment, error) {
	if owner <= 0 {
		return models.Enrollment{}, ErrInvalid
	}
	return scanEnrollment(
		this.executor.QueryRowContext(ctx, `
			SELECT id, owner_id, case_number, password, stage, checked_at
			  FROM enrollments
			 WHERE deleted_at IS NULL
			   AND owner_id = ?
			`, owner,
		),
	)
}

func (this *EnrollmentRepository) FindByIdentity(ctx context.Context, owner int64, id string) (models.Enrollment, error) {
	if !validID(id) {
		return models.Enrollment{}, ErrInvalid
	}
	e, err := this.FindByOwner(ctx, owner)
	if err != nil {
		return models.Enrollment{}, err
	}
	if e.ID != id {
		return models.Enrollment{}, ErrNotFound
	}
	return e, nil
}

func (this *EnrollmentRepository) FindAll(ctx context.Context) ([]models.Enrollment, error) {
	rows, err := this.executor.QueryContext(ctx, `
		SELECT id, owner_id, case_number, password, stage, checked_at
		FROM enrollments
		WHERE deleted_at IS NULL
		ORDER BY owner_id
	`)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	result := []models.Enrollment{}
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

func (this *EnrollmentRepository) Create(
	ctx context.Context,
	owner int64,
	id string,
	number string,
	password string,
	stage string,
	checkedAt time.Time,
) error {
	if owner <= 0 || !validID(id) || strings.TrimSpace(number) == "" || strings.TrimSpace(password) == "" || checkedAt.IsZero() {
		return ErrInvalid
	}
	var exists bool
	if this.executor.QueryRowContext(ctx, `
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
	`, owner, id).Scan(&exists) != nil {
		return ErrStorage
	}
	if exists {
		return ErrExists
	}
	_, err := this.executor.ExecContext(ctx, `
		INSERT INTO enrollments (
			id, owner_id, case_number, password, stage, checked_at
		)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, owner, number, password, stage, stamp(checkedAt))
	if err != nil {
		return ErrStorage
	}
	return nil
}

func (this *EnrollmentRepository) UpdateObservation(
	ctx context.Context,
	owner int64,
	id string,
	stage string,
	checkedAt time.Time,
) error {
	if owner <= 0 || !validID(id) || checkedAt.IsZero() {
		return ErrInvalid
	}
	return rowAffected(
		this.executor.ExecContext(ctx, `
			UPDATE enrollments
			   SET stage = ?
			     , checked_at = ?
			 WHERE owner_id = ?
			   AND id = ?
			   AND deleted_at IS NULL
			`, stage, stamp(checkedAt), owner, id,
		),
	)
}

func (this *EnrollmentRepository) SoftDelete(ctx context.Context, owner int64, id string, now time.Time) error {
	if owner <= 0 || !validID(id) || now.IsZero() {
		return ErrInvalid
	}
	return rowAffected(
		this.executor.ExecContext(ctx, `
			UPDATE enrollments
			   SET deleted_at = ?
			     , case_number = NULL
				 , password = NULL
				 , stage = NULL
				 , checked_at = NULL
			 WHERE owner_id = ?
			   AND id = ?
			   AND deleted_at IS NULL
			`, stamp(now), owner, id,
		),
	)
}

// ---- Internal implementation --------------------------------------------------------------------

func scanEnrollment(
	row interface {
		Scan(...any) error
	},
) (models.Enrollment, error) {
	var e models.Enrollment
	var checked string
	err := row.Scan(&e.ID, &e.OwnerID, &e.Number, &e.Password, &e.Stage, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	if err != nil {
		return models.Enrollment{}, ErrStorage
	}
	e.CheckedAt, err = time.Parse(time.RFC3339Nano, checked)
	if err != nil {
		return models.Enrollment{}, ErrStorage
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
