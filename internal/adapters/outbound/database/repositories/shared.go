package repositories

import (
	"encoding/hex"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/domain"
)

var (
	ErrStorage  = domain.ErrStorage
	ErrNotFound = domain.ErrNotFound
	ErrExists   = domain.ErrExists
	ErrInvalid  = domain.ErrInvalid
)

// -- Internals ----------------------------------------------------------------

func validID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 16 && id == strings.ToLower(id)
}

func stamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
