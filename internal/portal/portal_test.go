package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestProbeRejectsUnexpectedBootstrap(t *testing.T) {
	// Given a portal serving an unfamiliar page.
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte("<html>unavailable</html>")) }))
	defer server.Close()
	// When probing it.
	result, err := Probe(context.Background(), server.URL, "synthetic-case", "synthetic-password")
	// Then it stops after the bootstrap without submitting credentials.
	if err == nil || calls != 1 || result.Requests != 1 {
		t.Fatalf("expected one request and an error; result=%+v error=%v calls=%d", result, err, calls)
	}
}

func TestProbeDoesNotFollowRedirect(t *testing.T) {
	// Given a redirecting portal.
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "/other")
		w.WriteHeader(302)
	}))
	defer server.Close()
	// When probing it.
	_, err := Probe(context.Background(), server.URL, "synthetic-case", "synthetic-password")
	// Then the redirect does not consume extra requests.
	if err == nil || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestProbeAuthenticatesUsingFreshSessionAndExtractsStage(t *testing.T) {
	// Given the sanitized recorded portal sequence and a fresh session.
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
				t.Fatal(err)
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
			t.Fatal(err)
		}
		w.Write(b)
	}))
	defer server.Close()
	// When one bounded authentication attempt is made.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := Probe(ctx, server.URL, "synthetic-case", "synthetic-password")
	// Then it extracts the synthetic stage using five requests.
	if err != nil || result.Stage != "Synthetic stage" || result.Requests != 5 || calls != 5 {
		t.Fatalf("result=%+v error=%v calls=%d", result, err, calls)
	}
}

func TestProbeRejectsMalformedRPCWithoutPanic(t *testing.T) {
	// Given a normal bootstrap followed by malformed changes.
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
	// When the first event is submitted.
	result, err := Probe(context.Background(), server.URL, "synthetic-case", "synthetic-password")
	// Then it fails without retrying.
	if err == nil || result.Requests != 2 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestProbeHonorsCanceledContext(t *testing.T) {
	// Given an already canceled operation.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// When a probe starts.
	_, err := Probe(ctx, "http://127.0.0.1:1", "synthetic-case", "synthetic-password")
	// Then it returns an error.
	if err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestProbeStopsOnRejectedOrUnexpectedReplies(t *testing.T) {
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
			// Given a fresh bootstrap and an unsuccessful RPC conversation.
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
			// When the bounded probe runs without test delays.
			result, err := probe(context.Background(), server.URL, "synthetic-case", "synthetic-password", 0, tc.budget)
			// Then no false stage or unbounded retries are produced.
			if err == nil || result.Stage != "" || calls != tc.wantRequests {
				t.Fatalf("result=%+v error=%v calls=%d", result, err, calls)
			}
		})
	}
}

func TestProbeDiscoversChangedNodeIdentifiers(t *testing.T) {
	// Given the same recorded UI with entirely different node IDs.
	calls := 0
	var expected []float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := calls
		calls++
		name := "testdata/bootstrap.html"
		if index > 0 {
			name = fmt.Sprintf("testdata/rpc%d.json", index-1)
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			u, _ := object(b, "var uidl =")
			remapNodes(u)
			for _, raw := range u["changes"].([]any) {
				c := raw.(map[string]any)
				if c["key"] == "tag" && (c["value"] == "vaadin-password-field" || c["value"] == "vaadin-text-field") {
					expected = append(expected, c["node"].(float64))
				}
			}
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
	// When authentication runs.
	result, err := probe(context.Background(), server.URL, "synthetic-case", "synthetic-password", 0, 6)
	// Then stage extraction still succeeds.
	if err != nil || result.Stage != "Synthetic stage" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
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

func TestProbeStopsWhenDeadlineExpires(t *testing.T) {
	// Given a portal slower than the caller's deadline.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// When a probe starts.
	result, err := Probe(ctx, server.URL, "synthetic-case", "synthetic-password")
	// Then it stops without retrying.
	if !errors.Is(err, context.DeadlineExceeded) || result.Requests != 1 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestProbeRejectsMissingOrAmbiguousLoginFields(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		// Given a bootstrap with a missing or ambiguous password component.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := os.ReadFile("testdata/bootstrap.html")
			u, _ := object(b, "var uidl =")
			changes := u["changes"].([]any)
			for _, raw := range changes {
				c := raw.(map[string]any)
				if c["key"] == "tag" && c["value"] == "vaadin-password-field" {
					if ambiguous {

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
		// When discovery runs, then it stops before submitting credentials.
		result, err := Probe(context.Background(), server.URL, "synthetic-case", "synthetic-password")
		server.Close()
		if err == nil || result.Requests != 1 {
			t.Fatalf("result=%+v error=%v", result, err)
		}
	}
}
