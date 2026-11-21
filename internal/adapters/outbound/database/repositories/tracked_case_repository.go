package repositories

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/domain"
)

var _ domain.TrackedCaseRepository = (*TrackedCaseRepository)(nil)

type TrackedCaseRepository struct {
	executor QueryExecutor
}

func NewTrackedCaseRepository(executor QueryExecutor) *TrackedCaseRepository {
	return &TrackedCaseRepository{executor: executor}
}

func (this *TrackedCaseRepository) FindByOwner(ctx context.Context, owner int64) (domain.TrackedCase, error) {
	if owner <= 0 {
		return domain.TrackedCase{}, ErrInvalid
	}
	return scanTrackedCase(
		this.executor.QueryRowContext(ctx, `
			SELECT id, owner_id, case_number, password, curr_state, checked_at
			  FROM tracked_cases
			 WHERE deleted_at IS NULL
			   AND owner_id = ?
			`, owner,
		),
	)
}

func (this *TrackedCaseRepository) FindByIdentity(ctx context.Context, owner int64, id string) (domain.TrackedCase, error) {
	if !validID(id) {
		return domain.TrackedCase{}, ErrInvalid
	}
	trackedCase, err := this.FindByOwner(ctx, owner)
	if err != nil {
		return domain.TrackedCase{}, err
	}
	if trackedCase.ID != id {
		return domain.TrackedCase{}, ErrNotFound
	}
	return trackedCase, nil
}

func (this *TrackedCaseRepository) FindAll(ctx context.Context) ([]domain.TrackedCase, error) {
	rows, err := this.executor.QueryContext(ctx, `
		SELECT id, owner_id, case_number, password, curr_state, checked_at
		  FROM tracked_cases
		 WHERE deleted_at IS NULL
		 ORDER BY owner_id
	`)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	result := []domain.TrackedCase{}
	for rows.Next() {
		trackedCase, err := scanTrackedCase(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, trackedCase)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return result, nil
}

func (this *TrackedCaseRepository) Create(
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
		      FROM tracked_cases
		     WHERE owner_id = ?
		       AND deleted_at IS NULL
		     UNION ALL
		    SELECT 1
		      FROM tracked_cases
		     WHERE id = ?
		)
	`, owner, id).Scan(&exists) != nil {
		return ErrStorage
	}
	if exists {
		return ErrExists
	}
	_, err := this.executor.ExecContext(ctx, `
		INSERT INTO tracked_cases (id, owner_id, case_number, password, curr_state, checked_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, owner, number, password, stage, stamp(checkedAt))
	if err != nil {
		return ErrStorage
	}
	return nil
}

func (this *TrackedCaseRepository) UpdateObservation(
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
			UPDATE tracked_cases
			   SET curr_state = ?
			     , checked_at = ?
			 WHERE owner_id = ?
			   AND id = ?
			   AND deleted_at IS NULL
			`, stage, stamp(checkedAt), owner, id,
		),
	)
}

func (this *TrackedCaseRepository) SoftDelete(ctx context.Context, owner int64, id string, now time.Time) error {
	if owner <= 0 || !validID(id) || now.IsZero() {
		return ErrInvalid
	}
	return rowAffected(
		this.executor.ExecContext(ctx, `
			UPDATE tracked_cases
			   SET deleted_at = ?
			     , case_number = NULL
			     , password = NULL
			     , curr_state = NULL
			     , checked_at = NULL
			 WHERE owner_id = ?
			   AND id = ?
			   AND deleted_at IS NULL
			`, stamp(now), owner, id,
		),
	)
}

// -- Internals ----------------------------------------------------------------

func scanTrackedCase(
	row interface {
		Scan(...any) error
	},
) (domain.TrackedCase, error) {
	var trackedCase domain.TrackedCase
	var checked string
	err := row.Scan(&trackedCase.ID, &trackedCase.OwnerID, &trackedCase.Number, &trackedCase.Password, &trackedCase.Stage, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return trackedCase, ErrNotFound
	}
	if err != nil {
		return domain.TrackedCase{}, ErrStorage
	}
	trackedCase.CheckedAt, err = time.Parse(time.RFC3339Nano, checked)
	if err != nil {
		return domain.TrackedCase{}, ErrStorage
	}
	return trackedCase, nil
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
