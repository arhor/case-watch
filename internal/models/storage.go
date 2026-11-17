package models

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

var (
	ErrStorage  = errors.New("storage operation failed")
	ErrNotFound = errors.New("record not found")
	ErrExists   = errors.New("owner already enrolled or identity already used")
	ErrInvalid  = errors.New("invalid storage input")
)

type Store interface {
	Enrollments() EnrollmentRepository
	History() HistoryRepository
	Cooldowns() CooldownRepository
	RunInTx(context.Context, func(Store) error) error
}

type EnrollmentRepository interface {
	FindByOwner(ctx context.Context, owner int64) (Enrollment, error)
	FindByIdentity(ctx context.Context, owner int64, id string) (Enrollment, error)
	FindAll(ctx context.Context) ([]Enrollment, error)
	Create(
		ctx context.Context,
		owner int64,
		id string,
		number string,
		password string,
		stage string,
		checkedAt time.Time,
	) error
	UpdateObservation(
		ctx context.Context,
		owner int64,
		id string,
		stage string,
		checkedAt time.Time,
	) error
	SoftDelete(ctx context.Context, owner int64, id string, now time.Time) error
}

type HistoryRepository interface {
	FindByEnrollment(ctx context.Context, owner int64, id string) ([]Transition, error)
	Append(
		ctx context.Context,
		id string,
		previous string,
		stage string,
		checkedAt time.Time,
	) error
	DeleteByEnrollmentID(ctx context.Context, id string) error
}

type CooldownRepository interface {
	FindBySubjectTypeAndSubjectID(
		ctx context.Context,
		subjectType string,
		subjectID string,
	) (Cooldown, error)
	Upsert(
		ctx context.Context,
		c Cooldown,
	) error
	DeleteBySubjectTypeAndSubjectID(
		ctx context.Context,
		subjectType,
		subjectID string,
	) error
}

// NewEnrollmentID allocates an opaque identity used to fence late work.
func NewEnrollmentID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", ErrStorage
	}
	return hex.EncodeToString(b), nil
}
