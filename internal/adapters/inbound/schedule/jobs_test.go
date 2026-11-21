package schedule_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/adapters/inbound/schedule"
	"github.com/arhor/case-watch/internal/adapters/outbound/database"
	"github.com/arhor/case-watch/internal/adapters/outbound/database/repositories"
	"github.com/arhor/case-watch/internal/models"
	"github.com/arhor/case-watch/internal/services"
	"github.com/arhor/case-watch/internal/testing/assert"
	"github.com/arhor/case-watch/internal/testing/clock"
)

func TestJobsNew(test *testing.T) {
	test.Run("should panic when case service is missing", func(test *testing.T) {
		// Given
		config := schedule.Config{Clock: clock.NewFixedClock(time.Time{})}
		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			schedule.New(config)
		}()

		// Then
		assert.Equal[any](test, "required property is missing: TrackedCaseService", recovered)
	})

	test.Run("should panic when clock is missing", func(test *testing.T) {
		// Given
		config := schedule.Config{TrackedCaseService: tracking{}}
		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			schedule.New(config)
		}()

		// Then
		assert.Equal[any](test, "required property is missing: Clock", recovered)
	})
}

func TestJobsNext(test *testing.T) {
	for _, scenario := range []struct{ name, after, expected string }{
		{
			name:     "should return next Warsaw slot for winter opening",
			after:    "2026-01-05T06:59:59Z",
			expected: "2026-01-05T07:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for summer opening",
			after:    "2026-07-06T05:59:59Z",
			expected: "2026-07-06T06:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for exact slot boundary",
			after:    "2026-01-05T07:00:00Z",
			expected: "2026-01-05T09:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for nanosecond before slot",
			after:    "2026-01-05T06:59:59.999999999Z",
			expected: "2026-01-05T07:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for nanosecond after slot",
			after:    "2026-01-05T07:00:00.000000001Z",
			expected: "2026-01-05T09:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for year rollover",
			after:    "2026-12-31T17:00:00Z",
			expected: "2027-01-01T07:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for summer noon",
			after:    "2026-07-06T08:00:00Z",
			expected: "2026-07-06T10:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for summer afternoon",
			after:    "2026-07-06T10:00:00Z",
			expected: "2026-07-06T12:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for summer late afternoon",
			after:    "2026-07-06T12:00:00Z",
			expected: "2026-07-06T14:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for summer final slot",
			after:    "2026-07-06T14:00:00Z",
			expected: "2026-07-06T16:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for spring clock change",
			after:    "2026-03-27T17:00:00Z",
			expected: "2026-03-30T06:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for autumn clock change",
			after:    "2026-10-23T16:00:00Z",
			expected: "2026-10-26T07:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for weekday holiday",
			after:    "2026-12-24T17:00:00Z",
			expected: "2026-12-25T07:00:00Z",
		},
		{
			name:     "should return next Warsaw slot for weekend in another timezone",
			after:    "2026-07-04T12:00:00-07:00",
			expected: "2026-07-06T06:00:00Z",
		},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			// Given
			scheduler := newScheduler(test, schedule.Config{TrackedCaseService: tracking{}, Clock: clock.NewFixedClock(time.Time{})})
			after := parseTime(scenario.after)
			expected := parseTime(scenario.expected)

			// When
			actual := scheduler.Next(after)

			// Then
			assert.Equal(test, expected, actual)
		})
	}
}

func TestJobsRun(test *testing.T) {
	test.Run("should stop when scheduled owner inventory fails", func(test *testing.T) {
		// Given
		clock := clock.NewFixedClock(parseTime("2026-07-06T05:00:00Z"))
		scheduler := newScheduler(test, schedule.Config{
			Clock:              clock,
			TrackedCaseService: tracking{
				ownersError: errors.New("private storage details"),
			},
			Wait: func(_ context.Context, deadline time.Time) error {
				clock.Set(deadline)
				return nil
			},
		})

		// When
		err := scheduler.Run(context.Background())

		// Then
		assert.ErrorIs(test, err, schedule.ErrScheduler)
		assert.Equal(test, "scheduler operation failed", err.Error(), "internal storage details should not leak")
	})

	test.Run("should skip crossed slots and recheck queued authorization when first check is slow", func(test *testing.T) {
		// Given
		clock := clock.NewFixedClock(parseTime("2026-07-06T05:00:00Z"))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var calls []int64
		var deadlines []time.Time
		scheduler := newScheduler(test, schedule.Config{
			Clock:              clock,
			TrackedCaseService: tracking{
				owners: []int64{1, 2, 3},
				scheduled: func(_ context.Context, owner int64) (models.Status, error) {
					calls = append(calls, owner)
					if owner == 1 {
						clock.Set(parseTime("2026-07-06T10:30:00Z"))
					}
					return models.Status{}, errors.New("synthetic failure")
				},
			},
			Wait: func(_ context.Context, deadline time.Time) error {
				deadlines = append(deadlines, deadline)
				if len(deadlines) == 2 {
					cancel()
					return ctx.Err()
				}
				clock.Set(deadline)
				return nil
			},
		})

		// When
		err := scheduler.Run(ctx)

		// Then
		assert.ErrorIs(test, err, context.Canceled)
		assert.DeepEqual(test, []int64{1, 2, 3}, calls)
		assert.Equal(test, 2, len(deadlines))
		assert.Equal(test, parseTime("2026-07-06T12:00:00Z"), deadlines[1])
	})

	for _, start := range []string{"2026-07-06T06:00:00Z", "2026-07-06T07:59:59Z"} {
		test.Run("should wait for future slot when starting at "+start, func(test *testing.T) {
			// Given
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			clock := clock.NewFixedClock(parseTime(start))
			calls := 0
			var waitedUntil time.Time
			scheduler := newScheduler(test, schedule.Config{
				Clock:              clock,
				TrackedCaseService: tracking{
					owners: []int64{1},
					scheduled: func(context.Context, int64) (models.Status, error) {
						calls++
						return models.Status{}, nil
					},
				},
				Wait: func(_ context.Context, deadline time.Time) error {
					waitedUntil = deadline
					cancel()
					return ctx.Err()
				},
			})

			// When
			err := scheduler.Run(ctx)

			// Then
			assert.ErrorIs(test, err, context.Canceled)
			assert.Equal(test, 0, calls)
			assert.Equal(test, parseTime("2026-07-06T08:00:00Z"), waitedUntil)
		})
	}

	test.Run("should drain active call and reject overlap when canceled", func(test *testing.T) {
		// Given
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		clock := clock.NewFixedClock(parseTime("2026-07-06T05:00:00Z"))
		entered := make(chan struct{})
		canceled := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		scheduler := newScheduler(test, schedule.Config{Clock: clock, TrackedCaseService: tracking{owners: []int64{1, 2}, scheduled: func(ctx context.Context, _ int64) (models.Status, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return models.Status{}, ctx.Err()
		}}, Wait: func(_ context.Context, deadline time.Time) error { clock.Set(deadline); return nil }})

		// When
		go func() { done <- scheduler.Run(ctx) }()
		<-entered
		overlap := scheduler.Run(context.Background())
		cancel()
		<-canceled

		// Then
		select {
		case err := <-done:
			assert.Equal(test, context.Canceled, err)
		default:
		}
		assert.ErrorIs(test, overlap, schedule.ErrScheduler)
		close(release)
		{
			err := <-done
			assert.ErrorIs(test, err, context.Canceled)
		}
	})

	test.Run("should skip missed minute and accept timer jitter when wait returns late", func(test *testing.T) {
		// Given
		clock := clock.NewFixedClock(parseTime("2026-07-06T05:00:00Z"))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		waits, calls := 0, 0
		scheduler := newScheduler(test, schedule.Config{
			Clock:              clock,
			TrackedCaseService: tracking{
				owners: []int64{1},
				scheduled: func(context.Context, int64) (models.Status, error) {
					calls++
					return models.Status{}, nil
				},
			},
			Wait: func(_ context.Context, deadline time.Time) error {
				waits++
				switch waits {
				case 1:
					clock.Set(deadline.Add(time.Minute))
				case 2:
					clock.Set(deadline.Add(time.Millisecond))
				default:
					cancel()
					return ctx.Err()
				}
				return nil
			},
		})

		// When
		err := scheduler.Run(ctx)

		// Then
		assert.ErrorIs(test, err, context.Canceled)
		assert.Equal(test, 1, calls)
		assert.Equal(test, 3, waits)
	})

	test.Run("should share manual guard when slot follows manual attempt", func(test *testing.T) {
		// Given
		ctx := context.Background()
		db, err := database.Open(ctx, filepath.Join(test.TempDir(), "state.db"))
		assert.NoError(test, err)
		test.Cleanup(func() { _ = db.Close() })
		clock              := clock.NewFixedClock(time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC))
		calls              := 0
		trackedCaseService := services.NewTrackedCaseService(services.TrackedCaseServiceConfig{
			Store: repositories.NewStore(db),
			Portal: scheduledPortal(func(context.Context, string, string) (string, time.Time, error) {
				calls++
				return "A", time.Time{}, nil
			}),
			Clock: clock,
		})
		test.Cleanup(trackedCaseService.Shutdown)
		{
			_, _, err := trackedCaseService.StoreCaseToTrack(ctx, 101, "synthetic-number", "synthetic-password")
			assert.NoError(test, err)
		}
		monday := time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
		clock.Set(monday.Add(-time.Second))
		_, manualErr := trackedCaseService.Status(ctx, 101)
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		waits := 0
		scheduler := newScheduler(test, schedule.Config{
			TrackedCaseService: trackedCaseService,
			Clock:              clock,
			Wait:               func(_ context.Context, deadline time.Time) error {
				waits++
				if waits == 2 {
					cancel()
					return runCtx.Err()
				}
				clock.Set(deadline)
				return nil
			},
		})

		// When
		runErr := scheduler.Run(runCtx)

		// Then
		assert.NoError(test, manualErr)
		assert.ErrorIs(test, runErr, context.Canceled)
		assert.Equal(test, 2, calls)
		assert.Equal(test, 2, waits)
	})
}

func TestJobsNotifications(test *testing.T) {
	test.Run("should notify changed owners and continue when delivery fails", func(test *testing.T) {
		// Given
		fixedClock := clock.NewFixedClock(parseTime("2026-07-06T05:00:00Z"))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var recipients []int64
		change := models.StageChange{PreviousStage: "A", Stage: "B", CheckedAt: fixedClock.Now()}
		waits := 0
		scheduler := newScheduler(test, schedule.Config{
			Clock:              fixedClock,
			TrackedCaseService: tracking{owners: []int64{1, 2, 3, 4}, scheduled: func(_ context.Context, owner int64) (models.Status, error) {
				if owner == 2 {
					return models.Status{}, nil
				}
				if owner == 3 {
					return models.Status{Change: &change}, errors.New("check failed")
				}
				return models.Status{Fresh: true, Change: &change}, nil
			}},
			Notifier: notifierFunc(func(_ context.Context, owner int64, actual models.StageChange) error {
				recipients = append(recipients, owner)
				assert.DeepEqual(test, change, actual)
				return errors.New("delivery failed")
			}),
			Wait: func(_ context.Context, deadline time.Time) error {
				waits++
				if waits == 2 {
					cancel()
					return ctx.Err()
				}
				fixedClock.Set(deadline)
				return nil
			},
		})

		// When
		err := scheduler.Run(ctx)

		// Then
		assert.ErrorIs(test, err, context.Canceled)
		assert.DeepEqual(test, []int64{1, 4}, recipients)
	})
}

func TestJobsStageChangeIntegration(test *testing.T) {
	test.Run("should send once when changed check is followed by unchanged check", func(test *testing.T) {
		// Given
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		db, err := database.Open(ctx, filepath.Join(test.TempDir(), "state.db"))
		assert.NoError(test, err)
		test.Cleanup(func() { _ = db.Close() })
		fixedClock := clock.NewFixedClock(parseTime("2026-07-06T05:00:00Z"))
		stage := "A"
		service := services.NewTrackedCaseService(services.TrackedCaseServiceConfig{
			Store: repositories.NewStore(db), Clock: fixedClock,
			Portal: scheduledPortal(func(context.Context, string, string) (string, time.Time, error) { return stage, time.Time{}, nil }),
		})
		test.Cleanup(service.Shutdown)
		_, _, err = service.StoreCaseToTrack(ctx, 101, "number", "password")
		assert.NoError(test, err)
		stage = "B"
		var changes []models.StageChange
		var recipients []int64
		waits := 0
		scheduler := newScheduler(test, schedule.Config{
			TrackedCaseService: service, Clock: fixedClock,
			Notifier:           notifierFunc(func(_ context.Context, owner int64, change models.StageChange) error {
				recipients = append(recipients, owner)
				changes = append(changes, change)
				return nil
			}),
			Wait: func(_ context.Context, deadline time.Time) error {
				waits++
				if waits == 3 {
					cancel()
					return ctx.Err()
				}
				fixedClock.Set(deadline)
				return nil
			},
		})

		// When
		err = scheduler.Run(ctx)

		// Then
		assert.ErrorIs(test, err, context.Canceled)
		assert.DeepEqual(test, []int64{101}, recipients)
		assert.DeepEqual(test, []models.StageChange{{PreviousStage: "A", Stage: "B", CheckedAt: parseTime("2026-07-06T06:00:00Z")}}, changes)
	})
}

// -- Internals ----------------------------------------------------------------

type tracking struct {
	owners      []int64
	ownersError error
	scheduled   func(context.Context, int64) (models.Status, error)
}

func (this tracking) Scheduled(ctx context.Context, owner int64) (models.Status, error) {
	return this.scheduled(ctx, owner)
}

func (this tracking) ScheduledOwners(context.Context) ([]int64, error) {
	return this.owners, this.ownersError
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func newScheduler(test *testing.T, config schedule.Config) *schedule.Jobs {
	test.Helper()
	if config.Notifier == nil {
		config.Notifier = notifierFunc(func(context.Context, int64, models.StageChange) error { return nil })
	}
	return schedule.New(config)
}

type scheduledPortal func(context.Context, string, string) (string, time.Time, error)

func (this scheduledPortal) Check(ctx context.Context, _ int64, number, password string) (string, time.Time, error) {
	return this(ctx, number, password)
}

func (scheduledPortal) ForgetSession(int64) {}

type notifierFunc func(context.Context, int64, models.StageChange) error

func (this notifierFunc) NotifyStageChanged(ctx context.Context, owner int64, change models.StageChange) error {
	return this(ctx, owner, change)
}
