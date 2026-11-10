package repositories

import (
	"context"
	"time"
)

type HistoryRepository struct {
	executor QueryExecutor
}

func NewHistoryRepository(executor QueryExecutor) *HistoryRepository {
	return &HistoryRepository{executor: executor}
}

// FindByEnrollment returns transitions belonging to the supplied owner and identity.
func (this *HistoryRepository) FindByEnrollment(ctx context.Context, owner int64, id string) ([]Transition, error) {
	rows, err := this.executor.QueryContext(ctx, `
		SELECT h.sequence, h.enrollment_id, h.previous_stage, h.stage, h.checked_at
		  FROM history AS h
		  JOIN enrollments AS e
		    ON e.id = h.enrollment_id
		 WHERE e.owner_id = ?
		   AND e.id = ?
		   AND e.deleted_at IS NULL
		 ORDER BY h.sequence
	`, owner, string(id))
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	result := []Transition{}
	for rows.Next() {
		var t Transition
		var identity, checked string
		if rows.Scan(&t.Sequence, &identity, &t.PreviousStage, &t.Stage, &checked) != nil {
			return nil, ErrStorage
		}
		t.EnrollmentID = identity
		t.CheckedAt, err = time.Parse(time.RFC3339Nano, checked)
		if err != nil {
			return nil, ErrStorage
		}
		result = append(result, t)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return result, nil
}

func (this *HistoryRepository) Append(
	ctx context.Context,
	id string,
	previous string,
	stage string,
	checkedAt time.Time,
) error {
	if !validID(id) || checkedAt.IsZero() {
		return ErrInvalid
	}
	_, err := this.executor.ExecContext(ctx, `
		INSERT INTO history (enrollment_id, previous_stage, stage, checked_at)
		VALUES (?, ?, ?, ?)
	`, id, previous, stage, stamp(checkedAt))
	if err != nil {
		return ErrStorage
	}
	return nil
}

func (this *HistoryRepository) DeleteByEnrollmentID(ctx context.Context, id string) error {
	if !validID(id) {
		return ErrInvalid
	}
	if _, err := this.executor.ExecContext(ctx, `
		DELETE FROM history
		WHERE enrollment_id = ?
	`, id); err != nil {
		return ErrStorage
	}
	return nil
}
