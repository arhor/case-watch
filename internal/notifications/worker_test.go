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
	r, e := (notifications.Persistence{DB: f.s}).RecipientState(ctx, 101)
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

func TestWorkerShouldGateAllRecipientsWhenRateLimitFailureOccurs(t *testing.T) {
	for _, category := range []application.SendCategory{application.SendRateLimited} {
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
			_, directErr := w.SendDirect(ctx, 202, "synthetic")
			// Then
			q := queue(t, f, 202)
			if calls != 1 || directErr != nil || q[0].LifetimeAttempts != 0 {
				t.Fatalf("calls=%d error=%v queue=%+v", calls, directErr, q)
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
		return application.SendOutcome{Category: application.SendRateLimited, RetryAfter: time.Hour}, nil
	})
	// When
	_, e := w.SendDirect(ctx, 202, "synthetic")
	step(t, w)
	// Then
	q := queue(t, f, 101)
	if e != nil || q[0].LifetimeAttempts != 0 || q[0].Status != "pending" {
		t.Fatalf("err=%v queue=%+v", e, q)
	}
}

func TestWorkerShouldRetainExhaustionAndLifetimeWhenRestartAndUnblock(t *testing.T) {
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
	e := (notifications.Persistence{DB: f.s}).ResumeUnblocked(ctx, 101)
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
	r, e := (notifications.Persistence{DB: f.s}).RecipientState(ctx, 101)
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
	for _, category := range []application.SendCategory{application.SendRateLimited} {
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
			q, qe := s.Pending.FindByRecipient(ctx, 101)
			// Then
			if directErr == nil || stepErr != nil || e != nil || qe != nil || calls != 1 || q[0].LifetimeAttempts != 0 || q[0].Status != "pending" {
				t.Fatalf("errors=%v/%v/%v/%v calls=%d queue=%+v", directErr, stepErr, e, qe, calls, q)
			}
		})
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

func TestWorkerShouldPreserveBudgetAndStopSendingWhenTokenIsRejected(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	f.observe(t, 101, "C")
	f.observe(t, 202, "B")
	calls := 0
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		return application.SendOutcome{Category: application.SendInvalidToken}, errors.New("sanitized")
	})
	// When
	err := w.Step(ctx)
	_, directErr := w.SendDirect(ctx, 202, "synthetic")
	again := w.Step(ctx)
	before := queue(t, f, 101)
	restarted := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		return application.SendOutcome{Category: application.SendAccepted, MessageID: 42}, nil
	})
	retry := restarted.Step(ctx)
	// Then
	if !errors.Is(err, notifications.ErrWorker) || !errors.Is(directErr, notifications.ErrWorker) || !errors.Is(again, notifications.ErrWorker) || calls != 1 || retry != nil {
		t.Fatalf("errors=%v/%v/%v retry=%v calls=%d", err, directErr, again, retry, calls)
	}
	if len(before) != 2 || before[0].CycleAttempts != 0 || before[0].LifetimeAttempts != 0 || before[0].Status != "pending" || before[1].Status != "pending" {
		t.Fatalf("rejected attempt altered queue: %+v", before)
	}
	q := queue(t, f, 101)
	if q[0].Status != "delivered" || q[0].CycleAttempts != 1 || q[0].LifetimeAttempts != 1 || q[1].Status != "pending" {
		t.Fatalf("restart did not resume in order: %+v", q)
	}
}

func TestSendDirectShouldStopDurableSendingWhenTokenIsRejectedDuringCancellation(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	calls := 0
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		cancel()
		return application.SendOutcome{Category: application.SendInvalidToken}, errors.New("sanitized")
	})
	// When
	_, directErr := w.SendDirect(c, 202, "synthetic")
	stepErr := w.Step(ctx)
	// Then
	q := queue(t, f, 101)
	if !errors.Is(directErr, notifications.ErrWorker) || !errors.Is(stepErr, notifications.ErrWorker) || calls != 1 || q[0].LifetimeAttempts != 0 || q[0].Status != "pending" {
		t.Fatalf("direct=%v step=%v calls=%d queue=%+v", directErr, stepErr, calls, q)
	}
}

func TestWorkerShouldRetainLastRetryWhenSixthAttemptRejectsToken(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	calls := 0
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		category := application.SendTransient
		if calls == 6 {
			category = application.SendInvalidToken
		}
		return application.SendOutcome{Category: category}, errors.New("sanitized")
	})
	for i := 0; i < 5; i++ {
		step(t, w)
		q := queue(t, f, 101)
		f.clock.add(q[0].NextAttemptAt.Sub(f.clock.Now()))
	}
	// When
	err := w.Step(ctx)
	q := queue(t, f, 101)
	r, gateErr := (notifications.Persistence{DB: f.s}).RecipientState(ctx, 101)
	// Then
	if !errors.Is(err, notifications.ErrWorker) || gateErr != nil || r.Pause != "" || q[0].Status != "pending" || q[0].CycleAttempts != 5 || q[0].LifetimeAttempts != 5 {
		t.Fatalf("error=%v gate=%v recipient=%+v queue=%+v", err, gateErr, r, q)
	}
}

func TestWorkerShouldRefuseFurtherSendsWhenRejectedTokenOutcomeCannotPersist(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	fs := &faultStore{Persistence: notifications.Persistence{DB: f.s}, fail: true}
	calls := 0
	w, err := notifications.New(notifications.Config{Store: fs, Coordinator: f.c, Policy: f.policy, Clock: f.clock, Sender: sender(func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		return application.SendOutcome{Category: application.SendInvalidToken}, errors.New("sanitized")
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// When
	stepErr := w.Step(ctx)
	_, directErr := w.SendDirect(ctx, 202, "synthetic")
	// Then
	q := queue(t, f, 101)
	if !errors.Is(stepErr, notifications.ErrWorker) || !errors.Is(directErr, notifications.ErrWorker) || calls != 1 || q[0].Status != "reserved" {
		t.Fatalf("step=%v direct=%v calls=%d queue=%+v", stepErr, directErr, calls, q)
	}
}

func TestWorkerShouldResumeAtCooldownDeadlineWhenTelegramRateLimitsDirectReply(t *testing.T) {
	for _, retryAfter := range []time.Duration{0, -time.Second, time.Minute} {
		t.Run(retryAfter.String(), func(t *testing.T) {
			// Given
			f := setup(t)
			f.observe(t, 101, "B")
			calls := 0
			w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
				calls++
				if calls == 1 {
					return application.SendOutcome{Category: application.SendRateLimited, RetryAfter: retryAfter}, nil
				}
				return application.SendOutcome{Category: application.SendAccepted, MessageID: int64(calls)}, nil
			})
			delay := retryAfter
			if delay <= 0 {
				delay = 30 * time.Second
			}
			// When
			_, firstErr := w.SendDirect(ctx, 202, "synthetic")
			f.clock.add(delay - time.Nanosecond)
			step(t, w)
			_, suppressedErr := w.SendDirect(ctx, 101, "synthetic")
			before := calls
			f.clock.add(time.Nanosecond)
			step(t, w)
			_, resumedErr := w.SendDirect(ctx, 202, "synthetic")
			// Then
			q := queue(t, f, 101)
			if firstErr != nil || suppressedErr != nil || resumedErr != nil || before != 1 || calls != 3 || q[0].Status != "delivered" || q[0].LifetimeAttempts != 1 {
				t.Fatalf("errors=%v/%v/%v before=%d calls=%d queue=%+v", firstErr, suppressedErr, resumedErr, before, calls, q)
			}
		})
	}
}

func TestWorkerShouldForgetSharedCooldownWhenRestartedButRetainNotificationDeadline(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		return application.SendOutcome{Category: application.SendRateLimited, RetryAfter: time.Hour}, nil
	})
	step(t, w)
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.s, err = data.Open(ctx, f.path)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	restarted := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		return application.SendOutcome{Category: application.SendAccepted, MessageID: 1}, nil
	})
	// When
	step(t, restarted)
	out, directErr := restarted.SendDirect(ctx, 202, "synthetic")
	// Then
	q := queue(t, f, 101)
	if directErr != nil || out.Category != application.SendAccepted || calls != 1 || q[0].LifetimeAttempts != 1 || q[0].Status != "pending" || !q[0].NextAttemptAt.Equal(epoch.Add(time.Hour)) {
		t.Fatalf("error=%v calls=%d outcome=%+v queue=%+v", directErr, calls, out, q)
	}
}

func TestSendDirectShouldHonorCooldownWithoutStorageWhenDatabaseIsClosed(t *testing.T) {
	// Given
	f := setup(t)
	calls := 0
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		return application.SendOutcome{Category: application.SendRateLimited, RetryAfter: time.Minute}, nil
	})
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	// When
	_, firstErr := w.SendDirect(ctx, 101, "synthetic")
	_, suppressedErr := w.SendDirect(ctx, 202, "synthetic")
	f.clock.add(time.Minute)
	_, nextErr := w.SendDirect(ctx, 202, "synthetic")
	// Then
	if firstErr != nil || suppressedErr != nil || nextErr != nil || calls != 2 {
		t.Fatalf("errors=%v/%v/%v calls=%d", firstErr, suppressedErr, nextErr, calls)
	}
}

func TestWorkerShouldRetainCooldownWhenNotificationOutcomeCannotBePersisted(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	fs := &faultStore{Persistence: notifications.Persistence{DB: f.s}, fail: true}
	calls := 0
	w, err := notifications.New(notifications.Config{Store: fs, Coordinator: f.c, Policy: f.policy, Clock: f.clock, Sender: sender(func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		return application.SendOutcome{Category: application.SendRateLimited, RetryAfter: time.Hour}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// When
	stepErr := w.Step(ctx)
	_, directErr := w.SendDirect(ctx, 202, "synthetic")
	// Then
	q := queue(t, f, 101)
	if !errors.Is(stepErr, notifications.ErrWorker) || directErr != nil || calls != 1 || q[0].Status != "reserved" || q[0].LifetimeAttempts != 1 {
		t.Fatalf("errors=%v/%v calls=%d queue=%+v", stepErr, directErr, calls, q)
	}
}

func TestWorkerShouldKeepSharedCooldownWhenRecipientIsResumed(t *testing.T) {
	// Given
	f := setup(t)
	f.observe(t, 101, "B")
	calls := 0
	w := f.worker(t, func(context.Context, int64, string) (application.SendOutcome, error) {
		calls++
		if calls == 1 {
			return application.SendOutcome{Category: application.SendRateLimited, RetryAfter: time.Minute}, nil
		}
		return application.SendOutcome{Category: application.SendAccepted, MessageID: 1}, nil
	})
	step(t, w)
	// When
	w.Membership(ctx, notifications.Event{Owner: 101, Blocked: true})
	w.Membership(ctx, notifications.Event{Owner: 101, Unblocked: true})
	step(t, w)
	before := calls
	f.clock.add(time.Minute)
	step(t, w)
	// Then
	q := queue(t, f, 101)
	if before != 1 || calls != 2 || q[0].Status != "delivered" || q[0].CycleAttempts != 1 || q[0].LifetimeAttempts != 2 {
		t.Fatalf("before=%d calls=%d queue=%+v", before, calls, q)
	}
}
