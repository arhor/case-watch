package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/credentials"
	"github.com/arhor/case-watch/internal/data"
	"github.com/arhor/case-watch/internal/notifications"
	"github.com/arhor/case-watch/internal/telegram"
	"github.com/arhor/case-watch/internal/tracking"
)

type fixture struct {
	db, key string
	k       *credentials.Keyring
	env     map[string]string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	keys := filepath.Join(root, "keys")
	for _, d := range []string{data, keys} {
		if e := os.Mkdir(d, 0700); e != nil {
			t.Fatal(e)
		}
	}
	f := &fixture{db: filepath.Join(data, "state.db"), key: filepath.Join(keys, "ring")}
	if e := credentials.Provision(f.key, "old"); e != nil {
		t.Fatal(e)
	}
	var e error
	f.k, e = credentials.Load(f.key)
	if e != nil {
		t.Fatal(e)
	}
	f.env = map[string]string{"CASE_WATCH_TELEGRAM_BOT_TOKEN": "123:" + strings.Repeat("a", 35), "CASE_WATCH_DB_PATH": f.db, "CASE_WATCH_KEY_FILE": f.key, "CASE_WATCH_ALLOW_ALL_USERS": "true"}
	return f
}
func (f *fixture) lookup(k string) (string, bool) { v, ok := f.env[k]; return v, ok }
func (f *fixture) open(t *testing.T) *data.Store {
	t.Helper()
	if e := databaseFile(f.db, true); e != nil {
		t.Fatal(e)
	}
	s, e := data.Open(context.Background(), f.db)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func seed(t *testing.T, s *data.Store, k *credentials.Keyring, owner int64) application.SealedCredentials {
	t.Helper()
	id, e := data.NewEnrollmentID()
	if e != nil {
		t.Fatal(e)
	}
	r, e := k.Seal(id, application.Credentials{Number: "synthetic-number", Password: "synthetic-password"})
	if e != nil {
		t.Fatal(e)
	}
	if e = (tracking.Persistence{DB: s}).Enroll(context.Background(), owner, r, application.Observation{Stage: "A", CheckedAt: time.Now().Add(-time.Hour)}); e != nil {
		t.Fatal(e)
	}
	return r
}

type portalFunc func(context.Context, application.Credentials) (application.Observation, error)

func (p portalFunc) Check(c context.Context, v application.Credentials) (application.Observation, error) {
	return p(c, v)
}

type transport struct {
	start func(context.Context)
	close func()
	send  func(context.Context, int64, string) (application.SendOutcome, error)
}

func (t *transport) Start(c context.Context) { t.start(c) }
func (t *transport) Close() {
	if t.close != nil {
		t.close()
	}
}
func (t *transport) Send(c context.Context, o int64, text string) (application.SendOutcome, error) {
	if t.send != nil {
		return t.send(c, o, text)
	}
	return application.SendOutcome{Category: application.SendAccepted, MessageID: 1}, nil
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
	for _, kind := range []string{"config", "missing_key", "undecryptable"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			calls := 0
			switch kind {
			case "config":
				f.env["CASE_WATCH_TELEGRAM_BOT_TOKEN"] = "secret"
			case "missing_key":
				os.Remove(f.key)
			case "undecryptable":
				s := f.open(t)
				r := seed(t, s, f.k, 1)
				s.Close()
				os.Remove(f.key)
				if e := credentials.Provision(f.key, "other"); e != nil {
					t.Fatal(e)
				}
				_ = r
			}
			// When
			e := Run(context.Background(), f.lookup, Factories{Portal: func() application.Portal { calls++; return nil }, Telegram: func(telegram.Config) (Transport, error) { calls++; return nil, nil }})
			// Then
			if e == nil || calls != 0 || strings.Contains(e.Error(), "secret") {
				t.Fatal("startup did not stop before transports")
			}
			if kind == "undecryptable" {
				s := f.open(t)
				defer s.Close()
				rows, e := s.Enrollments.Enrollments(context.Background())
				if e != nil || len(rows) != 1 {
					t.Fatal("undecryptable state was reset")
				}
			}
		})
	}
}

func TestPathsShouldRejectKeyInsideDataOrRepositoryWhenAliasesResolve(t *testing.T) {
	// Given
	f := setup(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if e := os.Symlink(filepath.Dir(f.db), alias); e != nil {
		t.Fatal(e)
	}
	cwd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	// When
	_, _, inside := Paths(f.db, filepath.Join(alias, "ring"))
	_, _, repo := Paths(f.db, filepath.Join(cwd, "ring"))
	// Then
	if inside == nil || repo == nil {
		t.Fatal("key separation bypassed")
	}
}

func TestRunShouldExcludeOtherServicesAndMaintenanceWhenDatabaseLocked(t *testing.T) {
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
	callback := false
	// When
	second := Run(context.Background(), f.lookup, Factories{})
	maintenance := Maintain(context.Background(), f.db, f.key, func(*data.Store, *credentials.Keyring, string) error { callback = true; return nil })
	cancel()
	stopped := result(t, done)
	// Then
	if !errors.Is(second, ErrLocked) || !errors.Is(maintenance, ErrLocked) || callback || stopped != nil {
		t.Fatalf("exclusive process lock failed: %v %v %v", second, maintenance, stopped)
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
	seed(t, s, f.k, 1)
	s.Close()
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	callerDone := make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var admitted application.Tracking
	go func() {
		done <- Run(ctx, f.lookup, Factories{Portal: func() application.Portal {
			return portalFunc(func(c context.Context, _ application.Credentials) (application.Observation, error) {
				close(entered)
				<-c.Done()
				close(cancelled)
				<-release
				return application.Observation{}, c.Err()
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
	if _, e = s.Enrollments.Enrollment(context.Background(), 1); e != nil {
		t.Fatal("database failed to reopen")
	}
}

func TestRunShouldRecoverUncertainReservationsBeforePollingWhenStarted(t *testing.T) {
	// Given
	f := setup(t)
	s := f.open(t)
	r := seed(t, s, f.k, 1)
	now := time.Now()
	if e := (tracking.Persistence{DB: s}).Observe(context.Background(), 1, r.ID, application.Observation{Stage: "B", CheckedAt: now}); e != nil {
		t.Fatal(e)
	}
	if _, e := (notifications.Persistence{DB: s}).ReserveDelivery(context.Background(), 1, now); e != nil {
		t.Fatal(e)
	}
	s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recovered := false
	var recoveryRows []data.Delivery
	var recoveryReadErr error
	// When
	e := Run(ctx, f.lookup, Factories{Telegram: func(tc telegram.Config) (Transport, error) {
		return &transport{start: func(c context.Context) {
			reader := tc.Store.(monitoredReader)
			q, e := reader.s.Pending.Queue(c, 1)
			recoveryRows, recoveryReadErr = q, e
			recovered = e == nil && len(q) == 1 && q[0].Status != "reserved" && q[0].LifetimeAttempts == 1 && q[0].Failure == "uncertain"
			cancel()
		}}, nil
	}})
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

func TestRunShouldPersistKnownDirectGateWhenShutdownCancelsCaller(t *testing.T) {
	for _, category := range []application.SendCategory{application.SendRateLimited, application.SendInvalidToken} {
		t.Run(string(category), func(t *testing.T) {
			// Given
			f := setup(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// When
			e := Run(ctx, f.lookup, Factories{Report: func(string) {}, Telegram: func(tc telegram.Config) (Transport, error) {
				return &transport{start: func(c context.Context) { tc.DirectSend(c, 101, "synthetic") }, send: func(context.Context, int64, string) (application.SendOutcome, error) {
					cancel()
					return application.SendOutcome{Category: category, RetryAfter: time.Hour}, errors.New("sanitized")
				}}, nil
			}})
			s := f.open(t)
			defer s.Close()
			_, g, readErr := (notifications.Persistence{DB: s}).DeliveryGates(context.Background(), 101)
			// Then
			if e != nil || readErr != nil || (category == application.SendRateLimited && g.RateUntil.Before(time.Now())) || (category == application.SendInvalidToken && !g.TokenPaused) {
				t.Fatalf("run=%v read=%v gate=%+v", e, readErr, g)
			}
		})
	}
}

func TestRunShouldFailWhenCancelledKnownDirectOutcomeCannotPersist(t *testing.T) {
	// Given
	f := setup(t)
	s := f.open(t)
	s.Close()
	db, e := sql.Open("sqlite", f.db)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TRIGGER reject_global BEFORE UPDATE ON delivery_gates BEGIN SELECT RAISE(ABORT, 'synthetic fault'); END"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// When
	e = Run(ctx, f.lookup, Factories{Report: func(string) {}, Telegram: func(tc telegram.Config) (Transport, error) {
		return &transport{start: func(c context.Context) { tc.DirectSend(c, 101, "synthetic") }, send: func(context.Context, int64, string) (application.SendOutcome, error) {
			cancel()
			return application.SendOutcome{Category: application.SendInvalidToken}, errors.New("sanitized")
		}}, nil
	}})
	// Then
	if !errors.Is(e, ErrService) {
		t.Fatalf("result=%v", e)
	}
}
