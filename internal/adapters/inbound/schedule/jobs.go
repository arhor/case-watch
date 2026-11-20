package schedule

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	_ "time/tzdata" // Keep Warsaw rules available in minimal containers.

	"github.com/arhor/case-watch/internal/models"
	"github.com/arhor/case-watch/internal/platform/guard"
	"github.com/robfig/cron/v3"
)

var ErrScheduler = errors.New("scheduler operation failed")

type Cases interface {
	ScheduledOwners(context.Context) ([]int64, error)
	// Scheduled shares manual coordination and returns after canceled work drains.
	Scheduled(context.Context, int64) (models.Status, error)
}

type StageChangeNotifier interface {
	NotifyStageChanged(context.Context, int64, models.StageChange) error
}

type Config struct {
	Notifier    StageChangeNotifier
	CaseService Cases
	Clock       models.Clock
	// Wait waits until the absolute deadline, or returns the context error.
	// Nil uses a stoppable standard-library timer.
	Wait func(context.Context, time.Time) error
}

type Jobs struct {
	c        Config
	schedule cron.Schedule
	running  sync.Mutex
}

func (this *Jobs) Next(after time.Time) time.Time {
	return this.schedule.Next(after)
}

// Run does no startup check. Runs and case calls are sequential; computing the
// next slot after completion discards every slot crossed by a slow run. Repeated
// concurrent Run calls are rejected rather than queued. Cancellation drains the
// active Scheduled call before returning; the shared case service owns its I/O.
func (this *Jobs) Run(ctx context.Context) error {
	if !this.running.TryLock() {
		return ErrScheduler
	}
	defer this.running.Unlock()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slot := this.Next(this.c.Clock.Now())
		if err := this.c.Wait(ctx, slot); err != nil {
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
		if now := this.c.Clock.Now(); now.Before(slot) || !now.Before(slot.Add(time.Minute)) {
			continue
		}
		owners, err := this.c.CaseService.ScheduledOwners(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrScheduler
		}
		for _, owner := range owners {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			status, err := this.c.CaseService.Scheduled(ctx, owner)
			if err != nil {
				if ctx.Err() == nil {
					log.Print("scheduled case check failed")
				}
				continue
			}
			if status.Change != nil && ctx.Err() == nil {
				if err := this.c.Notifier.NotifyStageChanged(ctx, owner, *status.Change); err != nil && ctx.Err() == nil {
					log.Print("stage change notification failed")
				}
			}
		}
	}
}

func New(c Config) *Jobs {
	c.CaseService = guard.Require("CaseService", c.CaseService)
	c.Clock = guard.Require("Clock", c.Clock)
	c.Notifier = guard.Require("Notifier", c.Notifier)
	schedule := guard.Must(cron.ParseStandard("CRON_TZ=Europe/Warsaw 0 8,10,12,14,16,18 * * 1-5"))
	if c.Wait == nil {
		c.Wait = wait
	}
	return &Jobs{c: c, schedule: schedule}
}

// Next returns the first strictly future Warsaw slot. Startup at a slot never
// replays that slot; holidays use the same weekday rules as every other date.

// -- Internals ----------------------------------------------------------------

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
