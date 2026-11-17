package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	telegramModels "github.com/go-telegram/bot/models"

	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestNewShouldSynchronizeCommandsOnlyWhenTheyChange(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		modify  func([]telegramModels.BotCommand) []telegramModels.BotCommand
		wantSet bool
	}{
		{"unchanged", func(commands []telegramModels.BotCommand) []telegramModels.BotCommand { return commands }, false},
		{"missing", func([]telegramModels.BotCommand) []telegramModels.BotCommand { return nil }, true},
		{"description changed", func(commands []telegramModels.BotCommand) []telegramModels.BotCommand {
			commands[0].Description = "Old description"
			return commands
		}, true},
		{"order changed", func(commands []telegramModels.BotCommand) []telegramModels.BotCommand {
			commands[0], commands[1] = commands[1], commands[0]
			return commands
		}, true},
		{"obsolete command", func(commands []telegramModels.BotCommand) []telegramModels.BotCommand {
			return append(commands, telegramModels.BotCommand{Command: "obsolete", Description: "Obsolete"})
		}, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Given
			f := setup(t)
			current := testCase.modify(slices.Clone(botCommands))
			var calls []string
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/getMe"):
					calls = append(calls, "getMe")
					w.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`))
				case strings.HasSuffix(r.URL.Path, "/getMyCommands"):
					calls = append(calls, "getMyCommands")
					writeBotCommands(w, current)
				case strings.HasSuffix(r.URL.Path, "/setMyCommands"):
					calls = append(calls, "setMyCommands")
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					var sent []telegramModels.BotCommand
					if err := json.Unmarshal([]byte(r.FormValue("commands")), &sent); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if !slices.Equal(sent, botCommands) {
						t.Errorf("published commands = %v, want %v", sent, botCommands)
					}
					if r.FormValue("scope") != "" || r.FormValue("language_code") != "" {
						t.Error("synchronization must only modify the default command list")
					}
					w.Write([]byte(`{"ok":true,"result":true}`))
				default:
					t.Errorf("unexpected Telegram API method: %s", r.URL.Path)
				}
			}))
			config := Config{Token: "123:synthetic-token", CaseService: f.a.caseService, Clock: f.clock}
			config.Endpoint = server.URL
			config.HTTPClient = server.Client()
			// When
			a, err := New(context.Background(), config)
			// Then
			assert.NoError(t, err)
			t.Cleanup(a.Close)

			wantCalls := []string{"getMe", "getMyCommands"}
			if testCase.wantSet {
				wantCalls = append(wantCalls, "setMyCommands")
			}
			assert.True(t, slices.Equal(calls, wantCalls), "startup should perform only the required command synchronization calls")
		})
	}
}

func TestNewShouldReportFailuresWhenCommandSynchronizationFails(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		failingStep string
		status      int
		result      string
		wantInvalid bool
	}{
		{"get fails", "getMyCommands", 500, "", false},
		{"set fails", "setMyCommands", 500, "", false},
		{"set returns false", "setMyCommands", 200, "false", false},
		{"get unauthorized", "getMyCommands", 401, "", true},
		{"set unauthorized", "setMyCommands", 401, "", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Given
			f := setup(t)
			var diagnostics []string
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/getMe"):
					w.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`))
				case strings.HasSuffix(r.URL.Path, "/getMyCommands") && testCase.failingStep != "getMyCommands":
					writeBotCommands(w, nil)
				case strings.HasSuffix(r.URL.Path, "/"+testCase.failingStep):
					if testCase.status == 200 {
						w.Write([]byte(`{"ok":true,"result":` + testCase.result + `}`))
					} else {
						w.Write([]byte(`{"ok":false,"error_code":` + strconv.Itoa(testCase.status) + `,"description":"synthetic-secret"}`))
					}
				default:
					t.Errorf("unexpected Telegram API method: %s", r.URL.Path)
				}
			}))
			config := Config{Token: "123:synthetic-token", CaseService: f.a.caseService, Clock: f.clock}
			config.Endpoint = server.URL
			config.HTTPClient = server.Client()
			config.Diagnostic = func(category string) { diagnostics = append(diagnostics, category) }
			// When
			a, err := New(context.Background(), config)
			// Then
			if a != nil {
				t.Cleanup(a.Close)
			}
			assert.Equal(t, testCase.wantInvalid, (a == nil), "(a == nil) should match the expected value")
			assert.Equal(t, testCase.wantInvalid, (err == ErrInvalidToken), "(err == ErrInvalidToken) should match the expected value")
			wantDiagnostic := "telegram command synchronization failure"
			if testCase.wantInvalid {
				wantDiagnostic = "telegram_invalid_token"
			}
			assert.True(t, slices.Equal(diagnostics, []string{wantDiagnostic}), "startup should emit the expected sanitized diagnostic")
			assert.True(t, !(err != nil && strings.Contains(err.Error(), "synthetic-secret")), "Telegram response details leaked in startup error")
		})
	}
}

func TestBotShouldKeepHandlersAndDescriptionsConsistentWhenCommandsArePublished(t *testing.T) {
	// Given
	f := setup(t)
	// When
	f.message(101, "/help")
	help := f.replies[len(f.replies)-1]
	// Then
	assert.Equal(t, len(botCommands), len(f.a.handlers), "every published command should have a handler")
	for _, command := range botCommands {
		assert.True(t, command.Description != "", "published command /"+command.Command+" should have a description")
		_, ok := f.a.handlers["/"+command.Command]
		assert.True(t, ok, "published command /"+command.Command+" should have a handler")
		assert.True(t, strings.Contains(help, "/"+command.Command+" — "+command.Description), "help should describe /"+command.Command)
	}
}
