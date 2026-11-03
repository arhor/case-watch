package notifications_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/auth"
	"github.com/arhor/case-watch/internal/credentials"
	"github.com/arhor/case-watch/internal/data"
	"github.com/arhor/case-watch/internal/notifications"
	"github.com/arhor/case-watch/internal/tracking"
)

var ctx = context.Background()
var epoch = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) add(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.now = c.now.Add(d) }

type sender func(context.Context, int64, string) (application.SendOutcome, error)

func (f sender) Send(c context.Context, o int64, s string) (application.SendOutcome, error) {
	return f(c, o, s)
}

type portal struct{}

func (portal) Check(context.Context, application.Credentials) (application.Observation, error) {
	return application.Observation{}, errors.New("unused")
}

type fixture struct {
	s      *data.Store
	c      *tracking.Coordinator
	clock  *clock
	policy application.Policy
	ids    map[int64]string
	path   string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{clock: &clock{now: epoch}, path: filepath.Join(t.TempDir(), "state.db"), policy: auth.New(false, []int64{101, 202}), ids: map[int64]string{}}
	var e error
	f.s, e = data.Open(ctx, f.path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.s.Close() })
	k, e := credentials.Parse([]byte(`{"version":1,"active_key_id":"test","keys":{"test":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}}`))
	if e != nil {
		t.Fatal(e)
	}
	f.c = tracking.New(tracking.Persistence{DB: f.s}, k, portal{}, f.policy, f.clock)
	for _, o := range []int64{101, 202} {
		id, e := data.NewEnrollmentID()
		if e != nil {
			t.Fatal(e)
		}
		sealed, e := k.Seal(id, application.Credentials{Number: "synthetic", Password: "synthetic"})
		if e != nil {
			t.Fatal(e)
		}
		if e = (tracking.Persistence{DB: f.s}).Enroll(ctx, o, sealed, application.Observation{Stage: "A", CheckedAt: epoch}); e != nil {
			t.Fatal(e)
		}
		f.ids[o] = id
	}
	return f
}
func (f *fixture) observe(t *testing.T, o int64, stage string) {
	t.Helper()
	if e := (tracking.Persistence{DB: f.s}).Observe(ctx, o, f.ids[o], application.Observation{Stage: stage, CheckedAt: f.clock.Now()}); e != nil {
		t.Fatal(e)
	}
}
func (f *fixture) worker(t *testing.T, s sender) *notifications.Worker {
	t.Helper()
	w, e := notifications.New(notifications.Config{Store: notifications.Persistence{DB: f.s}, Coordinator: f.c, Policy: f.policy, Clock: f.clock, Sender: s})
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Start(ctx); e != nil {
		t.Fatal(e)
	}
	return w
}
func step(t *testing.T, w *notifications.Worker) {
	t.Helper()
	if e := w.Step(ctx); e != nil {
		t.Fatal(e)
	}
}
func queue(t *testing.T, f *fixture, o int64) []data.Delivery {
	t.Helper()
	q, e := f.s.Pending.FindByRecipient(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	return q
}

func TestWorkerShouldExhaustSixAttemptsWithoutOvertakingWhenHeadFails(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	f.observe(t, 101, "A")
	f.observe(t, 202, "B")
	calls := map[int64]int{}
	w := f.worker(t, func(_ context.Context, o int64, _ string) (application.SendOutcome, error) {
		calls[o]++
		f.clock.add(time.Second)
		if o == 202 {
			return application.SendOutcome{Category: application.SendAccepted, MessageID: 1}, nil
		}
		return application.SendOutcome{Category: application.SendTransient}, nil
	})
	// When
	for i := 0; i < 6; i++ {
		step(t, w)
		q := queue(t, f, 101)
		if q[0].CycleAttempts != i+1 {
			t.Fatalf("attempt %d: %+v", i, q)
		}
		if i < 5 {
			deadline := q[0].NextAttemptAt
			want := f.clock.Now().Add(30 * time.Second * time.Duration(1<<i))
			if !deadline.Equal(want) && i > 0 {
				t.Fatalf("deadline %v want %v", deadline, want)
			}
			f.clock.add(deadline.Sub(f.clock.Now()) - time.Nanosecond)
			step(t, w)
			if calls[101] != i+1 {
				t.Fatal("early retry")
			}
			f.clock.add(time.Nanosecond)
		}
	}
	f.clock.add(24 * time.Hour)
	step(t, w)
	f.observe(t, 101, "C")
	step(t, w)
	w.Membership(ctx, notifications.Event{Owner: 101, Interaction: true})
	step(t, w)
	// Then
	q := queue(t, f, 101)
	r, _, e := (notifications.Persistence{DB: f.s}).DeliveryGates(ctx, 101)
	if e != nil || calls[101] != 6 || calls[202] != 1 || r.Pause != "exhausted" || q[0].LifetimeAttempts != 6 || q[1].CycleAttempts != 0 || len(q) != 3 {
		t.Fatalf("calls=%v queue=%+v gates=%+v err=%v", calls, q, r, e)
	}
}

func TestWorkerShouldResumeFreshCycleWithoutStartWhenBlockCycleObserved(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	calls := 0
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		if calls == 1 {
			return application.SendOutcome{Category: application.SendBlocked}, nil
		}
		return application.SendOutcome{Category: application.SendAccepted, MessageID: 7}, nil
	})
	// When
	step(t, w)
	step(t, w)
	w.Membership(ctx, notifications.Event{Owner: 101, Unblocked: true})
	step(t, w)
	// Then
	q := queue(t, f, 101)
	if calls != 2 || q[0].Status != "delivered" || q[0].LifetimeAttempts != 2 || q[0].CycleAttempts != 1 {
		t.Fatalf("calls=%d queue=%+v", calls, q)
	}
}

func TestWorkerShouldGateAllRecipientsWhenRateOrTokenFailureOccurs(t *testing.T) {
	for _, category := range []application.SendCategory{application.SendRateLimited, application.SendInvalidToken} {
		t.Run(string(category), func(t *testing.T) {
			// Given
			f := setup(t)
			f.observe(t, 101, "B")
			f.observe(t, 202, "B")
			calls := 0
			w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
				calls++
				return application.SendOutcome{Category: category, RetryAfter: time.Hour}, errors.New("sanitized failure")
			})
			// When
			step(t, w)
			step(t, w)
			allowed := w.AllowOutbound(ctx, 202)
			// Then
			q := queue(t, f, 202)
			if calls != 1 || allowed || q[0].LifetimeAttempts != 0 {
				t.Fatalf("calls=%d allowed=%v queue=%+v", calls, allowed, q)
			}
		})
	}
}

func TestWorkerShouldRetainAttemptAndDelayWhenRestartRecoversUncertainAcceptance(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	d, e := (notifications.Persistence{DB: f.s}).ReserveDelivery(ctx, 101, epoch)
	if e != nil {
		t.Fatal(e)
	}
	f.s.Close()
	f.s, e = data.Open(ctx, f.path)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	f.c = tracking.New(tracking.Persistence{DB: f.s}, nil, portal{}, f.policy, f.clock)
	// When
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		return application.SendOutcome{Category: application.SendAccepted, MessageID: 9}, nil
	})
	step(t, w)
	before := queue(t, f, 101)[0]
	f.clock.add(30 * time.Second)
	step(t, w)
	after := queue(t, f, 101)[0]
	// Then
	if calls != 1 || before.Failure != "uncertain" || before.LifetimeAttempts != 1 || after.LifetimeAttempts != 2 || after.Status != "delivered" || d.LifetimeAttempts != 1 {
		t.Fatalf("before=%+v after=%+v calls=%d", before, after, calls)
	}
}

func TestWorkerShouldApplyUnblockAfterActiveSendWhenMembershipArrivesDuringReservation(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		if calls == 1 {
			close(entered)
			<-release
			return application.SendOutcome{Category: application.SendBlocked}, errors.New("sanitized")
		}
		return application.SendOutcome{Category: application.SendAccepted, MessageID: 3}, nil
	})
	// When
	sent := make(chan error, 1)
	go func() { sent <- w.Step(ctx) }()
	<-entered
	event := make(chan struct{})
	go func() { w.Membership(ctx, notifications.Event{Owner: 101, Unblocked: true}); close(event) }()
	close(release)
	sendErr := <-sent
	<-event
	step(t, w)
	// Then
	q := queue(t, f, 101)
	if sendErr != nil || calls != 2 || q[0].Status != "delivered" || q[0].LifetimeAttempts != 2 || q[0].CycleAttempts != 1 {
		t.Fatalf("calls=%d queue=%+v err=%v", calls, q, sendErr)
	}
}

type faultStore struct {
	notifications.Persistence
	fail bool
}

func (s *faultStore) CompleteDelivery(c context.Context, d data.Delivery, o application.SendOutcome, n time.Time) error {
	if s.fail {
		return data.ErrStorage
	}
	return s.Persistence.CompleteDelivery(c, d, o, n)
}
func TestWorkerShouldStopForRecoveryWhenAcceptedOutcomeCannotBePersisted(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	fs := &faultStore{Persistence: notifications.Persistence{DB: f.s}, fail: true}
	calls := 0
	w, e := notifications.New(notifications.Config{Store: fs, Coordinator: f.c, Policy: f.policy, Clock: f.clock, Sender: sender(func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		return application.SendOutcome{Category: application.SendAccepted, MessageID: 3}, nil
	})})
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Start(ctx); e != nil {
		t.Fatal(e)
	}
	// When
	err := w.Step(ctx)
	q := queue(t, f, 101)
	// Then
	if !errors.Is(err, notifications.ErrWorker) || calls != 1 || q[0].Status != "reserved" || q[0].LifetimeAttempts != 1 {
		t.Fatalf("err=%v calls=%d queue=%+v", err, calls, q)
	}
}

func TestWorkerShouldKeepDirectRepliesSeparateWhenGlobalFailureRecorded(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		t.Fatal("unexpected durable send")
		return application.SendOutcome{}, nil
	})
	// When
	e := w.RecordDirectOutcome(ctx, application.SendOutcome{Category: application.SendRateLimited, RetryAfter: time.Hour})
	step(t, w)
	// Then
	q := queue(t, f, 101)
	if e != nil || w.AllowOutbound(ctx, 202) || q[0].LifetimeAttempts != 0 || q[0].Status != "pending" {
		t.Fatalf("err=%v queue=%+v", e, q)
	}
}

func TestWorkerShouldRetainExhaustionAndLifetimeWhenRestartAndOperatorResume(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		return application.SendOutcome{Category: application.SendTransient}, nil
	})
	for i := 0; i < 6; i++ {
		step(t, w)
		f.clock.add(time.Hour)
	}
	// When
	restarted := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		return application.SendOutcome{Category: application.SendAccepted, MessageID: 8}, nil
	})
	step(t, restarted)
	before := queue(t, f, 101)[0]
	e := (notifications.Persistence{DB: f.s}).ResumeRecipient(ctx, 101)
	step(t, restarted)
	// Then
	q := queue(t, f, 101)
	if e != nil || before.LifetimeAttempts != 6 || before.CycleAttempts != 6 || q[0].LifetimeAttempts != 7 || q[0].CycleAttempts != 1 || q[0].Status != "delivered" {
		t.Fatalf("err=%v before=%+v after=%+v", e, before, q)
	}
}

type switchPolicy struct {
	mu      sync.Mutex
	allowed bool
}

func (p *switchPolicy) Allows(int64) bool { p.mu.Lock(); defer p.mu.Unlock(); return p.allowed }
func (p *switchPolicy) set(v bool)        { p.mu.Lock(); defer p.mu.Unlock(); p.allowed = v }
func TestWorkerShouldPreserveCountersWhenAuthorizationSuppressesDelivery(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	p := &switchPolicy{}
	calls := 0
	w, e := notifications.New(notifications.Config{Store: notifications.Persistence{DB: f.s}, Coordinator: f.c, Policy: p, Clock: f.clock, Sender: sender(func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		return application.SendOutcome{Category: application.SendAccepted, MessageID: 1}, nil
	})})
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Start(ctx); e != nil {
		t.Fatal(e)
	}
	// When
	step(t, w)
	suppressed := queue(t, f, 101)[0]
	p.set(true)
	step(t, w)
	// Then
	q := queue(t, f, 101)
	if calls != 1 || suppressed.LifetimeAttempts != 0 || q[0].Status != "delivered" {
		t.Fatalf("calls=%d suppressed=%+v queue=%+v", calls, suppressed, q)
	}
}

func TestWorkerShouldDrainBeforeDeletionWhenRemovalCancelsActiveSend(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	entered := make(chan struct{})
	returned := make(chan struct{})
	w := f.worker(t, func(c context.Context, _ int64, _ string) (application.SendOutcome, error) {
		close(entered)
		<-c.Done()
		close(returned)
		return application.SendOutcome{Category: application.SendBlocked}, errors.New("sanitized")
	})
	// When
	done := make(chan error, 1)
	go func() { done <- w.Step(ctx) }()
	<-entered
	removed := f.c.Remove(ctx, 101)
	sendErr := <-done
	<-returned
	q := queue(t, f, 101)
	r, _, e := (notifications.Persistence{DB: f.s}).DeliveryGates(ctx, 101)
	// Then
	if removed != nil || sendErr != nil || e != nil || len(q) != 0 || !r.Blocked {
		t.Fatalf("removed=%v send=%v gates=%+v queue=%+v err=%v", removed, sendErr, r, q, e)
	}
}

func TestSendDirectShouldWaitForDurableGlobalGateWhenWorkerSendIsActive(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		if calls == 1 {
			close(entered)
			<-release
		}
		return application.SendOutcome{Category: application.SendRateLimited, RetryAfter: time.Hour}, errors.New("sanitized")
	})
	// When
	done := make(chan error, 1)
	go func() { done <- w.Step(ctx) }()
	<-entered
	direct := make(chan error, 1)
	go func() { _, e := w.SendDirect(ctx, 202, "synthetic"); direct <- e }()
	close(release)
	e1, e2 := <-done, <-direct
	// Then
	q := queue(t, f, 101)
	if e1 != nil || e2 != nil || calls != 1 || q[0].LifetimeAttempts != 1 || q[0].Status != "pending" {
		t.Fatalf("errors=%v/%v calls=%d queue=%+v", e1, e2, calls, q)
	}
}

func TestSendDirectShouldGateDurableSendWhenDirectOutcomeArrivesDuringCancellation(t *testing.T) {
	for _, category := range []application.SendCategory{application.SendRateLimited, application.SendInvalidToken} {
		t.Run(string(category), func(t *testing.T) {
			// Given
			f := setup(t)
			f.observe(t, 101, "B")
			c, cancel := context.WithCancel(ctx)
			entered, release := make(chan struct{}), make(chan struct{})
			calls := 0
			w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
				calls++
				close(entered)
				<-release
				cancel()
				return application.SendOutcome{Category: category, RetryAfter: time.Hour}, errors.New("sanitized")
			})
			// When
			direct := make(chan error, 1)
			go func() { _, e := w.SendDirect(c, 202, "synthetic"); direct <- e }()
			<-entered
			done := make(chan error, 1)
			go func() { done <- w.Step(ctx) }()
			close(release)
			directErr, stepErr := <-direct, <-done
			f.s.Close()
			s, e := data.Open(ctx, f.path)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			_, g, e := (notifications.Persistence{DB: s}).DeliveryGates(ctx, 101)
			q, qe := s.Pending.FindByRecipient(ctx, 101)
			// Then
			if directErr == nil || stepErr != nil || e != nil || qe != nil || calls != 1 || q[0].LifetimeAttempts != 0 || q[0].Status != "pending" || (category == application.SendRateLimited && !g.RateUntil.Equal(epoch.Add(time.Hour))) || (category == application.SendInvalidToken && !g.TokenPaused) {
				t.Fatalf("errors=%v/%v/%v/%v calls=%d gates=%+v queue=%+v", directErr, stepErr, e, qe, calls, g, q)
			}
		})
	}
}

func (s *faultStore) RecordGlobalOutcome(c context.Context, o application.SendOutcome, n time.Time) error {
	if s.fail {
		return data.ErrStorage
	}
	return s.Persistence.RecordGlobalOutcome(c, o, n)
}
func TestSendDirectShouldReturnFatalFailureWhenCancelledOutcomeCannotPersist(t *testing.T) {
	// Given
	f := setup(t)
	c, cancel := context.WithCancel(ctx)
	fs := &faultStore{Persistence: notifications.Persistence{DB: f.s}, fail: true}
	w, e := notifications.New(notifications.Config{Store: fs, Coordinator: f.c, Policy: f.policy, Clock: f.clock, Sender: sender(func(context.Context, int64, string) (application.SendOutcome, error) {
		cancel()
		return application.SendOutcome{Category: application.SendInvalidToken}, errors.New("sanitized")
	})})
	if e != nil {
		t.Fatal(e)
	}
	// When
	_, err := w.SendDirect(c, 101, "synthetic")
	// Then
	if !errors.Is(err, notifications.ErrWorker) {
		t.Fatalf("err=%v", err)
	}
}

func TestWorkerShouldResumeOnExplicitUnblockWhenBlockUpdateWasMissed(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	calls := 0
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		if calls == 1 {
			return application.SendOutcome{Category: application.SendPermanent}, errors.New("sanitized")
		}
		return application.SendOutcome{Category: application.SendAccepted, MessageID: 2}, nil
	})
	// When
	step(t, w)
	w.Membership(ctx, notifications.Event{Owner: 101, Interaction: true})
	step(t, w)
	before := queue(t, f, 101)[0]
	w.Membership(ctx, notifications.Event{Owner: 101, Unblocked: true})
	step(t, w)
	// Then
	q := queue(t, f, 101)
	if before.LifetimeAttempts != 1 || calls != 2 || q[0].CycleAttempts != 1 || q[0].LifetimeAttempts != 2 || q[0].Status != "delivered" {
		t.Fatalf("calls=%d before=%+v queue=%+v", calls, before, q)
	}
}
