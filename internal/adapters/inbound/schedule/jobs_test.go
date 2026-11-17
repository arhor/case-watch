package schedule_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/adapters/inbound/schedule"
	"github.com/arhor/case-watch/internal/adapters/outbound/database"
	"github.com/arhor/case-watch/internal/adapters/outbound/database/repositories"
	"github.com/arhor/case-watch/internal/models"
	"github.com/arhor/case-watch/internal/services"
	"github.com/arhor/case-watch/internal/testing/assert"
)

type clock struct{ now time.Time }

func (this *clock) Now() time.Time { return this.now }

type tracking struct {
	owners    []int64
	scheduled func(context.Context, int64) (models.Status, error)
}

func (this tracking) Scheduled(ctx context.Context, owner int64) (models.Status, error) {
	return this.scheduled(ctx, owner)
}
func (this tracking) ScheduledOwners(context.Context) ([]int64, error) { return this.owners, nil }
func parse(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
func newScheduler(t *testing.T, c schedule.Config) *schedule.Jobs {
	t.Helper()
	s, err := schedule.New(c)
	assert.NoError(t, err)
	return s
}
func TestNextShouldReturnStrictWarsawSlotsWhenDatesCrossSeasonsWeekendsAndHolidays(t *testing.T) {
	// Given
	s := newScheduler(t, schedule.Config{CaseService: tracking{scheduled: func(context.Context, int64) (models.Status, error) { return models.Status{}, nil }}, Clock: &clock{}})
	cases := []struct{ after, want string }{
		{"2026-01-05T06:59:59Z", "2026-01-05T07:00:00Z"},
		{"2026-07-06T05:59:59Z", "2026-07-06T06:00:00Z"},
		{"2026-01-05T07:00:00Z", "2026-01-05T09:00:00Z"},
		{"2026-01-05T06:59:59.999999999Z", "2026-01-05T07:00:00Z"},
		{"2026-01-05T07:00:00.000000001Z", "2026-01-05T09:00:00Z"},
		{"2026-12-31T17:00:00Z", "2027-01-01T07:00:00Z"},
		{"2026-07-06T08:00:00Z", "2026-07-06T10:00:00Z"},
		{"2026-07-06T10:00:00Z", "2026-07-06T12:00:00Z"},
		{"2026-07-06T12:00:00Z", "2026-07-06T14:00:00Z"},
		{"2026-07-06T14:00:00Z", "2026-07-06T16:00:00Z"},
		{"2026-03-27T17:00:00Z", "2026-03-30T06:00:00Z"},
		{"2026-10-23T16:00:00Z", "2026-10-26T07:00:00Z"},
		{"2026-12-24T17:00:00Z", "2026-12-25T07:00:00Z"},
		{"2026-07-04T12:00:00-07:00", "2026-07-06T06:00:00Z"},
	}
	// When
	var got []time.Time
	for _, c := range cases {
		got = append(got, s.Next(parse(c.after)))
	}
	// Then
	for i, c := range cases {
		assert.True(t, got[i].Equal(parse(c.want)), fmt.Sprintf("next slot after %s should be %s, got %s", c.after, c.want, got[i]))
	}
}
func TestRunShouldSkipCrossedSlotsAndRecheckQueuedAuthorizationWhenFirstCheckIsSlow(t *testing.T) {
	// Given
	c := &clock{now: parse("2026-07-06T05:00:00Z")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls []int64
	var deadlines []time.Time
	s := newScheduler(t, schedule.Config{Clock: c, CaseService: tracking{owners: []int64{1, 2, 3}, scheduled: func(_ context.Context, owner int64) (models.Status, error) {
		calls = append(calls, owner)
		if owner == 1 {
			c.now = parse("2026-07-06T10:30:00Z")
		}
		return models.Status{}, errors.New("synthetic failure")
	}}, Wait: func(_ context.Context, deadline time.Time) error {
		deadlines = append(deadlines, deadline)
		if len(deadlines) == 2 {
			cancel()
			return ctx.Err()
		}
		c.now = deadline
		return nil
	}})
	// When
	err := s.Run(ctx)
	// Then
	assert.ErrorIs(t, err, context.Canceled)
	assert.DeepEqual(t, []int64{1, 2, 3}, calls, "calls should match the expected values")
	assert.Equal(t, 2, len(deadlines), "len(deadlines) should match the expected value")
	assert.True(t, deadlines[1].Equal(parse("2026-07-06T12:00:00Z")), "deadlines[1].Equal(parse(\"2026-07-06T12:00:00Z\")) should hold")
}
func TestRunShouldWaitForFutureSlotWhenStartingAtBoundaryOrRestartingAfterMissedSlots(t *testing.T) {
	// Given
	for _, start := range []string{"2026-07-06T06:00:00Z", "2026-07-06T07:59:59Z"} {
		ctx, cancel := context.WithCancel(context.Background())
		c := &clock{now: parse(start)}
		calls := 0
		var deadline time.Time
		s := newScheduler(t, schedule.Config{Clock: c, CaseService: tracking{owners: []int64{1}, scheduled: func(context.Context, int64) (models.Status, error) { calls++; return models.Status{}, nil }}, Wait: func(_ context.Context, d time.Time) error { deadline = d; cancel(); return ctx.Err() }})
		// When
		err := s.Run(ctx)
		// Then
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 0, calls, "calls should match the expected value")
		assert.True(t, deadline.Equal(parse("2026-07-06T08:00:00Z")), "deadline.Equal(parse(\"2026-07-06T08:00:00Z\")) should hold")
	}
}
func TestRunShouldDrainActiveCallAndRejectOverlapWhenCanceled(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &clock{now: parse("2026-07-06T05:00:00Z")}
	entered := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	s := newScheduler(t, schedule.Config{Clock: c, CaseService: tracking{owners: []int64{1, 2}, scheduled: func(ctx context.Context, _ int64) (models.Status, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return models.Status{}, ctx.Err()
	}}, Wait: func(_ context.Context, d time.Time) error { c.now = d; return nil }})
	// When
	go func() { done <- s.Run(ctx) }()
	<-entered
	overlap := s.Run(context.Background())
	cancel()
	<-canceled
	// Then
	select {
	case err := <-done:
		assert.True(t, false, fmt.Sprintf("scheduler should wait for the active call to drain, got %v", err))
	default:
	}
	assert.ErrorIs(t, overlap, schedule.ErrScheduler)
	close(release)
	{
		err := <-done
		assert.ErrorIs(t, err, context.Canceled)
	}
}

func TestRunShouldSkipMissedMinuteAndAcceptTimerJitterWhenWaitReturnsLate(t *testing.T) {
	// Given
	c := &clock{now: parse("2026-07-06T05:00:00Z")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waits, calls := 0, 0
	s := newScheduler(t, schedule.Config{Clock: c, CaseService: tracking{owners: []int64{1}, scheduled: func(context.Context, int64) (models.Status, error) { calls++; return models.Status{}, nil }}, Wait: func(_ context.Context, d time.Time) error {
		waits++
		switch waits {
		case 1:
			c.now = d.Add(time.Minute)
		case 2:
			c.now = d.Add(time.Millisecond)
		default:
			cancel()
			return ctx.Err()
		}
		return nil
	}})
	// When
	err := s.Run(ctx)
	// Then
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls, "calls should match the expected value")
	assert.Equal(t, 3, waits, "waits should match the expected value")
}

func TestScheduledJobsShouldShareManualGuardWhenSlotFollowsManualAttempt(t *testing.T) {
	// Given
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	assert.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	c := &clock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	calls := 0
	caseService := services.NewCaseService(services.CaseServiceConfig{
		Store: repositories.NewStore(db),
		Portal: scheduledPortal(func(context.Context, string, string) (string, time.Time, error) {
			calls++
			return "A", time.Time{}, nil
		}),
		Clock: c,
	})
	t.Cleanup(caseService.Shutdown)
	{
		_, _, err := caseService.Enroll(ctx, 101, "synthetic-number", "synthetic-password")
		assert.NoError(t, err)
	}
	monday := time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
	c.now = monday.Add(-time.Second)
	_, manualErr := caseService.Status(ctx, 101)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	waits := 0
	s, err := schedule.New(schedule.Config{
		CaseService: caseService,
		Clock:       c,
		Wait: func(_ context.Context, deadline time.Time) error {
			waits++
			if waits == 2 {
				cancel()
				return runCtx.Err()
			}
			c.now = deadline
			return nil
		},
	})
	assert.NoError(t, err)
	// When
	runErr := s.Run(runCtx)
	// Then
	assert.NoError(t, manualErr)
	assert.ErrorIs(t, runErr, context.Canceled)
	assert.Equal(t, 2, calls, "calls should match the expected value")
	assert.Equal(t, 2, waits, "waits should match the expected value")
}

type scheduledPortal func(context.Context, string, string) (string, time.Time, error)

func (this scheduledPortal) Check(ctx context.Context, _ int64, number, password string) (string, time.Time, error) {
	return this(ctx, number, password)
}

func (scheduledPortal) ForgetSession(int64) {}
