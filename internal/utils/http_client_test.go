package utils

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type sessionTransport func(*http.Request) (*http.Response, error)

func (this sessionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return this(r)
}

func TestHttpClientShouldIsolateCookiesWhenUsersShareTransport(t *testing.T) {
	// Given
	var cookies []string
	session1ID := "session-1"
	session2ID := "session-2"

	client := NewHttpClient(HttpClientOptions{
		Transport: sessionTransport(func(r *http.Request) (*http.Response, error) {
			cookies = append(cookies, r.Header.Get("Cookie"))
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Set-Cookie": []string{"session=value; Path=/"}},
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		}),
	})
	defer client.Close()

	call := func(sessionID string) {
		req, _ := http.NewRequest("GET", "https://example.test/", nil)
		res, err := client.Do(sessionID, req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}

	// When
	for _, sessionID := range []string{
		session1ID,
		session1ID,
		session2ID,
	} {
		call(sessionID)
	}
	client.InvalidateSession(session1ID)

	call(session1ID)

	// Then
	if len(cookies) != 4 || cookies[0] != "" || cookies[1] != "session=value" || cookies[2] != "" || cookies[3] != "" {
		t.Fatalf("cookies=%v", cookies)
	}
}
