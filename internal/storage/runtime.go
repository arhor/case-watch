package storage

import (
	"context"
	"database/sql"
	"errors"
	"github.com/arhor/case-watch/internal/application"
	"time"
)

const runtimeSchema = `
CREATE TABLE manual_guards(owner_id INTEGER PRIMARY KEY, started INTEGER NOT NULL);
CREATE TABLE case_guards(enrollment_id TEXT PRIMARY KEY REFERENCES enrollments(id) ON DELETE CASCADE, started INTEGER NOT NULL);
CREATE TABLE recipients(recipient_id INTEGER PRIMARY KEY, pause TEXT NOT NULL DEFAULT '', blocked INTEGER NOT NULL DEFAULT 0);
INSERT INTO recipients(recipient_id) SELECT DISTINCT recipient_id FROM pending;
CREATE TABLE delivery_gates(singleton INTEGER PRIMARY KEY CHECK(singleton=1), token_paused INTEGER NOT NULL DEFAULT 0, rate_until INTEGER NOT NULL DEFAULT 0);
INSERT INTO delivery_gates(singleton) VALUES(1);
ALTER TABLE pending ADD COLUMN status TEXT NOT NULL DEFAULT 'pending';
ALTER TABLE pending ADD COLUMN cycle_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pending ADD COLUMN lifetime_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pending ADD COLUMN next_attempt INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pending ADD COLUMN reserved_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pending ADD COLUMN failure TEXT NOT NULL DEFAULT '';
ALTER TABLE pending ADD COLUMN message_id INTEGER NOT NULL DEFAULT 0;
CREATE TRIGGER pending_recipient AFTER INSERT ON pending BEGIN INSERT OR IGNORE INTO recipients(recipient_id) VALUES(NEW.recipient_id); END;
`

// CheckReservation is durable attempt-start eligibility, separate from cache freshness.
// The coordinator must join in-process work before requesting another reservation.
type CheckReservation struct {
	Reserved       bool
	NextEligibleAt time.Time
}

func (s *Store) ReserveCheck(ctx context.Context, owner int64, id application.EnrollmentID, manual bool, now time.Time) (CheckReservation, error) {
	if owner <= 0 || !validID(id) || now.IsZero() {
		return CheckReservation{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CheckReservation{}, ErrStorage
	}
	defer tx.Rollback()
	var exists int
	if tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM enrollments WHERE owner_id=? AND id=?)", owner, string(id)).Scan(&exists) != nil {
		return CheckReservation{}, ErrStorage
	}
	if exists == 0 {
		return CheckReservation{}, ErrNotFound
	}
	var started int64
	if tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT started FROM case_guards WHERE enrollment_id=?),0)", string(id)).Scan(&started) != nil {
		return CheckReservation{}, ErrStorage
	}
	deadline := int64(0)
	if started != 0 {
		deadline = started + int64(time.Minute)
	}
	if manual {
		var user int64
		if tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT started FROM manual_guards WHERE owner_id=?),0)", owner).Scan(&user) != nil {
			return CheckReservation{}, ErrStorage
		}
		if user != 0 && user+int64(time.Minute) > deadline {
			deadline = user + int64(time.Minute)
		}
	}
	if now.UnixNano() < deadline {
		return CheckReservation{NextEligibleAt: time.Unix(0, deadline).UTC()}, nil
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO case_guards VALUES(?,?) ON CONFLICT(enrollment_id) DO UPDATE SET started=excluded.started", string(id), now.UnixNano()); err != nil {
		return CheckReservation{}, ErrStorage
	}
	if manual {
		if _, err = tx.ExecContext(ctx, "INSERT INTO manual_guards VALUES(?,?) ON CONFLICT(owner_id) DO UPDATE SET started=excluded.started", owner, now.UnixNano()); err != nil {
			return CheckReservation{}, ErrStorage
		}
	}
	if tx.Commit() != nil {
		return CheckReservation{}, ErrStorage
	}
	return CheckReservation{Reserved: true, NextEligibleAt: now.Add(time.Minute)}, nil
}

// Remove deletes only the exact owner enrollment; registry and delivery gates survive.
// Callers must cancel/drain work first. Identity predicates fence late results.
func (s *Store) Remove(ctx context.Context, owner int64, id application.EnrollmentID) error {
	if owner <= 0 || !validID(id) {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM enrollments WHERE owner_id=? AND id=?", owner, string(id))
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

type Delivery struct {
	Sequence, RecipientID           int64
	EnrollmentID                    application.EnrollmentID
	PreviousStage, Stage            string
	CheckedAt                       time.Time
	Status                          string
	CycleAttempts, LifetimeAttempts int
	NextAttemptAt, ReservedAt       time.Time
	Failure                         string
	MessageID                       int64
}
type RecipientState struct {
	Pause   string
	Blocked bool
}
type GlobalState struct {
	TokenPaused bool
	RateUntil   time.Time
}

func instant(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}
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
func (s *Store) Queue(ctx context.Context, recipient int64) ([]Delivery, error) {
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

// ReserveDelivery atomically reserves only the oldest outstanding item. Authorization
// must be checked by the worker immediately before calling and before sending.
func (s *Store) ReserveDelivery(ctx context.Context, recipient int64, now time.Time) (Delivery, error) {
	if recipient <= 0 || now.IsZero() {
		return Delivery{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Delivery{}, ErrStorage
	}
	defer tx.Rollback()
	var paused, blocked, token int
	var rate int64
	if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM recipients WHERE recipient_id=? AND pause<>''),COALESCE((SELECT blocked FROM recipients WHERE recipient_id=?),0),token_paused,rate_until FROM delivery_gates`, recipient, recipient).Scan(&paused, &blocked, &token, &rate) != nil {
		return Delivery{}, ErrStorage
	}
	if paused != 0 || blocked != 0 || token != 0 || now.UnixNano() < rate {
		return Delivery{}, ErrNotEligible
	}
	d, err := scanDelivery(tx.QueryRowContext(ctx, "SELECT "+deliveryColumns+" FROM pending p JOIN history h ON h.sequence=p.history_sequence WHERE p.recipient_id=? AND p.status<>'delivered' ORDER BY p.sequence LIMIT 1", recipient))
	if err != nil {
		return Delivery{}, err
	}
	if d.Status == "reserved" || d.CycleAttempts >= 6 || now.Before(d.NextAttemptAt) {
		return Delivery{}, ErrNotEligible
	}
	if _, err = tx.ExecContext(ctx, "UPDATE pending SET status='reserved',cycle_attempts=cycle_attempts+1,lifetime_attempts=lifetime_attempts+1,reserved_at=? WHERE sequence=?", now.UnixNano(), d.Sequence); err != nil {
		return Delivery{}, ErrStorage
	}
	if tx.Commit() != nil {
		return Delivery{}, ErrStorage
	}
	d.Status = "reserved"
	d.CycleAttempts++
	d.LifetimeAttempts++
	d.ReservedAt = now
	return d, nil
}

var ErrNotEligible = errors.New("attempt not eligible")

// CompleteDelivery accepts only the exact durable attempt token, fencing stale
// completions after recovery, removal, and a new cycle. All outcomes are sanitized.
func (s *Store) CompleteDelivery(ctx context.Context, d Delivery, out application.SendOutcome, now time.Time) error {
	if d.Sequence <= 0 || d.LifetimeAttempts <= 0 || !validID(d.EnrollmentID) || now.IsZero() || out.RetryAfter < 0 {
		return ErrInvalid
	}
	switch out.Category {
	case application.SendAccepted:
		if out.MessageID <= 0 {
			return ErrInvalid
		}
	case application.SendTransient, application.SendRateLimited, application.SendBlocked, application.SendInvalidToken, application.SendPermanent:
	default:
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	current, err := scanDelivery(tx.QueryRowContext(ctx, "SELECT "+deliveryColumns+" FROM pending p JOIN history h ON h.sequence=p.history_sequence WHERE p.sequence=? AND h.enrollment_id=? AND p.status='reserved' AND p.lifetime_attempts=?", d.Sequence, string(d.EnrollmentID), d.LifetimeAttempts))
	if err != nil {
		return err
	}
	if err = finishDelivery(ctx, tx, current, out, now); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return ErrStorage
	}
	return nil
}
func finishDelivery(ctx context.Context, tx *sql.Tx, d Delivery, out application.SendOutcome, now time.Time) error {
	status := "pending"
	pause := ""
	next := int64(0)
	failure := string(out.Category)
	if out.Category == application.SendAccepted {
		status = "delivered"
		failure = ""
	} else {
		delay := 30 * time.Second * time.Duration(1<<uint(d.CycleAttempts-1))
		if out.RetryAfter > delay {
			delay = out.RetryAfter
		}
		next = now.Add(delay).UnixNano()
		switch out.Category {
		case application.SendBlocked:
			pause = "blocked"
		case application.SendPermanent:
			pause = "permanent"
		case application.SendInvalidToken:
			if _, err := tx.ExecContext(ctx, "UPDATE delivery_gates SET token_paused=1"); err != nil {
				return ErrStorage
			}
		}
		if d.CycleAttempts >= 6 && pause == "" {
			pause = "exhausted"
		}
		if out.Category == application.SendRateLimited {
			if _, err := tx.ExecContext(ctx, "UPDATE delivery_gates SET rate_until=MAX(rate_until,?)", now.Add(out.RetryAfter).UnixNano()); err != nil {
				return ErrStorage
			}
		}
	}
	if out.Category != application.SendAccepted {
		out.MessageID = 0
	}
	if pause != "" {
		blocked := 0
		if pause == "blocked" {
			blocked = 1
		}
		if _, err := tx.ExecContext(ctx, "UPDATE recipients SET pause=?,blocked=MAX(blocked,?) WHERE recipient_id=?", pause, blocked, d.RecipientID); err != nil {
			return ErrStorage
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE pending SET status=?,failure=?,next_attempt=?,message_id=? WHERE sequence=?", status, failure, next, out.MessageID, d.Sequence); err != nil {
		return ErrStorage
	}
	return nil
}

// RecoverDeliveries is a startup-only operation, before any worker starts. Each
// interrupted reservation consumes its existing attempt and starts backoff at now.
func (s *Store) RecoverDeliveries(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT "+deliveryColumns+" FROM pending p JOIN history h ON h.sequence=p.history_sequence WHERE p.status='reserved'")
	if err != nil {
		return ErrStorage
	}
	var work []Delivery
	for rows.Next() {
		d, e := scanDelivery(rows)
		if e != nil {
			rows.Close()
			return e
		}
		work = append(work, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ErrStorage
	}
	for _, d := range work {
		if err = finishDelivery(ctx, tx, d, application.SendOutcome{Category: application.SendTransient}, now); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE pending SET failure='uncertain' WHERE sequence=?", d.Sequence); err != nil {
			return ErrStorage
		}
	}
	if tx.Commit() != nil {
		return ErrStorage
	}
	return nil
}

func (s *Store) DeliveryGates(ctx context.Context, recipient int64) (RecipientState, GlobalState, error) {
	if recipient <= 0 {
		return RecipientState{}, GlobalState{}, ErrInvalid
	}
	var r RecipientState
	var g GlobalState
	var rate int64
	if s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT pause FROM recipients WHERE recipient_id=?),''),COALESCE((SELECT blocked FROM recipients WHERE recipient_id=?),0),token_paused,rate_until FROM delivery_gates`, recipient, recipient).Scan(&r.Pause, &r.Blocked, &g.TokenPaused, &rate) != nil {
		return r, g, ErrStorage
	}
	g.RateUntil = instant(rate)
	return r, g, nil
}

// ResumeRecipient is explicit operator recovery. It preserves lifetime counts and
// refuses to invalidate a live reservation. Ordinary private interactions should
// call SetBlocked(false), which only clears evidence-backed block pauses.
func (s *Store) ResumeRecipient(ctx context.Context, recipient int64) error {
	return s.resume(ctx, recipient, false)
}
func (s *Store) resume(ctx context.Context, recipient int64, onlyBlocked bool) error {
	if recipient <= 0 {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	var blocked, active int
	if tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT blocked FROM recipients WHERE recipient_id=?),0),EXISTS(SELECT 1 FROM pending WHERE recipient_id=? AND status='reserved')", recipient, recipient).Scan(&blocked, &active) != nil {
		return ErrStorage
	}
	if onlyBlocked && blocked == 0 {
		return nil
	}
	if active != 0 {
		return ErrNotEligible
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO recipients(recipient_id) VALUES(?) ON CONFLICT(recipient_id) DO UPDATE SET pause='',blocked=0", recipient); err != nil {
		return ErrStorage
	}
	if _, err = tx.ExecContext(ctx, "UPDATE pending SET cycle_attempts=0,next_attempt=0,failure='' WHERE sequence=(SELECT sequence FROM pending WHERE recipient_id=? AND status<>'delivered' ORDER BY sequence LIMIT 1)", recipient); err != nil {
		return ErrStorage
	}
	if tx.Commit() != nil {
		return ErrStorage
	}
	return nil
}
func (s *Store) SetBlocked(ctx context.Context, recipient int64, blocked bool) error {
	if recipient <= 0 {
		return ErrInvalid
	}
	if !blocked {
		return s.resume(ctx, recipient, true)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO recipients(recipient_id,pause,blocked) VALUES(?,'blocked',1) ON CONFLICT(recipient_id) DO UPDATE SET pause='blocked',blocked=1", recipient); err != nil {
		return ErrStorage
	}
	return nil
}

// ResumeToken is explicit maintenance after correcting credentials. Rate limits
// remain untouched and cannot be bypassed by token or recipient recovery.
func (s *Store) ResumeToken(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE delivery_gates SET token_paused=0"); err != nil {
		return ErrStorage
	}
	return nil
}

// CheckEligibility reads existing attempt-start guards without reserving work.
// Cached replies use this to explain eligibility without consuming an attempt.
func (s *Store) CheckEligibility(ctx context.Context, owner int64, id application.EnrollmentID, manual bool) (time.Time, error) {
	if owner <= 0 || !validID(id) {
		return time.Time{}, ErrInvalid
	}
	var caseStart, userStart int64
	err := s.db.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT started FROM case_guards WHERE enrollment_id=e.id),0),
 COALESCE((SELECT started FROM manual_guards WHERE owner_id=e.owner_id),0)
 FROM enrollments e WHERE e.owner_id=? AND e.id=?`, owner, string(id)).Scan(&caseStart, &userStart)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, ErrStorage
	}
	if manual && userStart > caseStart {
		caseStart = userStart
	}
	if caseStart == 0 {
		return time.Time{}, nil
	}
	return time.Unix(0, caseStart).UTC().Add(time.Minute), nil
}

// DeliveryHeads inventories only the oldest outstanding item for each recipient.
// Authorization and durable gates must still be checked by its consumer.
func (s *Store) DeliveryHeads(ctx context.Context) ([]Delivery, error) {
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
func (s *Store) ReleaseDelivery(ctx context.Context, d Delivery) error {
	if d.Sequence <= 0 || d.LifetimeAttempts <= 0 || !validID(d.EnrollmentID) {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE pending SET status='pending',cycle_attempts=cycle_attempts-1,lifetime_attempts=lifetime_attempts-1,reserved_at=0 WHERE sequence=? AND status='reserved' AND lifetime_attempts=? AND history_sequence IN (SELECT sequence FROM history WHERE enrollment_id=?)`, d.Sequence, d.LifetimeAttempts, string(d.EnrollmentID))
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

// RecordGlobalOutcome lets direct replies share global transport gates without
// acknowledging or consuming any durable notification reservation.
func (s *Store) RecordGlobalOutcome(ctx context.Context, out application.SendOutcome, now time.Time) error {
	if now.IsZero() || out.RetryAfter < 0 {
		return ErrInvalid
	}
	switch out.Category {
	case application.SendInvalidToken:
		if _, err := s.db.ExecContext(ctx, "UPDATE delivery_gates SET token_paused=1"); err != nil {
			return ErrStorage
		}
	case application.SendRateLimited:
		if _, err := s.db.ExecContext(ctx, "UPDATE delivery_gates SET rate_until=MAX(rate_until,?)", now.Add(out.RetryAfter).UnixNano()); err != nil {
			return ErrStorage
		}
	}
	return nil
}

// ResumeUnblocked applies explicit authenticated block-to-unblock evidence even
// when the preceding blocked update was missed. Interaction fallback must use
// SetBlocked(false) instead. Lifetime counts and global gates remain intact.
func (s *Store) ResumeUnblocked(ctx context.Context, recipient int64) error {
	return s.resume(ctx, recipient, false)
}
