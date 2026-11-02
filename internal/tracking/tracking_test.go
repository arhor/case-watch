package tracking_test

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/auth"
	"github.com/arhor/case-watch/internal/credentials"
	"github.com/arhor/case-watch/internal/data"
	"github.com/arhor/case-watch/internal/notifications"
	"github.com/arhor/case-watch/internal/scheduler"
	"github.com/arhor/case-watch/internal/tracking"
)

var epoch = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
var plain = application.Credentials{Number: "synthetic-number", Password: "synthetic-password"}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.now = t }

type portalFunc func(context.Context, application.Credentials) (application.Observation, error)

func (f portalFunc) Check(ctx context.Context, p application.Credentials) (application.Observation, error) {
	return f(ctx, p)
}

type fixture struct {
	c     *tracking.Coordinator
	s     *data.Store
	k     *credentials.Keyring
	clock *clock
	path  string
	calls atomic.Int32
}

func setup(t *testing.T, p portalFunc) *fixture {
	t.Helper()
	f := &fixture{clock: &clock{now: epoch}, path: filepath.Join(t.TempDir(), "state.db")}
	var err error
	f.s, err = data.Open(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.s.Close() })
	f.k, err = credentials.Parse([]byte(`{"version":1,"active_key_id":"test","keys":{"test":"` + base64.StdEncoding.EncodeToString(make([]byte, 32)) + `"}}`))
	if err != nil {
		t.Fatal(err)
	}
	wrapped := portalFunc(func(ctx context.Context, creds application.Credentials) (application.Observation, error) {
		f.calls.Add(1)
		if p != nil {
			return p(ctx, creds)
		}
		return application.Observation{Stage: "A"}, nil
	})
	f.c = tracking.New(tracking.Persistence{DB: f.s}, f.k, wrapped, auth.New(false, []int64{101, 202}), f.clock)
	return f
}
func (f *fixture) enroll(t *testing.T, owner int64) application.EnrollmentID {
	t.Helper()
	if _, err := f.c.Enroll(context.Background(), owner, plain); err != nil {
		t.Fatal(err)
	}
	e, err := f.s.Enrollments.Enrollment(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	return e.Credentials.ID
}
func obs(t *testing.T, s application.Status, stage string, checked time.Time) {
	t.Helper()
	if s.Observation == nil || s.Observation.Stage != stage || !s.Observation.CheckedAt.Equal(checked) {
		t.Fatalf("unexpected observation: %+v", s)
	}
}

func TestStatusShouldUseCompletionFreshnessWithoutConsumingGuardsWhenCacheIsRecent(t *testing.T) {
	// Given
	f := setup(t, func(context.Context, application.Credentials) (application.Observation, error) {
		return application.Observation{Stage: "A", CheckedAt: epoch.Add(-time.Hour)}, nil
	})
	f.enroll(t, 101)
	f.clock.set(epoch.Add(59 * time.Second))
	// When
	recent, e1 := f.c.Status(context.Background(), 101)
	f.clock.set(epoch.Add(time.Minute))
	at, e2 := f.c.Status(context.Background(), 101)
	e, _ := f.s.Enrollments.Enrollment(context.Background(), 101)
	deadline, e3 := (tracking.Persistence{DB: f.s}).CheckEligibility(context.Background(), 101, e.Credentials.ID, true)
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
	f := setup(t, func(context.Context, application.Credentials) (application.Observation, error) {
		if fail.Load() {
			return application.Observation{}, errors.New("secret remote details")
		}
		return application.Observation{Stage: "A"}, nil
	})
	f.enroll(t, 101)
	fail.Store(true)
	f.clock.set(epoch.Add(time.Minute))
	// When
	failed, e1 := f.c.Status(context.Background(), 101)
	f.s.Close()
	reopened, err := data.Open(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.s = reopened
	f.c = tracking.New(tracking.Persistence{DB: f.s}, f.k, portalFunc(func(context.Context, application.Credentials) (application.Observation, error) {
		f.calls.Add(1)
		return application.Observation{}, errors.New("secret")
	}), auth.New(false, []int64{101}), f.clock)
	f.clock.set(epoch.Add(119 * time.Second))
	before, e2 := f.c.Status(context.Background(), 101)
	f.clock.set(epoch.Add(2 * time.Minute))
	at, e3 := f.c.Status(context.Background(), 101)
	// Then
	if !errors.Is(e1, tracking.ErrCheck) || e2 != nil || !errors.Is(e3, tracking.ErrCheck) || failed.Fresh || before.Fresh || at.Fresh || f.calls.Load() != 3 || strings.Contains(e1.Error(), "secret") || !before.NextEligibleAt.Equal(epoch.Add(2*time.Minute)) {
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
	history, e5 := f.s.History.History(context.Background(), 101, id)
	// Then
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || f.calls.Load() != 3 || !scheduled.Fresh || !guarded.NextEligibleAt.Equal(epoch.Add(2*time.Minute)) || cached.Fresh || !manual.Fresh || len(history) != 0 {
		t.Fatalf("scheduled guard: %+v %+v %+v calls=%d errors=%v/%v/%v/%v/%v", scheduled, guarded, cached, f.calls.Load(), e1, e2, e3, e4, e5)
	}
}

func TestScheduledShouldPreventManualRetryWhenScheduledAttemptFails(t *testing.T) {
	// Given
	var fail atomic.Bool
	f := setup(t, func(context.Context, application.Credentials) (application.Observation, error) {
		if fail.Load() {
			return application.Observation{}, errors.New("private")
		}
		return application.Observation{Stage: "A"}, nil
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
	if !errors.Is(e1, tracking.ErrCheck) || e2 != nil || !errors.Is(e3, tracking.ErrCheck) || before.Fresh || f.calls.Load() != 3 {
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
	s   application.Status
	err error
}

func TestStatusShouldShareSingleFlightAndKeepOtherWaitersWhenOneCallerCancels(t *testing.T) {
	// Given
	entered, release := make(chan struct{}), make(chan struct{})
	var block atomic.Bool
	f := setup(t, func(ctx context.Context, _ application.Credentials) (application.Observation, error) {
		if block.Load() {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return application.Observation{}, ctx.Err()
			}
		}
		return application.Observation{Stage: "A"}, nil
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

func TestTrackingShouldDenyAllWorkBeforeDataAccessWhenOwnerIsUnauthorized(t *testing.T) {
	// Given
	f := setup(t, nil)
	f.enroll(t, 101)
	// When
	_, e1 := f.c.Enroll(context.Background(), 303, plain)
	_, e2 := f.c.Status(context.Background(), 303)
	_, e3 := f.c.Scheduled(context.Background(), 303)
	e4 := f.c.Remove(context.Background(), 303)
	e5 := f.c.RunOwnerWork(context.Background(), 303, "", func(context.Context) error { t.Error("unauthorized callback"); return nil })
	_, other := f.c.Status(context.Background(), 202)
	// Then
	for _, err := range []error{e1, e2, e3, e4, e5} {
		if !errors.Is(err, tracking.ErrDenied) {
			t.Fatalf("denial: %v", err)
		}
	}
	if !errors.Is(other, data.ErrNotFound) || f.calls.Load() != 1 {
		t.Fatalf("owner isolation: %v calls=%d", other, f.calls.Load())
	}
}

func TestEnrollShouldRefuseReplacementWithoutPortalCheckWhenOwnerAlreadyExists(t *testing.T) {
	// Given
	f := setup(t, nil)
	id := f.enroll(t, 101)
	// When
	_, err := f.c.Enroll(context.Background(), 101, plain)
	e, read := f.s.Enrollments.Enrollment(context.Background(), 101)
	queue, qerr := f.s.Pending.Queue(context.Background(), 101)
	// Then
	if !errors.Is(err, data.ErrExists) || read != nil || qerr != nil || e.Credentials.ID != id || f.calls.Load() != 1 || len(queue) != 0 {
		t.Fatalf("replacement: %v/%v/%v calls=%d queue=%d", err, read, qerr, f.calls.Load(), len(queue))
	}
	recovered, err := f.k.Open(e.Credentials)
	if err != nil || recovered != plain {
		t.Fatal("enrollment credentials missing")
	}
}

type brokenVault struct{ tracking.Vault }

func (v brokenVault) Seal(application.EnrollmentID, application.Credentials) (application.SealedCredentials, error) {
	return application.SealedCredentials{}, errors.New("private encryption detail")
}

type failingStore struct{ tracking.Store }

func (s failingStore) Enroll(context.Context, int64, application.SealedCredentials, application.Observation) error {
	return errors.New("private storage detail")
}
func TestEnrollShouldLeaveNoCommittedCaseWhenAnyEnrollmentStageFails(t *testing.T) {
	for _, kind := range []string{"portal", "encryption", "storage", "cancellation", "validation"} {
		t.Run(kind, func(t *testing.T) {
			// Given
			f := setup(t, nil)
			var v tracking.Vault = f.k
			var s tracking.Store = tracking.Persistence{DB: f.s}
			ctx := context.Background()
			input := plain
			p := portalFunc(func(context.Context, application.Credentials) (application.Observation, error) {
				return application.Observation{Stage: "A"}, nil
			})
			switch kind {
			case "portal":
				p = func(context.Context, application.Credentials) (application.Observation, error) {
					return application.Observation{}, errors.New("private remote detail")
				}
			case "encryption":
				v = brokenVault{v}
			case "storage":
				s = failingStore{s}
			case "cancellation":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "validation":
				input.Password = ""
			}
			c := tracking.New(s, v, p, auth.New(false, []int64{101}), f.clock)
			// When
			_, err := c.Enroll(ctx, 101, input)
			_, read := f.s.Enrollments.Enrollment(context.Background(), 101)
			// Then
			if err == nil || strings.Contains(err.Error(), "private") || !errors.Is(read, data.ErrNotFound) {
				t.Fatalf("failed enrollment committed: %v/%v", err, read)
			}
		})
	}
}

func TestRemoveShouldCancelAndDrainPortalAndSendBeforeDeletingWhenLateResultsArrive(t *testing.T) {
	// Given
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var block atomic.Bool
	f := setup(t, func(ctx context.Context, _ application.Credentials) (application.Observation, error) {
		if block.Load() {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return application.Observation{Stage: "LATE"}, nil
		}
		return application.Observation{Stage: "A"}, nil
	})
	id := f.enroll(t, 101)
	block.Store(true)
	f.clock.set(epoch.Add(time.Minute))
	checked := make(chan answer, 1)
	go func() { s, e := f.c.Status(context.Background(), 101); checked <- answer{s, e} }()
	<-entered
	sendEntered, sendCanceled, sendRelease := make(chan struct{}), make(chan struct{}), make(chan struct{})
	sent := make(chan error, 1)
	go func() {
		sent <- f.c.RunOwnerWork(context.Background(), 101, id, func(ctx context.Context) error {
			close(sendEntered)
			<-ctx.Done()
			close(sendCanceled)
			<-sendRelease
			return ctx.Err()
		})
	}()
	<-sendEntered
	removed := make(chan error, 1)
	// When
	go func() { removed <- f.c.Remove(context.Background(), 101) }()
	<-canceled
	<-sendCanceled
	_, busyStatus := f.c.Status(context.Background(), 101)
	_, busyEnroll := f.c.Enroll(context.Background(), 101, plain)
	busySend := f.c.RunOwnerWork(context.Background(), 101, id, func(context.Context) error { t.Error("send during remove"); return nil })
	before, beforeErr := f.s.Enrollments.Enrollment(context.Background(), 101)
	select {
	case err := <-removed:
		t.Fatalf("removal returned without drain: %v", err)
	default:
	}
	close(release)
	late := <-checked
	close(sendRelease)
	sendErr := <-sent
	removeErr := <-removed
	_, missing := f.s.Enrollments.Enrollment(context.Background(), 101)
	block.Store(false)
	newID := f.enroll(t, 101)
	lateObservation := (tracking.Persistence{DB: f.s}).Observe(context.Background(), 101, id, application.Observation{Stage: "LATE", CheckedAt: f.clock.Now()})
	oldSend := f.c.RunOwnerWork(context.Background(), 101, id, func(context.Context) error { t.Error("old enrollment sent"); return nil })
	f.clock.set(epoch.Add(119 * time.Second))
	cached, statusErr := f.c.Status(context.Background(), 101)
	// Then
	if !errors.Is(busyStatus, tracking.ErrRemoving) || !errors.Is(busyEnroll, tracking.ErrRemoving) || !errors.Is(busySend, tracking.ErrRemoving) || beforeErr != nil || before.Credentials.ID != id || !errors.Is(late.err, tracking.ErrRemoving) || !errors.Is(sendErr, context.Canceled) || removeErr != nil || !errors.Is(missing, data.ErrNotFound) || newID == id || !errors.Is(lateObservation, data.ErrNotFound) || !errors.Is(oldSend, data.ErrNotFound) || statusErr != nil || cached.Fresh || !cached.NextEligibleAt.Equal(epoch.Add(2*time.Minute)) {
		t.Fatalf("removal results: late=%v send=%v remove=%v missing=%v lateObservation=%v oldSend=%v status=%+v err=%v", late.err, sendErr, removeErr, missing, lateObservation, oldSend, cached, statusErr)
	}
	obs(t, cached, "A", epoch.Add(time.Minute))
}

func TestRemoveShouldPreventBaselineCommitWhenEnrollmentIsCanceledDuringCheck(t *testing.T) {
	// Given
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f := setup(t, func(ctx context.Context, _ application.Credentials) (application.Observation, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return application.Observation{Stage: "late"}, nil
	})
	enrolled := make(chan error, 1)
	go func() { _, e := f.c.Enroll(context.Background(), 101, plain); enrolled <- e }()
	<-entered
	removed := make(chan error, 1)
	// When
	go func() { removed <- f.c.Remove(context.Background(), 101) }()
	<-canceled
	_, busy := f.c.Enroll(context.Background(), 101, plain)
	close(release)
	enrollErr, removeErr := <-enrolled, <-removed
	_, missing := f.s.Enrollments.Enrollment(context.Background(), 101)
	// Then
	if !errors.Is(busy, tracking.ErrRemoving) || !errors.Is(enrollErr, context.Canceled) || !errors.Is(removeErr, data.ErrNotFound) || !errors.Is(missing, data.ErrNotFound) {
		t.Fatalf("enrollment cancellation: busy=%v enroll=%v remove=%v missing=%v", busy, enrollErr, removeErr, missing)
	}
}

func TestOwnerWorkShouldSerializeMembershipAfterSendWhenAnUnblockArrives(t *testing.T) {
	// Given
	f := setup(t, nil)
	id := f.enroll(t, 101)
	entered, release := make(chan struct{}), make(chan struct{})
	sent := make(chan error, 1)
	go func() {
		sent <- f.c.RunOwnerWork(context.Background(), 101, id, func(context.Context) error {
			close(entered)
			<-release
			return (notifications.Persistence{DB: f.s}).SetBlocked(context.Background(), 101, true)
		})
	}()
	<-entered
	eventCtx := waitingContext(context.Background())
	event := make(chan error, 1)
	// When
	go func() {
		event <- f.c.RunOwnerWork(eventCtx, 101, "", func(ctx context.Context) error {
			return (notifications.Persistence{DB: f.s}).SetBlocked(ctx, 101, false)
		})
	}()
	<-eventCtx.entered
	select {
	case err := <-event:
		t.Fatalf("membership passed active send: %v", err)
	default:
	}
	close(release)
	sendErr, eventErr := <-sent, <-event
	recipient, _, gateErr := (notifications.Persistence{DB: f.s}).DeliveryGates(context.Background(), 101)
	removed := f.c.Remove(context.Background(), 101)
	noCaseEvent := f.c.RunOwnerWork(context.Background(), 101, "", func(ctx context.Context) error {
		return (notifications.Persistence{DB: f.s}).SetBlocked(ctx, 101, true)
	})
	// Then
	if sendErr != nil || eventErr != nil || gateErr != nil || recipient.Blocked || recipient.Pause != "" || removed != nil || noCaseEvent != nil {
		t.Fatalf("serialized membership: send=%v event=%v state=%+v gate=%v remove=%v noCase=%v", sendErr, eventErr, recipient, gateErr, removed, noCaseEvent)
	}
}

func TestStatusShouldCancelUnderlyingFlightWhenLastWaiterLeaves(t *testing.T) {
	// Given
	var block atomic.Bool
	entered, canceled := make(chan struct{}), make(chan struct{})
	f := setup(t, func(ctx context.Context, _ application.Credentials) (application.Observation, error) {
		if block.Load() {
			close(entered)
			<-ctx.Done()
			close(canceled)
			return application.Observation{}, ctx.Err()
		}
		return application.Observation{Stage: "A"}, nil
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

type rejectingVault struct{ tracking.Vault }

func (v rejectingVault) Open(application.SealedCredentials) (application.Credentials, error) {
	return application.Credentials{}, errors.New("private authentication details")
}

type rejectingObserve struct{ tracking.Store }

func (s rejectingObserve) Observe(context.Context, int64, application.EnrollmentID, application.Observation) error {
	return errors.New("private database details")
}
func TestStatusShouldPreserveOldCacheWhenCredentialAuthenticationOrObservationCommitFails(t *testing.T) {
	for _, kind := range []string{"authentication", "observation"} {
		t.Run(kind, func(t *testing.T) {
			// Given
			f := setup(t, nil)
			id := f.enroll(t, 101)
			var v tracking.Vault = f.k
			var s tracking.Store = tracking.Persistence{DB: f.s}
			var calls atomic.Int32
			if kind == "authentication" {
				v = rejectingVault{v}
			} else {
				s = rejectingObserve{s}
			}
			c := tracking.New(s, v, portalFunc(func(context.Context, application.Credentials) (application.Observation, error) {
				calls.Add(1)
				return application.Observation{Stage: "B"}, nil
			}), auth.New(false, []int64{101}), f.clock)
			f.clock.set(epoch.Add(time.Minute))
			// When
			status, err := c.Status(context.Background(), 101)
			e, readErr := f.s.Enrollments.Enrollment(context.Background(), 101)
			history, historyErr := f.s.History.History(context.Background(), 101, id)
			// Then
			expected := tracking.ErrCredentials
			if kind == "observation" {
				expected = tracking.ErrStorage
			}
			if !errors.Is(err, expected) || readErr != nil || historyErr != nil || e.Observation.Stage != "A" || !e.Observation.CheckedAt.Equal(epoch) || len(history) != 0 || status.Fresh || (kind == "authentication" && calls.Load() != 0) {
				t.Fatalf("failed refresh: status=%+v err=%v read=%v history=%v calls=%d", status, err, readErr, historyErr, calls.Load())
			}
			obs(t, status, "A", epoch)
		})
	}
}

type mutablePolicy struct{ allowed atomic.Bool }

func (p *mutablePolicy) Allows(owner int64) bool { return owner == 101 && p.allowed.Load() }
func TestScheduledShouldRecheckAuthorizationBeforeCommitWhenEligibilityChangesDuringCheck(t *testing.T) {
	// Given
	f := setup(t, nil)
	f.enroll(t, 101)
	policy := &mutablePolicy{}
	policy.allowed.Store(true)
	entered, release := make(chan struct{}), make(chan struct{})
	c := tracking.New(tracking.Persistence{DB: f.s}, f.k, portalFunc(func(context.Context, application.Credentials) (application.Observation, error) {
		close(entered)
		<-release
		return application.Observation{Stage: "B"}, nil
	}), policy, f.clock)
	f.clock.set(epoch.Add(time.Minute))
	result := make(chan error, 1)
	// When
	go func() { _, e := c.Scheduled(context.Background(), 101); result <- e }()
	<-entered
	policy.allowed.Store(false)
	close(release)
	err := <-result
	e, readErr := f.s.Enrollments.Enrollment(context.Background(), 101)
	// Then
	if !errors.Is(err, tracking.ErrDenied) || readErr != nil || e.Observation.Stage != "A" {
		t.Fatalf("authorization withdrawal: err=%v read=%v stage=%s", err, readErr, e.Observation.Stage)
	}
}

func TestStatusShouldCreateDurableTransitionsFromBothSourcesWhenStagesChange(t *testing.T) {
	// Given
	var stage atomic.Value
	stage.Store("A")
	f := setup(t, func(context.Context, application.Credentials) (application.Observation, error) {
		return application.Observation{Stage: stage.Load().(string)}, nil
	})
	id := f.enroll(t, 101)
	// When
	f.clock.set(epoch.Add(time.Minute))
	stage.Store("B")
	manual, e1 := f.c.Status(context.Background(), 101)
	f.clock.set(epoch.Add(2 * time.Minute))
	stage.Store("A")
	scheduled, e2 := f.c.Scheduled(context.Background(), 101)
	history, e3 := f.s.History.History(context.Background(), 101, id)
	queue, e4 := f.s.Pending.Queue(context.Background(), 101)
	// Then
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || !manual.Fresh || !scheduled.Fresh || len(history) != 2 || len(queue) != 2 || queue[0].Stage != "B" || queue[1].Stage != "A" || queue[0].MessageID != 0 || queue[1].MessageID != 0 {
		t.Fatalf("shared observation transaction: history=%+v queue=%+v errors=%v/%v/%v/%v", history, queue, e1, e2, e3, e4)
	}
}

func TestRemoveShouldRetainEnrollmentAndReopenWorkWhenCallerCancelsDuringDrain(t *testing.T) {
	// Given
	f := setup(t, nil)
	id := f.enroll(t, 101)
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	work := make(chan error, 1)
	go func() {
		work <- f.c.RunOwnerWork(context.Background(), 101, id, func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return ctx.Err()
		})
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	removed := make(chan error, 1)
	// When
	go func() { removed <- f.c.Remove(ctx, 101) }()
	<-canceled
	cancel()
	close(release)
	workErr, removeErr := <-work, <-removed
	e, readErr := f.s.Enrollments.Enrollment(context.Background(), 101)
	status, statusErr := f.c.Status(context.Background(), 101)
	// Then
	if !errors.Is(workErr, context.Canceled) || !errors.Is(removeErr, context.Canceled) || readErr != nil || e.Credentials.ID != id || statusErr != nil || status.Observation == nil {
		t.Fatalf("canceled removal: work=%v remove=%v read=%v status=%v", workErr, removeErr, readErr, statusErr)
	}
}

func TestScheduledShouldReuseRecentEnrollmentBaselineWithoutReservationWhenRunAfterRestart(t *testing.T) {
	// Given
	f := setup(t, nil)
	id := f.enroll(t, 101)
	f.s.Close()
	reopened, err := data.Open(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.s = reopened
	c := tracking.New(tracking.Persistence{DB: f.s}, f.k, portalFunc(func(context.Context, application.Credentials) (application.Observation, error) {
		t.Error("recent baseline triggered portal work")
		return application.Observation{}, nil
	}), auth.New(false, []int64{101}), f.clock)
	f.clock.set(epoch.Add(59 * time.Second))
	// When
	status, statusErr := c.Scheduled(context.Background(), 101)
	next, guardErr := (tracking.Persistence{DB: f.s}).CheckEligibility(context.Background(), 101, id, true)
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
	s, err := scheduler.New(scheduler.Config{Inventory: f.s.Enrollments, Tracking: f.c, Policy: auth.New(false, []int64{101}), Clock: f.clock, Wait: func(_ context.Context, deadline time.Time) error {
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
	f := setup(t, nil)
	started := make(chan struct{}, 2)
	cancelled := make(chan struct{}, 2)
	release := make(chan struct{})
	finished := make(chan error, 2)
	for _, owner := range []int64{101, 202} {
		go func(owner int64) {
			finished <- f.c.RunOwnerWork(context.Background(), owner, "", func(ctx context.Context) error {
				started <- struct{}{}
				<-ctx.Done()
				cancelled <- struct{}{}
				<-release
				return ctx.Err()
			})
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
	if e := f.c.RunOwnerWork(context.Background(), 101, "", func(context.Context) error { t.Fatal("admitted callback after shutdown"); return nil }); !errors.Is(e, tracking.ErrClosed) {
		t.Fatal("terminal admission not enforced")
	}
	close(release)
	<-done
	for i := 0; i < 2; i++ {
		if e := <-finished; !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	}
	f.c.Shutdown()
	if _, e := f.c.Enroll(context.Background(), 101, plain); !errors.Is(e, tracking.ErrClosed) {
		t.Fatal("enrollment admitted after shutdown")
	}
	if e := f.c.Remove(context.Background(), 101); !errors.Is(e, tracking.ErrClosed) {
		t.Fatal("removal admitted after shutdown")
	}
}
