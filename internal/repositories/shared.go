package repositories

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/domain"
)

var (
	ErrTransaction = errors.New("invalid transaction lifecycle operation")
	ErrNotEligible = errors.New("attempt not eligible")
	ErrStorage     = errors.New("storage operation failed")
	ErrNotFound    = errors.New("record not found")
	ErrExists      = errors.New("owner already enrolled or identity already used")
	ErrSchema      = errors.New("unsupported database schema")
	ErrInvalid     = errors.New("invalid storage input")
)

type HistoryRepository struct {
	db DBTX
}

type PendingDeliveryRepository struct {
	db DBTX
}

// NewEnrollmentID allocates an opaque identity used to fence late work.
func NewEnrollmentID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", ErrStorage
	}
	return hex.EncodeToString(b), nil
}

func validID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 16 && id == strings.ToLower(id)
}

func validObservation(o domain.Observation) bool {
	return !o.CheckedAt.IsZero()
}

func stamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func instant(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}
