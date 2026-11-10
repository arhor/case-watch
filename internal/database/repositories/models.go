package repositories

import "time"

type Enrollment struct {
	ID               string
	OwnerID          int64
	Number, Password string
	Stage            string
	CheckedAt        time.Time
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

// Cooldown records the last attempt start for a typed subject.
type Cooldown struct {
	SubjectType string
	SubjectID   string
	StartedAt   time.Time
}
