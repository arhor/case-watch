package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/arhor/case-watch/internal/application"
)

func (s *CheckGuardRepository) ReserveCheck(ctx context.Context, owner int64, id application.EnrollmentID, manual bool, now time.Time) (CheckReservation, error) {
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

func (s *CheckGuardRepository) CheckEligibility(ctx context.Context, owner int64, id application.EnrollmentID, manual bool) (time.Time, error) {
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
