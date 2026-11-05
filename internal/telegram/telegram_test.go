package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/auth"
	"github.com/arhor/case-watch/internal/data"
	"github.com/arhor/case-watch/internal/notifications"
	"github.com/arhor/case-watch/internal/tracking"
	"github.com/go-telegram/bot/models"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type portal func(context.Context, application.Credentials) (application.Observation, error)

func (p portal) Check(c context.Context, v application.Credentials) (application.Observation, error) {
	return p(c, v)
}

type fixture struct {
	a       *Adapter
	s       *data.Store
	clock   *fakeClock
	mu      sync.Mutex
	replies []string
	deletes int
	calls   int
	stage   string
	path    string
	logs    []string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{clock: &fakeClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}}
	server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			r.ParseMultipartForm(1 << 20)
			f.replies = append(f.replies, r.FormValue("text"))
			w.Write([]byte(`{"ok":true,"result":{"message_id":42,"chat":{"id":101,"type":"private"},"date":1}}`))
		case strings.HasSuffix(r.URL.Path, "/deleteMessage"):
			f.deletes++
			w.Write([]byte(`{"ok":false,"error_code":400,"description":"synthetic-password"}`))
		default:
			t.Error("unexpected API method")
			w.Write([]byte(`{"ok":false}`))
		}
	}))
	t.Cleanup(server.Close)
	var err error
	f.path = filepath.Join(t.TempDir(), "state.db")
	f.s, err = data.NewStore(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.s.Close() })
	policy := auth.New(true, nil)
	coordinator := tracking.New(tracking.Persistence{DB: f.s}, portal(func(context.Context, application.Credentials) (application.Observation, error) {
		f.calls++
		stage := f.stage
		if stage == "" {
			stage = "Etap <A>"
		}
		return application.Observation{Stage: stage}, nil
	}), policy, f.clock)
	f.a, err = New(Config{Token: "123:synthetic-token", Endpoint: server.URL, HTTPClient: server.Client(), Policy: policy, Store: f.s.Enrollments, Tracking: coordinator, Clock: f.clock, Diagnostic: func(s string) { f.logs = append(f.logs, s) }})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) message(id int64, text string) {
	f.a.handle(context.Background(), nil, &models.Update{Message: &models.Message{ID: 7, From: &models.User{ID: id}, Chat: models.Chat{ID: id, Type: models.ChatTypePrivate}, Text: text}})
	if text == "synthetic-password" {
		deadline := time.Now().Add(5 * time.Second)
		for {
			s := f.a.owner(id)
			s.mu.Lock()
			busy := s.cancel != nil
			s.mu.Unlock()
			if !busy {
				break
			}
			if time.Now().After(deadline) {
				panic("enrollment did not complete")
			}
			runtime.Gosched()
		}
	}
}
func TestConversationShouldEnrollBaselineAndRemoveOnlyConfirmedOwnerWhenInputsAreSeparate(t *testing.T) {
	// Given
	f := setup(t)
	// When
	f.message(101, "/enroll")
	f.message(101, "synthetic-number")
	f.message(101, "synthetic-password")
	e, err := f.s.Enrollments.FindByOwner(context.Background(), 101)
	history, herr := f.s.History.FindByEnrollment(context.Background(), 101, e.ID)
	f.message(101, "/status")
	f.message(202, "/remove")
	f.message(202, "REMOVE")
	_, still := f.s.Enrollments.FindByOwner(context.Background(), 101)
	f.message(101, "/remove")
	f.message(101, "REMOVE")
	_, removed := f.s.Enrollments.FindByOwner(context.Background(), 101)
	// Then
	if err != nil || herr != nil || len(history) != 0 || still != nil || !errors.Is(removed, data.ErrNotFound) || f.calls != 1 || f.deletes != 2 {
		t.Fatalf("unexpected enrollment/removal outcome: %v %v %v %v calls=%d deletes=%d", err, herr, still, removed, f.calls, f.deletes)
	}
	combined := strings.Join(append(f.replies, f.logs...), "\n")
	if strings.Contains(combined, "synthetic-password") || strings.Contains(combined, "synthetic-number") || strings.Contains(combined, "synthetic-token") {
		t.Fatal("sensitive output")
	}
	if !strings.Contains(combined, "Cached stage: Etap <A>") {
		t.Fatal("missing plain cached stage")
	}
}
func TestConversationShouldExpireDraftAndConfirmationAtDeadlineWhenTimeAdvances(t *testing.T) {
	// Given
	f := setup(t)
	// When
	f.message(101, "/enroll")
	f.message(101, "/enroll secret")
	f.message(101, "/enroll")
	f.message(101, "\n")
	f.message(101, "synthetic-number")
	f.clock.now = f.clock.now.Add(10 * time.Minute)
	f.message(101, "synthetic-password")
	_, expired := f.s.Enrollments.FindByOwner(context.Background(), 101)
	f.message(101, "/enroll")
	f.message(101, "synthetic-number")
	f.message(101, "synthetic-password")
	f.message(101, "/remove")
	f.clock.now = f.clock.now.Add(10 * time.Minute)
	f.message(101, "REMOVE")
	_, retained := f.s.Enrollments.FindByOwner(context.Background(), 101)
	f.message(101, "/remove")
	f.message(101, "/cancel")
	f.message(101, "REMOVE")
	_, cancelled := f.s.Enrollments.FindByOwner(context.Background(), 101)
	// Then
	if !errors.Is(expired, data.ErrNotFound) || retained != nil || cancelled != nil || f.calls != 1 || f.deletes != 5 {
		t.Fatalf("expiry/cancellation failure: %v %v %v calls=%d deletes=%d", expired, retained, cancelled, f.calls, f.deletes)
	}
}
func TestSenderShouldClassifyFailuresWithoutReturningSecretsWhenAPIRejectsSend(t *testing.T) {
	// Given
	for _, tc := range []struct {
		code        int
		description string
		category    application.SendCategory
	}{{401, "synthetic-token", application.SendInvalidToken}, {403, "Forbidden: bot was blocked by the user", application.SendBlocked}, {403, "Forbidden: other reason", application.SendPermanent}, {429, "synthetic-password", application.SendRateLimited}, {500, "synthetic-password", application.SendTransient}} {
		t.Run(string(tc.category)+tc.description, func(t *testing.T) {
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": tc.code, "description": tc.description, "parameters": map[string]int{"retry_after": 9}})
			}))
			defer server.Close()
			f := setup(t)

			a, err := New(Config{Token: "123:synthetic-token", Endpoint: server.URL, HTTPClient: server.Client(), Policy: f.a.config.Policy, Store: f.s.Enrollments, Tracking: f.a.config.Tracking, Clock: f.clock})
			if err != nil {
				t.Fatal(err)
			}
			// When
			outcome, sendErr := a.Send(context.Background(), 101, "synthetic-body")
			// Then
			if sendErr == nil || outcome.Category != tc.category || strings.Contains(sendErr.Error(), "synthetic") {
				t.Fatalf("classification: %+v %v", outcome, sendErr)
			}
			if tc.code == 429 && outcome.RetryAfter != 9*time.Second {
				t.Fatal("retry deadline lost")
			}
		})
	}
}

type fakeServer struct {
	handler http.Handler
	URL     string
}

func newServer(h http.Handler) *fakeServer { return &fakeServer{h, "https://offline.invalid"} }
func (s *fakeServer) Close()               {}
func (s *fakeServer) Client() *http.Client {
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		s.handler.ServeHTTP(w, r)
		return w.Result(), nil
	})}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type cancelTracking struct{ started, done chan struct{} }

func (c *cancelTracking) Enroll(ctx context.Context, _ int64, _ application.Credentials) (application.Observation, error) {
	close(c.started)
	<-ctx.Done()
	close(c.done)
	return application.Observation{}, ctx.Err()
}
func (c *cancelTracking) Status(context.Context, int64) (application.Status, error) {
	return application.Status{}, nil
}
func (c *cancelTracking) Remove(context.Context, int64) error { return nil }
func TestConversationShouldCancelPortalAndDeleteUnknownSlashInputWhenDraftIsActive(t *testing.T) {
	// Given
	f := setup(t)
	work := &cancelTracking{make(chan struct{}), make(chan struct{})}
	f.a.config.Tracking = work
	// When
	f.message(101, "/enroll")
	f.message(101, "/synthetic-secret")
	f.message(101, "synthetic-number")
	f.message(101, "other-password")
	<-work.started
	f.message(101, "/cancel")
	<-work.done
	f.a.Close()
	// Then
	if f.deletes != 3 || strings.Contains(strings.Join(f.replies, " "), "synthetic-secret") {
		t.Fatal("credential deletion or cancellation failed")
	}
}
func TestPollingShouldDispatchUpdatesAndSanitizeFailuresWhenCancelled(t *testing.T) {
	// Given
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	polls := 0
	replied := make(chan struct{})
	failed := make(chan struct{})
	logs := make(chan string, 10)
	server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "getUpdates"):
			mu.Lock()
			polls++
			n := polls
			mu.Unlock()
			if n == 1 {
				w.Write([]byte(`{"ok":true,"result":[{"update_id":1,"message":{"message_id":1,"from":{"id":101,"is_bot":false},"chat":{"id":101,"type":"private"},"text":"/help"}}]}`))
			} else {
				w.Write([]byte(`{"ok":false,"error_code":429,"description":"synthetic-token synthetic-password","parameters":{"retry_after":99}}`))
				select {
				case <-failed:
				default:
					close(failed)
				}
			}
		case strings.HasSuffix(r.URL.Path, "sendMessage"):
			w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
			close(replied)
		default:
			t.Error("unexpected startup network")
		}
	}))
	a, err := New(Config{Token: "123:synthetic-token", Endpoint: server.URL, HTTPClient: server.Client(), Policy: f.a.config.Policy, Tracking: f.a.config.Tracking, Store: f.s.Enrollments, Clock: f.clock, Diagnostic: func(s string) { logs <- s }})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	// When
	go func() { a.Start(ctx); close(done) }()
	<-replied
	<-failed
	log := <-logs
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("polling did not cancel")
	}
	// Then
	if log != "telegram polling failure" || strings.Contains(log, "synthetic") {
		t.Fatal("raw polling error leaked")
	}
}

func TestReplyShouldUseSerializedDirectSendWhenRateLimitReported(t *testing.T) {
	// Given
	f := setup(t)
	calls := 0
	server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		w.Write([]byte(`{"ok":false,"error_code":429,"description":"synthetic","parameters":{"retry_after":60}}`))
	}))
	a, e := New(Config{Token: "123:synthetic-token", Endpoint: server.URL, HTTPClient: server.Client(), Policy: f.a.config.Policy, Store: f.s.Enrollments, Tracking: f.a.config.Tracking, Clock: f.clock})
	if e != nil {
		t.Fatal(e)
	}
	worker, e := notifications.New(notifications.Config{Store: notifications.Persistence{DB: f.s}, Coordinator: f.a.config.Tracking.(notifications.Coordinator), Policy: f.a.config.Policy, Clock: f.clock, Sender: a})
	if e != nil {
		t.Fatal(e)
	}
	a.config.DirectSend = worker.SendDirect
	// When
	a.reply(context.Background(), 101, "synthetic")
	a.reply(context.Background(), 101, "synthetic")
	// Then
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}

// scriptedSender keeps notification failures independent of command replies.
type scriptedSender struct {
	calls int
}

func (s *scriptedSender) Send(context.Context, int64, string) (application.SendOutcome, error) {
	s.calls++
	if s.calls == 1 {
		return application.SendOutcome{Category: application.SendRateLimited, RetryAfter: time.Minute}, nil
	}
	if s.calls == 2 {
		return application.SendOutcome{Category: application.SendBlocked}, nil
	}
	return application.SendOutcome{Category: application.SendAccepted, MessageID: int64(s.calls)}, nil
}

func TestConversationShouldPreserveOrderedTransitionsAndGatesWhenDeliveryRestartsBeforeRemoval(t *testing.T) {
	// Given
	f := setup(t)
	ctx := context.Background()
	coordinator := f.a.config.Tracking.(*tracking.Coordinator)
	sender := &scriptedSender{}
	newWorker := func() *notifications.Worker {
		w, err := notifications.New(notifications.Config{Store: notifications.Persistence{DB: f.s}, Coordinator: coordinator, Policy: f.a.config.Policy, Clock: f.clock, Sender: sender})
		if err != nil {
			t.Fatal(err)
		}
		if err = w.Start(ctx); err != nil {
			t.Fatal(err)
		}
		return w
	}
	w := newWorker()
	// When
	f.message(101, "/enroll")
	f.message(101, "synthetic-number")
	f.message(101, "synthetic-password")
	f.clock.now = f.clock.now.Add(time.Minute)
	f.stage = "Etap B"
	f.message(101, "/status")
	f.message(101, "/status") // Cached replies never acknowledge the transition.
	f.clock.now = f.clock.now.Add(time.Minute)
	f.stage = "Etap <A>"
	if _, err := coordinator.Scheduled(ctx, 101); err != nil {
		t.Fatal(err)
	}
	e, err := f.s.Enrollments.FindByOwner(ctx, 101)
	if err != nil {
		t.Fatal(err)
	}
	history, err := f.s.History.FindByEnrollment(ctx, 101, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Step(ctx); err != nil {
		t.Fatal(err)
	} // The notification retry deadline persists.
	path := f.path
	coordinator.Shutdown()
	if err = f.s.Close(); err != nil {
		t.Fatal(err)
	}
	f.s, err = data.NewStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	coordinator = tracking.New(tracking.Persistence{DB: f.s}, portal(func(context.Context, application.Credentials) (application.Observation, error) {
		t.Error("restart/cache/removal unexpectedly checked portal")
		return application.Observation{}, errors.New("unexpected check")
	}), f.a.config.Policy, f.clock)
	f.a.config.Tracking = coordinator
	f.a.config.Store = f.s.Enrollments
	w = newWorker()
	if err = w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	gatedCalls := sender.calls
	f.clock.now = f.clock.now.Add(time.Minute)
	if err = w.Step(ctx); err != nil {
		t.Fatal(err)
	} // The recipient blocks.
	w.Membership(ctx, notifications.Event{Owner: 101, Unblocked: true})
	if err = w.Step(ctx); err != nil {
		t.Fatal(err)
	} // Retry oldest before newer.
	remaining, err := f.s.Pending.FindByRecipient(ctx, 101)
	if err != nil {
		t.Fatal(err)
	}
	f.message(101, "/remove")
	f.message(101, "REMOVE")
	_, removed := f.s.Enrollments.FindByOwner(ctx, 101)
	queue, err := f.s.Pending.FindByRecipient(ctx, 101)
	// Then
	if len(history) != 2 || gatedCalls != 1 || sender.calls != 3 || len(remaining) != 2 || remaining[0].Status != "delivered" || remaining[1].Status == "delivered" {
		t.Fatalf("transition ordering or restart gates failed: history=%d gated=%d sends=%d queue=%+v", len(history), gatedCalls, sender.calls, remaining)
	}
	if err != nil || !errors.Is(removed, data.ErrNotFound) || len(queue) != 0 || f.calls != 3 {
		t.Fatalf("confirmed removal did not clear retained work: %v %v queued=%d checks=%d", removed, err, len(queue), f.calls)
	}
}

func TestValidateShouldCheckAcceptanceWithoutLeakingSecretsWhenTelegramResponds(t *testing.T) {
	for _, code := range []int{200, 401, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			// Given
			f := setup(t)
			called, bounded := false, false
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = strings.HasSuffix(r.URL.Path, "/getMe")
				_, bounded = r.Context().Deadline()
				if code == 200 {
					w.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`))
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": code, "description": "synthetic-secret"})
			}))
			config := f.a.config
			config.Endpoint, config.HTTPClient = server.URL, server.Client()
			a, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			// When
			err = a.Validate(context.Background())
			// Then
			if !called || !bounded || (err == nil) != (code == 200) {
				t.Fatalf("error=%v called=%t bounded=%t", err, called, bounded)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic") {
				t.Fatal("secret leaked")
			}
			logs := strings.Join(f.logs, " ")
			if strings.Contains(logs, "synthetic") || (code == 401 && !strings.Contains(logs, "telegram_invalid_token")) || (code == 500 && !strings.Contains(logs, "telegram validation failure")) {
				t.Fatalf("logs=%s", logs)
			}
		})
	}
}

func TestValidateShouldHonorCancellationWhenStartupIsCanceled(t *testing.T) {
	// Given
	f := setup(t)
	config := f.a.config
	config.HTTPClient = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
	a, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// When
	err = a.Validate(ctx)
	// Then
	if err == nil || strings.Contains(err.Error(), "synthetic") {
		t.Fatalf("error=%v", err)
	}
}

func TestAdapterShouldReportTokenRejectionWhenPollingOrDeletionIsUnauthorized(t *testing.T) {
	for _, operation := range []string{"polling", "deletion"} {
		t.Run(operation, func(t *testing.T) {
			// Given
			f := setup(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"ok":false,"error_code":401,"description":"synthetic-secret"}`))
			}))
			config := f.a.config
			config.Endpoint, config.HTTPClient = server.URL, server.Client()
			reported := false
			config.Diagnostic = func(category string) {
				if category == "telegram_invalid_token" {
					reported = true
					cancel()
				}
			}
			a, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			// When
			if operation == "polling" {
				a.Start(ctx)
			} else {
				a.erase(ctx, 101, 1)
			}
			// Then
			if !reported || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatal("unauthorized operation did not trigger shutdown")
			}
		})
	}
}
