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

func (s *PendingDeliveryRepository) Create(ctx context.Context, historySequence, recipient int64) error {
	if historySequence <= 0 || recipient <= 0 {
		return ErrInvalid
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO pending(history_sequence,recipient_id) VALUES(?,?)", historySequence, recipient); err != nil {
		return ErrStorage
	}
	return nil
}
func (s *PendingDeliveryRepository) Head(ctx context.Context, recipient int64) (Delivery, error) {
	if recipient <= 0 {
		return Delivery{}, ErrInvalid
	}
	return scanDelivery(s.db.QueryRowContext(ctx, "SELECT "+deliveryColumns+" FROM pending p JOIN history h ON h.sequence=p.history_sequence WHERE p.recipient_id=? AND p.status<>'delivered' ORDER BY p.sequence LIMIT 1", recipient))
}
func (s *PendingDeliveryRepository) Attempt(ctx context.Context, d Delivery) (Delivery, error) {
	if d.Sequence <= 0 || d.LifetimeAttempts <= 0 || !validID(d.EnrollmentID) {
		return Delivery{}, ErrInvalid
	}
	return scanDelivery(s.db.QueryRowContext(ctx, "SELECT "+deliveryColumns+" FROM pending p JOIN history h ON h.sequence=p.history_sequence WHERE p.sequence=? AND h.enrollment_id=? AND p.status='reserved' AND p.lifetime_attempts=?", d.Sequence, string(d.EnrollmentID), d.LifetimeAttempts))
}
func (s *PendingDeliveryRepository) Reserve(ctx context.Context, d Delivery, now time.Time) error {
	if d.Sequence <= 0 || !validID(d.EnrollmentID) || now.IsZero() {
		return ErrInvalid
	}
	return affected(s.db.ExecContext(ctx, `UPDATE pending SET status='reserved',cycle_attempts=cycle_attempts+1,lifetime_attempts=lifetime_attempts+1,reserved_at=? WHERE sequence=? AND status='pending' AND lifetime_attempts=? AND history_sequence IN (SELECT sequence FROM history WHERE enrollment_id=?)`, now.UnixNano(), d.Sequence, d.LifetimeAttempts, string(d.EnrollmentID)))
}
func (s *PendingDeliveryRepository) Reserved(ctx context.Context) ([]Delivery, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+deliveryColumns+" FROM pending p JOIN history h ON h.sequence=p.history_sequence WHERE p.status='reserved'")
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return out, nil
}

// Finish updates only the exact reserved attempt, fencing removed identities and stale workers.
func (s *PendingDeliveryRepository) Finish(ctx context.Context, d Delivery, status, failure string, next time.Time, messageID int64) error {
	if d.Sequence <= 0 || d.LifetimeAttempts <= 0 || !validID(d.EnrollmentID) {
		return ErrInvalid
	}
	var at int64
	if !next.IsZero() {
		at = next.UnixNano()
	}
	return affected(s.db.ExecContext(ctx, `UPDATE pending SET status=?,failure=?,next_attempt=?,message_id=? WHERE sequence=? AND status='reserved' AND lifetime_attempts=? AND history_sequence IN (SELECT sequence FROM history WHERE enrollment_id=?)`, status, failure, at, messageID, d.Sequence, d.LifetimeAttempts, string(d.EnrollmentID)))
}
func (s *PendingDeliveryRepository) Release(ctx context.Context, d Delivery) error {
	if d.Sequence <= 0 || d.LifetimeAttempts <= 0 || !validID(d.EnrollmentID) {
		return ErrInvalid
	}
	return affected(s.db.ExecContext(ctx, `UPDATE pending SET status='pending',cycle_attempts=cycle_attempts-1,lifetime_attempts=lifetime_attempts-1,reserved_at=0 WHERE sequence=? AND status='reserved' AND lifetime_attempts=? AND history_sequence IN (SELECT sequence FROM history WHERE enrollment_id=?)`, d.Sequence, d.LifetimeAttempts, string(d.EnrollmentID)))
}
func (s *PendingDeliveryRepository) HasReservation(ctx context.Context, recipient int64) (bool, error) {
	if recipient <= 0 {
		return false, ErrInvalid
	}
	var active bool
	if s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pending WHERE recipient_id=? AND status='reserved')", recipient).Scan(&active) != nil {
		return false, ErrStorage
	}
	return active, nil
}
func (s *PendingDeliveryRepository) ResetHead(ctx context.Context, recipient int64) error {
	if recipient <= 0 {
		return ErrInvalid
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE pending SET cycle_attempts=0,next_attempt=0,failure='' WHERE sequence=(SELECT sequence FROM pending WHERE recipient_id=? AND status<>'delivered' ORDER BY sequence LIMIT 1)", recipient); err != nil {
		return ErrStorage
	}
	return nil
}
