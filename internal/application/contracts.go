// Package application defines transport-independent Case Watch contracts.
// See docs/product-contract.md and docs/adr/0003_choose-telegram-bot-integration.md.
// These are boundaries, not implementations; adapters must not leak SQL or Telegram types.
package application

import (
	"context"
	"time"
)

// Credentials are transient plaintext. Never log, persist, or include them in errors.
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

// Policy is shared by interactive commands and background workers.
// The transport must first establish an ordinary private chat and valid sender.
type Policy interface {
	Allows(userID int64) bool
}

// Clock lets coordinators and delivery workers use deterministic time in tests.
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

// Tracking is the authenticated owner-facing use case boundary. Enrollment is
// called only after the transport's ephemeral, separate-message draft is complete.
// Enroll refuses replacement and commits encrypted credentials plus baseline
// atomically. Confirmation is sent separately after success.
// Remove is called only after explicit, unexpired owner confirmation. It cancels
// and drains checks/sends and rejects late results before returning success.
type Tracking interface {
	Enroll(context.Context, int64, Credentials) (Observation, error)
	Status(context.Context, int64) (Status, error)
	Remove(context.Context, int64) error
}

// SealedCredentials contains only authenticated ciphertext and format metadata.
// Credential ID is also the immutable destination identity used for AAD binding.
type SealedCredentials struct {
	ID            string
	FormatVersion int
	KeyID         string
	Payload       []byte
}

type SendCategory string

const (
	SendAccepted     SendCategory = "accepted"
	SendTransient    SendCategory = "transient"
	SendRateLimited  SendCategory = "rate_limited"
	SendBlocked      SendCategory = "blocked"
	SendInvalidToken SendCategory = "invalid_token"
	SendPermanent    SendCategory = "permanent"
)

// SendOutcome carries sanitized classification, never raw transport errors.
// RetryAfter is a minimum duration reported by the remote transport.
type SendOutcome struct {
	Category   SendCategory
	MessageID  int64
	RetryAfter time.Duration
}

// Sender makes one attempt; durable reservation/retry policy belongs to callers.
// Text contains no credentials and must not appear in diagnostics.
type Sender interface {
	Send(context.Context, int64, string) (SendOutcome, error)
}
