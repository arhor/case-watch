package models

import (
	"context"
	"time"
)

// Portal performs one bounded logical check without automatic retries and
// returns the portal's stage text verbatim with its completion time.
type Portal interface {
	Check(ctx context.Context, ownerID int64, number, password string) (stage string, checkedAt time.Time, err error)
	ForgetSession(ownerID int64)
}

// StageChange describes a committed observation that changed the stored stage.
type StageChange struct {
	PreviousStage string
	Stage         string
	CheckedAt     time.Time
}

// Status separates successful cache age from attempt eligibility. A failed refresh
// may return both a previous observation and an error; it must not relabel it fresh.
// A zero CheckedAt means no successful observation is available.
type Status struct {
	// Change is present only on the fresh result of the check that committed it.
	Change         *StageChange
	Stage          string
	CheckedAt      time.Time
	Fresh          bool
	InProgress     bool
	NextEligibleAt time.Time
}

type TrackedCase struct {
	ID               string
	OwnerID          int64
	Number, Password string
	Stage            string
	CheckedAt        time.Time
}

type Transition struct {
	Sequence      int64
	TrackedCaseID string
	PreviousStage string
	Stage         string
	CheckedAt     time.Time
}

type CheckReservation struct {
	Reserved       bool
	NextEligibleAt time.Time
}

// Cooldown records the last attempt start for a typed subject.
type Cooldown struct {
	SubjectType string
	SubjectID   string
	StartedAt   time.Time
}
