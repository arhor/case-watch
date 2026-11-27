package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	telegramApi "github.com/go-telegram/bot"
	telegramModels "github.com/go-telegram/bot/models"

	"github.com/arhor/case-watch/internal/adapters/outbound/database"
	"github.com/arhor/case-watch/internal/adapters/outbound/database/repositories"
	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/services"
	"github.com/arhor/case-watch/internal/testing/assert"
	"github.com/arhor/case-watch/internal/testing/clock"
)

func TestBotHandle(test *testing.T) {
	test.Run("should describe supported commands when help is requested", func(test *testing.T) {
		// Given
		environment := setup(test)

		// When
		environment.message(101, "/help")
		help := environment.replies[len(environment.replies)-1]

		// Then
		for _, command := range []string{"/start", "/status", "/remove", "/cancel", "/help"} {
			assert.True(test, strings.Contains(help, command), "help should describe every supported command")
		}
		assert.True(test, strings.Contains(help, "Доступные команды:"))
		assert.True(test, !strings.Contains(help, "/enroll"), "help should omit the obsolete command")
		assert.True(test, !strings.Contains(help, "08:00"), "help should omit the obsolete schedule")
		assert.True(test, !strings.Contains(help, "шифрованием"), "help should not claim encryption")
	})

	test.Run("should reject obsolete command when requested", func(test *testing.T) {
		// Given
		environment := setup(test)

		// When
		environment.message(101, "/enroll")

		// Then
		assert.Equal(test, "Неизвестная команда. Список доступных команд: /help.", environment.replies[len(environment.replies)-1])
	})

	test.Run("should request case number when case tracking starts", func(test *testing.T) {
		// Given
		environment := setup(test)

		// When
		environment.message(101, "/start")
		started := environment.replies[len(environment.replies)-1]

		// Then
		assert.True(test, strings.Contains(started, "отправьте номер дела"))
		assert.True(test, !strings.Contains(started, "шифрованием"), "tracked case prompt should not claim encryption")
	})

	test.Run("should save case baseline without exposing credentials when inputs are separate", func(test *testing.T) {
		// Given
		environment := setup(test)

		// When
		environment.message(303, "/start")
		environment.message(303, "synthetic-number")
		environment.message(303, "synthetic-password")
		environment.message(303, "/status")

		// Then
		trackedCase, err := environment.store.TrackedCases().FindByOwner(context.Background(), 303)
		assert.NoError(test, err)
		history, historyError := environment.store.TrackedCasesHistory().FindByTrackedCase(context.Background(), 303, trackedCase.ID)
		assert.NoError(test, historyError)
		assert.Equal(test, 0, len(history), "tracked case baseline should not create a transition")
		assert.Equal(test, 1, environment.calls, "recent baseline should satisfy status without another portal check")
		assert.Equal(test, 2, environment.deletes, "submitted credentials should be deleted")
		assert.Equal(test, "synthetic-number", trackedCase.Number)
		assert.Equal(test, "synthetic-password", trackedCase.Password)
		combined := strings.Join(environment.replies, "\n")
		assert.True(test, !strings.Contains(combined, "synthetic-password"), "output should not expose the password")
		assert.True(test, !strings.Contains(combined, "synthetic-number"), "output should not expose the case number")
		assert.True(test, !strings.Contains(combined, "synthetic-token"), "output should not expose the token")
		assert.True(test, strings.Contains(combined, "Статус дела: Etap <A>"), "status should display the cached stage")
	})

	test.Run("should remove only confirmed owner when another owner attempts removal", func(test *testing.T) {
		// Given
		environment := setup(test)
		environment.message(303, "/start")
		environment.message(303, "synthetic-number")
		environment.message(303, "synthetic-password")

		// When
		environment.message(202, "/remove")
		environment.message(202, "Да")
		_, otherOwnerError := environment.store.TrackedCases().FindByOwner(context.Background(), 303)
		environment.message(303, "/remove")
		environment.message(303, "Да")
		_, ownerError := environment.store.TrackedCases().FindByOwner(context.Background(), 303)

		// Then
		assert.NoError(test, otherOwnerError, "another owner should not remove the tracked case")
		assert.ErrorIs(test, ownerError, repositories.ErrNotFound)
		assert.Equal(test, 1, environment.calls)
		assert.Equal(test, 2, environment.deletes)
	})

	for _, scenario := range []struct {
		name            string
		answer          string
		removed         bool
		pending         bool
		expectedMessage string
	}{
		{name: "yes", answer: "Да", removed: true, pending: false, expectedMessage: "Дело удалено из отслеживания."},
		{name: "yes ignores case and whitespace", answer: " дА ", removed: true, pending: false, expectedMessage: "Дело удалено из отслеживания."},
		{name: "no", answer: "Нет", removed: false, pending: false, expectedMessage: "Удаление отменено."},
		{name: "no ignores case and whitespace", answer: " нЕт ", removed: false, pending: false, expectedMessage: "Удаление отменено."},
		{name: "old confirmation is not accepted", answer: "REMOVE", removed: false, pending: true, expectedMessage: "Пожалуйста, ответьте «Да» или «Нет»."},
		{name: "other answer is not accepted", answer: "возможно", removed: false, pending: true, expectedMessage: "Пожалуйста, ответьте «Да» или «Нет»."},
	} {
		test.Run("should accept yes or no and reject other answers when confirmation is pending: "+scenario.name, func(test *testing.T) {
			// Given
			environment := setup(test)
			environment.message(101, "/start")
			environment.message(101, "synthetic-number")
			environment.message(101, "synthetic-password")
			environment.message(101, "/remove")

			// When
			environment.message(101, scenario.answer)

			// Then
			_, err := environment.store.TrackedCases().FindByOwner(context.Background(), 101)
			assert.Equal(test, scenario.removed, errors.Is(err, repositories.ErrNotFound))
			{
				actual := environment.replies[len(environment.replies)-1]
				assert.True(test, strings.Contains(actual, scenario.expectedMessage), "reply should contain the expected confirmation message")
			}
			state := environment.bot.owner(101)
			state.mu.Lock()
			pending := !state.removal.IsZero()
			state.mu.Unlock()
			assert.Equal(test, scenario.pending, pending)
		})
	}

	test.Run("should keep tracked case when confirmation follows declined removal", func(test *testing.T) {
		// Given
		environment := setup(test)
		environment.message(101, "/start")
		environment.message(101, "synthetic-number")
		environment.message(101, "synthetic-password")
		environment.message(101, "/remove")
		environment.message(101, "Нет")

		// When
		environment.message(101, "Да")

		// Then
		_, err := environment.store.TrackedCases().FindByOwner(context.Background(), 101)
		assert.NoError(test, err, "declined removal should clear the pending confirmation")
	})

	test.Run("should remove tracked case when valid confirmation follows invalid input", func(test *testing.T) {
		// Given
		environment := setup(test)
		environment.message(101, "/start")
		environment.message(101, "synthetic-number")
		environment.message(101, "synthetic-password")
		environment.message(101, "/remove")
		environment.message(101, "возможно")

		// When
		environment.message(101, "Да")

		// Then
		_, err := environment.store.TrackedCases().FindByOwner(context.Background(), 101)
		assert.ErrorIs(test, err, repositories.ErrNotFound, "invalid input should retain the pending confirmation")
	})

	test.Run("should expire tracked case draft when deadline is reached", func(test *testing.T) {
		// Given
		environment := setup(test)
		environment.message(101, "/start")
		environment.message(101, "/start secret")
		environment.message(101, "/start")
		environment.message(101, "\n")
		environment.message(101, "synthetic-number")

		// When
		environment.clock.Set(environment.clock.Now().Add(10 * time.Minute))
		environment.message(101, "synthetic-password")

		// Then
		_, err := environment.store.TrackedCases().FindByOwner(context.Background(), 101)
		assert.ErrorIs(test, err, repositories.ErrNotFound)
		assert.Equal(test, 0, environment.calls, "expired draft should not trigger a portal check")
		assert.Equal(test, 3, environment.deletes, "parameterized command and draft inputs should be deleted")
	})

	test.Run("should retain tracked case when removal confirmation expires", func(test *testing.T) {
		// Given
		environment := setup(test)
		environment.message(101, "/start")
		environment.message(101, "synthetic-number")
		environment.message(101, "synthetic-password")
		environment.message(101, "/remove")

		// When
		environment.clock.Set(environment.clock.Now().Add(10 * time.Minute))
		environment.message(101, "Да")

		// Then
		_, err := environment.store.TrackedCases().FindByOwner(context.Background(), 101)
		assert.NoError(test, err)
		assert.Equal(test, 1, environment.calls)
		assert.Equal(test, 2, environment.deletes, "submitted credentials should be deleted")
	})

	test.Run("should retain tracked case when confirmation follows canceled removal", func(test *testing.T) {
		// Given
		environment := setup(test)
		environment.message(101, "/start")
		environment.message(101, "synthetic-number")
		environment.message(101, "synthetic-password")
		environment.message(101, "/remove")
		environment.message(101, "/cancel")

		// When
		environment.message(101, "Да")

		// Then
		_, err := environment.store.TrackedCases().FindByOwner(context.Background(), 101)
		assert.NoError(test, err)
		assert.Equal(test, 1, environment.calls)
		assert.Equal(test, 2, environment.deletes)
	})

	test.Run("should cancel portal and delete unknown slash input when draft is active", func(test *testing.T) {
		// Given
		environment := setup(test)
		started, done := make(chan struct{}), make(chan struct{})
		environment.bot.trackedCaseService = services.NewTrackedCaseService(services.TrackedCaseServiceConfig{
			Store: environment.store,
			Portal: portal(func(ctx context.Context, _, _ string) (string, time.Time, error) {
				close(started)
				<-ctx.Done()
				close(done)
				return "", time.Time{}, ctx.Err()
			}),
			Clock: environment.clock,
		})

		// When
		environment.message(101, "/start")
		environment.message(101, "/synthetic-secret")
		environment.message(101, "synthetic-number")
		environment.message(101, "other-password")
		<-started
		environment.message(101, "/cancel")
		<-done
		environment.bot.Close()
		environment.bot.wg.Wait()

		// Then
		assert.Equal(test, 3, environment.deletes, "credential deletion or cancellation failed")
		assert.True(test, !strings.Contains(strings.Join(environment.replies, " "), "synthetic-secret"), "credential deletion or cancellation failed")
	})

	for _, scenario := range []struct {
		name   string
		sender *telegramModels.User
		chat   telegramModels.Chat
	}{
		{name: "missing sender", sender: nil, chat: telegramModels.Chat{ID: 303, Type: telegramModels.ChatTypePrivate}},
		{name: "bot sender", sender: &telegramModels.User{ID: 303, IsBot: true}, chat: telegramModels.Chat{ID: 303, Type: telegramModels.ChatTypePrivate}},
		{name: "zero sender", sender: &telegramModels.User{}, chat: telegramModels.Chat{Type: telegramModels.ChatTypePrivate}},
		{name: "negative sender", sender: &telegramModels.User{ID: -303}, chat: telegramModels.Chat{ID: -303, Type: telegramModels.ChatTypePrivate}},
		{name: "mismatched chat", sender: &telegramModels.User{ID: 303}, chat: telegramModels.Chat{ID: 404, Type: telegramModels.ChatTypePrivate}},
		{name: "group", sender: &telegramModels.User{ID: 303}, chat: telegramModels.Chat{ID: 303, Type: telegramModels.ChatTypeGroup}},
		{name: "channel", sender: &telegramModels.User{ID: 303}, chat: telegramModels.Chat{ID: 303, Type: telegramModels.ChatTypeChannel}},
	} {
		test.Run("should ignore updates when private sender metadata is invalid: "+scenario.name, func(test *testing.T) {
			// Given
			environment := setup(test)

			// When
			environment.bot.handle(context.Background(), &telegramModels.Update{Message: &telegramModels.Message{ID: 7, From: scenario.sender, Chat: scenario.chat, Text: "/start"}})
			if scenario.sender != nil {
				environment.bot.handle(context.Background(), &telegramModels.Update{MyChatMember: &telegramModels.ChatMemberUpdated{From: *scenario.sender, Chat: scenario.chat, NewChatMember: telegramModels.ChatMember{Type: telegramModels.ChatMemberTypeBanned}}})
			}

			// Then
			assert.Equal(test, 0, len(environment.replies), "invalid transport metadata processed")
			assert.Equal(test, 0, environment.deletes, "invalid transport metadata processed")
			assert.Equal(test, 0, environment.calls, "invalid transport metadata processed")
			assert.Equal(test, 0, len(environment.bot.owners), "invalid transport metadata processed")
		})
	}

	test.Run("should keep handlers and descriptions consistent when commands are published", func(test *testing.T) {
		// Given
		environment := setup(test)

		// When
		environment.message(101, "/help")
		help := environment.replies[len(environment.replies)-1]

		// Then
		assert.Equal(test, len(botCommands), len(environment.bot.handlers), "every published command should have a handler")
		for _, command := range botCommands {
			assert.True(test, command.Description != "", "published command /"+command.Command+" should have a description")
			_, ok := environment.bot.handlers["/"+command.Command]
			assert.True(test, ok, "published command /"+command.Command+" should have a handler")
			assert.True(test, strings.Contains(help, "/"+command.Command+" — "+command.Description), "help should describe /"+command.Command)
		}
	})
}

func TestFormatStatus(test *testing.T) {
	for _, scenario := range []struct {
		name            string
		status          domain.Status
		err             error
		expectedMessage string
	}{
		{
			name:            "should report missing data when no check has succeeded",
			expectedMessage: "Пока нет данных об успешных проверках вашего дела.",
		},
		{
			name:            "should report failure and missing data when refresh fails without cache",
			err:             services.ErrCheck,
			expectedMessage: "Не удалось обновить статус дела.\n\nПока нет данных об успешных проверках вашего дела.",
		},
		{
			name: "should display unspecified stage when successful observation has empty stage",
			status: domain.Status{
				CheckedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
			},
			expectedMessage: "Статус дела: Не указан\nПоследняя успешная проверка: 01.11.2026, 13:00",
		},
		{
			name: "should describe update success when check is fresh",
			status: domain.Status{
				Stage: "Etap <A>", CheckedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Fresh: true,
			},
			expectedMessage: "Статус дела: Etap <A>\nПоследняя проверка: 01.11.2026, 13:00\n\nДанные успешно обновлены.",
		},
		{
			name: "should show cached observation when refresh fails",
			status: domain.Status{
				Stage: "Etap <A>", CheckedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Fresh: true,
			},
			err:             services.ErrCheck,
			expectedMessage: "Не удалось обновить статус дела.\n\nПоказываю данные последней успешной проверки:\n\nСтатус дела: Etap <A>\nПроверено: 01.11.2026, 13:00",
		},
		{
			name: "should show cached observation when cooldown is active",
			status: domain.Status{
				Stage: "Etap <A>", CheckedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
				NextEligibleAt: time.Date(2026, 11, 1, 12, 5, 0, 0, time.UTC),
			},
			expectedMessage: "Статус дела: Etap <A>\nПоследняя успешная проверка: 01.11.2026, 13:00",
		},
		{
			name: "should use Warsaw summer time and report progress when check is active",
			status: domain.Status{
				Stage: "Etap <A>", CheckedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
				InProgress: true, NextEligibleAt: time.Date(2026, 6, 1, 12, 5, 0, 0, time.UTC),
			},
			expectedMessage: "Статус дела: Etap <A>\nПоследняя успешная проверка: 01.06.2026, 14:00\n\nПроверка статуса ещё выполняется.",
		},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			// Given
			status := scenario.status

			// When
			actual := formatStatus(status, scenario.err)

			// Then
			assert.Equal(test, scenario.expectedMessage, actual)
		})
	}
}

func TestBotRun(test *testing.T) {
	test.Run("should report token rejection when unauthorized", func(test *testing.T) {
		// Given
		environment := setup(test)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server := newServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			responseWriter.Write([]byte(`{"ok":false,"error_code":401,"description":"synthetic-secret"}`))
		}))
		config := Config{TrackedCaseService: environment.bot.trackedCaseService, Clock: environment.clock}
		var fatalError error
		config.OnFatal = func(err error) {
			fatalError = err
			cancel()
		}
		bot := environment.newBot(test, server, config)

		// When
		bot.Run(ctx)

		// Then
		assert.ErrorIs(test, fatalError, ErrInvalidToken)
		assert.True(test, !errors.Is(ctx.Err(), context.DeadlineExceeded), "unauthorized operation did not trigger shutdown")
	})

	test.Run("should dispatch updates and sanitize failures when cancelled", func(test *testing.T) {
		// Given
		environment := setup(test)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var mu sync.Mutex
		polls := 0
		replied := make(chan struct{})
		failed := make(chan struct{})
		fatalErrors := make(chan error, 1)
		server := newServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			switch {
			case strings.HasSuffix(request.URL.Path, "getUpdates"):
				mu.Lock()
				polls++
				pollCount := polls
				mu.Unlock()
				if pollCount == 1 {
					responseWriter.Write([]byte(`{"ok":true,"result":[{"update_id":1,"message":{"message_id":1,"from":{"id":101,"is_bot":false},"chat":{"id":101,"type":"private"},"text":"/help"}}]}`))
				} else {
					responseWriter.Write([]byte(`{"ok":false,"error_code":429,"description":"synthetic-token synthetic-password","parameters":{"retry_after":99}}`))
					select {
					case <-failed:
					default:
						close(failed)
					}
				}
			case strings.HasSuffix(request.URL.Path, "sendMessage"):
				responseWriter.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
				close(replied)
			default:
				test.Error("unexpected startup network")
			}
		}))
		bot := environment.newBot(test, server, Config{TrackedCaseService: environment.bot.trackedCaseService, Clock: environment.clock, OnFatal: func(err error) { fatalErrors <- err }})
		done := make(chan struct{})

		// When
		go func() { bot.Run(ctx); close(done) }()
		<-replied
		<-failed
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			assert.True(test, false, "polling should stop when its context is canceled")
		}

		// Then
		assert.Equal(test, 0, len(fatalErrors), "transient polling errors must not stop the application")
	})

	test.Run("should reject another run when polling has already started", func(test *testing.T) {
		// Given
		environment := setup(test)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// When
		first := environment.bot.Run(ctx)
		second := environment.bot.Run(ctx)

		// Then
		assert.ErrorIs(test, first, context.Canceled)
		assert.ErrorIs(test, second, errPolling)
	})
}

func TestBotReply(test *testing.T) {
	test.Run("should attempt every send when rate limit reported", func(test *testing.T) {
		// Given
		environment := setup(test)
		calls := 0
		server := newServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			calls++
			responseWriter.Header().Set("Content-Type", "application/json")
			responseWriter.WriteHeader(429)
			responseWriter.Write([]byte(`{"ok":false,"error_code":429,"description":"synthetic","parameters":{"retry_after":60}}`))
		}))
		bot := environment.newBot(test, server, Config{TrackedCaseService: environment.bot.trackedCaseService, Clock: environment.clock})

		// When
		bot.reply(context.Background(), 101, "synthetic")
		bot.reply(context.Background(), 202, "synthetic")

		// Then
		assert.Equal(test, 2, calls)
	})

	test.Run("should suppress concurrent sends when token is rejected", func(test *testing.T) {
		// Given
		environment := setup(test)
		calls, rejected := 0, 0
		server := newServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			calls++
			responseWriter.Write([]byte(`{"ok":false,"error_code":401,"description":"synthetic-secret"}`))
		}))
		config := Config{TrackedCaseService: environment.bot.trackedCaseService, Clock: environment.clock, OnFatal: func(err error) {
			if errors.Is(err, ErrInvalidToken) {
				rejected++
			}
		}}
		bot := environment.newBot(test, server, config)
		var wg sync.WaitGroup

		// When
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				bot.reply(context.Background(), 101, "test")
			}()
		}
		wg.Wait()

		// Then
		assert.Equal(test, 1, calls)
		assert.Equal(test, 1, rejected)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, scenario := range []struct {
		name    string
		context context.Context
		owner   int64
	}{
		{name: "should skip send when context is canceled", context: ctx, owner: 101},
		{name: "should skip send when owner is zero", context: context.Background(), owner: 0},
		{name: "should skip send when owner is negative", context: context.Background(), owner: -1},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			// Given
			environment := setup(test)

			// When
			environment.bot.reply(scenario.context, scenario.owner, "test")

			// Then
			assert.Equal(test, 0, len(environment.replies))
		})
	}

	test.Run("should bound send when context has no deadline", func(test *testing.T) {
		// Given
		environment := setup(test)
		var remaining time.Duration
		telegram, err := telegramApi.New(
			"123:synthetic-token",
			telegramApi.WithSkipGetMe(),
			telegramApi.WithHTTPClient(30*time.Second, &http.Client{Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
				deadline, ok := request.Context().Deadline()
				if ok {
					remaining = time.Until(deadline)
				}
				return nil, errors.New("synthetic transport failure")
			})}),
		)
		assert.NoError(test, err)
		environment.bot.client = telegram

		// When
		environment.bot.reply(context.Background(), 101, "test")

		// Then
		assert.True(test, remaining > 0, "send should have a future deadline")
		assert.True(test, remaining <= 15*time.Second, "send deadline should be bounded to fifteen seconds")
	})
}

func TestBotErase(test *testing.T) {
	test.Run("should report token rejection when unauthorized", func(test *testing.T) {
		// Given
		environment := setup(test)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server := newServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			responseWriter.Write([]byte(`{"ok":false,"error_code":401,"description":"synthetic-secret"}`))
		}))
		config := Config{TrackedCaseService: environment.bot.trackedCaseService, Clock: environment.clock}
		var fatalError error
		config.OnFatal = func(err error) {
			fatalError = err
			cancel()
		}
		bot := environment.newBot(test, server, config)

		// When
		bot.erase(ctx, 101, 1)

		// Then
		assert.ErrorIs(test, fatalError, ErrInvalidToken)
		assert.True(test, !errors.Is(ctx.Err(), context.DeadlineExceeded), "unauthorized operation did not trigger shutdown")
	})
}

func TestBotNotifyStageChanged(test *testing.T) {
	test.Run("should send literal stages and Warsaw time to owner when stage changes", func(test *testing.T) {
		// Given
		environment := setup(test)
		var recipient, parseMode string
		server := newServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			request.ParseMultipartForm(1 << 20)
			recipient, parseMode = request.FormValue("chat_id"), request.FormValue("parse_mode")
			environment.replies = append(environment.replies, request.FormValue("text"))
			writer.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
		}))
		bot := environment.newBot(test, server, Config{TrackedCaseService: environment.bot.trackedCaseService, Clock: environment.clock})
		change := domain.StageChange{Stage: "Etap <B>", CheckedAt: environment.clock.Now()}

		// When
		err := bot.NotifyStageChanged(context.Background(), 303, change)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "303", recipient)
		assert.Equal(test, "", parseMode)
		assert.Equal(test, "Статус вашего дела изменился.\n\nПредыдущий статус: Не указан\nНовый статус: Etap <B>\nПроверено: 01.11.2026, 13:00", environment.replies[len(environment.replies)-1])
	})
	test.Run("should return sanitized error without retry when delivery fails", func(test *testing.T) {
		// Given
		environment := setup(test)
		attempts := 0
		server := newServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			attempts++
			writer.Write([]byte(`{"ok":false,"error_code":400,"description":"synthetic-secret"}`))
		}))
		bot := environment.newBot(test, server, Config{TrackedCaseService: environment.bot.trackedCaseService, Clock: environment.clock})

		// When
		err := bot.NotifyStageChanged(context.Background(), 101, domain.StageChange{})

		// Then
		assert.True(test, err != nil)
		assert.Equal(test, "telegram message delivery failed", err.Error())
		assert.Equal(test, 1, attempts)
	})

	test.Run("should stop sending and report fatal error when token is rejected", func(test *testing.T) {
		// Given
		environment := setup(test)
		attempts := 0
		var fatalError error
		server := newServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			attempts++
			writer.Write([]byte(`{"ok":false,"error_code":401,"description":"synthetic-secret"}`))
		}))
		bot := environment.newBot(test, server, Config{TrackedCaseService: environment.bot.trackedCaseService, Clock: environment.clock, OnFatal: func(err error) { fatalError = err }})

		// When
		first := bot.NotifyStageChanged(context.Background(), 101, domain.StageChange{})
		second := bot.NotifyStageChanged(context.Background(), 101, domain.StageChange{})

		// Then
		assert.ErrorIs(test, first, ErrInvalidToken)
		assert.ErrorIs(test, second, ErrInvalidToken)
		assert.ErrorIs(test, fatalError, ErrInvalidToken)
		assert.Equal(test, 1, attempts)
	})
	test.Run("should skip delivery when context is canceled", func(test *testing.T) {
		// Given
		environment := setup(test)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// When
		err := environment.bot.NotifyStageChanged(ctx, 101, domain.StageChange{})

		// Then
		assert.ErrorIs(test, err, context.Canceled)
		assert.Equal(test, 0, len(environment.replies))
	})
}

// -- Internals ----------------------------------------------------------------

type portal func(context.Context, string, string) (string, time.Time, error)

func (this portal) Check(ctx context.Context, owner int64, number, password string) (domain.PortalSnapshot, error) {
	stage, checkedAt, err := this(ctx, number, password)
	return domain.PortalSnapshot{Stage: stage, CheckedAt: checkedAt}, err
}

type fixture struct {
	bot                 *Bot
	store               *repositories.Store
	clock               *clock.FixedClock
	mu                  sync.Mutex
	replies             []string
	richMessages        []string
	richCalls           int
	richStatus          int
	skipEntityDetection bool
	deletes             int
	calls               int
	stage               string
	path                string
}

func setup(test *testing.T) *fixture {
	test.Helper()
	environment := &fixture{clock: clock.NewFixedClock(time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC))}
	server := newServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		environment.mu.Lock()
		defer environment.mu.Unlock()
		responseWriter.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(request.URL.Path, "/sendRichMessage"):
			request.ParseMultipartForm(1 << 20)
			var payload struct {
				HTML                string `json:"html"`
				SkipEntityDetection bool   `json:"skip_entity_detection"`
			}
			if err := json.Unmarshal([]byte(request.FormValue("rich_message")), &payload); err != nil {
				test.Errorf("decode rich message: %v", err)
			}
			environment.richCalls++
			environment.richMessages = append(environment.richMessages, payload.HTML)
			environment.skipEntityDetection = payload.SkipEntityDetection
			if environment.richStatus > 0 && environment.richStatus != http.StatusOK {
				responseWriter.Write([]byte(fmt.Sprintf(`{"ok":false,"error_code":%d,"description":"synthetic rejection"}`, environment.richStatus)))
			} else {
				responseWriter.Write([]byte(`{"ok":true,"result":{"message_id":42,"chat":{"id":101,"type":"private"},"date":1}}`))
			}
		case strings.HasSuffix(request.URL.Path, "/sendMessage"):
			request.ParseMultipartForm(1 << 20)
			environment.replies = append(environment.replies, request.FormValue("text"))
			responseWriter.Write([]byte(`{"ok":true,"result":{"message_id":42,"chat":{"id":101,"type":"private"},"date":1}}`))
		case strings.HasSuffix(request.URL.Path, "/deleteMessage"):
			environment.deletes++
			responseWriter.Write([]byte(`{"ok":false,"error_code":400,"description":"synthetic-password"}`))
		default:
			test.Error("unexpected API method")
			responseWriter.Write([]byte(`{"ok":false}`))
		}
	}))
	test.Cleanup(server.Close)
	environment.path = filepath.Join(test.TempDir(), "state.db")
	db, err := database.Open(context.Background(), environment.path)
	assert.NoError(test, err)
	environment.store = repositories.NewStore(db)
	test.Cleanup(func() { db.Close() })
	trackedCaseService := services.NewTrackedCaseService(services.TrackedCaseServiceConfig{
		Store: environment.store,
		Portal: portal(func(context.Context, string, string) (string, time.Time, error) {
			environment.calls++
			stage := environment.stage
			if stage == "" {
				stage = "Etap <A>"
			}
			return stage, time.Time{}, nil
		}),
		Clock: environment.clock,
	})
	environment.bot = environment.newBot(test, server, Config{
		TrackedCaseService: trackedCaseService,
		Clock:              environment.clock,
	})
	return environment
}

func (this *fixture) message(id int64, text string) {
	this.bot.handle(context.Background(), &telegramModels.Update{Message: &telegramModels.Message{ID: 7, From: &telegramModels.User{ID: id}, Chat: telegramModels.Chat{ID: id, Type: telegramModels.ChatTypePrivate}, Text: text}})
	if text == "synthetic-password" {
		this.bot.wg.Wait()
	}
}

type fakeServer struct {
	handler http.Handler
	URL     string
}

func newServer(handler http.Handler) *fakeServer {
	return &fakeServer{handler, "https://offline.invalid"}
}

func (this *fakeServer) Close() {}

func (this *fakeServer) Client() *http.Client {
	return &http.Client{Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		responseWriter := httptest.NewRecorder()
		this.handler.ServeHTTP(responseWriter, request)
		return responseWriter.Result(), nil
	})}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (this roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return this(request) }

// newBot mirrors the production reply/intake construction order while
// supplying only synthetic HTTP exchanges and a deterministic clock.
func (this *fixture) newBot(test *testing.T, server *fakeServer, config Config) *Bot {
	test.Helper()
	config.Token = "123:synthetic-token"
	config.Endpoint = server.URL
	config.HTTPClient = (&fakeServer{http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/getMe") {
			responseWriter.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`))
			return
		}
		if strings.HasSuffix(request.URL.Path, "/getMyCommands") {
			writeBotCommands(responseWriter, botCommands)
			return
		}
		server.handler.ServeHTTP(responseWriter, request)
	}), server.URL}).Client()
	bot, err := New(context.Background(), config)
	assert.NoError(test, err)
	test.Cleanup(bot.Close)
	return bot
}

func writeBotCommands(responseWriter http.ResponseWriter, commands []telegramModels.BotCommand) {
	_ = json.NewEncoder(responseWriter).Encode(struct {
		OK     bool                        `json:"ok"`
		Result []telegramModels.BotCommand `json:"result"`
	}{OK: true, Result: commands})
}

func (portal) ForgetSession(int64) {}
