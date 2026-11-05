package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/entrypoints/telegram"
	"github.com/arhor/case-watch/internal/repositories"
	"github.com/arhor/case-watch/internal/services/notifications"
	"github.com/arhor/case-watch/internal/services/tracking"
)

type fixture struct {
	db  string
	env map[string]string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if e := os.Mkdir(data, 0700); e != nil {
		t.Fatal(e)
	}
	f := &fixture{db: filepath.Join(data, "state.db")}
	f.env = map[string]string{"CASE_WATCH_TELEGRAM_BOT_TOKEN": "123:" + strings.Repeat("a", 35), "CASE_WATCH_DB_PATH": f.db, "CASE_WATCH_ALLOW_ALL_USERS": "true"}
	return f
}
func (f *fixture) lookup(k string) (string, bool) { v, ok := f.env[k]; return v, ok }
func (f *fixture) open(t *testing.T) *repositories.Store {
	t.Helper()
	if e := databaseFile(f.db, true); e != nil {
		t.Fatal(e)
	}
	s, e := repositories.NewStore(context.Background(), f.db)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func seed(t *testing.T, s *repositories.Store, owner int64) string {
	t.Helper()
	id, e := repositories.NewEnrollmentID()
	if e != nil {
		t.Fatal(e)
	}
	if e = (tracking.Persistence{DB: s}).Enroll(context.Background(), owner, id, domain.Credentials{Number: "synthetic-number", Password: "synthetic-password"}, domain.Observation{Stage: "A", CheckedAt: time.Now().Add(-time.Hour)}); e != nil {
		t.Fatal(e)
	}
	return id
}

type portalFunc func(context.Context, domain.Credentials) (domain.Observation, error)

func (p portalFunc) Check(c context.Context, v domain.Credentials) (domain.Observation, error) {
	return p(c, v)
}

type transport struct {
	validate func(context.Context) error
	start    func(context.Context)
	close    func()
	send     func(context.Context, int64, string) (domain.SendOutcome, error)
}

func (t *transport) Validate(c context.Context) error {
	if t.validate != nil {
		return t.validate(c)
	}
	return nil
}
func (t *transport) Start(c context.Context) { t.start(c) }
func (t *transport) Close() {
	if t.close != nil {
		t.close()
	}
}
func (t *transport) Send(c context.Context, o int64, text string) (domain.SendOutcome, error) {
	if t.send != nil {
		return t.send(c, o, text)
	}
	return domain.SendOutcome{Category: domain.SendAccepted, MessageID: 1}, nil
}
func wait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("synchronization deadline")
	}
}
func result(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("service did not drain")
		return nil
	}
}

func TestRunShouldAvoidTransportConstructionWhenStartupValidationFails(t *testing.T) {
	// Given
	for _, kind := range []string{"config"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			calls := 0
			switch kind {
			case "config":
				f.env["CASE_WATCH_TELEGRAM_BOT_TOKEN"] = "secret"
			}
			// When
			e := Run(context.Background(), f.lookup, Factories{Portal: func() domain.Portal { calls++; return nil }, Telegram: func(telegram.Config) (Transport, error) { calls++; return nil, nil }})
			// Then
			if e == nil || calls != 0 || strings.Contains(e.Error(), "secret") {
				t.Fatal("startup did not stop before transports")
			}
		})
	}
}

func TestRunShouldExcludeOtherServicesWhenDatabaseLocked(t *testing.T) {
	// Given
	f := setup(t)
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, f.lookup, Factories{Telegram: func(telegram.Config) (Transport, error) {
			return &transport{start: func(c context.Context) { close(started); <-c.Done() }}, nil
		}})
	}()
	wait(t, started)
	// When
	second := Run(context.Background(), f.lookup, Factories{})
	cancel()
	stopped := result(t, done)
	// Then
	if !errors.Is(second, ErrLocked) || stopped != nil {
		t.Fatalf("exclusive process lock failed: %v %v", second, stopped)
	}
	s := f.open(t)
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestRunShouldDrainDetachedFlightsBeforeClosingDatabaseWhenCancelled(t *testing.T) {
	// Given
	f := setup(t)
	s := f.open(t)
	seed(t, s, 1)
	s.Close()
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	callerDone := make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var admitted domain.Tracking
	go func() {
		done <- Run(ctx, f.lookup, Factories{Portal: func() domain.Portal {
			return portalFunc(func(c context.Context, _ domain.Credentials) (domain.Observation, error) {
				close(entered)
				<-c.Done()
				close(cancelled)
				<-release
				return domain.Observation{}, c.Err()
			})
		}, Telegram: func(tc telegram.Config) (Transport, error) {
			admitted = tc.Tracking
			return &transport{start: func(c context.Context) { _, _ = tc.Tracking.Status(c, 1); close(callerDone); <-c.Done() }}, nil
		}})
	}()
	wait(t, entered)
	// When
	cancel()
	wait(t, cancelled)
	wait(t, callerDone)
	// Then
	select {
	case <-done:
		t.Fatal("service returned with detached portal still running")
	default:
	}
	close(release)
	if e := result(t, done); e != nil {
		t.Fatal(e)
	}
	_, e := admitted.Status(context.Background(), 1)
	if !errors.Is(e, tracking.ErrClosed) {
		t.Fatal("shutdown allowed new work")
	}
	s = f.open(t)
	defer s.Close()
	if _, e = s.Enrollments.FindByOwner(context.Background(), 1); e != nil {
		t.Fatal("database failed to reopen")
	}
}

func TestRunShouldRecoverUncertainReservationsBeforePollingWhenStarted(t *testing.T) {
	// Given
	f := setup(t)
	s := f.open(t)
	r := seed(t, s, 1)
	now := time.Now()
	if e := (tracking.Persistence{DB: s}).Observe(context.Background(), 1, r, domain.Observation{Stage: "B", CheckedAt: now}); e != nil {
		t.Fatal(e)
	}
	if _, e := (notifications.Persistence{DB: s}).ReserveDelivery(context.Background(), 1, now); e != nil {
		t.Fatal(e)
	}
	s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// When
	e := Run(ctx, f.lookup, Factories{Telegram: func(telegram.Config) (Transport, error) {
		return &transport{start: func(context.Context) { cancel() }}, nil
	}})
	reopened := f.open(t)
	defer reopened.Close()
	recoveryRows, recoveryReadErr := reopened.Pending.FindByRecipient(context.Background(), 1)
	recovered := recoveryReadErr == nil && len(recoveryRows) == 1 && recoveryRows[0].Status != "reserved" && recoveryRows[0].LifetimeAttempts == 1 && recoveryRows[0].Failure == "uncertain"
	// Then
	if e != nil || !recovered {
		t.Fatalf("startup result=%v recovery read=%v rows=%+v", e, recoveryReadErr, recoveryRows)
	}
}

func TestRunShouldReturnSanitizedFailureAndDrainWhenPollingStopsUnexpectedly(t *testing.T) {
	// Given
	f := setup(t)
	var mu sync.Mutex
	closed := false
	// When
	e := Run(context.Background(), f.lookup, Factories{Telegram: func(telegram.Config) (Transport, error) {
		return &transport{start: func(context.Context) {}, close: func() { mu.Lock(); closed = true; mu.Unlock() }}, nil
	}})
	// Then
	mu.Lock()
	defer mu.Unlock()
	if !errors.Is(e, ErrService) || !closed {
		t.Fatal("unexpected polling exit did not stop service")
	}
}

func TestRunShouldDrainNormallyWhenRateLimitedDirectSendIsCancelled(t *testing.T) {
	// Given
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// When
	err := Run(ctx, f.lookup, Factories{Report: func(string) {}, Telegram: func(tc telegram.Config) (Transport, error) {
		return &transport{start: func(c context.Context) { tc.DirectSend(c, 101, "synthetic") }, send: func(context.Context, int64, string) (domain.SendOutcome, error) {
			cancel()
			return domain.SendOutcome{Category: domain.SendRateLimited, RetryAfter: time.Hour}, errors.New("sanitized")
		}}, nil
	}})
	// Then
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunShouldCloseTransportWithoutStartingWorkersWhenTokenValidationFails(t *testing.T) {
	// Given
	f := setup(t)
	s := f.open(t)
	r := seed(t, s, 101)
	p := notifications.Persistence{DB: s}
	if e := (tracking.Persistence{DB: s}).Observe(context.Background(), 101, r, domain.Observation{Stage: "B", CheckedAt: time.Now()}); e != nil {
		t.Fatal(e)
	}
	if _, e := p.ReserveDelivery(context.Background(), 101, time.Now()); e != nil {
		t.Fatal(e)
	}
	s.Close()
	started, closed, validated := false, false, false
	// When
	err := Run(context.Background(), f.lookup, Factories{Telegram: func(telegram.Config) (Transport, error) {
		return &transport{validate: func(context.Context) error { validated = true; return errors.New("synthetic-token") }, start: func(context.Context) { started = true }, close: func() { closed = true }}, nil
	}})
	s = f.open(t)
	defer s.Close()
	q, readErr := s.Pending.FindByRecipient(context.Background(), 101)
	// Then
	if !errors.Is(err, ErrStartup) || !validated || started || !closed || readErr != nil || len(q) != 1 || q[0].Status != "reserved" {
		t.Fatalf("error=%v validated=%t started=%t closed=%t queue=%+v read=%v", err, validated, started, closed, q, readErr)
	}
}

func TestRunShouldFailAndDrainWhenRuntimeTokenIsRejected(t *testing.T) {
	for _, source := range []string{"polling", "direct", "durable"} {
		t.Run(source, func(t *testing.T) {
			// Given
			f := setup(t)
			if source == "durable" {
				s := f.open(t)
				r := seed(t, s, 101)
				if e := (tracking.Persistence{DB: s}).Observe(context.Background(), 101, r, domain.Observation{Stage: "B", CheckedAt: time.Now()}); e != nil {
					t.Fatal(e)
				}
				s.Close()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			closed := false
			// When
			err := Run(ctx, f.lookup, Factories{Report: func(string) {}, Telegram: func(tc telegram.Config) (Transport, error) {
				return &transport{start: func(c context.Context) {
					if source == "polling" {
						tc.Diagnostic("telegram_invalid_token")
					}
					if source == "direct" {
						tc.DirectSend(c, 101, "synthetic")
					}
					<-c.Done()
				}, send: func(context.Context, int64, string) (domain.SendOutcome, error) {
					return domain.SendOutcome{Category: domain.SendInvalidToken}, errors.New("sanitized")
				}, close: func() { closed = true }}, nil
			}})
			s := f.open(t)
			defer s.Close()
			q, readErr := s.Pending.FindByRecipient(context.Background(), 101)
			// Then
			if !errors.Is(err, ErrService) || !closed || ctx.Err() != nil || readErr != nil {
				t.Fatalf("error=%v closed=%t deadline=%v read=%v", err, closed, ctx.Err(), readErr)
			}
			if source == "durable" && (len(q) != 1 || q[0].Status != "pending" || q[0].CycleAttempts != 0 || q[0].LifetimeAttempts != 0) {
				t.Fatalf("queue=%+v", q)
			}
		})
	}
}
