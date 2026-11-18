package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	telegramModels "github.com/go-telegram/bot/models"

	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestNew(test *testing.T) {
	test.Run("should reject construction when token is missing", func(test *testing.T) {
		// Given
		environment := setup(test)
		config := Config{Token: "123:synthetic-token", CaseService: environment.bot.caseService, Clock: environment.clock}
		config.Token = ""

		// When
		bot, err := New(context.Background(), config)

		// Then
		assert.True(test, bot == nil, "failed startup should not return a bot")
		assert.True(test, err != nil, "missing collaborator should return an error")
	})

	test.Run("should panic when case service is missing", func(test *testing.T) {
		// Given
		environment := setup(test)
		config := Config{Token: "123:synthetic-token", CaseService: environment.bot.caseService, Clock: environment.clock}
		config.CaseService = nil

		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			_, _ = New(context.Background(), config)
		}()

		// Then
		assert.Equal[any](test, "required property is missing: CaseService", recovered)
	})

	test.Run("should panic when clock is missing", func(test *testing.T) {
		// Given
		environment := setup(test)
		config := Config{Token: "123:synthetic-token", CaseService: environment.bot.caseService, Clock: environment.clock}
		config.Clock = nil

		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			_, _ = New(context.Background(), config)
		}()

		// Then
		assert.Equal[any](test, "required property is missing: Clock", recovered)
	})
	for _, scenario := range []struct {
		status               int
		expectedCalls        int
		expectedSuccess      bool
		expectedInvalidToken bool
	}{
		{status: 200, expectedCalls: 2, expectedSuccess: true},
		{status: 401, expectedCalls: 1, expectedInvalidToken: true},
		{status: 500, expectedCalls: 1},
	} {
		test.Run("should preserve startup results when Telegram responds with "+http.StatusText(scenario.status), func(test *testing.T) {
			// Given
			environment := setup(test)
			calls := 0
			server := newServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				calls++
				switch {
				case strings.HasSuffix(request.URL.Path, "/getMe"):
					if scenario.status == 200 {
						responseWriter.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`))
					} else {
						responseWriter.Write([]byte(fmt.Sprintf(`{"ok":false,"error_code":%d,"description":"synthetic-secret"}`, scenario.status)))
					}
				case strings.HasSuffix(request.URL.Path, "/getMyCommands"):
					writeBotCommands(responseWriter, botCommands)
				default:
					test.Error("unexpected API method")
				}
			}))
			test.Cleanup(server.Close)
			config := Config{Token: "123:synthetic-token", CaseService: environment.bot.caseService, Clock: environment.clock}
			config.Endpoint = server.URL
			config.HTTPClient = server.Client()

			// When
			bot, err := New(context.Background(), config)
			if bot != nil {
				test.Cleanup(bot.Close)
			}

			// Then
			assert.Equal(test, scenario.expectedCalls, calls)
			assert.Equal(test, scenario.expectedSuccess, err == nil)
			assert.Equal(test, scenario.expectedInvalidToken, errors.Is(err, ErrInvalidToken))
			assert.True(test, !(err != nil && strings.Contains(err.Error(), "synthetic")), "secret leaked")
		})
	}

	test.Run("should return no bot when validation is canceled", func(test *testing.T) {
		// Given
		environment := setup(test)
		config := Config{Token: "123:synthetic-token", CaseService: environment.bot.caseService, Clock: environment.clock}
		config.HTTPClient = &http.Client{
			Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, request.Context().Err()
			}),
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// When
		bot, err := New(ctx, config)

		// Then
		assert.True(test, bot == nil, "failed startup should not return a bot")
		assert.True(test, err != nil, "operation should return an error")
	})

	changedDescription := slices.Clone(botCommands)
	changedDescription[0].Description = "Old description"
	changedOrder := slices.Clone(botCommands)
	changedOrder[0], changedOrder[1] = changedOrder[1], changedOrder[0]
	for _, scenario := range []struct {
		name            string
		currentCommands []telegramModels.BotCommand
		expectedCalls   []string
	}{
		{name: "unchanged", currentCommands: slices.Clone(botCommands), expectedCalls: []string{"getMe", "getMyCommands"}},
		{name: "missing", currentCommands: nil, expectedCalls: []string{"getMe", "getMyCommands", "setMyCommands"}},
		{name: "description changed", currentCommands: changedDescription, expectedCalls: []string{"getMe", "getMyCommands", "setMyCommands"}},
		{name: "order changed", currentCommands: changedOrder, expectedCalls: []string{"getMe", "getMyCommands", "setMyCommands"}},
		{name: "obsolete command", currentCommands: append(slices.Clone(botCommands), telegramModels.BotCommand{Command: "obsolete", Description: "Obsolete"}), expectedCalls: []string{"getMe", "getMyCommands", "setMyCommands"}},
	} {
		test.Run("should synchronize commands only when they change: "+scenario.name, func(test *testing.T) {
			// Given
			environment := setup(test)
			current := slices.Clone(scenario.currentCommands)
			var calls []string
			server := newServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				switch {
				case strings.HasSuffix(request.URL.Path, "/getMe"):
					calls = append(calls, "getMe")
					responseWriter.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`))
				case strings.HasSuffix(request.URL.Path, "/getMyCommands"):
					calls = append(calls, "getMyCommands")
					writeBotCommands(responseWriter, current)
				case strings.HasSuffix(request.URL.Path, "/setMyCommands"):
					calls = append(calls, "setMyCommands")
					if err := request.ParseMultipartForm(1 << 20); err != nil {
						test.Error(err)
						responseWriter.WriteHeader(http.StatusBadRequest)
						return
					}
					var sent []telegramModels.BotCommand
					if err := json.Unmarshal([]byte(request.FormValue("commands")), &sent); err != nil {
						test.Error(err)
						responseWriter.WriteHeader(http.StatusBadRequest)
						return
					}
					assert.DeepEqual(test, botCommands, sent, "startup should publish the configured commands")
					assert.Equal(test, "", request.FormValue("scope"), "synchronization should use the default scope")
					assert.Equal(test, "", request.FormValue("language_code"), "synchronization should use the default language")
					responseWriter.Write([]byte(`{"ok":true,"result":true}`))
				default:
					test.Errorf("unexpected Telegram API method: %s", request.URL.Path)
				}
			}))
			config := Config{Token: "123:synthetic-token", CaseService: environment.bot.caseService, Clock: environment.clock}
			config.Endpoint = server.URL
			config.HTTPClient = server.Client()

			// When
			bot, err := New(context.Background(), config)

			// Then
			assert.NoError(test, err)
			test.Cleanup(bot.Close)

			assert.DeepEqual(test, scenario.expectedCalls, calls, "startup should perform only the required command synchronization calls")
		})
	}

	for _, scenario := range []struct {
		name                 string
		failingStep          string
		status               int
		result               string
		expectedInvalidToken bool
		expectedDiagnostic   string
	}{
		{name: "get fails", failingStep: "getMyCommands", status: 500, result: "", expectedInvalidToken: false, expectedDiagnostic: "telegram command synchronization failure"},
		{name: "set fails", failingStep: "setMyCommands", status: 500, result: "", expectedInvalidToken: false, expectedDiagnostic: "telegram command synchronization failure"},
		{name: "set returns false", failingStep: "setMyCommands", status: 200, result: "false", expectedInvalidToken: false, expectedDiagnostic: "telegram command synchronization failure"},
		{name: "get unauthorized", failingStep: "getMyCommands", status: 401, result: "", expectedInvalidToken: true, expectedDiagnostic: "telegram_invalid_token"},
		{name: "set unauthorized", failingStep: "setMyCommands", status: 401, result: "", expectedInvalidToken: true, expectedDiagnostic: "telegram_invalid_token"},
	} {
		test.Run("should report failures when command synchronization fails: "+scenario.name, func(test *testing.T) {
			// Given
			environment := setup(test)
			var diagnostics []string
			server := newServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				switch {
				case strings.HasSuffix(request.URL.Path, "/getMe"):
					responseWriter.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`))
				case strings.HasSuffix(request.URL.Path, "/getMyCommands") && scenario.failingStep != "getMyCommands":
					writeBotCommands(responseWriter, nil)
				case strings.HasSuffix(request.URL.Path, "/"+scenario.failingStep):
					if scenario.status == 200 {
						responseWriter.Write([]byte(`{"ok":true,"result":` + scenario.result + `}`))
					} else {
						responseWriter.Write([]byte(`{"ok":false,"error_code":` + strconv.Itoa(scenario.status) + `,"description":"synthetic-secret"}`))
					}
				default:
					test.Errorf("unexpected Telegram API method: %s", request.URL.Path)
				}
			}))
			config := Config{Token: "123:synthetic-token", CaseService: environment.bot.caseService, Clock: environment.clock}
			config.Endpoint = server.URL
			config.HTTPClient = server.Client()
			config.Diagnostic = func(category string) { diagnostics = append(diagnostics, category) }

			// When
			bot, err := New(context.Background(), config)

			// Then
			if bot != nil {
				test.Cleanup(bot.Close)
			}
			assert.Equal(test, scenario.expectedInvalidToken, (bot == nil))
			assert.Equal(test, scenario.expectedInvalidToken, (err == ErrInvalidToken))
			assert.DeepEqual(test, []string{scenario.expectedDiagnostic}, diagnostics, "startup should emit the expected sanitized diagnostic")
			assert.True(test, !(err != nil && strings.Contains(err.Error(), "synthetic-secret")), "Telegram response details leaked in startup error")
		})
	}
}
