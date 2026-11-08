package application

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"testing"
	"time"

	"github.com/arhor/case-watch/internal/bot"
	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/repositories"
	"github.com/go-telegram/bot/models"
)

type fixture struct {
	db  string
	env map[string]string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{db: filepath.Join(t.TempDir(), "state.db")}
	f.env = map[string]string{"CASE_WATCH_TELEGRAM_BOT_TOKEN": "123:" + strings.Repeat("a", 35), "CASE_WATCH_DB_PATH": f.db}
	return f
}
func (f *fixture) lookup(k string) (string, bool) { v, ok := f.env[k]; return v, ok }
func (f *fixture) open(t *testing.T) *repositories.Store {
	t.Helper()
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
	if e = s.Enrollments.Create(context.Background(), owner, id, domain.Credentials{Number: "synthetic-number", Password: "synthetic-password"}, domain.Observation{Stage: "A", CheckedAt: time.Now().Add(-time.Hour)}); e != nil {
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
	return domain.SendOutcome{Category: domain.SendAccepted}, nil
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
				f.db = filepath.Join(t.TempDir(), "missing", "state.db")
				f.env["CASE_WATCH_DB_PATH"] = f.db
			case "schema":
				if err := os.WriteFile(f.db, []byte("invalid database"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// When
			err := f.run(context.Background(), dependencies{
				portal:   func() domain.Portal { calls++; return nil },
				telegram: func(bot.ClientConfig) (telegramClient, error) { calls++; return nil, nil },
			})
			// Then
			if err == nil || calls != 0 || strings.Contains(err.Error(), "secret") {
				t.Fatal("startup did not stop before transports")
			}
		})
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
		}, telegram: func(bot.ClientConfig) (telegramClient, error) {
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

func TestRunShouldReturnSanitizedFailureAndDrainWhenPollingStopsUnexpectedly(t *testing.T) {
	for _, outcome := range []string{"nil", "error", "canceled"} {
		t.Run(outcome, func(t *testing.T) {
			// Given
			f := setup(t)
			// When
			err := f.run(context.Background(), dependencies{telegram: func(bot.ClientConfig) (telegramClient, error) {
				return &transport{poll: func(context.Context, func(context.Context, *models.Update)) error {
					switch outcome {
					case "error":
						return errors.New("synthetic-secret")
					case "canceled":
						return context.Canceled
					default:
						return nil
					}
				}}, nil
			}})
			// Then
			if !errors.Is(err, errService) {
				t.Fatalf("unexpected polling exit did not stop service: %v", err)
			}
		})
	}
}

func TestRunShouldDrainNormallyWhenRateLimitedDirectSendIsCancelled(t *testing.T) {
	// Given
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// When
	err := f.run(ctx, dependencies{report: func(string) {}, telegram: func(bot.ClientConfig) (telegramClient, error) {
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
	if err := s.Enrollments.UpdateObservation(context.Background(), 101, id, domain.Observation{Stage: "B", CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	started, validated := false, false
	// When
	err := f.run(context.Background(), dependencies{telegram: func(bot.ClientConfig) (telegramClient, error) {
		return &transport{validate: func(context.Context) error { validated = true; return errors.New("synthetic-token") }, poll: func(context.Context, func(context.Context, *models.Update)) error { started = true; return nil }, send: func(context.Context, int64, string) (domain.SendOutcome, error) {
			started = true
			return domain.SendOutcome{}, nil
		}}, nil
	}})
	// Then
	s = f.open(t)
	defer s.Close()
	enrollment, readErr := s.Enrollments.FindByOwner(context.Background(), 101)
	if !errors.Is(err, errStartup) || !validated || started || readErr != nil || enrollment.Observation.Stage != "B" {
		t.Fatalf("error=%v validated=%t started=%t enrollment=%+v read=%v", err, validated, started, enrollment, readErr)
	}
}

func TestRunShouldReleaseResourcesWhenTelegramConstructionFails(t *testing.T) {
	// Given
	f := setup(t)
	// When
	err := f.run(context.Background(), dependencies{telegram: func(bot.ClientConfig) (telegramClient, error) {
		return nil, errors.New("synthetic-secret")
	}})
	// Then
	if !errors.Is(err, errStartup) {
		t.Fatalf("unsanitized startup error: %v", err)
	}
	s := f.open(t)
	defer s.Close()
	if _, readErr := s.Enrollments.FindByOwner(context.Background(), 1); !errors.Is(readErr, repositories.ErrNotFound) {
		t.Fatal(readErr)
	}
}

func TestRunShouldFailAndDrainWhenRuntimeTokenIsRejected(t *testing.T) {
	for _, source := range []string{"polling", "deletion", "direct"} {
		t.Run(source, func(t *testing.T) {
			// Given
			f := setup(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stopped := false
			// When
			err := f.run(ctx, dependencies{report: func(string) {}, telegram: func(c bot.ClientConfig) (telegramClient, error) {
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
					return bot.ErrInvalidToken
				}, send: func(context.Context, int64, string) (domain.SendOutcome, error) {
					return domain.SendOutcome{Category: domain.SendInvalidToken}, errors.New("sanitized")
				}}, nil
			}})
			// Then
			if !errors.Is(err, errService) || !stopped || ctx.Err() != nil {
				t.Fatalf("error=%v stopped=%t deadline=%v", err, stopped, ctx.Err())
			}
		})
	}
}

func TestRunShouldAvoidStartingLoopsWhenFatalFailureOccursDuringValidation(t *testing.T) {
	// Given
	f := setup(t)
	started := false
	// When
	err := f.run(context.Background(), dependencies{report: func(string) {}, telegram: func(c bot.ClientConfig) (telegramClient, error) {
		return &transport{validate: func(context.Context) error { c.Diagnostic("telegram_invalid_token"); return nil }, poll: func(context.Context, func(context.Context, *models.Update)) error { started = true; return nil }}, nil
	}})
	// Then
	if !errors.Is(err, errService) || started {
		t.Fatalf("early failure: err=%v started=%t", err, started)
	}
}

func TestRunShouldStopWhenCaseServiceReportsStorageFailure(t *testing.T) {
	// Given
	f := setup(t)
	store := f.open(t)
	seed(t, store, 101)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopped := false
	// When
	err := f.run(ctx, dependencies{report: func(string) {}, telegram: func(bot.ClientConfig) (telegramClient, error) {
		return &transport{poll: func(ctx context.Context, handle func(context.Context, *models.Update)) error {
			db, err := sql.Open("sqlite", f.db)
			if err != nil {
				return err
			}
			defer db.Close()
			if _, err := db.Exec("DROP TABLE cooldowns"); err != nil {
				return err
			}
			handle(ctx, message(101, "/status"))
			<-ctx.Done()
			stopped = true
			return ctx.Err()
		}}, nil
	}})
	// Then
	if !errors.Is(err, errService) || !stopped || ctx.Err() != nil {
		t.Fatalf("storage failure: err=%v stopped=%t deadline=%v", err, stopped, ctx.Err())
	}
}

