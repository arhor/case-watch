package repositories

import (
	"encoding/hex"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/models"
)

var (
	ErrStorage  = models.ErrStorage
	ErrNotFound = models.ErrNotFound
	ErrExists   = models.ErrExists
	ErrInvalid  = models.ErrInvalid
)

// -- Internals ----------------------------------------------------------------

func validID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 16 && id == strings.ToLower(id)
}

func stamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
