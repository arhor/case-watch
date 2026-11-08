// Package domain defines values and the small collaborator interfaces shared by
// Case Watch services, repositories, and entry points.
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
	Check(context.Context, Credentials) (Observation, error)
}

// Clock lets case services and reply senders use deterministic time in tests.
type Clock interface {
	Now() time.Time
}

// Status separates successful cache age from attempt eligibility. A failed refresh
// may return both a previous observation and an error; it must not relabel it fresh.
type Status struct {
	Observation    *Observation
	Fresh          bool
	InProgress     bool
	NextEligibleAt time.Time
}

type SendCategory string

const (
	SendAccepted     SendCategory = "accepted"
	SendFailed       SendCategory = "failed"
	SendRateLimited  SendCategory = "rate_limited"
	SendInvalidToken SendCategory = "invalid_token"
)

// SendOutcome carries sanitized classification, never raw transport errors.
// RetryAfter is a minimum duration reported by the remote transport.
type SendOutcome struct {
	Category   SendCategory
	RetryAfter time.Duration
}

// Sender makes one attempt without automatic retries.
// Text contains no credentials and must not appear in diagnostics.
type Sender interface {
	Send(context.Context, int64, string) (SendOutcome, error)
}
