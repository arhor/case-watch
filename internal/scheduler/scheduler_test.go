package scheduler_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/scheduler"
	"github.com/arhor/case-watch/internal/storage"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

type inventory struct{ owners []int64 }

func (i inventory) Enrollments(context.Context) ([]storage.Enrollment, error) {
	var result []storage.Enrollment
	for _, owner := range i.owners {
		result = append(result, storage.Enrollment{OwnerID: owner})
	}
	return result, nil
}

type policy map[int64]bool

func (p policy) Allows(owner int64) bool { return p[owner] }

type tracking func(context.Context, int64) (application.Status, error)

func (f tracking) Scheduled(ctx context.Context, owner int64) (application.Status, error) {
	return f(ctx, owner)
}
func parse(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
func newScheduler(t *testing.T, c scheduler.Config) *scheduler.Scheduler {
	t.Helper()
	s, err := scheduler.New(c)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestNextShouldReturnStrictWarsawSlotsWhenDatesCrossSeasonsWeekendsAndHolidays(t *testing.T) {
	// Given
	s := newScheduler(t, scheduler.Config{Inventory: inventory{}, Tracking: tracking(func(context.Context, int64) (application.Status, error) { return application.Status{}, nil }), Policy: policy{}, Clock: &clock{}})
	cases := []struct{ after, want string }{
		{"2026-01-05T06:59:59Z", "2026-01-05T07:00:00Z"},
		{"2026-07-06T05:59:59Z", "2026-07-06T06:00:00Z"},
		{"2026-01-05T07:00:00Z", "2026-01-05T09:00:00Z"},
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
		if !got[i].Equal(parse(c.want)) {
			t.Errorf("after %s: %s want %s", c.after, got[i], c.want)
		}
	}
}
func TestRunShouldSkipCrossedSlotsAndRecheckQueuedAuthorizationWhenFirstCheckIsSlow(t *testing.T) {
	// Given
	c := &clock{now: parse("2026-07-06T05:00:00Z")}
	p := policy{1: true, 2: true, 3: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls []int64
	var deadlines []time.Time
	s := newScheduler(t, scheduler.Config{Inventory: inventory{[]int64{1, 2, 3}}, Policy: p, Clock: c, Tracking: tracking(func(_ context.Context, owner int64) (application.Status, error) {
		calls = append(calls, owner)
		if owner == 1 {
			c.now = parse("2026-07-06T10:30:00Z")
			p[2] = false
		}
		return application.Status{}, errors.New("synthetic failure")
	}), Wait: func(_ context.Context, deadline time.Time) error {
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
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(calls, []int64{1, 3}) || len(deadlines) != 2 || !deadlines[1].Equal(parse("2026-07-06T12:00:00Z")) {
		t.Fatalf("calls=%v deadlines=%v err=%v", calls, deadlines, err)
	}
}
func TestRunShouldWaitForFutureSlotWhenStartingAtBoundaryOrRestartingAfterMissedSlots(t *testing.T) {
	// Given
	for _, start := range []string{"2026-07-06T06:00:00Z", "2026-07-06T07:59:59Z"} {
		ctx, cancel := context.WithCancel(context.Background())
		c := &clock{now: parse(start)}
		calls := 0
		var deadline time.Time
		s := newScheduler(t, scheduler.Config{Inventory: inventory{[]int64{1}}, Policy: policy{1: true}, Clock: c, Tracking: tracking(func(context.Context, int64) (application.Status, error) { calls++; return application.Status{}, nil }), Wait: func(_ context.Context, d time.Time) error { deadline = d; cancel(); return ctx.Err() }})
		// When
		err := s.Run(ctx)
		// Then
		if !errors.Is(err, context.Canceled) || calls != 0 || !deadline.Equal(parse("2026-07-06T08:00:00Z")) {
			t.Fatalf("start=%s deadline=%s calls=%d err=%v", start, deadline, calls, err)
		}
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
	s := newScheduler(t, scheduler.Config{Inventory: inventory{[]int64{1, 2}}, Policy: policy{1: true, 2: true}, Clock: c, Tracking: tracking(func(ctx context.Context, _ int64) (application.Status, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return application.Status{}, ctx.Err()
	}), Wait: func(_ context.Context, d time.Time) error { c.now = d; return nil }})
	// When
	go func() { done <- s.Run(ctx) }()
	<-entered
	overlap := s.Run(context.Background())
	cancel()
	<-canceled
	// Then
	select {
	case err := <-done:
		t.Fatalf("returned before drain: %v", err)
	default:
	}
	if !errors.Is(overlap, scheduler.ErrScheduler) {
		t.Fatalf("overlap=%v", overlap)
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRunShouldSkipMissedMinuteAndAcceptTimerJitterWhenWaitReturnsLate(t *testing.T) {
	// Given
	c := &clock{now: parse("2026-07-06T05:00:00Z")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waits, calls := 0, 0
	s := newScheduler(t, scheduler.Config{Inventory: inventory{[]int64{1}}, Policy: policy{1: true}, Clock: c, Tracking: tracking(func(context.Context, int64) (application.Status, error) { calls++; return application.Status{}, nil }), Wait: func(_ context.Context, d time.Time) error {
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
	if !errors.Is(err, context.Canceled) || calls != 1 || waits != 3 {
		t.Fatalf("err=%v calls=%d waits=%d", err, calls, waits)
	}
}
