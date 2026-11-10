package bot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/database"
	"github.com/arhor/case-watch/internal/database/repositories"
	"github.com/arhor/case-watch/internal/database/store"
	"github.com/arhor/case-watch/internal/services/cases"
	telegramModels "github.com/go-telegram/bot/models"

	"github.com/arhor/case-watch/internal/models"
)

type portal func(context.Context, string, string) (string, time.Time, error)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (p portal) Check(c context.Context, Number, Password string) (string, time.Time, error) {
	return p(c, Number, Password)
}

type fixture struct {
	a       *Bot
	s       *store.Store
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
	f.path = filepath.Join(t.TempDir(), "state.db")
	db, err := database.Open(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.s = store.New(db)
	t.Cleanup(func() { db.Close() })
	caseService := cases.New(cases.Config{
		DB: f.s,
		Portal: portal(func(context.Context, string, string) (string, time.Time, error) {
			f.calls++
			stage := f.stage
			if stage == "" {
				stage = "Etap <A>"
			}
			return stage, time.Time{}, nil
		}),
		Clock: f.clock,
	})
	f.a = f.bot(t, server, Config{
		CaseService: caseService,
		Clock:       f.clock,
		Diagnostic:  func(s string) { f.logs = append(f.logs, s) },
	})
	return f
}
func (f *fixture) message(id int64, text string) {
	f.a.handle(context.Background(), &telegramModels.Update{Message: &telegramModels.Message{ID: 7, From: &telegramModels.User{ID: id}, Chat: telegramModels.Chat{ID: id, Type: telegramModels.ChatTypePrivate}, Text: text}})
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
	f.message(303, "/enroll")
	f.message(303, "synthetic-number")
	f.message(303, "synthetic-password")
	e, err := f.s.Enrollments.FindByOwner(context.Background(), 303)
	history, herr := f.s.History.FindByEnrollment(context.Background(), 303, e.ID)
	f.message(303, "/status")
	f.message(202, "/remove")
	f.message(202, "REMOVE")
	_, still := f.s.Enrollments.FindByOwner(context.Background(), 303)
	f.message(303, "/remove")
	f.message(303, "REMOVE")
	_, removed := f.s.Enrollments.FindByOwner(context.Background(), 303)
	// Then
	if err != nil || herr != nil || len(history) != 0 || still != nil || !errors.Is(removed, repositories.ErrNotFound) || f.calls != 1 || f.deletes != 2 {
		t.Fatalf("unexpected enrollment/removal outcome: %v %v %v %v calls=%d deletes=%d", err, herr, still, removed, f.calls, f.deletes)
	}
	if e.Number != "synthetic-number" || e.Password != "synthetic-password" {
		t.Fatal("enrollment did not preserve the submitted case number and password")
	}
	combined := strings.Join(append(f.replies, f.logs...), "\n")
	if strings.Contains(combined, "synthetic-password") || strings.Contains(combined, "synthetic-number") || strings.Contains(combined, "synthetic-token") {
		t.Fatal("sensitive output")
	}
	if !strings.Contains(combined, "Cached stage: Etap <A>") {
		t.Fatal("missing plain cached stage")
	}
}

func TestFormatStatusShouldDescribeSuccessfulCheckWhenTimestampDeterminesAvailability(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		status models.Status
		err    error
		want   string
	}{
		{
			name: "no successful check",
			want: "No successful observation available.",
		},
		{
			name:   "successful check with empty stage",
			status: models.Status{CheckedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)},
			want:   "Cached stage: \nLast successful check: 2026-11-01T12:00:00Z",
		},
		{
			name:   "fresh check",
			status: models.Status{Stage: "Etap <A>", CheckedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Fresh: true},
			want:   "Fresh stage: Etap <A>\nLast successful check: 2026-11-01T12:00:00Z",
		},
		{
			name:   "failed refresh with cached check",
			status: models.Status{Stage: "Etap <A>", CheckedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Fresh: true},
			err:    cases.ErrCheck,
			want:   "Refresh failed. Cached stage: Etap <A>\nLast successful check: 2026-11-01T12:00:00Z",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Given
			status := testCase.status

			// When
			got := formatStatus(status, testCase.err)

			// Then
			if got != testCase.want {
				t.Fatalf("formatStatus() = %q, want %q", got, testCase.want)
			}
		})
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
	if !errors.Is(expired, repositories.ErrNotFound) || retained != nil || cancelled != nil || f.calls != 1 || f.deletes != 5 {
		t.Fatalf("expiry/cancellation failure: %v %v %v calls=%d deletes=%d", expired, retained, cancelled, f.calls, f.deletes)
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

func TestConversationShouldCancelPortalAndDeleteUnknownSlashInputWhenDraftIsActive(t *testing.T) {
	// Given
	f := setup(t)
	started, done := make(chan struct{}), make(chan struct{})
	f.a.caseService = cases.New(cases.Config{
		DB: f.s,
		Portal: portal(func(ctx context.Context, _, _ string) (string, time.Time, error) {
			close(started)
			<-ctx.Done()
			close(done)
			return "", time.Time{}, ctx.Err()
		}),
		Clock: f.clock,
	})
	// When
	f.message(101, "/enroll")
	f.message(101, "/synthetic-secret")
	f.message(101, "synthetic-number")
	f.message(101, "other-password")
	<-started
	f.message(101, "/cancel")
	<-done
	f.a.Close()
	f.a.wg.Wait()
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
	a := f.bot(t, server, Config{CaseService: f.a.config.CaseService, Clock: f.clock, Diagnostic: func(s string) { logs <- s }})
	done := make(chan struct{})
	// When
	go func() { a.Run(ctx); close(done) }()
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
	a := f.bot(t, server, f.a.config)
	// When
	a.reply(context.Background(), 101, "synthetic")
	a.reply(context.Background(), 101, "synthetic")
	// Then
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestNewShouldRejectMissingCollaboratorsWhenBotIsConstructed(t *testing.T) {
	for _, missing := range []string{"client", "case service", "clock"} {
		t.Run(missing, func(t *testing.T) {
			// Given
			f := setup(t)
			c := f.a.config
			switch missing {
			case "client":
				c.Client = nil
			case "case service":
				c.CaseService = nil
			case "clock":
				c.Clock = nil
			}
			// When
			a, err := New(c)
			// Then
			if a != nil || err == nil {
				t.Fatalf("accepted missing %s", missing)
			}
		})
	}
}

func TestBotShouldReportTokenRejectionWhenPollingOrDeletionIsUnauthorized(t *testing.T) {
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
			reported := false
			config.Diagnostic = func(category string) {
				if category == "telegram_invalid_token" {
					reported = true
					cancel()
				}
			}
			a := f.bot(t, server, config)
			// When
			if operation == "polling" {
				a.Run(ctx)
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

// bot mirrors the production client/reply/intake construction order while
// supplying only synthetic HTTP exchanges and a deterministic clock.
func (f *fixture) bot(t *testing.T, server *fakeServer, c Config) *Bot {
	t.Helper()
	client, err := NewClient(ClientConfig{
		Token: "123:synthetic-token", Endpoint: server.URL,
		HTTPClient: server.Client(), Diagnostic: c.Diagnostic,
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Client = client
	a, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

func TestBotShouldIgnoreUpdatesWhenPrivateSenderMetadataIsInvalid(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		sender *telegramModels.User
		chat   telegramModels.Chat
	}{
		{"missing sender", nil, telegramModels.Chat{ID: 303, Type: telegramModels.ChatTypePrivate}},
		{"bot sender", &telegramModels.User{ID: 303, IsBot: true}, telegramModels.Chat{ID: 303, Type: telegramModels.ChatTypePrivate}},
		{"zero sender", &telegramModels.User{}, telegramModels.Chat{Type: telegramModels.ChatTypePrivate}},
		{"negative sender", &telegramModels.User{ID: -303}, telegramModels.Chat{ID: -303, Type: telegramModels.ChatTypePrivate}},
		{"mismatched chat", &telegramModels.User{ID: 303}, telegramModels.Chat{ID: 404, Type: telegramModels.ChatTypePrivate}},
		{"group", &telegramModels.User{ID: 303}, telegramModels.Chat{ID: 303, Type: telegramModels.ChatTypeGroup}},
		{"channel", &telegramModels.User{ID: 303}, telegramModels.Chat{ID: 303, Type: telegramModels.ChatTypeChannel}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Given
			f := setup(t)
			// When
			f.a.handle(context.Background(), &telegramModels.Update{Message: &telegramModels.Message{ID: 7, From: testCase.sender, Chat: testCase.chat, Text: "/enroll"}})
			if testCase.sender != nil {
				f.a.handle(context.Background(), &telegramModels.Update{MyChatMember: &telegramModels.ChatMemberUpdated{From: *testCase.sender, Chat: testCase.chat, NewChatMember: telegramModels.ChatMember{Type: telegramModels.ChatMemberTypeBanned}}})
			}
			// Then
			if len(f.replies) != 0 || f.deletes != 0 || f.calls != 0 || len(f.a.owners) != 0 {
				t.Fatalf("invalid transport metadata processed: replies=%v", f.replies)
			}
		})
	}
}
