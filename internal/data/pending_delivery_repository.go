package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/arhor/case-watch/internal/application"
)

func scanDelivery(row interface{ Scan(...any) error }) (Delivery, error) {
	var d Delivery
	var id, checked string
	var next, reserved int64
	err := row.Scan(&d.Sequence, &d.RecipientID, &id, &d.PreviousStage, &d.Stage, &checked, &d.Status, &d.CycleAttempts, &d.LifetimeAttempts, &next, &reserved, &d.Failure, &d.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, ErrStorage
	}
	d.EnrollmentID = application.EnrollmentID(id)
	d.CheckedAt, err = time.Parse(time.RFC3339Nano, checked)
	if err != nil {
		return Delivery{}, ErrStorage
	}
	d.NextAttemptAt = instant(next)
	d.ReservedAt = instant(reserved)
	return d, nil
}

const deliveryColumns = `p.sequence,p.recipient_id,h.enrollment_id,h.previous_stage,h.stage,h.checked_at,p.status,p.cycle_attempts,p.lifetime_attempts,p.next_attempt,p.reserved_at,p.failure,p.message_id`

// Queue includes confirmed records for diagnosis; ordering ignores confirmed rows.

func (s *PendingDeliveryRepository) Queue(ctx context.Context, recipient int64) ([]Delivery, error) {
	if recipient <= 0 {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+deliveryColumns+" FROM pending p JOIN history h ON h.sequence=p.history_sequence WHERE p.recipient_id=? ORDER BY p.sequence", recipient)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		d, e := scanDelivery(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return out, nil
}

func (s *PendingDeliveryRepository) DeliveryHeads(ctx context.Context) ([]Delivery, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+deliveryColumns+" FROM pending p JOIN history h ON h.sequence=p.history_sequence WHERE p.status<>'delivered' AND p.sequence=(SELECT MIN(q.sequence) FROM pending q WHERE q.recipient_id=p.recipient_id AND q.status<>'delivered') ORDER BY p.sequence")
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		d, e := scanDelivery(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return out, nil
}

// ReleaseDelivery cancels a reservation only when no network attempt took place.
// Callers must serialize this decision with sends and membership changes. The
// exact attempt token prevents rollback of a later attempt or removed enrollment.
