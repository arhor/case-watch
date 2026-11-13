package bot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	telegramApi "github.com/go-telegram/bot"
	telegramModels "github.com/go-telegram/bot/models"

	"github.com/arhor/case-watch/internal/database"
	"github.com/arhor/case-watch/internal/database/repositories"
	"github.com/arhor/case-watch/internal/database/store"
	"github.com/arhor/case-watch/internal/services/cases"

	"github.com/arhor/case-watch/internal/models"
)

type portal func(context.Context, string, string) (string, time.Time, error)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (p portal) Check(c context.Context, owner int64, Number, Password string) (string, time.Time, error) {
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
func TestStartShouldBeginEnrollmentAndHelpShouldExplainCommands(t *testing.T) {
	// Given
	f := setup(t)

	// When
	f.message(101, "/help")
	help := f.replies[len(f.replies)-1]
	f.message(101, "/enroll")
	legacy := f.replies[len(f.replies)-1]
	f.message(101, "/start")
	started := f.replies[len(f.replies)-1]

	// Then
	for _, command := range []string{"/start", "/status", "/remove", "/cancel", "/help"} {
		if !strings.Contains(help, command) {
			t.Fatalf("help omits %s: %q", command, help)
		}
	}
	if !strings.Contains(help, "Доступные команды:") ||
		strings.Contains(help, "/enroll") ||
		strings.Contains(help, "08:00") ||
		strings.Contains(help, "шифрованием") ||
		legacy != "Неизвестная команда. Список доступных команд: /help." ||
		!strings.Contains(started, "отправьте номер дела") ||
		strings.Contains(started, "шифрованием") {
		t.Fatalf("unexpected command behavior: help=%q legacy=%q start=%q", help, legacy, started)
	}
}

func TestConversationShouldEnrollBaselineAndRemoveOnlyConfirmedOwnerWhenInputsAreSeparate(t *testing.T) {
	// Given
	f := setup(t)
	// When
	f.message(303, "/start")
	f.message(303, "synthetic-number")
	f.message(303, "synthetic-password")
	e, err := f.s.Enrollments.FindByOwner(context.Background(), 303)
	history, herr := f.s.History.FindByEnrollment(context.Background(), 303, e.ID)
	f.message(303, "/status")
	f.message(202, "/remove")
	f.message(202, "Да")
	_, still := f.s.Enrollments.FindByOwner(context.Background(), 303)
	f.message(303, "/remove")
	f.message(303, "Да")
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
	if !strings.Contains(combined, "Статус дела: Etap <A>") {
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
			want: "Пока нет данных об успешных проверках вашего дела.",
		},
		{
			name: "no observation after failed refresh",
			err:  cases.ErrCheck,
			want: "Не удалось обновить статус дела.\n\nПока нет данных об успешных проверках вашего дела.",
		},
		{
			name: "successful check with empty stage",
			status: models.Status{
				CheckedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
			},
			want: "Статус дела: Не указан\nПоследняя успешная проверка: 01.11.2026, 13:00",
		},
		{
			name: "fresh check",
			status: models.Status{
				Stage: "Etap <A>", CheckedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Fresh: true,
			},
			want: "Статус дела: Etap <A>\nПоследняя проверка: 01.11.2026, 13:00\n\nДанные успешно обновлены.",
		},
		{
			name: "failed refresh with cached check",
			status: models.Status{
				Stage: "Etap <A>", CheckedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Fresh: true,
			},
			err:  cases.ErrCheck,
			want: "Не удалось обновить статус дела.\n\nПоказываю данные последней успешной проверки:\n\nСтатус дела: Etap <A>\nПроверено: 01.11.2026, 13:00",
		},
		{
			name: "cached check with cooldown",
			status: models.Status{
				Stage: "Etap <A>", CheckedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
				NextEligibleAt: time.Date(2026, 11, 1, 12, 5, 0, 0, time.UTC),
			},
			want: "Статус дела: Etap <A>\nПоследняя успешная проверка: 01.11.2026, 13:00",
		},
		{
			name: "daylight saving time and active check",
			status: models.Status{
				Stage: "Etap <A>", CheckedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
				InProgress: true, NextEligibleAt: time.Date(2026, 6, 1, 12, 5, 0, 0, time.UTC),
			},
			want: "Статус дела: Etap <A>\nПоследняя успешная проверка: 01.06.2026, 14:00\n\nПроверка статуса ещё выполняется.",
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

func TestRemoveShouldAcceptYesOrNoAndRejectOtherAnswers(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		answer      string
		removed     bool
		pending     bool
		wantMessage string
	}{
		{"yes", "Да", true, false, "Дело удалено из отслеживания."},
		{"yes ignores case and whitespace", " дА ", true, false, "Дело удалено из отслеживания."},
		{"no", "Нет", false, false, "Удаление отменено."},
		{"no ignores case and whitespace", " нЕт ", false, false, "Удаление отменено."},
		{"old confirmation is not accepted", "REMOVE", false, true, "Пожалуйста, ответьте «Да» или «Нет»."},
		{"other answer is not accepted", "возможно", false, true, "Пожалуйста, ответьте «Да» или «Нет»."},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			f := setup(t)
			f.message(101, "/start")
			f.message(101, "synthetic-number")
			f.message(101, "synthetic-password")
			f.message(101, "/remove")
			f.message(101, testCase.answer)

			_, err := f.s.Enrollments.FindByOwner(context.Background(), 101)
			if errors.Is(err, repositories.ErrNotFound) != testCase.removed {
				t.Fatalf("removed=%v, want %v", errors.Is(err, repositories.ErrNotFound), testCase.removed)
			}
			if got := f.replies[len(f.replies)-1]; !strings.Contains(got, testCase.wantMessage) {
				t.Fatalf("last reply=%q, want %q", got, testCase.wantMessage)
			}
			state := f.a.owner(101)
			state.mu.Lock()
			pending := !state.removal.IsZero()
			state.mu.Unlock()
			if pending != testCase.pending {
				t.Fatalf("pending=%v, want %v", pending, testCase.pending)
			}

			if !testCase.removed && !testCase.pending {
				f.message(101, "Да")
				if _, err := f.s.Enrollments.FindByOwner(context.Background(), 101); err != nil {
					t.Fatalf("case removed after declining removal: %v", err)
				}
			}
			if testCase.pending {
				f.message(101, "Да")
				if _, err := f.s.Enrollments.FindByOwner(context.Background(), 101); !errors.Is(err, repositories.ErrNotFound) {
					t.Fatalf("case not removed after valid confirmation: %v", err)
				}
			}
		})
	}
}

func TestConversationShouldExpireDraftAndConfirmationAtDeadlineWhenTimeAdvances(t *testing.T) {
	// Given
	f := setup(t)
	// When
	f.message(101, "/start")
	f.message(101, "/start secret")
	f.message(101, "/start")
	f.message(101, "\n")
	f.message(101, "synthetic-number")
	f.clock.now = f.clock.now.Add(10 * time.Minute)
	f.message(101, "synthetic-password")
	_, expired := f.s.Enrollments.FindByOwner(context.Background(), 101)
	f.message(101, "/start")
	f.message(101, "synthetic-number")
	f.message(101, "synthetic-password")
	f.message(101, "/remove")
	f.clock.now = f.clock.now.Add(10 * time.Minute)
	f.message(101, "Да")
	_, retained := f.s.Enrollments.FindByOwner(context.Background(), 101)
	f.message(101, "/remove")
	f.message(101, "/cancel")
	f.message(101, "Да")
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
	f.message(101, "/start")
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

func TestRunShouldRejectAnotherRunWhenPollingHasAlreadyStarted(t *testing.T) {
	// Given
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	first := f.a.Run(ctx)
	second := f.a.Run(ctx)

	// Then
	if !errors.Is(first, context.Canceled) || !errors.Is(second, errPolling) {
		t.Fatalf("first=%v second=%v", first, second)
	}
}

func TestReplyShouldAttemptEverySendWhenRateLimitReported(t *testing.T) {
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
	a.reply(context.Background(), 202, "synthetic")
	// Then
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestNewShouldRejectMissingCollaboratorsWhenBotIsConstructed(t *testing.T) {
	for _, missing := range []string{"token", "case service", "clock"} {
		t.Run(missing, func(t *testing.T) {
			// Given
			f := setup(t)
			c := f.a.config
			switch missing {
			case "token":
				c.Token = ""
			case "case service":
				c.CaseService = nil
			case "clock":
				c.Clock = nil
			}
			// When
			a, err := New(context.Background(), c)
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

// bot mirrors the production reply/intake construction order while
// supplying only synthetic HTTP exchanges and a deterministic clock.
func (f *fixture) bot(t *testing.T, server *fakeServer, c Config) *Bot {
	t.Helper()
	c.Token = "123:synthetic-token"
	c.Endpoint = server.URL
	c.HTTPClient = (&fakeServer{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			w.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`))
			return
		}
		server.handler.ServeHTTP(w, r)
	}), server.URL}).Client()
	a, err := New(context.Background(), c)
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
			f.a.handle(context.Background(), &telegramModels.Update{Message: &telegramModels.Message{ID: 7, From: testCase.sender, Chat: testCase.chat, Text: "/start"}})
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

func TestReplyShouldSuppressConcurrentSendsWhenTokenIsRejected(t *testing.T) {
	// Given
	f := setup(t)
	calls, rejected, failures := 0, 0, 0
	server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"ok":false,"error_code":401,"description":"synthetic-secret"}`))
	}))
	config := f.a.config
	config.Diagnostic = func(category string) {
		switch category {
		case "telegram_invalid_token":
			rejected++
		case "telegram reply failure":
			failures++
		}
	}
	a := f.bot(t, server, config)
	var wg sync.WaitGroup
	// When
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.reply(context.Background(), 101, "test")
		}()
	}
	wg.Wait()
	// Then
	if calls != 1 || rejected != 1 || failures != 9 {
		t.Fatalf("calls=%d rejected=%d failures=%d", calls, rejected, failures)
	}
}

func TestReplyShouldSkipSendWhenContextIsCanceledOrOwnerIsInvalid(t *testing.T) {
	// Given
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// When
	f.a.reply(ctx, 101, "canceled")
	f.a.reply(context.Background(), 0, "invalid")
	f.a.reply(context.Background(), -1, "invalid")
	// Then
	if len(f.replies) != 0 || strings.Join(f.logs, ",") != "telegram reply failure" {
		t.Fatalf("replies=%v logs=%v", f.replies, f.logs)
	}
}

func TestReplyShouldBoundSendWhenContextHasNoDeadline(t *testing.T) {
	// Given
	f := setup(t)
	var remaining time.Duration
	telegram, err := telegramApi.New(
		"123:synthetic-token",
		telegramApi.WithSkipGetMe(),
		telegramApi.WithHTTPClient(30*time.Second, &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			deadline, ok := r.Context().Deadline()
			if ok {
				remaining = time.Until(deadline)
			}
			return nil, errors.New("synthetic transport failure")
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.a.client = telegram
	// When
	f.a.reply(context.Background(), 101, "test")
	// Then
	if remaining <= 0 || remaining > 15*time.Second {
		t.Fatalf("send deadline remaining=%v", remaining)
	}
}

func TestNewShouldPreserveStartupResultsWhenTelegramResponds(t *testing.T) {
	for _, code := range []int{200, 401, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			// Given
			f := setup(t)
			calls := 0
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if !strings.HasSuffix(r.URL.Path, "/getMe") {
					t.Error("unexpected API method")
				}
				if code == 200 {
					w.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`))
				} else {
					w.Write([]byte(fmt.Sprintf(`{"ok":false,"error_code":%d,"description":"synthetic-secret"}`, code)))
				}
			}))
			t.Cleanup(server.Close)
			c := f.a.config
			c.Endpoint = server.URL
			c.HTTPClient = server.Client()
			// When
			a, err := New(context.Background(), c)
			if a != nil {
				t.Cleanup(a.Close)
			}
			// Then
			if calls != 1 || (err == nil) != (code == 200) || errors.Is(err, ErrInvalidToken) != (code == 401) {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic") {
				t.Fatal("secret leaked")
			}
		})
	}
}

func TestNewShouldReturnNoBotWhenValidationIsCanceled(t *testing.T) {
	// Given
	f := setup(t)
	c := f.a.config
	c.HTTPClient = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// When
	a, err := New(ctx, c)
	// Then
	if a != nil || err == nil {
		t.Fatalf("bot=%v error=%v", a, err)
	}
}

func (portal) ForgetSession(int64) {}
