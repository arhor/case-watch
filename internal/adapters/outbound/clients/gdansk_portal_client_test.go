package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/testing/assert"
)

func newGdanskTestClient(baseURL string) *GdanskPortalClient {
	client := NewGdanskPortalClient(GdanskPortalClientOptions{BaseURL: baseURL})
	client.interval = 0
	return client
}

type fakeClock struct {
	now time.Time
}

func (this *fakeClock) Now() time.Time { return this.now }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (this roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return this(r) }
func reply(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestGdanskPortalClientShouldCompleteWithFreshCookiesAndCompletionTimeWhenReused(t *testing.T) {
	// Given
	calls := 0
	clock := &fakeClock{now: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	waits := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		step := calls % 5
		calls++
		file := "testdata/bootstrap.html"
		if step == 0 {
			if calls == 1 && len(r.Cookies()) != 0 {
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
		assert.NoError(t, err)
		response := reply(string(b))
		if step == 0 {
			response.Header.Set("Set-Cookie", "session=fresh; Path=/")
		}
		return response, nil
	})
	c := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: transport, Clock: clock, Wait: func(ctx context.Context, d time.Duration) error {
		if d != 2*time.Second {
			t.Errorf("interval=%s", d)
		}
		waits++
		clock.now = clock.now.Add(d)
		return ctx.Err()
	}})
	// When
	firstStage, firstCheckedAt, err := c.Check(context.Background(), 1, "synthetic-case", "synthetic-password")
	secondStage, secondCheckedAt, err2 := c.Check(context.Background(), 1, "", "")
	// Then
	assert.NoError(t, err)
	assert.NoError(t, err2)
	assert.Equal(t, 10, calls, "calls should match the expected value")
	assert.Equal(t, 8, waits, "waits should match the expected value")
	assert.Equal(t, "Synthetic stage", firstStage, "firstStage should match the expected value")
	assert.Equal(t, firstStage, secondStage, "secondStage should match the expected value")
	assert.True(t, secondCheckedAt.Equal(clock.now), "completion timestamp should use the injected clock")
	assert.True(t, firstCheckedAt.Before(secondCheckedAt), "each check should record its own completion time")
}

func TestGdanskPortalClientShouldReturnSanitizedClassificationWhenExchangeFails(t *testing.T) {
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
			c := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: tc.transport})
			// When
			stage, checkedAt, err := c.Check(context.Background(), 1, "", "secret-password")
			// Then
			var safe *Error
			assert.True(t, errors.As(err, &safe), "portal failure should expose a safe error classification")
			assert.Equal(t, tc.kind, safe.Kind, "safe.Kind should match the expected value")
			assert.True(t, !strings.Contains(err.Error(), "secret"), "portal error should not expose secrets")
			assert.Equal(t, "", stage, "stage should match the expected value")
			assert.True(t, checkedAt.IsZero(), "failed check should not report a completion timestamp")
		})
	}
}
func TestGdanskPortalClientShouldStopWithoutRetryWhenOwnedDeadlineExpires(t *testing.T) {
	// Given
	calls := 0
	c := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	c.overall = time.Millisecond
	// When
	_, _, err := c.Check(context.Background(), 1, "", "")
	// Then
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, calls, "calls should match the expected value")
}
func TestGdanskPortalClientShouldMakeNoRequestWhenAlreadyCanceled(t *testing.T) {
	// Given
	calls := 0
	c := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return reply(""), nil })})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// When
	_, _, err := c.Check(ctx, 1, "", "")
	// Then
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, calls, "calls should match the expected value")
}

func TestGdanskPortalClientShouldStopWhenPerRequestDeadlineExpires(t *testing.T) {
	// Given
	calls := 0
	c := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	c.perRequest = time.Millisecond
	// When
	_, _, err := c.Check(context.Background(), 1, "", "")
	// Then
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, calls, "calls should match the expected value")
}

func TestGdanskPortalClientShouldCancelBetweenRequestsWhenWaitIsCanceled(t *testing.T) {
	// Given
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	c := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		b, _ := os.ReadFile("testdata/bootstrap.html")
		return reply(string(b)), nil
	}), Wait: func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }})
	// When
	_, _, err := c.Check(ctx, 1, "", "")
	// Then
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls, "calls should match the expected value")
}

func TestGdanskPortalClientShouldRejectBootstrapWhenPageIsUnexpected(t *testing.T) {
	// Given
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte("<html>unavailable</html>")) }))
	defer server.Close()
	// When
	_, _, err := newGdanskTestClient(server.URL).Check(context.Background(), 1, "synthetic-case", "synthetic-password")
	// Then
	assert.True(t, err != nil, "operation should return an error")
	assert.Equal(t, 1, calls, "calls should match the expected value")
}

func TestGdanskPortalClientShouldStopWhenPortalRedirects(t *testing.T) {
	// Given
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "/other")
		w.WriteHeader(302)
	}))
	defer server.Close()
	// When
	_, _, err := newGdanskTestClient(server.URL).Check(context.Background(), 1, "synthetic-case", "synthetic-password")
	// Then
	assert.True(t, err != nil, "operation should return an error")
	assert.Equal(t, 1, calls, "calls should match the expected value")
}

func TestGdanskPortalClientShouldExtractStageWhenAuthenticationSucceeds(t *testing.T) {
	// Given
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := calls
		calls++
		if index == 0 {
			if r.Method != "GET" {
				t.Error("bootstrap must use GET")
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-session"})
			b, err := os.ReadFile("testdata/bootstrap.html")
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Write(b)
			return
		}
		if index > 4 {
			t.Error("unexpected additional request")
			w.WriteHeader(500)
			return
		}
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value != "synthetic-session" {
			t.Error("missing fresh session cookie")
		}
		if r.Method != "POST" || r.URL.Query().Get("v-r") != "uidl" || r.URL.Query().Get("v-uiId") != "42" {
			t.Error("incorrect RPC route")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["csrfToken"] != "synthetic-token" || body["syncId"] != float64(index-1) || body["clientId"] != float64(index-1) {
			t.Error("incorrect fresh token or counters")
		}
		rpc := body["rpc"].([]any)
		if index == 2 || index == 3 {
			update := rpc[0].(map[string]any)
			want := "synthetic-case"
			if index == 3 {
				want = "synthetic-password"
			}
			if update["value"] != want {
				t.Error("incorrect field value")
			}
		}
		b, err := os.ReadFile(fmt.Sprintf("testdata/rpc%d.json", index-1))
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write(b)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// When
	stage, _, err := newGdanskTestClient(server.URL).Check(ctx, 1, "synthetic-case", "synthetic-password")
	// Then
	assert.NoError(t, err)
	assert.Equal(t, "Synthetic stage", stage, "stage should match the expected value")
	assert.Equal(t, 5, calls, "calls should match the expected value")
}

func TestGdanskPortalClientShouldRejectRPCWhenChangesAreMalformed(t *testing.T) {
	// Given
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			b, _ := os.ReadFile("testdata/bootstrap.html")
			w.Write(b)
			return
		}
		w.Write([]byte("for(;;);[{\"syncId\":1,\"clientId\":1,\"changes\":[null]}]"))
	}))
	defer server.Close()
	// When
	_, _, err := newGdanskTestClient(server.URL).Check(context.Background(), 1, "synthetic-case", "synthetic-password")
	// Then
	assert.True(t, err != nil, "operation should return an error")
	assert.Equal(t, 2, calls, "calls should match the expected value")
}

func TestGdanskPortalClientShouldStopWhenRepliesAreRejectedOrUnexpected(t *testing.T) {
	for _, tc := range []struct {
		name, reply  string
		budget       int
		wantRequests int
	}{
		{"authentication rejected", "for(;;);[{\"syncId\":1,\"clientId\":1}]", 6, 5},
		{"malformed JSON", "not JSON", 6, 2},
		{"expired session", "for(;;);[{\"meta\":{\"sessionExpired\":true}}]", 6, 2},
		{"unsupported change", "for(;;);[{\"syncId\":1,\"clientId\":1,\"changes\":[{\"node\":1,\"type\":\"unknown\"}]}]", 6, 2},
		{"request budget", "for(;;);[{\"syncId\":1,\"clientId\":1}]", 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					b, _ := os.ReadFile("testdata/bootstrap.html")
					w.Write(b)
					return
				}
				w.Write([]byte(tc.reply))
			}))
			defer server.Close()
			client := newGdanskTestClient(server.URL)
			client.budget = tc.budget
			// When
			stage, _, err := client.Check(context.Background(), 1, "synthetic-case", "synthetic-password")
			// Then
			assert.True(t, err != nil, "operation should return an error")
			assert.Equal(t, "", stage, "stage should match the expected value")
			assert.Equal(t, tc.wantRequests, calls, "calls should match the expected value")
		})
	}
}

func TestGdanskPortalClientShouldDiscoverComponentsWhenNodeIdentifiersChange(t *testing.T) {
	// Given
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := calls
		calls++
		name := "testdata/bootstrap.html"
		if index > 0 {
			name = fmt.Sprintf("testdata/rpc%d.json", index-1)
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if index == 0 {
			u, _ := gdanskBootstrapObject(b, "var uidl =")
			remapNodes(u)
			encoded, _ := json.Marshal(u)
			w.Write(append(append([]byte("<script>var uidl = "), encoded...), []byte(";var config = {\"v-uiId\":42};</script>")...))
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		for _, rpc := range req["rpc"].([]any) {
			n := rpc.(map[string]any)["node"].(float64)
			if n < 1000 {
				t.Error("reused captured node identifier")
			}
		}
		var replies []map[string]any
		json.Unmarshal(bytes.TrimPrefix(b, []byte("for(;;);")), &replies)
		for _, v := range replies {
			remapNodes(v)
		}
		encoded, _ := json.Marshal(replies)
		w.Write(append([]byte("for(;;);"), encoded...))
	}))
	defer server.Close()
	// When
	stage, _, err := newGdanskTestClient(server.URL).Check(context.Background(), 1, "synthetic-case", "synthetic-password")
	// Then
	assert.NoError(t, err)
	assert.Equal(t, "Synthetic stage", stage, "stage should match the expected value")
}

func remapNodes(v map[string]any) {
	cs, _ := v["changes"].([]any)
	for _, raw := range cs {
		c := raw.(map[string]any)
		c["node"] = c["node"].(float64) + 1000
		if nodes, ok := c["addNodes"].([]any); ok {
			for i, n := range nodes {
				nodes[i] = n.(float64) + 1000
			}
		}
	}
}

func TestGdanskPortalClientShouldStopWhenCallerDeadlineExpires(t *testing.T) {
	// Given
	calls := 0
	client := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// When
	_, _, err := client.Check(ctx, 1, "synthetic-case", "synthetic-password")
	// Then
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, calls, "calls should match the expected value")
}

// serveCheckFlow answers the five requests of one successful check by position within the check.
func serveCheckFlow(t *testing.T, w http.ResponseWriter, step int) {
	t.Helper()
	name := "testdata/bootstrap.html"
	if step > 0 {
		name = fmt.Sprintf("testdata/rpc%d.json", step-1)
	}
	b, err := os.ReadFile(name)
	if err != nil {
		t.Error(err)
		w.WriteHeader(500)
		return
	}
	w.Write(b)
}

func TestGdanskPortalClientShouldReuseConnectionWhenChecksRunSequentially(t *testing.T) {
	// Given
	var requests, connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveCheckFlow(t, w, int(requests.Add(1)-1)%5)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	client := newGdanskTestClient(server.URL)
	defer client.Close()
	// When
	_, _, err1 := client.Check(context.Background(), 1, "synthetic-case", "synthetic-password")
	_, _, err2 := client.Check(context.Background(), 1, "synthetic-case", "synthetic-password")
	// Then
	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.Equal(t, 10, requests.Load(), "requests.Load() should match the expected value")
	assert.Equal(t, 1, connections.Load(), "connections.Load() should match the expected value")
}

func TestGdanskPortalClientShouldNotResendWhenReusedConnectionIsClosedByServer(t *testing.T) {
	// Given
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(requests.Add(1) - 1)
		if index == 5 {
			// The second check's bootstrap GET arrives on the reused connection and is dropped unanswered.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		serveCheckFlow(t, w, index%5)
	}))
	defer server.Close()
	client := newGdanskTestClient(server.URL)
	defer client.Close()
	// When
	_, _, err1 := client.Check(context.Background(), 1, "synthetic-case", "synthetic-password")
	_, _, err2 := client.Check(context.Background(), 1, "synthetic-case", "synthetic-password")
	// Then
	var safe *Error
	assert.NoError(t, err1)
	assert.True(t, errors.As(err2, &safe), "connection failure should expose a safe error classification")
	assert.Equal(t, Transport, safe.Kind, "safe.Kind should match the expected value")
	assert.Equal(t, 6, requests.Load(), "requests.Load() should match the expected value")
}

func TestGdanskPortalClientShouldLeaveInjectedTransportOpenWhenClosed(t *testing.T) {
	// Given
	client := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return reply(""), nil
	})})
	// When
	client.Close()
	client.Close()
	// Then
	assert.True(t, client.httpClient != nil, "injected transport must not be owned")
}

func TestGdanskPortalClientShouldCloseRepeatedlyWhenTransportIsOwned(t *testing.T) {
	// Given
	client := NewGdanskPortalClient(GdanskPortalClientOptions{})
	// When
	client.Close()
	client.Close()
	// Then
	assert.True(t, client.httpClient != nil, "default transport must be created once and shared")
}

func TestGdanskPortalClientShouldRejectBootstrapWhenLoginFieldsAreMissingOrAmbiguous(t *testing.T) {
	for _, scenario := range []string{"missing", "ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			// Given
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				b, _ := os.ReadFile("testdata/bootstrap.html")
				u, _ := gdanskBootstrapObject(b, "var uidl =")
				changes := u["changes"].([]any)
				for _, raw := range changes {
					c := raw.(map[string]any)
					if c["key"] == "tag" && c["value"] == "vaadin-password-field" {
						if scenario == "ambiguous" {
							changes = append(changes, map[string]any{"type": "put", "node": float64(9999), "feat": float64(0), "key": "tag", "value": "vaadin-password-field"}, map[string]any{"type": "put", "node": float64(9999), "feat": float64(1), "key": "label", "value": "Password"})
						} else {
							c["value"] = "unknown-component"
						}
						break
					}
				}
				u["changes"] = changes
				encoded, _ := json.Marshal(u)
				w.Write(append(append([]byte("<script>var uidl = "), encoded...), []byte(";var config = {\"v-uiId\":42};</script>")...))
			}))
			defer server.Close()
			// When
			_, _, err := newGdanskTestClient(server.URL).Check(context.Background(), 1, "synthetic-case", "synthetic-password")
			// Then
			assert.True(t, err != nil, "operation should return an error")
			assert.Equal(t, 1, calls, "calls should match the expected value")
		})
	}
}
