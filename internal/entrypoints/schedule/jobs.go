// Package schedule initiates case workflows at Warsaw weekday slots (ADR 0007).
package schedule

import (
	"context"
	"errors"
	"sync"
	"time"
	_ "time/tzdata" // Keep Warsaw rules available in minimal containers.

	"github.com/robfig/cron/v3"

	"github.com/arhor/case-watch/internal/models"
	"github.com/arhor/case-watch/internal/utils"
)

var ErrScheduler = errors.New("scheduler operation failed")

type Cases interface {
	ScheduledOwners(context.Context) ([]int64, error)
	// Scheduled shares manual coordination and returns after canceled work drains.
	Scheduled(context.Context, int64) (models.Status, error)
}

type Config struct {
	CaseService Cases
	Clock       utils.Clock
	// Wait waits until the absolute deadline, or returns the context error.
	// Nil uses a stoppable standard-library timer.
	Wait func(context.Context, time.Time) error
	// Report receives fixed categories only, never collaborator errors.
	Report func(string)
}

type Jobs struct {
	c        Config
	schedule cron.Schedule
	running  sync.Mutex
}

func New(c Config) (*Jobs, error) {
	if c.CaseService == nil || c.Clock == nil {
		return nil, ErrScheduler
	}
	schedule, err := cron.ParseStandard("CRON_TZ=Europe/Warsaw 0 8,10,12,14,16,18 * * 1-5")
	if err != nil {
		return nil, ErrScheduler
	}
	if c.Wait == nil {
		c.Wait = wait
	}
	return &Jobs{c: c, schedule: schedule}, nil
}

// Next returns the first strictly future Warsaw slot. Startup at a slot never
// replays that slot; holidays use the same weekday rules as every other date.
func (s *Jobs) Next(after time.Time) time.Time {
	return s.schedule.Next(after)
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
func (s *Jobs) report(category string) {
	if s.c.Report != nil {
		s.c.Report(category)
	}
}

// Run does no startup check. Runs and case calls are sequential; computing the
// next slot after completion discards every slot crossed by a slow run. Repeated
// concurrent Run calls are rejected rather than queued. Cancellation drains the
// active Scheduled call before returning; the shared case service owns its I/O.
func (s *Jobs) Run(ctx context.Context) error {
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
		owners, err := s.c.CaseService.ScheduledOwners(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.report("inventory_failed")
			continue
		}
		for _, owner := range owners {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if _, err := s.c.CaseService.Scheduled(ctx, owner); err != nil {
				s.report("check_failed")
			}
		}
	}
}
