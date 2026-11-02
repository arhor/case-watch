package data

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/application"
)

func (s *Transactions) Remove(ctx context.Context, owner int64, id application.EnrollmentID) error {
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

// ReserveDelivery atomically reserves only the oldest outstanding item.
func (s *Transactions) ReserveDelivery(ctx context.Context, recipient int64, now time.Time) (Delivery, error) {
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

func (s *Transactions) CompleteDelivery(ctx context.Context, d Delivery, out application.SendOutcome, now time.Time) error {
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

func (s *Transactions) RecoverDeliveries(ctx context.Context, now time.Time) error {
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

// ReleaseDelivery cancels a reservation only when no network attempt took place.
func (s *Transactions) ReleaseDelivery(ctx context.Context, d Delivery) error {
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

func (s *Transactions) Enroll(ctx context.Context, owner int64, c application.SealedCredentials, o application.Observation) error {
	if owner <= 0 || !validID(c.ID) || c.FormatVersion != 1 || strings.TrimSpace(c.KeyID) == "" || len(c.Payload) < 28 || !validObservation(o) {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	var exists int
	if tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM enrollments WHERE owner_id=? UNION ALL SELECT 1 FROM enrollment_ids WHERE id=?)", owner, string(c.ID)).Scan(&exists) != nil {
		return ErrStorage
	}
	if exists != 0 {
		return ErrExists
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO enrollment_ids(id) VALUES(?)", string(c.ID)); err != nil {
		return ErrStorage
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO enrollments VALUES(?,?,?,?,?,?,?)", string(c.ID), owner, c.FormatVersion, c.KeyID, c.Payload, o.Stage, stamp(o.CheckedAt)); err != nil {
		return ErrStorage
	}
	if tx.Commit() != nil {
		return ErrStorage
	}
	return nil
}

func (s *Transactions) Observe(ctx context.Context, owner int64, id application.EnrollmentID, o application.Observation) error {
	if owner <= 0 || !validID(id) || !validObservation(o) {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT stage FROM enrollments WHERE owner_id=? AND id=?", owner, string(id)).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return ErrStorage
	}
	if _, err = tx.ExecContext(ctx, "UPDATE enrollments SET stage=?,checked_at=? WHERE owner_id=? AND id=?", o.Stage, stamp(o.CheckedAt), owner, string(id)); err != nil {
		return ErrStorage
	}
	if previous != o.Stage {
		result, e := tx.ExecContext(ctx, "INSERT INTO history(enrollment_id,previous_stage,stage,checked_at) VALUES(?,?,?,?)", string(id), previous, o.Stage, stamp(o.CheckedAt))
		if e != nil {
			return ErrStorage
		}
		sequence, e := result.LastInsertId()
		if e != nil {
			return ErrStorage
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO pending(history_sequence,recipient_id) VALUES(?,?)", sequence, owner); err != nil {
			return ErrStorage
		}
	}
	if tx.Commit() != nil {
		return ErrStorage
	}
	return nil
}

func (s *Transactions) RewriteCredentials(ctx context.Context, rewrite func(application.SealedCredentials) (application.SealedCredentials, error)) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT id,credential_version,key_id,payload FROM enrollments ORDER BY id`)
	if e != nil {
		return ErrStorage
	}
	var records []application.SealedCredentials
	for rows.Next() {
		var r application.SealedCredentials
		if rows.Scan(&r.ID, &r.FormatVersion, &r.KeyID, &r.Payload) != nil {
			rows.Close()
			return ErrStorage
		}
		records = append(records, r)
	}
	e = rows.Err()
	ce := rows.Close()
	if e != nil || ce != nil {
		return ErrStorage
	}
	for _, r := range records {
		n, e := rewrite(r)
		if e != nil {
			return ErrStorage
		}
		if n.ID != r.ID || n.FormatVersion != 1 || n.KeyID == "" || len(n.Payload) == 0 {
			return ErrInvalid
		}
		result, e := tx.ExecContext(ctx, `UPDATE enrollments SET credential_version=?,key_id=?,payload=? WHERE id=?`, n.FormatVersion, n.KeyID, n.Payload, string(r.ID))
		if e != nil {
			return ErrStorage
		}
		count, e := result.RowsAffected()
		if e != nil || count != 1 {
			return ErrStorage
		}
	}
	if tx.Commit() != nil {
		return ErrStorage
	}
	return nil
}
