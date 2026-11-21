package domain

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
	ErrExists   = errors.New("owner already tracks a case or identity already used")
	ErrInvalid  = errors.New("invalid storage input")
)

type Store interface {
	TrackedCases() TrackedCaseRepository
	TrackedCasesHistory() TrackedCasesHistoryRepository
	Cooldowns() CooldownRepository
	RunInTx(context.Context, func(Store) error) error
}

type TrackedCaseRepository interface {
	FindByOwner(ctx context.Context, owner int64) (TrackedCase, error)
	FindByIdentity(ctx context.Context, owner int64, id string) (TrackedCase, error)
	FindAll(ctx context.Context) ([]TrackedCase, error)
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

type TrackedCasesHistoryRepository interface {
	FindByTrackedCase(ctx context.Context, owner int64, id string) ([]Transition, error)
	Append(
		ctx context.Context,
		id string,
		previous string,
		stage string,
		checkedAt time.Time,
	) error
	DeleteByTrackedCaseID(ctx context.Context, id string) error
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

// NewTrackedCaseID allocates an opaque identity used to fence late work.
func NewTrackedCaseID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", ErrStorage
	}
	return hex.EncodeToString(b), nil
}
