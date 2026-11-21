package models

import (
	"context"
	"time"
)

// TrackedCaseService provides business operations for tracked cases.
// Calls may use a cached result when a new portal check is not eligible.
type TrackedCaseService interface {
	HasTrackedCase(context.Context, int64) (bool, error)
	StoreCaseToTrack(context.Context, int64, string, string) (string, time.Time, error)
	CheckCaseStatus(context.Context, int64) (Status, error)
	RunScheduledCaseCheck(context.Context, int64) (Status, error)
	ListTrackedCaseOwners(context.Context) ([]int64, error)
	RemoveTrackedCase(context.Context, int64) error
}
