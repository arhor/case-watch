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
	"github.com/arhor/case-watch/internal/testing/clock"
)

func TestGdanskPortalClientCheck(test *testing.T) {
	test.Run("should complete with fresh cookies and completion time when reused", func(test *testing.T) {
		// Given
		calls := 0
		clock := clock.NewFixedClock(time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC))
		waits := 0
		transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			step := calls % 5
			calls++
			file := "testdata/bootstrap.html"
			if step == 0 {
				if calls == 1 && len(request.Cookies()) != 0 {
					test.Error("session leaked between checks")
				}
			} else {
				file = fmt.Sprintf("testdata/rpc%d.json", step-1)
				if cookie, err := request.Cookie("session"); err != nil || cookie.Value != "fresh" {
					test.Error("fresh cookie missing")
				}
			}
			if _, ok := request.Context().Deadline(); !ok {
				test.Error("missing request deadline")
			}
			if request.GetBody != nil {
				test.Error("replayable request")
			}
			data, err := os.ReadFile(file)
			assert.NoError(test, err)
			response := reply(string(data))
			if step == 0 {
				response.Header.Set("Set-Cookie", "session=fresh; Path=/")
			}
			return response, nil
		})
		client := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: transport, Clock: clock, Wait: func(ctx context.Context, delay time.Duration) error {
			if delay != 2*time.Second {
				test.Errorf("interval=%s", delay)
			}
			waits++
			clock.Set(clock.Now().Add(delay))
			return ctx.Err()
		}})

		// When
		firstSnapshot, err := client.Check(context.Background(), 1, "synthetic-case", "synthetic-password")
		secondSnapshot, err2 := client.Check(context.Background(), 1, "", "")

		// Then
		assert.NoError(test, err)
		assert.NoError(test, err2)
		assert.Equal(test, 10, calls)
		assert.Equal(test, 8, waits)
		assert.Equal(test, "Synthetic stage", firstSnapshot.Stage)
		assert.Equal(test, firstSnapshot.Stage, secondSnapshot.Stage)
		assert.True(test, secondSnapshot.CheckedAt.Equal(clock.Now()), "completion timestamp should use the injected clock")
		assert.True(test, firstSnapshot.CheckedAt.Before(secondSnapshot.CheckedAt), "each check should record its own completion time")
	})

	for _, scenario := range []struct {
		name      string
		kind      Kind
		transport roundTripFunc
	}{
		{name: "transport", kind: Transport, transport: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("secret-password https://secret-token")
		}},
		{name: "authentication", kind: Authentication, transport: func(*http.Request) (*http.Response, error) {
			request := reply("secret-password")
			request.StatusCode = 403
			return request, nil
		}},
		{name: "malformed", kind: Protocol, transport: func(*http.Request) (*http.Response, error) { return reply("secret-password"), nil }},
		{name: "oversized", kind: Protocol, transport: func(*http.Request) (*http.Response, error) { return reply(strings.Repeat("x", (4<<20)+1)), nil }},
	} {
		test.Run("should return sanitized classification when exchange fails: "+scenario.name, func(test *testing.T) {
			// Given
			client := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: scenario.transport})

			// When
			snapshot, err := client.Check(context.Background(), 1, "", "secret-password")

			// Then
			var safe *Error
			assert.True(test, errors.As(err, &safe), "portal failure should expose a safe error classification")
			assert.Equal(test, scenario.kind, safe.Kind)
			assert.True(test, !strings.Contains(err.Error(), "secret"), "portal error should not expose secrets")
			assert.Equal(test, "", snapshot.Stage)
			assert.True(test, snapshot.CheckedAt.IsZero(), "failed check should not report a completion timestamp")
		})
	}

	test.Run("should stop without retry when owned deadline expires", func(test *testing.T) {
		// Given
		calls := 0
		client := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			<-request.Context().Done()
			return nil, request.Context().Err()
		})})
		client.overall = time.Millisecond

		// When
		_, err := client.Check(context.Background(), 1, "", "")

		// Then
		assert.ErrorIs(test, err, context.DeadlineExceeded)
		assert.Equal(test, 1, calls)
	})

	test.Run("should make no request when already canceled", func(test *testing.T) {
		// Given
		calls := 0
		client := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return reply(""), nil })})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// When
		_, err := client.Check(ctx, 1, "", "")

		// Then
		assert.ErrorIs(test, err, context.Canceled)
		assert.Equal(test, 0, calls)
	})

	test.Run("should stop when per request deadline expires", func(test *testing.T) {
		// Given
		calls := 0
		client := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			<-request.Context().Done()
			return nil, request.Context().Err()
		})})
		client.perRequest = time.Millisecond

		// When
		_, err := client.Check(context.Background(), 1, "", "")

		// Then
		assert.ErrorIs(test, err, context.DeadlineExceeded)
		assert.Equal(test, 1, calls)
	})

	test.Run("should cancel between requests when wait is canceled", func(test *testing.T) {
		// Given
		calls := 0
		ctx, cancel := context.WithCancel(context.Background())
		client := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			data, _ := os.ReadFile("testdata/bootstrap.html")
			return reply(string(data)), nil
		}), Wait: func(ctx context.Context, delay time.Duration) error { cancel(); return ctx.Err() }})

		// When
		_, err := client.Check(ctx, 1, "", "")

		// Then
		assert.ErrorIs(test, err, context.Canceled)
		assert.Equal(test, 1, calls)
	})

	test.Run("should reject bootstrap when page is unexpected", func(test *testing.T) {
		// Given
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			calls++
			responseWriter.Write([]byte("<html>unavailable</html>"))
		}))
		defer server.Close()

		// When
		_, err := newGdanskTestClient(server.URL).Check(context.Background(), 1, "synthetic-case", "synthetic-password")

		// Then
		assert.True(test, err != nil, "operation should return an error")
		assert.Equal(test, 1, calls)
	})

	test.Run("should stop when portal redirects", func(test *testing.T) {
		// Given
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			calls++
			responseWriter.Header().Set("Location", "/other")
			responseWriter.WriteHeader(302)
		}))
		defer server.Close()

		// When
		_, err := newGdanskTestClient(server.URL).Check(context.Background(), 1, "synthetic-case", "synthetic-password")

		// Then
		assert.True(test, err != nil, "operation should return an error")
		assert.Equal(test, 1, calls)
	})

	test.Run("should extract stage when authentication succeeds", func(test *testing.T) {
		// Given
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			index := calls
			calls++
			if index == 0 {
				if request.Method != "GET" {
					test.Error("bootstrap must use GET")
				}
				http.SetCookie(responseWriter, &http.Cookie{Name: "session", Value: "synthetic-session"})
				data, err := os.ReadFile("testdata/bootstrap.html")
				if err != nil {
					test.Error(err)
					responseWriter.WriteHeader(http.StatusInternalServerError)
					return
				}
				responseWriter.Write(data)
				return
			}
			if index > 4 {
				test.Error("unexpected additional request")
				responseWriter.WriteHeader(500)
				return
			}
			cookie, err := request.Cookie("session")
			if err != nil || cookie.Value != "synthetic-session" {
				test.Error("missing fresh session cookie")
			}
			if request.Method != "POST" || request.URL.Query().Get("v-r") != "uidl" || request.URL.Query().Get("v-uiId") != "42" {
				test.Error("incorrect RPC route")
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				test.Error(err)
				return
			}
			if body["csrfToken"] != "synthetic-token" || body["syncId"] != float64(index-1) || body["clientId"] != float64(index-1) {
				test.Error("incorrect fresh token or counters")
			}
			rpc := body["rpc"].([]any)
			if index == 2 || index == 3 {
				update := rpc[0].(map[string]any)
				expectedValue := "synthetic-case"
				if index == 3 {
					expectedValue = "synthetic-password"
				}
				if update["value"] != expectedValue {
					test.Error("incorrect field value")
				}
			}
			data, err := os.ReadFile(fmt.Sprintf("testdata/rpc%d.json", index-1))
			if err != nil {
				test.Error(err)
				responseWriter.WriteHeader(http.StatusInternalServerError)
				return
			}
			responseWriter.Write(data)
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// When
		snapshot, err := newGdanskTestClient(server.URL).Check(ctx, 1, "synthetic-case", "synthetic-password")

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "Synthetic stage", snapshot.Stage)
		assert.Equal(test, 5, calls)
	})

	test.Run("should reject rpc when changes are malformed", func(test *testing.T) {
		// Given
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			calls++
			if calls == 1 {
				data, _ := os.ReadFile("testdata/bootstrap.html")
				responseWriter.Write(data)
				return
			}
			responseWriter.Write([]byte("for(;;);[{\"syncId\":1,\"clientId\":1,\"changes\":[null]}]"))
		}))
		defer server.Close()

		// When
		_, err := newGdanskTestClient(server.URL).Check(context.Background(), 1, "synthetic-case", "synthetic-password")

		// Then
		assert.True(test, err != nil, "operation should return an error")
		assert.Equal(test, 2, calls)
	})

	for _, scenario := range []struct {
		name, reply      string
		budget           int
		expectedRequests int
	}{
		{name: "authentication rejected", reply: "for(;;);[{\"syncId\":1,\"clientId\":1}]", budget: 6, expectedRequests: 5},
		{name: "malformed JSON", reply: "not JSON", budget: 6, expectedRequests: 2},
		{name: "expired session", reply: "for(;;);[{\"meta\":{\"sessionExpired\":true}}]", budget: 6, expectedRequests: 2},
		{name: "unsupported change", reply: "for(;;);[{\"syncId\":1,\"clientId\":1,\"changes\":[{\"node\":1,\"type\":\"unknown\"}]}]", budget: 6, expectedRequests: 2},
		{name: "request budget", reply: "for(;;);[{\"syncId\":1,\"clientId\":1}]", budget: 2, expectedRequests: 2},
	} {
		test.Run("should stop when replies are rejected or unexpected: "+scenario.name, func(test *testing.T) {
			// Given
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				calls++
				if calls == 1 {
					data, _ := os.ReadFile("testdata/bootstrap.html")
					responseWriter.Write(data)
					return
				}
				responseWriter.Write([]byte(scenario.reply))
			}))
			defer server.Close()
			client := newGdanskTestClient(server.URL)
			client.budget = scenario.budget

			// When
			snapshot, err := client.Check(context.Background(), 1, "synthetic-case", "synthetic-password")

			// Then
			assert.True(test, err != nil, "operation should return an error")
			assert.Equal(test, "", snapshot.Stage)
			assert.Equal(test, scenario.expectedRequests, calls)
		})
	}

	test.Run("should discover components when node identifiers change", func(test *testing.T) {
		// Given
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			index := calls
			calls++
			name := "testdata/bootstrap.html"
			if index > 0 {
				name = fmt.Sprintf("testdata/rpc%d.json", index-1)
			}
			data, err := os.ReadFile(name)
			if err != nil {
				test.Error(err)
				responseWriter.WriteHeader(http.StatusInternalServerError)
				return
			}
			if index == 0 {
				bootstrap, _ := testBootstrapObject(data, "var uidl =")
				remapNodes(bootstrap)
				encoded, _ := json.Marshal(bootstrap)
				responseWriter.Write(append(append([]byte("<script>var uidl = "), encoded...), []byte(";var config = {\"v-uiId\":42};</script>")...))
				return
			}
			var requestPayload map[string]any
			json.NewDecoder(request.Body).Decode(&requestPayload)
			for _, rpc := range requestPayload["rpc"].([]any) {
				nodeID := rpc.(map[string]any)["node"].(float64)
				if nodeID < 1000 {
					test.Error("reused captured node identifier")
				}
			}
			var replies []map[string]any
			json.Unmarshal(bytes.TrimPrefix(data, []byte("for(;;);")), &replies)
			for _, payload := range replies {
				remapNodes(payload)
			}
			encoded, _ := json.Marshal(replies)
			responseWriter.Write(append([]byte("for(;;);"), encoded...))
		}))
		defer server.Close()

		// When
		snapshot, err := newGdanskTestClient(server.URL).Check(context.Background(), 1, "synthetic-case", "synthetic-password")

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "Synthetic stage", snapshot.Stage)
	})

	test.Run("should stop when caller deadline expires", func(test *testing.T) {
		// Given
		calls := 0
		client := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			<-request.Context().Done()
			return nil, request.Context().Err()
		})})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		// When
		_, err := client.Check(ctx, 1, "synthetic-case", "synthetic-password")

		// Then
		assert.ErrorIs(test, err, context.DeadlineExceeded)
		assert.Equal(test, 1, calls)
	})

	test.Run("should reuse connection when checks run sequentially", func(test *testing.T) {
		// Given
		var requests, connections atomic.Int32
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			serveCheckFlow(test, responseWriter, int(requests.Add(1)-1)%5)
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
		_, err1 := client.Check(context.Background(), 1, "synthetic-case", "synthetic-password")
		_, err2 := client.Check(context.Background(), 1, "synthetic-case", "synthetic-password")

		// Then
		assert.NoError(test, err1)
		assert.NoError(test, err2)
		assert.Equal(test, 10, requests.Load())
		assert.Equal(test, 1, connections.Load())
	})

	test.Run("should not resend when reused connection is closed by server", func(test *testing.T) {
		// Given
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			index := int(requests.Add(1) - 1)
			if index == 5 {
				// The second check's bootstrap GET arrives on the reused connection and is dropped unanswered.
				conn, _, err := responseWriter.(http.Hijacker).Hijack()
				if err != nil {
					test.Error(err)
					return
				}
				conn.Close()
				return
			}
			serveCheckFlow(test, responseWriter, index%5)
		}))
		defer server.Close()
		client := newGdanskTestClient(server.URL)
		defer client.Close()

		// When
		_, err1 := client.Check(context.Background(), 1, "synthetic-case", "synthetic-password")
		_, err2 := client.Check(context.Background(), 1, "synthetic-case", "synthetic-password")

		// Then
		var safe *Error
		assert.NoError(test, err1)
		assert.True(test, errors.As(err2, &safe), "connection failure should expose a safe error classification")
		assert.Equal(test, Transport, safe.Kind)
		assert.Equal(test, 6, requests.Load())
	})

	test.Run("should reject bootstrap when password field is missing", func(test *testing.T) {
		// Given
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			calls++
			data, _ := os.ReadFile("testdata/bootstrap.html")
			bootstrap, _ := testBootstrapObject(data, "var uidl =")
			changes := bootstrap["changes"].([]any)
			for _, raw := range changes {
				change := raw.(map[string]any)
				if change["key"] == "tag" && change["value"] == "vaadin-password-field" {

					change["value"] = "unknown-component"

					break
				}
			}
			bootstrap["changes"] = changes
			encoded, _ := json.Marshal(bootstrap)
			responseWriter.Write(append(append([]byte("<script>var uidl = "), encoded...), []byte(";var config = {\"v-uiId\":42};</script>")...))
		}))
		defer server.Close()

		// When
		_, err := newGdanskTestClient(server.URL).Check(context.Background(), 1, "synthetic-case", "synthetic-password")

		// Then
		assert.True(test, err != nil, "operation should return an error")
		assert.Equal(test, 1, calls)
	})

	test.Run("should reject bootstrap when password field is ambiguous", func(test *testing.T) {
		// Given
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			calls++
			data, _ := os.ReadFile("testdata/bootstrap.html")
			bootstrap, _ := testBootstrapObject(data, "var uidl =")
			changes := bootstrap["changes"].([]any)
			for _, raw := range changes {
				change := raw.(map[string]any)
				if change["key"] == "tag" && change["value"] == "vaadin-password-field" {

					changes = append(changes, map[string]any{"type": "put", "node": float64(9999), "feat": float64(0), "key": "tag", "value": "vaadin-password-field"}, map[string]any{"type": "put", "node": float64(9999), "feat": float64(1), "key": "label", "value": "Password"})
					break
				}
			}
			bootstrap["changes"] = changes
			encoded, _ := json.Marshal(bootstrap)
			responseWriter.Write(append(append([]byte("<script>var uidl = "), encoded...), []byte(";var config = {\"v-uiId\":42};</script>")...))
		}))
		defer server.Close()

		// When
		_, err := newGdanskTestClient(server.URL).Check(context.Background(), 1, "synthetic-case", "synthetic-password")

		// Then
		assert.True(test, err != nil, "operation should return an error")
		assert.Equal(test, 1, calls)
	})
	for _, flow := range []string{"authenticated", "incremental"} {
		test.Run("should render selected content and preserve authentication sequence for "+flow+" fixtures", func(test *testing.T) {
			// Given
			bootstrapFile := "testdata/vaadin/bootstrap-authenticated.html"
			expectedRequests := 1
			if flow == "incremental" {
				bootstrapFile = "testdata/vaadin/bootstrap-login.html"
				expectedRequests = 5
			}
			data, err := os.ReadFile(bootstrapFile)
			assert.NoError(test, err)
			bootstrap, err := testBootstrapObject(data, "var uidl =")
			assert.NoError(test, err)
			initialKey := bootstrap["Vaadin-Security-Key"]
			encoded, err := json.Marshal(bootstrap)
			assert.NoError(test, err)
			document := append(append([]byte("var uidl = "), encoded...), []byte(`;var config = {"v-uiId":42};`)...)
			responsesData, err := os.ReadFile("testdata/vaadin/rpc-authentication.json")
			assert.NoError(test, err)
			var responses []string
			assert.NoError(test, json.Unmarshal(responsesData, &responses))
			// The first HTTP reply carries two messages. Neither counter progression
			// nor key replacement is inferred from request/response position.
			responses[0] = `for(;;);[{"syncId":7,"clientId":8,"Vaadin-Security-Key":"intermediate-key"},{"syncId":20,"clientId":21,"Vaadin-Security-Key":"replacement-key","changes":[]}]`
			calls := 0
			client := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				index := calls
				calls++
				if index == 0 {
					return reply(string(document)), nil
				}
				var payload map[string]any
				assert.NoError(test, json.NewDecoder(request.Body).Decode(&payload))
				expectedSync, expectedClient := float64(index-1), float64(index-1)
				expectedKey := initialKey
				if index > 1 {
					expectedKey = "replacement-key"
				}
				if index == 2 {
					expectedSync, expectedClient = 20, 21
				}
				assert.Equal[any](test, expectedSync, payload["syncId"])
				assert.Equal[any](test, expectedClient, payload["clientId"])
				assert.Equal(test, expectedKey, payload["csrfToken"])
				assert.True(test, index <= len(responses))
				return reply(responses[index-1]), nil
			}), Wait: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }})

			// When
			snapshot, err := client.Check(context.Background(), 1, "EXAMPLE-0001", "example-password")

			// Then
			assert.NoError(test, err)
			assert.Equal(test, "Example stage", snapshot.Stage)
			expectedHTML, readError := os.ReadFile("testdata/vaadin/expected-content.html")
			assert.NoError(test, readError)
			assert.Equal(test, strings.TrimSpace(string(expectedHTML)), snapshot.HTML)
			assert.Equal(test, expectedRequests, calls)
		})
	}
	test.Run("should extract stage without a security key when an authenticated bootstrap uses legacy nodeValue text", func(test *testing.T) {
		// Given
		data, err := os.ReadFile("testdata/vaadin/bootstrap-authenticated.html")
		assert.NoError(test, err)
		bootstrap, err := testBootstrapObject(data, "var uidl =")
		assert.NoError(test, err)
		delete(bootstrap, "Vaadin-Security-Key")
		for _, raw := range bootstrap["changes"].([]any) {
			change := raw.(map[string]any)
			if change["key"] == "text" && change["value"] == "Stage of your case" {
				change["key"] = "nodeValue"
			}
		}
		encoded, err := json.Marshal(bootstrap)
		assert.NoError(test, err)
		document := append(append([]byte("var uidl = "), encoded...), []byte(`;var config = {"v-uiId":42};`)...)
		client := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return reply(string(document)), nil
		})})

		// When
		snapshot, err := client.Check(context.Background(), 1, "", "")

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "Example stage", snapshot.Stage)
	})

}

func TestGdanskPortalClientClose(test *testing.T) {
	test.Run("should leave injected transport open when closed", func(test *testing.T) {
		// Given
		client := NewGdanskPortalClient(GdanskPortalClientOptions{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return reply(""), nil
		})})

		// When
		client.Close()
		client.Close()

		// Then
		assert.True(test, client.httpClient != nil, "injected transport must not be owned")
	})

	test.Run("should close repeatedly when transport is owned", func(test *testing.T) {
		// Given
		client := NewGdanskPortalClient(GdanskPortalClientOptions{})

		// When
		client.Close()
		client.Close()

		// Then
		assert.True(test, client.httpClient != nil, "default transport must be created once and shared")
	})
}

// -- Internals ----------------------------------------------------------------

func newGdanskTestClient(baseURL string) *GdanskPortalClient {
	client := NewGdanskPortalClient(GdanskPortalClientOptions{BaseURL: baseURL})
	client.interval = 0
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (this roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return this(request)
}

func reply(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func remapNodes(payload map[string]any) {
	changes, _ := payload["changes"].([]any)
	for _, raw := range changes {
		change := raw.(map[string]any)
		change["node"] = change["node"].(float64) + 1000
		if nodes, ok := change["addNodes"].([]any); ok {
			for index, nodeID := range nodes {
				nodes[index] = nodeID.(float64) + 1000
			}
		}
	}
}

// serveCheckFlow answers the five requests of one successful check by position within the check.
func serveCheckFlow(test *testing.T, responseWriter http.ResponseWriter, step int) {
	test.Helper()
	name := "testdata/bootstrap.html"
	if step > 0 {
		name = fmt.Sprintf("testdata/rpc%d.json", step-1)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		test.Error(err)
		responseWriter.WriteHeader(500)
		return
	}
	responseWriter.Write(data)
}

// testBootstrapObject lets regression tests alter the original fixture JSON.
func testBootstrapObject(data []byte, marker string) (map[string]any, error) {
	index := bytes.Index(data, []byte(marker))
	if index < 0 {
		return nil, errors.New("bootstrap marker missing")
	}
	var object map[string]any
	err := json.NewDecoder(bytes.NewReader(data[index+len(marker):])).Decode(&object)
	return object, err
}
