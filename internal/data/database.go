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
	ErrStorage  = errors.New("storage operation failed")
	ErrNotFound = errors.New("enrollment not found")
	ErrExists   = errors.New("owner already enrolled or identity already used")
	ErrSchema   = errors.New("unsupported database schema")
	ErrInvalid  = errors.New("invalid storage input")
)

type Store struct {
	db *sql.DB
	*EnrollmentRepository
	*HistoryRepository
	*PendingDeliveryRepository
	*CheckGuardRepository
	*RecipientRepository
	*GlobalGateRepository
	*Transactions
}

type EnrollmentRepository struct{ db *sql.DB }
type HistoryRepository struct{ db *sql.DB }
type PendingDeliveryRepository struct{ db *sql.DB }
type CheckGuardRepository struct{ db *sql.DB }
type GlobalGateRepository struct{ db *sql.DB }

// Transactions owns writes that must update multiple logical entities atomically.
type Transactions struct{ db *sql.DB }

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
	s := &Store{db: db,
		EnrollmentRepository:      &EnrollmentRepository{db: db},
		HistoryRepository:         &HistoryRepository{db: db},
		PendingDeliveryRepository: &PendingDeliveryRepository{db: db},
		CheckGuardRepository:      &CheckGuardRepository{db: db},
		RecipientRepository:       &RecipientRepository{db: db},
		GlobalGateRepository:      &GlobalGateRepository{db: db},
		Transactions:              &Transactions{db: db},
	}
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

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func instant(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// TrackingRepositories exposes only the persistence operations tracking uses.
type TrackingRepositories struct {
	*EnrollmentRepository
	*CheckGuardRepository
	*Transactions
}

// NotificationRepositories exposes delivery state and its atomic transitions.
type NotificationRepositories struct {
	*PendingDeliveryRepository
	*RecipientRepository
	*GlobalGateRepository
	*Transactions
}

func (s *Store) TrackingRepositories() TrackingRepositories {
	return TrackingRepositories{s.EnrollmentRepository, s.CheckGuardRepository, s.Transactions}
}

func (s *Store) NotificationRepositories() NotificationRepositories {
	return NotificationRepositories{s.PendingDeliveryRepository, s.RecipientRepository, s.GlobalGateRepository, s.Transactions}
}
