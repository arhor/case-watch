package bot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/domain"
)

type emptyCases struct{}

func (emptyCases) Enroll(context.Context, int64, domain.Credentials) (domain.Observation, error) {
	return domain.Observation{}, nil
}
func (emptyCases) Status(context.Context, int64) (domain.Status, error) { return domain.Status{}, nil }
func (emptyCases) Remove(context.Context, int64) error                  { return nil }
func (emptyCases) HasEnrollment(context.Context, int64) (bool, error)   { return false, nil }

func apiBot(t *testing.T, server *fakeServer, diagnostic func(string)) *Bot {
	t.Helper()
	a, err := New(Config{
		Token: "123:synthetic-token", Endpoint: server.URL, HTTPClient: server.Client(),
		Cases: emptyCases{}, Clock: &fakeClock{now: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}, Diagnostic: diagnostic,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestValidateShouldCheckAcceptanceWithoutLeakingSecrets(t *testing.T) {
	for _, code := range []int{200, 401, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var logs []string
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if code == 200 {
					w.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`))
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": code, "description": "synthetic-secret"})
			}))
			a := apiBot(t, server, func(s string) { logs = append(logs, s) })
			err := a.Validate(context.Background())
			if (err == nil) != (code == 200) || strings.Contains(strings.Join(logs, " ")+" "+errorText(err), "synthetic-secret") {
				t.Fatalf("error=%v logs=%v", err, logs)
			}
			if code == 401 && (!errors.Is(err, ErrInvalidToken) || !strings.Contains(strings.Join(logs, " "), "telegram_invalid_token")) {
				t.Fatalf("token rejection not classified: %v %v", err, logs)
			}
		})
	}
}

func TestReplyShouldHonorRateLimitAndCloseAfterTokenRejection(t *testing.T) {
	for _, code := range []int{401, 429} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			calls := 0
			var logs []string
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": code, "description": "synthetic-secret", "parameters": map[string]int{"retry_after": 60}})
			}))
			a := apiBot(t, server, func(s string) { logs = append(logs, s) })
			a.reply(context.Background(), 101, "help")
			a.reply(context.Background(), 202, "help")
			if calls != 1 || strings.Contains(strings.Join(logs, " "), "synthetic-secret") {
				t.Fatalf("calls=%d logs=%v", calls, logs)
			}
		})
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
