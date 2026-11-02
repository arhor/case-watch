package data

import (
	"context"
	"time"

	"github.com/arhor/case-watch/internal/application"
)

// History returns transitions belonging to the supplied owner and identity.
func (s *HistoryRepository) History(ctx context.Context, owner int64, id application.EnrollmentID) ([]Transition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT h.sequence,h.enrollment_id,h.previous_stage,h.stage,h.checked_at FROM history h JOIN enrollments e ON e.id=h.enrollment_id WHERE e.owner_id=? AND e.id=? AND e.deleted_at IS NULL ORDER BY h.sequence`, owner, string(id))
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
		t.EnrollmentID = application.EnrollmentID(identity)
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
