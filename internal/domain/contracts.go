package domain

import (
	"context"
	"time"
)

// Observation preserves the portal's stage text verbatim.
type Observation struct {
	Stage     string
	CheckedAt time.Time
}

// Portal performs one bounded logical check without automatic retries.
type Portal interface {
	Check(ctx context.Context, Number, Password string) (Observation, error)
}

// Status separates successful cache age from attempt eligibility. A failed refresh
// may return both a previous observation and an error; it must not relabel it fresh.
type Status struct {
	Observation    *Observation
	Fresh          bool
	InProgress     bool
	NextEligibleAt time.Time
}
