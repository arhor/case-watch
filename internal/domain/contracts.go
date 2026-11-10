package domain

import (
	"context"
	"time"
)

// Credentials are plaintext. They are persisted only on the enrollment record
// during WIP development; never log or include them in errors or messages.
// Dropping references does not guarantee secure erasure of Go memory.
type Credentials struct {
	Number   string
	Password string
}

// Observation preserves the portal's stage text verbatim.
type Observation struct {
	Stage     string
	CheckedAt time.Time
}

// Portal performs one bounded logical check without automatic retries.
type Portal interface {
	Check(ctx context.Context, credentials Credentials) (Observation, error)
}

// Status separates successful cache age from attempt eligibility. A failed refresh
// may return both a previous observation and an error; it must not relabel it fresh.
type Status struct {
	Observation    *Observation
	Fresh          bool
	InProgress     bool
	NextEligibleAt time.Time
}
