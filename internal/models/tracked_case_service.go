package models

import "context"

// TrackedCaseService provides scheduled checks for tracked cases.
type TrackedCaseService interface {
	ScheduledOwners(context.Context) ([]int64, error)
	// Scheduled shares manual coordination and returns after canceled work drains.
	Scheduled(context.Context, int64) (Status, error)
}
