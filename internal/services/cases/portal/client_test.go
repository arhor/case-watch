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
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reply(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestClientShouldCompleteWithFreshCookiesAndCompletionTimeWhenReused(t *testing.T) {
	// Given
	calls := 0
	clock := &fakeClock{now: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
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
	c := NewClient(Options{Transport: transport, Clock: clock, Wait: func(ctx context.Context, d time.Duration) error {
		if d != 2*time.Second {
			t.Errorf("interval=%s", d)
		}
		waits++
		clock.now = clock.now.Add(d)
		return ctx.Err()
	}})
	// When
	firstStage, firstCheckedAt, err := c.Check(context.Background(), "synthetic-case", "synthetic-password")
	secondStage, secondCheckedAt, err2 := c.Check(context.Background(), "", "")
	// Then
	if err != nil || err2 != nil || calls != 10 || waits != 8 || firstStage != "Synthetic stage" || secondStage != firstStage || !secondCheckedAt.Equal(clock.now) || !firstCheckedAt.Before(secondCheckedAt) {
		t.Fatalf("stages=%q/%q checkedAt=%v/%v errors=%v %v calls=%d waits=%d", firstStage, secondStage, firstCheckedAt, secondCheckedAt, err, err2, calls, waits)
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
			stage, checkedAt, err := c.Check(context.Background(), "", "secret-password")
			// Then
			var safe *Error
			if !errors.As(err, &safe) || safe.Kind != tc.kind || strings.Contains(err.Error(), "secret") || stage != "" || !checkedAt.IsZero() {
				t.Fatalf("stage=%q checkedAt=%v error=%v", stage, checkedAt, err)
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
	_, _, err := c.Check(context.Background(), "", "")
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
	_, _, err := c.Check(ctx, "", "")
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
	_, _, err := c.Check(context.Background(), "", "")
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
	_, _, err := c.Check(ctx, "", "")
	// Then
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}
