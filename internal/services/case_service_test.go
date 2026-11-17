package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/adapters/outbound/database"
	"github.com/arhor/case-watch/internal/adapters/outbound/database/repositories"
	"github.com/arhor/case-watch/internal/models"
	"github.com/arhor/case-watch/internal/testing/assert"
)

var epoch = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

const number, password = "synthetic-number", "synthetic-password"

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (this *clock) Now() time.Time  { this.mu.Lock(); defer this.mu.Unlock(); return this.now }
func (this *clock) set(t time.Time) { this.mu.Lock(); defer this.mu.Unlock(); this.now = t }

type portalFunc func(context.Context, string, string) (string, time.Time, error)

func (this portalFunc) Check(ctx context.Context, owner int64, number, password string) (string, time.Time, error) {
	return this(ctx, number, password)
}

type fixture struct {
	c     *CaseService
	db    *sql.DB
	store *repositories.Store
	clock *clock
	path  string
	calls atomic.Int32
}

func setup(t *testing.T, p portalFunc) *fixture {
	t.Helper()
	f := &fixture{clock: &clock{now: epoch}, path: filepath.Join(t.TempDir(), "state.db")}
	var err error
	f.db, err = database.Open(context.Background(), f.path)
	assert.NoError(t, err)
	f.store = repositories.NewStore(f.db)
	t.Cleanup(func() { f.db.Close() })
	wrapped := portalFunc(func(ctx context.Context, number, password string) (string, time.Time, error) {
		f.calls.Add(1)
		if p != nil {
			return p(ctx, number, password)
		}
		return "A", time.Time{}, nil
	})
	f.c = NewCaseService(CaseServiceConfig{Store: f.store, Portal: wrapped, Clock: f.clock})
	return f
}
func (this *fixture) reopen(t *testing.T) {
	t.Helper()
	{
		err := this.db.Close()
		assert.NoError(t, err)
	}
	db, err := database.Open(context.Background(), this.path)
	assert.NoError(t, err)
	this.db = db
	this.store = repositories.NewStore(db)
}

func (this *fixture) enroll(t *testing.T, owner int64) string {
	t.Helper()
	{
		_, _, err := this.c.Enroll(context.Background(), owner, number, password)
		assert.NoError(t, err)
	}
	e, err := this.store.Enrollments().FindByOwner(context.Background(), owner)
	assert.NoError(t, err)
	return e.ID
}
func obs(t *testing.T, s models.Status, stage string, checked time.Time) {
	t.Helper()
	assert.True(t, !s.CheckedAt.IsZero(), "observation should have the expected stage and completion timestamp")
	assert.Equal(t, stage, s.Stage, "observation should have the expected stage and completion timestamp")
	assert.True(t, s.CheckedAt.Equal(checked), "observation should have the expected stage and completion timestamp")
}

func TestStatusShouldUseCompletionFreshnessWithoutConsumingGuardsWhenCacheIsRecent(t *testing.T) {
	// Given
	f := setup(t, func(context.Context, string, string) (string, time.Time, error) {
		return "A", epoch.Add(-time.Hour), nil
	})
	f.enroll(t, 101)
	f.clock.set(epoch.Add(59 * time.Second))
	// When
	recent, e1 := f.c.Status(context.Background(), 101)
	f.clock.set(epoch.Add(time.Minute))
	at, e2 := f.c.Status(context.Background(), 101)
	e, _ := f.store.Enrollments().FindByOwner(context.Background(), 101)
	deadline, e3 := f.c.checkEligibility(context.Background(), 101, e.ID, true)
	// Then
	assert.NoError(t, e1, "only completed checks should refresh cached status")
	assert.NoError(t, e2, "only completed checks should refresh cached status")
	assert.NoError(t, e3, "only completed checks should refresh cached status")
	assert.True(t, !recent.Fresh, "only completed checks should refresh cached status")
	assert.True(t, at.Fresh, "only completed checks should refresh cached status")
	assert.Equal(t, 2, f.calls.Load(), "only completed checks should refresh cached status")
	assert.True(t, recent.NextEligibleAt.Equal(epoch.Add(time.Minute)), "only completed checks should refresh cached status")
	assert.True(t, deadline.Equal(epoch.Add(2*time.Minute)), "only completed checks should refresh cached status")
	obs(t, recent, "A", epoch)
	obs(t, at, "A", epoch.Add(time.Minute))
}

func TestStatusShouldRetainStaleCacheAndGuardWhenRefreshFailsAcrossRestart(t *testing.T) {
	// Given
	var fail atomic.Bool
	f := setup(t, func(context.Context, string, string) (string, time.Time, error) {
		if fail.Load() {
			return "", time.Time{}, errors.New("secret remote details")
		}
		return "A", time.Time{}, nil
	})
	f.enroll(t, 101)
	fail.Store(true)
	f.clock.set(epoch.Add(time.Minute))
	// When
	failed, e1 := f.c.Status(context.Background(), 101)
	f.reopen(t)
	f.c = NewCaseService(CaseServiceConfig{Store: f.store, Portal: portalFunc(func(context.Context, string, string) (string, time.Time, error) {
		f.calls.Add(1)
		return "", time.Time{}, errors.New("secret")
	}), Clock: f.clock})
	f.clock.set(epoch.Add(119 * time.Second))
	before, e2 := f.c.Status(context.Background(), 101)
	f.clock.set(epoch.Add(2 * time.Minute))
	at, e3 := f.c.Status(context.Background(), 101)
	// Then
	assert.ErrorIs(t, e1, ErrCheck, "failed checks should retain stale status and the retry guard")
	assert.NoError(t, e2, "failed checks should retain stale status and the retry guard")
	assert.ErrorIs(t, e3, ErrCheck, "failed checks should retain stale status and the retry guard")
	assert.True(t, !failed.Fresh, "failed checks should retain stale status and the retry guard")
	assert.True(t, !before.Fresh, "failed checks should retain stale status and the retry guard")
	assert.True(t, !at.Fresh, "failed checks should retain stale status and the retry guard")
	assert.Equal(t, 3, f.calls.Load(), "failed checks should retain stale status and the retry guard")
	assert.True(t, !strings.Contains(e1.Error(), "secret"), "failed checks should retain stale status and the retry guard")
	assert.True(t, before.NextEligibleAt.Equal(epoch.Add(2*time.Minute)), "failed checks should retain stale status and the retry guard")
	obs(t, failed, "A", epoch)
	obs(t, before, "A", epoch)
	obs(t, at, "A", epoch)
}

func TestScheduledShouldSkipGuardedCaseAndPreserveManualBudgetWhenRunAtSlot(t *testing.T) {
	// Given
	f := setup(t, nil)
	id := f.enroll(t, 101)
	f.clock.set(epoch.Add(time.Minute))
	// When
	scheduled, e1 := f.c.Scheduled(context.Background(), 101)
	f.clock.set(epoch.Add(119 * time.Second))
	guarded, e2 := f.c.Scheduled(context.Background(), 101)
	cached, e3 := f.c.Status(context.Background(), 101)
	f.clock.set(epoch.Add(2 * time.Minute))
	manual, e4 := f.c.Status(context.Background(), 101)
	history, e5 := f.store.History().FindByEnrollment(context.Background(), 101, id)
	// Then
	assert.NoError(t, e1, "scheduled checks should preserve the shared guard and manual budget")
	assert.NoError(t, e2, "scheduled checks should preserve the shared guard and manual budget")
	assert.NoError(t, e3, "scheduled checks should preserve the shared guard and manual budget")
	assert.NoError(t, e4, "scheduled checks should preserve the shared guard and manual budget")
	assert.NoError(t, e5, "scheduled checks should preserve the shared guard and manual budget")
	assert.Equal(t, 3, f.calls.Load(), "scheduled checks should preserve the shared guard and manual budget")
	assert.True(t, scheduled.Fresh, "scheduled checks should preserve the shared guard and manual budget")
	assert.True(t, guarded.NextEligibleAt.Equal(epoch.Add(2*time.Minute)), "scheduled checks should preserve the shared guard and manual budget")
	assert.True(t, !cached.Fresh, "scheduled checks should preserve the shared guard and manual budget")
	assert.True(t, manual.Fresh, "scheduled checks should preserve the shared guard and manual budget")
	assert.Equal(t, 0, len(history), "scheduled checks should preserve the shared guard and manual budget")
}

func TestScheduledShouldPreventManualRetryWhenScheduledAttemptFails(t *testing.T) {
	// Given
	var fail atomic.Bool
	f := setup(t, func(context.Context, string, string) (string, time.Time, error) {
		if fail.Load() {
			return "", time.Time{}, errors.New("private")
		}
		return "A", time.Time{}, nil
	})
	f.enroll(t, 101)
	fail.Store(true)
	f.clock.set(epoch.Add(time.Minute))
	// When
	_, e1 := f.c.Scheduled(context.Background(), 101)
	f.clock.set(epoch.Add(119 * time.Second))
	before, e2 := f.c.Status(context.Background(), 101)
	f.clock.set(epoch.Add(2 * time.Minute))
	_, e3 := f.c.Status(context.Background(), 101)
	// Then
	assert.ErrorIs(t, e1, ErrCheck, "failed schedule guard")
	assert.NoError(t, e2, "failed schedule guard")
	assert.ErrorIs(t, e3, ErrCheck, "failed schedule guard")
	assert.True(t, !before.Fresh, "failed schedule guard")
	assert.Equal(t, 3, f.calls.Load(), "failed schedule guard")
}

// Notifies when a caller reaches the cancellable shared-flight wait, avoiding
// sleeps or inspecting the coordinator's internal synchronization.
type waitContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
}

func (this *waitContext) Done() <-chan struct{} {
	this.once.Do(func() { close(this.entered) })
	return this.Context.Done()
}
func waitingContext(ctx context.Context) *waitContext {
	return &waitContext{Context: ctx, entered: make(chan struct{})}
}

type answer struct {
	s   models.Status
	err error
}

func TestStatusShouldShareSingleFlightAndKeepOtherWaitersWhenOneCallerCancels(t *testing.T) {
	// Given
	entered, release := make(chan struct{}), make(chan struct{})
	var block atomic.Bool
	f := setup(t, func(ctx context.Context, _ string, _ string) (string, time.Time, error) {
		if block.Load() {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return "", time.Time{}, ctx.Err()
			}
		}
		return "A", time.Time{}, nil
	})
	f.enroll(t, 101)
	block.Store(true)
	f.clock.set(epoch.Add(time.Minute))
	firstBase, cancel := context.WithCancel(context.Background())
	first := waitingContext(firstBase)
	second := waitingContext(context.Background())
	third := waitingContext(context.Background())
	a, b, d := make(chan answer, 1), make(chan answer, 1), make(chan answer, 1)
	// When
	go func() { s, e := f.c.Status(first, 101); a <- answer{s, e} }()
	<-entered
	<-first.entered
	go func() { s, e := f.c.Scheduled(second, 101); b <- answer{s, e} }()
	<-second.entered
	go func() { s, e := f.c.Status(third, 101); d <- answer{s, e} }()
	<-third.entered
	cancel()
	canceled := <-a
	f.clock.set(epoch.Add(130 * time.Second))
	close(release)
	joined1, joined2 := <-b, <-d
	f.clock.set(epoch.Add(189 * time.Second))
	recent, e := f.c.Status(context.Background(), 101)
	// Then
	assert.ErrorIs(t, canceled.err, context.Canceled, "joins")
	assert.NoError(t, joined1.err, "joins")
	assert.NoError(t, joined2.err, "joins")
	assert.True(t, e == nil, "joins")
	assert.Equal(t, 2, f.calls.Load(), "joins")
	assert.True(t, !recent.Fresh, "joins")
	assert.True(t, joined1.s.NextEligibleAt.Equal(epoch.Add(190*time.Second)), "joins")
	obs(t, joined1.s, "A", epoch.Add(130*time.Second))
	obs(t, joined2.s, "A", epoch.Add(130*time.Second))
}

func TestTrackingShouldDenyAllWorkBeforeDataAccessWhenOwnerIsInvalid(t *testing.T) {
	// Given
	f := setup(t, nil)
	f.enroll(t, 101)
	// When
	_, _, e1 := f.c.Enroll(context.Background(), 0, number, password)
	_, e2 := f.c.Status(context.Background(), 0)
	_, e3 := f.c.Scheduled(context.Background(), 0)
	e4 := f.c.Remove(context.Background(), 0)
	_, other := f.c.Status(context.Background(), 202)
	// Then
	for _, err := range []error{e1, e2, e3, e4} {
		assert.ErrorIs(t, err, ErrDenied, "denial")
	}
	assert.ErrorIs(t, other, repositories.ErrNotFound, "owners should only access their own enrollment")
	assert.Equal(t, 1, f.calls.Load(), "owners should only access their own enrollment")
}

func TestEnrollShouldRefuseReplacementWithoutPortalCheckWhenOwnerAlreadyExists(t *testing.T) {
	// Given
	f := setup(t, nil)
	id := f.enroll(t, 101)
	// When
	_, _, err := f.c.Enroll(context.Background(), 101, number, password)
	e, read := f.store.Enrollments().FindByOwner(context.Background(), 101)
	// Then
	assert.ErrorIs(t, err, repositories.ErrExists, "duplicate enrollment should preserve the original case without portal work")
	assert.True(t, read == nil, "duplicate enrollment should preserve the original case without portal work")
	assert.Equal(t, id, e.ID, "duplicate enrollment should preserve the original case without portal work")
	assert.Equal(t, 1, f.calls.Load(), "duplicate enrollment should preserve the original case without portal work")
	assert.Equal(t, number, e.Number, "enrollment credentials missing")
	assert.Equal(t, password, e.Password, "enrollment credentials missing")
}

func TestEnrollShouldLeaveNoCommittedCaseWhenAnyEnrollmentStageFails(t *testing.T) {
	for _, kind := range []string{"portal", "store", "cancellation", "validation"} {
		t.Run(kind, func(t *testing.T) {
			// Given
			f := setup(t, nil)
			ctx := context.Background()
			password := password
			p := portalFunc(func(context.Context, string, string) (string, time.Time, error) {
				return "A", time.Time{}, nil
			})
			switch kind {
			case "portal":
				p = func(context.Context, string, string) (string, time.Time, error) {
					return "", time.Time{}, errors.New("private remote detail")
				}
			case "store":
				rejectInsert(t, f.path, "enrollments")
			case "cancellation":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "validation":
				password = ""
			}
			c := NewCaseService(CaseServiceConfig{Store: f.store, Portal: p, Clock: f.clock})
			// When
			_, _, err := c.Enroll(ctx, 101, number, password)
			_, read := f.store.Enrollments().FindByOwner(context.Background(), 101)
			// Then
			assert.True(t, err != nil, "failed enrollment should not persist data")
			assert.True(t, !strings.Contains(err.Error(), "private"), "failed enrollment should not persist data")
			assert.ErrorIs(t, read, repositories.ErrNotFound, "failed enrollment should not persist data")
		})
	}
}

func TestRemoveShouldCancelAndDrainPortalBeforeDeletingWhenLateResultsArrive(t *testing.T) {
	// Given
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var block atomic.Bool
	f := setup(t, func(ctx context.Context, _ string, _ string) (string, time.Time, error) {
		if block.Load() {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return "LATE", time.Time{}, nil
		}
		return "A", time.Time{}, nil
	})
	id := f.enroll(t, 101)
	block.Store(true)
	f.clock.set(epoch.Add(time.Minute))
	checked := make(chan answer, 1)
	go func() { s, e := f.c.Status(context.Background(), 101); checked <- answer{s, e} }()
	<-entered
	removed := make(chan error, 1)
	// When
	go func() { removed <- f.c.Remove(context.Background(), 101) }()
	<-canceled
	_, busyStatus := f.c.Status(context.Background(), 101)
	_, _, busyEnroll := f.c.Enroll(context.Background(), 101, number, password)
	before, beforeErr := f.store.Enrollments().FindByOwner(context.Background(), 101)
	select {
	case err := <-removed:
		assert.True(t, false, fmt.Sprintf("removal should wait for in-flight work to drain, got %v", err))
	default:
	}
	close(release)
	late := <-checked
	removeErr := <-removed
	_, missing := f.store.Enrollments().FindByOwner(context.Background(), 101)
	block.Store(false)
	newID := f.enroll(t, 101)
	lateObservation := f.c.saveObservation(context.Background(), 101, id, "LATE", f.clock.Now())
	f.clock.set(epoch.Add(119 * time.Second))
	cached, statusErr := f.c.Status(context.Background(), 101)
	// Then
	assert.ErrorIs(t, busyStatus, ErrRemoving, "removal should drain work, delete data, and retain the manual guard")
	assert.ErrorIs(t, busyEnroll, ErrRemoving, "removal should drain work, delete data, and retain the manual guard")
	assert.NoError(t, beforeErr, "removal should drain work, delete data, and retain the manual guard")
	assert.Equal(t, id, before.ID, "removal should drain work, delete data, and retain the manual guard")
	assert.ErrorIs(t, late.err, ErrRemoving, "removal should drain work, delete data, and retain the manual guard")
	assert.NoError(t, removeErr, "removal should drain work, delete data, and retain the manual guard")
	assert.ErrorIs(t, missing, repositories.ErrNotFound, "removal should drain work, delete data, and retain the manual guard")
	assert.True(t, newID != id, "removal should drain work, delete data, and retain the manual guard")
	assert.ErrorIs(t, lateObservation, repositories.ErrNotFound, "removal should drain work, delete data, and retain the manual guard")
	assert.NoError(t, statusErr, "removal should drain work, delete data, and retain the manual guard")
	assert.True(t, !cached.Fresh, "removal should drain work, delete data, and retain the manual guard")
	assert.True(t, cached.NextEligibleAt.Equal(epoch.Add(2*time.Minute)), "removal should drain work, delete data, and retain the manual guard")
	obs(t, cached, "A", epoch.Add(time.Minute))
}

func TestRemoveShouldPreventBaselineCommitWhenEnrollmentIsCanceledDuringCheck(t *testing.T) {
	// Given
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f := setup(t, func(ctx context.Context, _ string, _ string) (string, time.Time, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return "late", time.Time{}, nil
	})
	enrolled := make(chan error, 1)
	go func() { _, _, e := f.c.Enroll(context.Background(), 101, number, password); enrolled <- e }()
	<-entered
	removed := make(chan error, 1)
	// When
	go func() { removed <- f.c.Remove(context.Background(), 101) }()
	<-canceled
	_, _, busy := f.c.Enroll(context.Background(), 101, number, password)
	close(release)
	enrollErr, removeErr := <-enrolled, <-removed
	_, missing := f.store.Enrollments().FindByOwner(context.Background(), 101)
	// Then
	assert.ErrorIs(t, busy, ErrRemoving, "enrollment cancellation")
	assert.ErrorIs(t, enrollErr, context.Canceled, "enrollment cancellation")
	assert.ErrorIs(t, removeErr, repositories.ErrNotFound, "enrollment cancellation")
	assert.ErrorIs(t, missing, repositories.ErrNotFound, "enrollment cancellation")
}

func TestStatusShouldCancelUnderlyingFlightWhenLastWaiterLeaves(t *testing.T) {
	// Given
	var block atomic.Bool
	entered, canceled := make(chan struct{}), make(chan struct{})
	f := setup(t, func(ctx context.Context, _ string, _ string) (string, time.Time, error) {
		if block.Load() {
			close(entered)
			<-ctx.Done()
			close(canceled)
			return "", time.Time{}, ctx.Err()
		}
		return "A", time.Time{}, nil
	})
	f.enroll(t, 101)
	f.clock.set(epoch.Add(time.Minute))
	block.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	// When
	go func() { _, e := f.c.Status(ctx, 101); result <- e }()
	<-entered
	cancel()
	err := <-result
	<-canceled
	removed := f.c.Remove(context.Background(), 101)
	// Then
	assert.ErrorIs(t, err, context.Canceled, "cancellation failed to drain")
	assert.True(t, removed == nil, "cancellation failed to drain")
}

func TestStatusShouldPreserveOldCacheWhenObservationCommitFails(t *testing.T) {
	t.Run("observation", func(t *testing.T) {
		// Given
		f := setup(t, nil)
		id := f.enroll(t, 101)
		var calls atomic.Int32
		rejectInsert(t, f.path, "history")
		c := NewCaseService(CaseServiceConfig{Store: f.store, Portal: portalFunc(func(context.Context, string, string) (string, time.Time, error) {
			calls.Add(1)
			return "B", time.Time{}, nil
		}), Clock: f.clock})
		f.clock.set(epoch.Add(time.Minute))
		// When
		status, err := c.Status(context.Background(), 101)
		e, readErr := f.store.Enrollments().FindByOwner(context.Background(), 101)
		history, historyErr := f.store.History().FindByEnrollment(context.Background(), 101, id)
		// Then
		assert.ErrorIs(t, err, ErrStorage, "failed refresh")
		assert.NoError(t, readErr, "failed refresh")
		assert.NoError(t, historyErr, "failed refresh")
		assert.Equal(t, "A", e.Stage, "failed refresh")
		assert.True(t, e.CheckedAt.Equal(epoch), "failed refresh")
		assert.Equal(t, 0, len(history), "failed refresh")
		assert.True(t, !status.Fresh, "failed refresh")
		assert.Equal(t, 1, calls.Load(), "failed refresh")
		obs(t, status, "A", epoch)
	})
}

func TestStatusShouldCreateDurableTransitionsFromBothSourcesWhenStagesChange(t *testing.T) {
	// Given
	var stage atomic.Value
	stage.Store("A")
	f := setup(t, func(context.Context, string, string) (string, time.Time, error) {
		return stage.Load().(string), time.Time{}, nil
	})
	id := f.enroll(t, 101)
	// When
	f.clock.set(epoch.Add(time.Minute))
	stage.Store("B")
	manual, e1 := f.c.Status(context.Background(), 101)
	f.clock.set(epoch.Add(2 * time.Minute))
	stage.Store("A")
	scheduled, e2 := f.c.Scheduled(context.Background(), 101)
	history, e3 := f.store.History().FindByEnrollment(context.Background(), 101, id)
	// Then
	assert.NoError(t, e1, "shared observation transaction")
	assert.NoError(t, e2, "shared observation transaction")
	assert.NoError(t, e3, "shared observation transaction")
	assert.True(t, manual.Fresh, "shared observation transaction")
	assert.True(t, scheduled.Fresh, "shared observation transaction")
	assert.Equal(t, 2, len(history), "shared observation transaction")
	assert.Equal(t, "B", history[0].Stage, "shared observation transaction")
	assert.Equal(t, "A", history[1].Stage, "shared observation transaction")
}

func TestRemoveShouldRetainEnrollmentAndReopenWorkWhenCallerCancelsDuringDrain(t *testing.T) {
	// Given
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var block atomic.Bool
	f := setup(t, func(ctx context.Context, _ string, _ string) (string, time.Time, error) {
		if block.Load() {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return "", time.Time{}, ctx.Err()
		}
		return "A", time.Time{}, nil
	})
	id := f.enroll(t, 101)
	f.clock.set(epoch.Add(time.Minute))
	block.Store(true)
	work := make(chan error, 1)
	go func() { _, err := f.c.Status(context.Background(), 101); work <- err }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	removed := make(chan error, 1)
	// When
	go func() { removed <- f.c.Remove(ctx, 101) }()
	<-canceled
	cancel()
	close(release)
	workErr, removeErr := <-work, <-removed
	e, readErr := f.store.Enrollments().FindByOwner(context.Background(), 101)
	status, statusErr := f.c.Status(context.Background(), 101)
	// Then
	assert.ErrorIs(t, workErr, ErrRemoving, "canceled removal")
	assert.ErrorIs(t, removeErr, context.Canceled, "canceled removal")
	assert.NoError(t, readErr, "canceled removal")
	assert.Equal(t, id, e.ID, "canceled removal")
	assert.NoError(t, statusErr, "canceled removal")
	assert.True(t, !status.CheckedAt.IsZero(), "canceled removal")
}

func TestScheduledShouldReuseRecentEnrollmentBaselineWithoutReservationWhenRunAfterRestart(t *testing.T) {
	// Given
	f := setup(t, nil)
	id := f.enroll(t, 101)
	f.reopen(t)
	var portalCalls atomic.Int32
	c := NewCaseService(CaseServiceConfig{Store: f.store, Portal: portalFunc(func(context.Context, string, string) (string, time.Time, error) {
		portalCalls.Add(1)
		return "", time.Time{}, nil
	}), Clock: f.clock})
	f.clock.set(epoch.Add(59 * time.Second))
	// When
	status, statusErr := c.Scheduled(context.Background(), 101)
	next, guardErr := c.checkEligibility(context.Background(), 101, id, true)
	// Then
	assert.Equal(t, int32(0), portalCalls.Load(), "recent baseline should not trigger portal work")
	assert.NoError(t, statusErr, "recent enrollment baseline should be reused without reserving a check")
	assert.NoError(t, guardErr, "recent enrollment baseline should be reused without reserving a check")
	assert.True(t, !status.Fresh, "recent enrollment baseline should be reused without reserving a check")
	assert.True(t, next.IsZero(), "recent enrollment baseline should be reused without reserving a check")
	assert.True(t, status.NextEligibleAt.Equal(epoch.Add(time.Minute)), "recent enrollment baseline should be reused without reserving a check")
	obs(t, status, "A", epoch)
}

func TestShutdownShouldCancelAndDrainAllOwnersWhenWorkIsActive(t *testing.T) {
	// Given
	started := make(chan struct{}, 2)
	cancelled := make(chan struct{}, 2)
	release := make(chan struct{})
	f := setup(t, func(ctx context.Context, _ string, _ string) (string, time.Time, error) {
		started <- struct{}{}
		<-ctx.Done()
		cancelled <- struct{}{}
		<-release
		return "", time.Time{}, ctx.Err()
	})
	finished := make(chan error, 2)
	for _, owner := range []int64{101, 202} {
		go func(owner int64) {
			_, _, err := f.c.Enroll(context.Background(), owner, number, password)
			finished <- err
		}(owner)
	}
	for i := 0; i < 2; i++ {
		<-started
	}
	done := make(chan struct{})
	// When
	go func() { f.c.Shutdown(); close(done) }()
	for i := 0; i < 2; i++ {
		<-cancelled
	}
	// Then
	select {
	case <-done:
		assert.True(t, false, "shutdown should wait for all callbacks to drain")
	default:
	}
	close(release)
	<-done
	for i := 0; i < 2; i++ {
		{
			e := <-finished
			assert.ErrorIs(t, e, context.Canceled)
		}
	}
	f.c.Shutdown()
	{
		_, _, e := f.c.Enroll(context.Background(), 101, number, password)
		assert.ErrorIs(t, e, ErrClosed, "enrollment admitted after shutdown")
	}
	{
		e := f.c.Remove(context.Background(), 101)
		assert.ErrorIs(t, e, ErrClosed, "removal admitted after shutdown")
	}
}

func TestScheduledShouldRefreshAnyEnrolledOwnerWhenNoAccessPolicyIsConfigured(t *testing.T) {
	// Given
	f := setup(t, nil)
	f.enroll(t, 303)
	f.clock.set(epoch.Add(time.Minute))
	// When
	owners, inventoryErr := f.c.ScheduledOwners(context.Background())
	status, err := f.c.Scheduled(context.Background(), 303)
	_, otherErr := f.c.Status(context.Background(), 404)
	// Then
	assert.NoError(t, inventoryErr)
	assert.Equal(t, 1, len(owners), "len(owners) should match the expected value")
	assert.Equal(t, 303, owners[0], "owners[0] should match the expected value")
	assert.NoError(t, err)
	assert.True(t, status.Fresh, "status.Fresh should hold")
	assert.Equal(t, 2, f.calls.Load(), "f.calls.Load() should match the expected value")
	assert.ErrorIs(t, otherErr, repositories.ErrNotFound, "another owner's case was accessible")
}

func TestObserveShouldRollBackCachedStatusWhenHistoryInsertFails(t *testing.T) {
	// Given
	f := setup(t, nil)
	id := f.enroll(t, 101)
	db, err := sql.Open("sqlite", f.path)
	assert.NoError(t, err)
	defer db.Close()
	{
		_, err := db.Exec(`CREATE TRIGGER reject_history BEFORE INSERT ON history
 BEGIN SELECT RAISE(ABORT, 'synthetic history failure'); END;`)
		assert.NoError(t, err)
	}
	// When
	observeErr := f.c.saveObservation(context.Background(), 101, id, "B", epoch.Add(time.Minute))
	enrollment, readErr := f.store.Enrollments().FindByOwner(context.Background(), 101)
	history, historyErr := f.store.History().FindByEnrollment(context.Background(), 101, id)
	// Then
	assert.ErrorIs(t, observeErr, repositories.ErrStorage, "observation rollback")
	assert.NoError(t, readErr, "observation rollback")
	assert.NoError(t, historyErr, "observation rollback")
	assert.Equal(t, "A", enrollment.Stage, "observation rollback")
	assert.True(t, enrollment.CheckedAt.Equal(epoch), "observation rollback")
	assert.Equal(t, 0, len(history), "observation rollback")
}

func rejectInsert(t *testing.T, path, table string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	assert.NoError(t, err)
	defer db.Close()
	assert.True(t, !(table != "history" && table != "enrollments"), "invalid fixture table")
	{
		_, err := db.Exec("CREATE TRIGGER reject_insert BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT, 'synthetic storage failure'); END;")
		assert.NoError(t, err)
	}
}

func TestServiceShouldReportOnlyActiveStorageFailureWhenDatabaseReadFails(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "canceled"}[canceled], func(t *testing.T) {
			// Given
			f := setup(t, nil)
			{
				err := f.db.Close()
				assert.NoError(t, err)
			}
			reports := 0
			f.c.onStorageError = func() { reports++ }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
			}
			// When
			_, err := f.c.HasEnrollment(ctx, 101)
			// Then
			want := 1
			if canceled {
				want = 0
			}
			assert.True(t, err != nil, "storage failure")
			assert.Equal(t, want, reports, "storage failure")
		})
	}
}

func (portalFunc) ForgetSession(int64) {}
