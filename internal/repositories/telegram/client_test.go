package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/go-telegram/bot/models"
)

func TestSenderShouldClassifyFailuresWithoutReturningSecretsWhenAPIRejectsSend(t *testing.T) {
	for _, tc := range []struct {
		code        int
		description string
		category    domain.SendCategory
	}{{401, "synthetic-token", domain.SendInvalidToken}, {403, "Forbidden: bot was blocked by the user", domain.SendBlocked}, {403, "Forbidden: other reason", domain.SendPermanent}, {429, "synthetic-password", domain.SendRateLimited}, {500, "synthetic-password", domain.SendTransient}} {
		t.Run(string(tc.category)+tc.description, func(t *testing.T) {
			// Given
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": tc.code, "description": tc.description, "parameters": map[string]int{"retry_after": 9}})
			}))
			defer server.Close()

			a, err := New(Config{Token: "123:synthetic-token", Endpoint: server.URL, HTTPClient: server.Client()})
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

func TestValidateShouldCheckAcceptanceWithoutLeakingSecretsWhenTelegramResponds(t *testing.T) {
	for _, code := range []int{200, 401, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			// Given
			var logs []string
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
			config := Config{Token: "123:synthetic-token", Endpoint: server.URL, HTTPClient: server.Client(), Diagnostic: func(s string) { logs = append(logs, s) }}
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
			combined := strings.Join(logs, " ")
			if strings.Contains(combined, "synthetic") || (code == 401 && !strings.Contains(combined, "telegram_invalid_token")) || (code == 500 && !strings.Contains(combined, "telegram validation failure")) {
				t.Fatalf("logs=%s", combined)
			}
		})
	}
}

func TestValidateShouldHonorCancellationWhenStartupIsCanceled(t *testing.T) {
	// Given
	config := Config{Token: "123:synthetic-token"}
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

func TestPollShouldRejectAnotherRunWhenPollingHasAlreadyStarted(t *testing.T) {
	// Given
	client, err := New(Config{Token: "123:synthetic-token"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handle := func(context.Context, *models.Update) {}
	// When
	first := client.Poll(ctx, handle)
	second := client.Poll(ctx, handle)
	// Then
	if !errors.Is(first, context.Canceled) || !errors.Is(second, errPolling) {
		t.Fatalf("first=%v second=%v", first, second)
	}
}

func TestPollShouldKeepPollingAvailableWhenHandlerIsMissing(t *testing.T) {
	// Given
	client, err := New(Config{Token: "123:synthetic-token"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// When
	missing := client.Poll(ctx, nil)
	valid := client.Poll(ctx, func(context.Context, *models.Update) {})
	// Then
	if !errors.Is(missing, errPolling) || !errors.Is(valid, context.Canceled) {
		t.Fatalf("missing=%v valid=%v", missing, valid)
	}
}

func TestPollShouldDispatchMessagesAndMembershipInOrderWhenUpdatesArrive(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := true
	server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if first {
			first = false
			w.Write([]byte(`{"ok":true,"result":[{"update_id":1,"message":{"message_id":1,"text":"first"}},{"update_id":2,"my_chat_member":{"chat":{"id":101,"type":"private"},"from":{"id":101},"old_chat_member":{"status":"member","user":{"id":123}},"new_chat_member":{"status":"kicked","user":{"id":123}}}}]}`))
			return
		}
		<-r.Context().Done()
	}))
	client, err := New(Config{Token: "123:synthetic-token", Endpoint: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	var received []string
	// When
	err = client.Poll(ctx, func(_ context.Context, update *models.Update) {
		if update.Message != nil {
			received = append(received, update.Message.Text)
		}
		if update.MyChatMember != nil {
			received = append(received, "membership")
			cancel()
		}
	})
	// Then
	if !errors.Is(err, context.Canceled) || strings.Join(received, ",") != "first,membership" {
		t.Fatalf("error=%v received=%v", err, received)
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
