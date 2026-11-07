package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"testing"
	"time"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/repositories"
	telegramrepository "github.com/arhor/case-watch/internal/repositories/telegram"
	"github.com/arhor/case-watch/internal/services/notifications"
	"github.com/arhor/case-watch/internal/services/tracking"
	"github.com/go-telegram/bot/models"
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
	f.env = map[string]string{"CASE_WATCH_TELEGRAM_BOT_TOKEN": "123:" + strings.Repeat("a", 35), "CASE_WATCH_DB_PATH": f.db}
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
	poll     func(context.Context, func(context.Context, *models.Update)) error
	send     func(context.Context, int64, string) (domain.SendOutcome, error)
	delete   func(context.Context, int64, int) error
}

func (t *transport) Validate(ctx context.Context) error {
	if t.validate != nil {
		return t.validate(ctx)
	}
	return nil
}
func (t *transport) Poll(ctx context.Context, handle func(context.Context, *models.Update)) error {
	if t.poll != nil {
		return t.poll(ctx, handle)
	}
	<-ctx.Done()
	return ctx.Err()
}
func (t *transport) DeleteMessage(ctx context.Context, owner int64, message int) error {
	if t.delete != nil {
		return t.delete(ctx, owner, message)
	}
	return nil
}
func (t *transport) Send(ctx context.Context, owner int64, text string) (domain.SendOutcome, error) {
	if t.send != nil {
		return t.send(ctx, owner, text)
	}
	return domain.SendOutcome{Category: domain.SendAccepted, MessageID: 1}, nil
}
func (f *fixture) run(ctx context.Context, d dependencies) error {
	d.lookup = f.lookup
	return run(ctx, d)
}
func message(owner int64, text string) *models.Update {
	return &models.Update{Message: &models.Message{ID: 1, From: &models.User{ID: owner}, Chat: models.Chat{ID: owner, Type: models.ChatTypePrivate}, Text: text}}
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
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("service did not drain")
		return nil
	}
}

func TestRunShouldAvoidTransportConstructionWhenStartupValidationFails(t *testing.T) {
	for _, kind := range []string{"config", "path", "schema"} {
		t.Run(kind, func(t *testing.T) {
			// Given
			f := setup(t)
			calls := 0
			switch kind {
			case "config":
				f.env["CASE_WATCH_TELEGRAM_BOT_TOKEN"] = "secret"
			case "path":
				if err := os.Chmod(filepath.Dir(f.db), 0755); err != nil {
					t.Fatal(err)
				}
			case "schema":
				if err := os.WriteFile(f.db, []byte("invalid database"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// When
			err := f.run(context.Background(), dependencies{
				portal:   func() domain.Portal { calls++; return nil },
				telegram: func(telegramrepository.Config) (telegramClient, error) { calls++; return nil, nil },
			})
			// Then
			if err == nil || calls != 0 || strings.Contains(err.Error(), "secret") {
				t.Fatal("startup did not stop before transports")
			}
			if kind == "schema" {
				lock, err := acquire(f.db)
				if err != nil {
					t.Fatalf("failed storage startup retained lock: %v", err)
				}
				lock.close()
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
		done <- f.run(ctx, dependencies{telegram: func(telegramrepository.Config) (telegramClient, error) {
			return &transport{poll: func(ctx context.Context, _ func(context.Context, *models.Update)) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}}, nil
		}})
	}()
	wait(t, started)
	// When
	second := f.run(context.Background(), dependencies{})
	cancel()
	stopped := result(t, done)
	// Then
	if !errors.Is(second, errLocked) || stopped != nil {
		t.Fatalf("exclusive process lock failed: %v %v", second, stopped)
	}
	lock, err := acquire(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.close()
	s := f.open(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunShouldDrainDetachedFlightsBeforeClosingDatabaseWhenCancelled(t *testing.T) {
	// Given
	f := setup(t)
	s := f.open(t)
	seed(t, s, 1)
	s.Close()
	entered, cancelled, release, callerDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		done <- f.run(ctx, dependencies{portal: func() domain.Portal {
			return portalFunc(func(ctx context.Context, _ domain.Credentials) (domain.Observation, error) {
				close(entered)
				<-ctx.Done()
				close(cancelled)
				<-release
				return domain.Observation{}, ctx.Err()
			})
		}, telegram: func(telegramrepository.Config) (telegramClient, error) {
			return &transport{poll: func(ctx context.Context, handle func(context.Context, *models.Update)) error {
				handle(ctx, message(1, "/status"))
				close(callerDone)
				<-ctx.Done()
				return ctx.Err()
			}}, nil
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
	lock, err := acquire(f.db)
	if lock != nil {
		lock.close()
	}
	if !errors.Is(err, errLocked) {
		t.Fatal("lock released before portal drained")
	}
	close(release)
	if err := result(t, done); err != nil {
		t.Fatal(err)
	}
	s = f.open(t)
	defer s.Close()
	if _, err := s.Enrollments.FindByOwner(context.Background(), 1); err != nil {
		t.Fatal("database failed to reopen")
	}
}

func TestRunShouldRecoverUncertainReservationsBeforePollingWhenStarted(t *testing.T) {
	// Given
	f := setup(t)
	s := f.open(t)
	id := seed(t, s, 1)
	now := time.Now()
	if err := (tracking.Persistence{DB: s}).Observe(context.Background(), 1, id, domain.Observation{Stage: "B", CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := (notifications.Persistence{DB: s}).ReserveDelivery(context.Background(), 1, now); err != nil {
		t.Fatal(err)
	}
	s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recoveredBeforePolling := false
	// When
	err := f.run(ctx, dependencies{telegram: func(telegramrepository.Config) (telegramClient, error) {
		return &transport{poll: func(context.Context, func(context.Context, *models.Update)) error {
			defer cancel()
			reopened, openErr := repositories.NewStore(context.Background(), f.db)
			if openErr != nil {
				return openErr
			}
			defer reopened.Close()
			rows, err := reopened.Pending.FindByRecipient(context.Background(), 1)
			recoveredBeforePolling = err == nil && len(rows) == 1 && rows[0].Status != "reserved" && rows[0].LifetimeAttempts == 1 && rows[0].Failure == "uncertain"
			return nil
		}}, nil
	}})
	// Then
	if err != nil || !recoveredBeforePolling {
		t.Fatalf("startup=%v recovery before polling=%t", err, recoveredBeforePolling)
	}
}

func TestRunShouldReturnSanitizedFailureAndDrainWhenPollingStopsUnexpectedly(t *testing.T) {
	// Given
	f := setup(t)
	// When
	err := f.run(context.Background(), dependencies{telegram: func(telegramrepository.Config) (telegramClient, error) {
		return &transport{poll: func(context.Context, func(context.Context, *models.Update)) error { return nil }}, nil
	}})
	// Then
	if !errors.Is(err, errService) {
		t.Fatalf("unexpected polling exit did not stop service: %v", err)
	}
	lock, err := acquire(f.db)
	if err != nil {
		t.Fatal("service did not release lock after drain")
	}
	lock.close()
}

func TestRunShouldDrainNormallyWhenRateLimitedDirectSendIsCancelled(t *testing.T) {
	// Given
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// When
	err := f.run(ctx, dependencies{report: func(string) {}, telegram: func(telegramrepository.Config) (telegramClient, error) {
		return &transport{poll: func(ctx context.Context, handle func(context.Context, *models.Update)) error {
			handle(ctx, message(101, "/help"))
			return ctx.Err()
		}, send: func(context.Context, int64, string) (domain.SendOutcome, error) {
			cancel()
			return domain.SendOutcome{Category: domain.SendRateLimited, RetryAfter: time.Hour}, errors.New("sanitized")
		}}, nil
	}})
	// Then
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunShouldReleaseResourcesWithoutStartingWorkersWhenTokenValidationFails(t *testing.T) {
	// Given
	f := setup(t)
	s := f.open(t)
	id := seed(t, s, 101)
	if err := (tracking.Persistence{DB: s}).Observe(context.Background(), 101, id, domain.Observation{Stage: "B", CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := (notifications.Persistence{DB: s}).ReserveDelivery(context.Background(), 101, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.Close()
	started, validated := false, false
	// When
	err := f.run(context.Background(), dependencies{telegram: func(telegramrepository.Config) (telegramClient, error) {
		return &transport{validate: func(context.Context) error { validated = true; return errors.New("synthetic-token") }, poll: func(context.Context, func(context.Context, *models.Update)) error { started = true; return nil }, send: func(context.Context, int64, string) (domain.SendOutcome, error) {
			started = true
			return domain.SendOutcome{}, nil
		}}, nil
	}})
	// Then
	lock, lockErr := acquire(f.db)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	defer lock.close()
	s = f.open(t)
	defer s.Close()
	rows, readErr := s.Pending.FindByRecipient(context.Background(), 101)
	if !errors.Is(err, errStartup) || !validated || started || readErr != nil || len(rows) != 1 || rows[0].Status != "reserved" {
		t.Fatalf("error=%v validated=%t started=%t queue=%+v read=%v", err, validated, started, rows, readErr)
	}
}

func TestRunShouldReleaseDatabaseLockWhenTelegramConstructionFails(t *testing.T) {
	// Given
	f := setup(t)
	// When
	err := f.run(context.Background(), dependencies{telegram: func(telegramrepository.Config) (telegramClient, error) {
		return nil, errors.New("synthetic-secret")
	}})
	// Then
	if !errors.Is(err, errStartup) {
		t.Fatalf("unsanitized startup error: %v", err)
	}
	lock, lockErr := acquire(f.db)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	defer lock.close()
	s := f.open(t)
	defer s.Close()
	if _, readErr := s.Enrollments.FindByOwner(context.Background(), 1); !errors.Is(readErr, repositories.ErrNotFound) {
		t.Fatal(readErr)
	}
}

func TestRunShouldPersistAcknowledgmentBeforeClosingDatabaseWhenSendIsCanceled(t *testing.T) {
	// Given
	f := setup(t)
	s := f.open(t)
	id := seed(t, s, 101)
	if err := (tracking.Persistence{DB: s}).Observe(context.Background(), 101, id, domain.Observation{Stage: "B", CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- f.run(ctx, dependencies{telegram: func(telegramrepository.Config) (telegramClient, error) {
			return &transport{send: func(ctx context.Context, _ int64, _ string) (domain.SendOutcome, error) {
				close(entered)
				<-ctx.Done()
				close(canceled)
				<-release
				return domain.SendOutcome{Category: domain.SendAccepted, MessageID: 42}, nil
			}}, nil
		}})
	}()
	wait(t, entered)
	// When
	cancel()
	wait(t, canceled)
	// Then
	select {
	case <-done:
		t.Fatal("service returned before the send drained")
	default:
	}
	lock, lockErr := acquire(f.db)
	if lock != nil {
		lock.close()
	}
	if !errors.Is(lockErr, errLocked) {
		t.Fatal("lock released before acknowledgment")
	}
	close(release)
	if err := result(t, done); err != nil {
		t.Fatal(err)
	}
	s = f.open(t)
	defer s.Close()
	rows, err := s.Pending.FindByRecipient(context.Background(), 101)
	if err != nil || len(rows) != 1 || rows[0].Status != "delivered" || rows[0].MessageID != 42 {
		t.Fatalf("acknowledgment did not survive shutdown: rows=%+v error=%v", rows, err)
	}
}

func TestRunShouldFailAndDrainWhenRuntimeTokenIsRejected(t *testing.T) {
	for _, source := range []string{"polling", "deletion", "direct", "durable"} {
		t.Run(source, func(t *testing.T) {
			// Given
			f := setup(t)
			if source == "durable" {
				s := f.open(t)
				id := seed(t, s, 101)
				if err := (tracking.Persistence{DB: s}).Observe(context.Background(), 101, id, domain.Observation{Stage: "B", CheckedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
				s.Close()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stopped := false
			// When
			err := f.run(ctx, dependencies{report: func(string) {}, telegram: func(c telegramrepository.Config) (telegramClient, error) {
				return &transport{poll: func(ctx context.Context, handle func(context.Context, *models.Update)) error {
					defer func() { stopped = true }()
					if source == "polling" {
						c.Diagnostic("telegram_invalid_token")
					}
					if source == "deletion" {
						handle(ctx, message(101, "/enroll secret"))
					}
					if source == "direct" {
						handle(ctx, message(101, "/help"))
					}
					<-ctx.Done()
					return ctx.Err()
				}, delete: func(context.Context, int64, int) error {
					c.Diagnostic("telegram_invalid_token")
					return telegramrepository.ErrInvalidToken
				}, send: func(context.Context, int64, string) (domain.SendOutcome, error) {
					return domain.SendOutcome{Category: domain.SendInvalidToken}, errors.New("sanitized")
				}}, nil
			}})
			// Then
			if !errors.Is(err, errService) || !stopped || ctx.Err() != nil {
				t.Fatalf("error=%v stopped=%t deadline=%v", err, stopped, ctx.Err())
			}
			s := f.open(t)
			defer s.Close()
			rows, readErr := s.Pending.FindByRecipient(context.Background(), 101)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if source == "durable" && (len(rows) != 1 || rows[0].Status != "pending" || rows[0].CycleAttempts != 0 || rows[0].LifetimeAttempts != 0) {
				t.Fatalf("queue=%+v", rows)
			}
		})
	}
}
