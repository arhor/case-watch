package cases

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/database"
	"github.com/arhor/case-watch/internal/database/repositories"
	"github.com/arhor/case-watch/internal/database/store"
	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/scheduler"
)

var epoch = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

const Number, Password = "synthetic-number", "synthetic-password"

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.now = t }

type portalFunc func(context.Context, string, string) (string, time.Time, error)

func (f portalFunc) Check(ctx context.Context, Number, Password string) (string, time.Time, error) {
	return f(ctx, Number, Password)
}

type fixture struct {
	c     *Service
	db    *sql.DB
	s     *store.Store
	clock *clock
	path  string
	calls atomic.Int32
}

func setup(t *testing.T, p portalFunc) *fixture {
	t.Helper()
	f := &fixture{clock: &clock{now: epoch}, path: filepath.Join(t.TempDir(), "state.db")}
	var err error
	f.db, err = database.Open(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.s = store.New(f.db)
	t.Cleanup(func() { f.db.Close() })
	wrapped := portalFunc(func(ctx context.Context, Number, Password string) (string, time.Time, error) {
		f.calls.Add(1)
		if p != nil {
			return p(ctx, Number, Password)
		}
		return "A", time.Time{}, nil
	})
	f.c = New(Config{DB: f.s, Portal: wrapped, Clock: f.clock})
	return f
}
func (f *fixture) reopen(t *testing.T) {
	t.Helper()
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.db = db
	f.s = store.New(db)
}

func (f *fixture) enroll(t *testing.T, owner int64) string {
	t.Helper()
	if _, _, err := f.c.Enroll(context.Background(), owner, Number, Password); err != nil {
		t.Fatal(err)
	}
	e, err := f.s.Enrollments.FindByOwner(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	return e.ID
}
func obs(t *testing.T, s domain.Status, stage string, checked time.Time) {
	t.Helper()
	if s.CheckedAt.IsZero() || s.Stage != stage || !s.CheckedAt.Equal(checked) {
		t.Fatalf("unexpected observation: %+v", s)
	}
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
	e, _ := f.s.Enrollments.FindByOwner(context.Background(), 101)
	deadline, e3 := f.c.checkEligibility(context.Background(), 101, e.ID, true)
	// Then
	if e1 != nil || e2 != nil || e3 != nil || recent.Fresh || !at.Fresh || f.calls.Load() != 2 || !recent.NextEligibleAt.Equal(epoch.Add(time.Minute)) || !deadline.Equal(epoch.Add(2*time.Minute)) {
		t.Fatalf("freshness: recent=%+v at=%+v calls=%d errors=%v/%v/%v", recent, at, f.calls.Load(), e1, e2, e3)
	}
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
	f.c = New(Config{DB: f.s, Portal: portalFunc(func(context.Context, string, string) (string, time.Time, error) {
		f.calls.Add(1)
		return "", time.Time{}, errors.New("secret")
	}), Clock: f.clock})
	f.clock.set(epoch.Add(119 * time.Second))
	before, e2 := f.c.Status(context.Background(), 101)
	f.clock.set(epoch.Add(2 * time.Minute))
	at, e3 := f.c.Status(context.Background(), 101)
	// Then
	if !errors.Is(e1, ErrCheck) || e2 != nil || !errors.Is(e3, ErrCheck) || failed.Fresh || before.Fresh || at.Fresh || f.calls.Load() != 3 || strings.Contains(e1.Error(), "secret") || !before.NextEligibleAt.Equal(epoch.Add(2*time.Minute)) {
		t.Fatalf("failure guard: %+v %+v errors=%v/%v/%v calls=%d", failed, before, e1, e2, e3, f.calls.Load())
	}
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
	history, e5 := f.s.History.FindByEnrollment(context.Background(), 101, id)
	// Then
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || f.calls.Load() != 3 || !scheduled.Fresh || !guarded.NextEligibleAt.Equal(epoch.Add(2*time.Minute)) || cached.Fresh || !manual.Fresh || len(history) != 0 {
		t.Fatalf("scheduled guard: %+v %+v %+v calls=%d errors=%v/%v/%v/%v/%v", scheduled, guarded, cached, f.calls.Load(), e1, e2, e3, e4, e5)
	}
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
	if !errors.Is(e1, ErrCheck) || e2 != nil || !errors.Is(e3, ErrCheck) || before.Fresh || f.calls.Load() != 3 {
		t.Fatalf("failed schedule guard: %+v errors=%v/%v/%v calls=%d", before, e1, e2, e3, f.calls.Load())
	}
}

// Notifies when a caller reaches the cancellable shared-flight wait, avoiding
// sleeps or inspecting the coordinator's internal synchronization.
type waitContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
}

func (c *waitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}
func waitingContext(ctx context.Context) *waitContext {
	return &waitContext{Context: ctx, entered: make(chan struct{})}
}

type answer struct {
	s   domain.Status
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
	if !errors.Is(canceled.err, context.Canceled) || joined1.err != nil || joined2.err != nil || e != nil || f.calls.Load() != 2 || recent.Fresh || !joined1.s.NextEligibleAt.Equal(epoch.Add(190*time.Second)) {
		t.Fatalf("joins: %+v %+v %+v recent=%+v err=%v calls=%d", canceled, joined1, joined2, recent, e, f.calls.Load())
	}
	obs(t, joined1.s, "A", epoch.Add(130*time.Second))
	obs(t, joined2.s, "A", epoch.Add(130*time.Second))
}

func TestTrackingShouldDenyAllWorkBeforeDataAccessWhenOwnerIsInvalid(t *testing.T) {
	// Given
	f := setup(t, nil)
	f.enroll(t, 101)
	// When
	_, _, e1 := f.c.Enroll(context.Background(), 0, Number, Password)
	_, e2 := f.c.Status(context.Background(), 0)
	_, e3 := f.c.Scheduled(context.Background(), 0)
	e4 := f.c.Remove(context.Background(), 0)
	_, other := f.c.Status(context.Background(), 202)
	// Then
	for _, err := range []error{e1, e2, e3, e4} {
		if !errors.Is(err, ErrDenied) {
			t.Fatalf("denial: %v", err)
		}
	}
	if !errors.Is(other, repositories.ErrNotFound) || f.calls.Load() != 1 {
		t.Fatalf("owner isolation: %v calls=%d", other, f.calls.Load())
	}
}

func TestEnrollShouldRefuseReplacementWithoutPortalCheckWhenOwnerAlreadyExists(t *testing.T) {
	// Given
	f := setup(t, nil)
	id := f.enroll(t, 101)
	// When
	_, _, err := f.c.Enroll(context.Background(), 101, Number, Password)
	e, read := f.s.Enrollments.FindByOwner(context.Background(), 101)
	// Then
	if !errors.Is(err, repositories.ErrExists) || read != nil || e.ID != id || f.calls.Load() != 1 {
		t.Fatalf("replacement: %v/%v calls=%d", err, read, f.calls.Load())
	}
	if e.Number != Number || e.Password != Password {
		t.Fatal("enrollment credentials missing")
	}
}

func TestEnrollShouldLeaveNoCommittedCaseWhenAnyEnrollmentStageFails(t *testing.T) {
	for _, kind := range []string{"portal", "storage", "cancellation", "validation"} {
		t.Run(kind, func(t *testing.T) {
			// Given
			f := setup(t, nil)
			ctx := context.Background()
			password := Password
			p := portalFunc(func(context.Context, string, string) (string, time.Time, error) {
				return "A", time.Time{}, nil
			})
			switch kind {
			case "portal":
				p = func(context.Context, string, string) (string, time.Time, error) {
					return "", time.Time{}, errors.New("private remote detail")
				}
			case "storage":
				rejectInsert(t, f.path, "enrollments")
			case "cancellation":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "validation":
				password = ""
			}
			c := New(Config{DB: f.s, Portal: p, Clock: f.clock})
			// When
			_, _, err := c.Enroll(ctx, 101, Number, password)
			_, read := f.s.Enrollments.FindByOwner(context.Background(), 101)
			// Then
			if err == nil || strings.Contains(err.Error(), "private") || !errors.Is(read, repositories.ErrNotFound) {
				t.Fatalf("failed enrollment committed: %v/%v", err, read)
			}
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
	_, _, busyEnroll := f.c.Enroll(context.Background(), 101, Number, Password)
	before, beforeErr := f.s.Enrollments.FindByOwner(context.Background(), 101)
	select {
	case err := <-removed:
		t.Fatalf("removal returned without drain: %v", err)
	default:
	}
	close(release)
	late := <-checked
	removeErr := <-removed
	_, missing := f.s.Enrollments.FindByOwner(context.Background(), 101)
	block.Store(false)
	newID := f.enroll(t, 101)
	lateObservation := f.c.saveObservation(context.Background(), 101, id, "LATE", f.clock.Now())
	f.clock.set(epoch.Add(119 * time.Second))
	cached, statusErr := f.c.Status(context.Background(), 101)
	// Then
	if !errors.Is(busyStatus, ErrRemoving) || !errors.Is(busyEnroll, ErrRemoving) || beforeErr != nil || before.ID != id || !errors.Is(late.err, ErrRemoving) || removeErr != nil || !errors.Is(missing, repositories.ErrNotFound) || newID == id || !errors.Is(lateObservation, repositories.ErrNotFound) || statusErr != nil || cached.Fresh || !cached.NextEligibleAt.Equal(epoch.Add(2*time.Minute)) {
		t.Fatalf("removal results: late=%v remove=%v missing=%v lateObservation=%v status=%+v err=%v", late.err, removeErr, missing, lateObservation, cached, statusErr)
	}
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
	go func() { _, _, e := f.c.Enroll(context.Background(), 101, Number, Password); enrolled <- e }()
	<-entered
	removed := make(chan error, 1)
	// When
	go func() { removed <- f.c.Remove(context.Background(), 101) }()
	<-canceled
	_, _, busy := f.c.Enroll(context.Background(), 101, Number, Password)
	close(release)
	enrollErr, removeErr := <-enrolled, <-removed
	_, missing := f.s.Enrollments.FindByOwner(context.Background(), 101)
	// Then
	if !errors.Is(busy, ErrRemoving) || !errors.Is(enrollErr, context.Canceled) || !errors.Is(removeErr, repositories.ErrNotFound) || !errors.Is(missing, repositories.ErrNotFound) {
		t.Fatalf("enrollment cancellation: busy=%v enroll=%v remove=%v missing=%v", busy, enrollErr, removeErr, missing)
	}
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
	if !errors.Is(err, context.Canceled) || removed != nil {
		t.Fatalf("cancellation failed to drain: %v/%v", err, removed)
	}
}

func TestStatusShouldPreserveOldCacheWhenObservationCommitFails(t *testing.T) {
	t.Run("observation", func(t *testing.T) {
		// Given
		f := setup(t, nil)
		id := f.enroll(t, 101)
		var calls atomic.Int32
		rejectInsert(t, f.path, "history")
		c := New(Config{DB: f.s, Portal: portalFunc(func(context.Context, string, string) (string, time.Time, error) {
			calls.Add(1)
			return "B", time.Time{}, nil
		}), Clock: f.clock})
		f.clock.set(epoch.Add(time.Minute))
		// When
		status, err := c.Status(context.Background(), 101)
		e, readErr := f.s.Enrollments.FindByOwner(context.Background(), 101)
		history, historyErr := f.s.History.FindByEnrollment(context.Background(), 101, id)
		// Then
		if !errors.Is(err, ErrStorage) || readErr != nil || historyErr != nil || e.Stage != "A" || !e.CheckedAt.Equal(epoch) || len(history) != 0 || status.Fresh || calls.Load() != 1 {
			t.Fatalf("failed refresh: status=%+v err=%v read=%v history=%v calls=%d", status, err, readErr, historyErr, calls.Load())
		}
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
	history, e3 := f.s.History.FindByEnrollment(context.Background(), 101, id)
	// Then
	if e1 != nil || e2 != nil || e3 != nil || !manual.Fresh || !scheduled.Fresh || len(history) != 2 || history[0].Stage != "B" || history[1].Stage != "A" {
		t.Fatalf("shared observation transaction: history=%+v errors=%v/%v/%v", history, e1, e2, e3)
	}
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
	e, readErr := f.s.Enrollments.FindByOwner(context.Background(), 101)
	status, statusErr := f.c.Status(context.Background(), 101)
	// Then
	if !errors.Is(workErr, ErrRemoving) || !errors.Is(removeErr, context.Canceled) || readErr != nil || e.ID != id || statusErr != nil || status.CheckedAt.IsZero() {
		t.Fatalf("canceled removal: work=%v remove=%v read=%v status=%v", workErr, removeErr, readErr, statusErr)
	}
}

func TestScheduledShouldReuseRecentEnrollmentBaselineWithoutReservationWhenRunAfterRestart(t *testing.T) {
	// Given
	f := setup(t, nil)
	id := f.enroll(t, 101)
	f.reopen(t)
	c := New(Config{DB: f.s, Portal: portalFunc(func(context.Context, string, string) (string, time.Time, error) {
		t.Error("recent baseline triggered portal work")
		return "", time.Time{}, nil
	}), Clock: f.clock})
	f.clock.set(epoch.Add(59 * time.Second))
	// When
	status, statusErr := c.Scheduled(context.Background(), 101)
	next, guardErr := c.checkEligibility(context.Background(), 101, id, true)
	// Then
	if statusErr != nil || guardErr != nil || status.Fresh || !next.IsZero() || !status.NextEligibleAt.Equal(epoch.Add(time.Minute)) {
		t.Fatalf("baseline reuse: %+v status=%v next=%v guard=%v", status, statusErr, next, guardErr)
	}
	obs(t, status, "A", epoch)
}

func TestSchedulerShouldShareManualGuardWhenSlotFollowsManualAttempt(t *testing.T) {
	// Given
	f := setup(t, nil)
	f.enroll(t, 101)
	monday := time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
	f.clock.set(monday.Add(-time.Second))
	_, manualErr := f.c.Status(context.Background(), 101)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waits := 0
	s, err := scheduler.New(scheduler.Config{Cases: f.c, Clock: f.clock, Wait: func(_ context.Context, deadline time.Time) error {
		waits++
		if waits == 2 {
			cancel()
			return ctx.Err()
		}
		f.clock.set(deadline)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	// When
	runErr := s.Run(ctx)
	// Then
	if manualErr != nil || !errors.Is(runErr, context.Canceled) || f.calls.Load() != 2 || waits != 2 {
		t.Fatalf("manual=%v run=%v calls=%d waits=%d", manualErr, runErr, f.calls.Load(), waits)
	}
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
			_, _, err := f.c.Enroll(context.Background(), owner, Number, Password)
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
		t.Fatal("shutdown returned before callbacks drained")
	default:
	}
	close(release)
	<-done
	for i := 0; i < 2; i++ {
		if e := <-finished; !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	}
	f.c.Shutdown()
	if _, _, e := f.c.Enroll(context.Background(), 101, Number, Password); !errors.Is(e, ErrClosed) {
		t.Fatal("enrollment admitted after shutdown")
	}
	if e := f.c.Remove(context.Background(), 101); !errors.Is(e, ErrClosed) {
		t.Fatal("removal admitted after shutdown")
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
	if inventoryErr != nil || len(owners) != 1 || owners[0] != 303 || err != nil || !status.Fresh || f.calls.Load() != 2 {
		t.Fatalf("owners=%v inventory=%v status=%+v err=%v calls=%d", owners, inventoryErr, status, err, f.calls.Load())
	}
	if !errors.Is(otherErr, repositories.ErrNotFound) {
		t.Fatalf("another owner's case was accessible: %v", otherErr)
	}
}

func TestObserveShouldRollBackCachedStatusWhenHistoryInsertFails(t *testing.T) {
	// Given
	f := setup(t, nil)
	id := f.enroll(t, 101)
	db, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER reject_history BEFORE INSERT ON history
 BEGIN SELECT RAISE(ABORT, 'synthetic history failure'); END;`); err != nil {
		t.Fatal(err)
	}
	// When
	observeErr := f.c.saveObservation(context.Background(), 101, id, "B", epoch.Add(time.Minute))
	enrollment, readErr := f.s.Enrollments.FindByOwner(context.Background(), 101)
	history, historyErr := f.s.History.FindByEnrollment(context.Background(), 101, id)
	// Then
	if !errors.Is(observeErr, repositories.ErrStorage) || readErr != nil || historyErr != nil || enrollment.Stage != "A" || !enrollment.CheckedAt.Equal(epoch) || len(history) != 0 {
		t.Fatalf("observation rollback: observation=%+v history=%+v errors=%v/%v/%v", enrollment, history, observeErr, readErr, historyErr)
	}
}

func rejectInsert(t *testing.T, path, table string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if table != "history" && table != "enrollments" {
		t.Fatal("invalid fixture table")
	}
	if _, err := db.Exec("CREATE TRIGGER reject_insert BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT, 'synthetic storage failure'); END;"); err != nil {
		t.Fatal(err)
	}
}

func TestServiceShouldReportOnlyActiveStorageFailureWhenDatabaseReadFails(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "canceled"}[canceled], func(t *testing.T) {
			// Given
			f := setup(t, nil)
			if err := f.db.Close(); err != nil {
				t.Fatal(err)
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
			if err == nil || reports != want {
				t.Fatalf("storage failure: err=%v reports=%d want=%d", err, reports, want)
			}
		})
	}
}
