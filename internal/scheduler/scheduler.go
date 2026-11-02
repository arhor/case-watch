// Package scheduler implements the Warsaw weekday slots in ADR 0007.
package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"
	_ "time/tzdata" // Keep Warsaw rules available in minimal containers.

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/data"
)

var ErrScheduler = errors.New("scheduler operation failed")

type Inventory interface {
	Enrollments(context.Context) ([]data.Enrollment, error)
}
type Tracking interface {
	// Scheduled shares manual coordination and returns after canceled work drains.
	Scheduled(context.Context, int64) (application.Status, error)
}
type Config struct {
	Inventory Inventory
	Tracking  Tracking
	Policy    application.Policy
	Clock     application.Clock
	// Wait waits until the absolute deadline, or returns the context error.
	// Nil uses a stoppable standard-library timer.
	Wait func(context.Context, time.Time) error
	// Report receives fixed categories only, never collaborator errors.
	Report func(string)
}
type Scheduler struct {
	c       Config
	zone    *time.Location
	running sync.Mutex
}

func New(c Config) (*Scheduler, error) {
	if c.Inventory == nil || c.Tracking == nil || c.Policy == nil || c.Clock == nil {
		return nil, ErrScheduler
	}
	zone, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		return nil, ErrScheduler
	}
	if c.Wait == nil {
		c.Wait = wait
	}
	return &Scheduler{c: c, zone: zone}, nil
}

// Next returns the first strictly future Warsaw slot. Startup at a slot never
// replays that slot; holidays use the same weekday rules as every other date.
func (s *Scheduler) Next(after time.Time) time.Time {
	local := after.In(s.zone)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.zone)
	for {
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			for _, hour := range [...]int{8, 10, 12, 14, 16, 18} {
				slot := time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, s.zone)
				if slot.After(after) {
					return slot
				}
			}
		}
		day = day.AddDate(0, 0, 1)
	}
}

func wait(ctx context.Context, deadline time.Time) error {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (s *Scheduler) report(category string) {
	if s.c.Report != nil {
		s.c.Report(category)
	}
}

// Run does no startup check. Runs and case calls are sequential; computing the
// next slot after completion discards every slot crossed by a slow run. Repeated
// concurrent Run calls are rejected rather than queued. Cancellation drains the
// active Scheduled call before returning; the shared coordinator owns its I/O.
func (s *Scheduler) Run(ctx context.Context) error {
	if !s.running.TryLock() {
		return ErrScheduler
	}
	defer s.running.Unlock()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slot := s.Next(s.c.Clock.Now())
		if err := s.c.Wait(ctx, slot); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrScheduler
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Allow ordinary timer jitter within the cron minute; suspension past
		// that minute misses the slot and must not trigger catch-up.
		if now := s.c.Clock.Now(); now.Before(slot) || !now.Before(slot.Add(time.Minute)) {
			continue
		}
		items, err := s.c.Inventory.Enrollments(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.report("inventory_failed")
			continue
		}
		for _, item := range items {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if item.OwnerID <= 0 || !s.c.Policy.Allows(item.OwnerID) {
				continue
			}
			if _, err := s.c.Tracking.Scheduled(ctx, item.OwnerID); err != nil {
				s.report("check_failed")
			}
		}
	}
}
