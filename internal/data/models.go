package data

import (
	"time"

	"github.com/arhor/case-watch/internal/application"
)

type Enrollment struct {
	OwnerID     int64
	Credentials application.SealedCredentials
	Observation application.Observation
}

type Transition struct {
	Sequence      int64
	EnrollmentID  string
	PreviousStage string
	Stage         string
	CheckedAt     time.Time
}

type CheckReservation struct {
	Reserved       bool
	NextEligibleAt time.Time
}

type Delivery struct {
	Sequence         int64
	RecipientID      int64
	EnrollmentID     string
	PreviousStage    string
	Stage            string
	CheckedAt        time.Time
	Status           string
	CycleAttempts    int
	LifetimeAttempts int
	NextAttemptAt    time.Time
	ReservedAt       time.Time
	Failure          string
	MessageID        int64
}

type RecipientState struct {
	Pause   string
	Blocked bool
}

// Cooldown records the last attempt start for a typed subject.
type Cooldown struct {
	SubjectType string
	SubjectID   string
	StartedAt   time.Time
}
