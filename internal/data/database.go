// Package data implements the SQLite boundary in ADRs 0004–0006.
package data

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
	ErrTransaction = errors.New("invalid transaction lifecycle operation")
	ErrNotEligible = errors.New("attempt not eligible")
	ErrStorage     = errors.New("storage operation failed")
	ErrNotFound    = errors.New("enrollment not found")
	ErrExists      = errors.New("owner already enrolled or identity already used")
	ErrSchema      = errors.New("unsupported database schema")
	ErrInvalid     = errors.New("invalid storage input")
)

// DBTX is the SQL surface shared by connections and transactions.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Store struct {
	db          *sql.DB
	inTx        bool
	Enrollments *EnrollmentRepository
	History     *HistoryRepository
	Pending     *PendingDeliveryRepository
	Guards      *CheckGuardRepository
	Recipients  *RecipientRepository
	Gates       *GlobalGateRepository
}

type EnrollmentRepository struct{ db DBTX }
type HistoryRepository struct{ db DBTX }
type PendingDeliveryRepository struct{ db DBTX }
type CheckGuardRepository struct{ db DBTX }
type GlobalGateRepository struct{ db DBTX }

func newStore(db *sql.DB, handle DBTX, inTx bool) *Store {
	return &Store{db: db, inTx: inTx,
		Enrollments: &EnrollmentRepository{db: handle},
		History:     &HistoryRepository{db: handle},
		Pending:     &PendingDeliveryRepository{db: handle},
		Guards:      &CheckGuardRepository{db: handle},
		Recipients:  &RecipientRepository{db: handle},
		Gates:       &GlobalGateRepository{db: handle},
	}
}

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
	s := newStore(db, db, false)
	if err = s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s.inTx {
		return ErrTransaction
	}
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

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func instant(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}
