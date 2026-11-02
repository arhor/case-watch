// Package storage implements the SQLite boundary in ADRs 0004–0006.
// Calls contain no network I/O. Errors never expose SQL, paths or record data.
package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/application"
	_ "modernc.org/sqlite"
)

var (
	ErrStorage  = errors.New("storage operation failed")
	ErrNotFound = errors.New("enrollment not found")
	ErrExists   = errors.New("owner already enrolled or identity already used")
	ErrSchema   = errors.New("unsupported database schema")
	ErrInvalid  = errors.New("invalid storage input")
)

type Store struct{ db *sql.DB }

var _ application.Store = (*Store)(nil)

// NewEnrollmentID allocates an opaque identity before sealing credentials.
func NewEnrollmentID() (application.EnrollmentID, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", ErrStorage
	}
	return application.EnrollmentID(hex.EncodeToString(b)), nil
}

// Open initializes or transactionally migrates one local file. The parent
// directory must already exist and be restricted to the service account.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, ErrInvalid
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrStorage
	}
	u := url.URL{Scheme: "file", Path: absolute}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(DELETE)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, ErrStorage
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err = s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error {
	if s.db.Close() != nil {
		return ErrStorage
	}
	return nil
}

func validID(id application.EnrollmentID) bool {
	b, e := hex.DecodeString(string(id))
	return e == nil && len(b) == 16 && string(id) == strings.ToLower(string(id))
}
func validObservation(o application.Observation) bool { return !o.CheckedAt.IsZero() }
func stamp(t time.Time) string                        { return t.UTC().Format(time.RFC3339Nano) }

func (s *Store) Enroll(ctx context.Context, owner int64, c application.SealedCredentials, o application.Observation) error {
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

func (s *Store) Observe(ctx context.Context, owner int64, id application.EnrollmentID, o application.Observation) error {
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

type Enrollment struct {
	OwnerID     int64
	Credentials application.SealedCredentials
	Observation application.Observation
}

func scanEnrollment(row interface{ Scan(...any) error }) (Enrollment, error) {
	var e Enrollment
	var id, checked string
	err := row.Scan(&e.OwnerID, &id, &e.Credentials.FormatVersion, &e.Credentials.KeyID, &e.Credentials.Payload, &e.Observation.Stage, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	if err != nil {
		return Enrollment{}, ErrStorage
	}
	e.Credentials.ID = application.EnrollmentID(id)
	e.Observation.CheckedAt, err = time.Parse(time.RFC3339Nano, checked)
	if err != nil {
		return Enrollment{}, ErrStorage
	}
	return e, nil
}

const enrollmentColumns = "owner_id,id,credential_version,key_id,payload,stage,checked_at"

func (s *Store) Enrollment(ctx context.Context, owner int64) (Enrollment, error) {
	if owner <= 0 {
		return Enrollment{}, ErrInvalid
	}
	return scanEnrollment(s.db.QueryRowContext(ctx, "SELECT "+enrollmentColumns+" FROM enrollments WHERE owner_id=?", owner))
}

// Enrollments is for startup authentication and policy-filtered background work.
// Callers must apply current authorization before decrypting or starting work.
func (s *Store) Enrollments(ctx context.Context) ([]Enrollment, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+enrollmentColumns+" FROM enrollments ORDER BY owner_id")
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	result := []Enrollment{}
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

type Transition struct {
	Sequence             int64
	EnrollmentID         application.EnrollmentID
	PreviousStage, Stage string
	CheckedAt            time.Time
}

// History returns only transitions belonging to the supplied owner and identity.
func (s *Store) History(ctx context.Context, owner int64, id application.EnrollmentID) ([]Transition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT h.sequence,h.enrollment_id,h.previous_stage,h.stage,h.checked_at FROM history h JOIN enrollments e ON e.id=h.enrollment_id WHERE e.owner_id=? AND e.id=? ORDER BY h.sequence`, owner, string(id))
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
