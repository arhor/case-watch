package portal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/application"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reply(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestClientShouldCompleteWithFreshCookiesAndCompletionTimeWhenReused(t *testing.T) {
	// Given
	calls := 0
	now := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	waits := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		step := calls % 5
		calls++
		file := "testdata/bootstrap.html"
		if step == 0 {
			if len(r.Cookies()) != 0 {
				t.Error("session leaked between checks")
			}
		} else {
			file = fmt.Sprintf("testdata/rpc%d.json", step-1)
			if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "fresh" {
				t.Error("fresh cookie missing")
			}
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("missing request deadline")
		}
		if r.GetBody != nil {
			t.Error("replayable request")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		response := reply(string(b))
		if step == 0 {
			response.Header.Set("Set-Cookie", "session=fresh; Path=/")
		}
		return response, nil
	})
	c := NewClient(Options{Transport: transport, Now: func() time.Time { return now }, Wait: func(ctx context.Context, d time.Duration) error {
		if d != 2*time.Second {
			t.Errorf("interval=%s", d)
		}
		waits++
		now = now.Add(d)
		return ctx.Err()
	}})
	// When
	first, err := c.Check(context.Background(), application.Credentials{Number: "synthetic-case", Password: "synthetic-password"})
	second, err2 := c.Check(context.Background(), application.Credentials{})
	// Then
	if err != nil || err2 != nil || calls != 10 || waits != 8 || first.Stage != "Synthetic stage" || second.Stage != first.Stage || !second.CheckedAt.Equal(now) || !first.CheckedAt.Before(second.CheckedAt) {
		t.Fatalf("observations=%+v %+v errors=%v %v calls=%d waits=%d", first, second, err, err2, calls, waits)
	}
}
func TestClientShouldReturnSanitizedClassificationWhenExchangeFails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kind      Kind
		transport roundTripFunc
	}{
		{"transport", Transport, func(*http.Request) (*http.Response, error) {
			return nil, errors.New("secret-password https://secret-token")
		}},
		{"authentication", Authentication, func(*http.Request) (*http.Response, error) {
			r := reply("secret-password")
			r.StatusCode = 403
			return r, nil
		}},
		{"malformed", Protocol, func(*http.Request) (*http.Response, error) { return reply("secret-password"), nil }},
		{"oversized", Protocol, func(*http.Request) (*http.Response, error) { return reply(strings.Repeat("x", (4<<20)+1)), nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			c := NewClient(Options{Transport: tc.transport})
			// When
			observation, err := c.Check(context.Background(), application.Credentials{Password: "secret-password"})
			// Then
			var safe *Error
			if !errors.As(err, &safe) || safe.Kind != tc.kind || strings.Contains(err.Error(), "secret") || observation.Stage != "" || !observation.CheckedAt.IsZero() {
				t.Fatalf("observation=%+v error=%v", observation, err)
			}
		})
	}
}
func TestClientShouldStopWithoutRetryWhenOwnedDeadlineExpires(t *testing.T) {
	// Given
	calls := 0
	c := NewClient(Options{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	c.overall = time.Millisecond
	// When
	_, err := c.Check(context.Background(), application.Credentials{})
	// Then
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}
func TestClientShouldMakeNoRequestWhenAlreadyCanceled(t *testing.T) {
	// Given
	calls := 0
	c := NewClient(Options{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return reply(""), nil })})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// When
	_, err := c.Check(ctx, application.Credentials{})
	// Then
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestClientShouldStopWhenPerRequestDeadlineExpires(t *testing.T) {
	// Given
	calls := 0
	c := NewClient(Options{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	c.perRequest = time.Millisecond
	// When
	_, err := c.Check(context.Background(), application.Credentials{})
	// Then
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestClientShouldCancelBetweenRequestsWhenWaitIsCanceled(t *testing.T) {
	// Given
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	c := NewClient(Options{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		b, _ := os.ReadFile("testdata/bootstrap.html")
		return reply(string(b)), nil
	}), Wait: func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }})
	// When
	_, err := c.Check(ctx, application.Credentials{})
	// Then
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}
