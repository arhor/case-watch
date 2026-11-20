package repositories

import (
	"context"
	"time"

	"github.com/arhor/case-watch/internal/models"
)

var _ models.TrackedCasesHistoryRepository = (*TrackedCasesHistoryRepository)(nil)

type TrackedCasesHistoryRepository struct {
	executor QueryExecutor
}

func NewTrackedCasesHistoryRepository(executor QueryExecutor) *TrackedCasesHistoryRepository {
	return &TrackedCasesHistoryRepository{executor: executor}
}

// FindByTrackedCase returns transitions belonging to the supplied owner and identity.
func (this *TrackedCasesHistoryRepository) FindByTrackedCase(ctx context.Context, owner int64, id string) ([]models.Transition, error) {
	rows, err := this.executor.QueryContext(ctx, `
		SELECT h.sequence, h.tracked_case_id, h.prev_stage, h.curr_state, h.checked_at
		  FROM tracked_cases_history AS h
		  JOIN tracked_cases AS c
		    ON c.id = h.tracked_case_id
		 WHERE c.owner_id = ?
		   AND c.id = ?
		   AND c.deleted_at IS NULL
		 ORDER BY h.sequence
	`, owner, string(id))
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	result := []models.Transition{}
	for rows.Next() {
		var t models.Transition
		var identity, checked string
		if rows.Scan(&t.Sequence, &identity, &t.PreviousStage, &t.Stage, &checked) != nil {
			return nil, ErrStorage
		}
		t.TrackedCaseID = identity
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

func (this *TrackedCasesHistoryRepository) Append(
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
		INSERT INTO tracked_cases_history (tracked_case_id, prev_stage, curr_state, checked_at)
		VALUES (?, ?, ?, ?)
	`, id, previous, stage, stamp(checkedAt))
	if err != nil {
		return ErrStorage
	}
	return nil
}

func (this *TrackedCasesHistoryRepository) DeleteByTrackedCaseID(ctx context.Context, id string) error {
	if !validID(id) {
		return ErrInvalid
	}
	if _, err := this.executor.ExecContext(ctx, `
		DELETE FROM tracked_cases_history
		WHERE tracked_case_id = ?
	`, id); err != nil {
		return ErrStorage
	}
	return nil
}
