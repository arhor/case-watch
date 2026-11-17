package httpclient

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestHttpClientDo(test *testing.T) {
	test.Run("should isolate cookies when users share transport", func(test *testing.T) {
		// Given
		var cookies []string
		client := NewHttpClient(HttpClientOptions{
			Transport: sessionTransport(func(request *http.Request) (*http.Response, error) {
				cookies = append(cookies, request.Header.Get("Cookie"))
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Set-Cookie": []string{"session=value; Path=/"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			}),
		})
		test.Cleanup(client.Close)

		// When
		for _, sessionID := range []string{"session-1", "session-1", "session-2"} {
			request, err := http.NewRequest(http.MethodGet, "https://example.test/", nil)
			assert.NoError(test, err)
			response, err := client.Do(sessionID, request)
			assert.NoError(test, err)
			assert.NoError(test, response.Body.Close())
		}

		// Then
		assert.DeepEqual(test, []string{"", "session=value", ""}, cookies, "each session should retain only its own cookies")
	})
}

func TestHttpClientInvalidateSession(test *testing.T) {
	test.Run("should discard cookies when session is invalidated", func(test *testing.T) {
		// Given
		var cookies []string
		client := NewHttpClient(HttpClientOptions{
			Transport: sessionTransport(func(request *http.Request) (*http.Response, error) {
				cookies = append(cookies, request.Header.Get("Cookie"))
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Set-Cookie": []string{"session=value; Path=/"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			}),
		})
		test.Cleanup(client.Close)
		request, err := http.NewRequest(http.MethodGet, "https://example.test/", nil)
		assert.NoError(test, err)
		response, err := client.Do("session-1", request)
		assert.NoError(test, err)
		assert.NoError(test, response.Body.Close())

		// When
		client.InvalidateSession("session-1")

		// Then
		request, err = http.NewRequest(http.MethodGet, "https://example.test/", nil)
		assert.NoError(test, err)
		response, err = client.Do("session-1", request)
		assert.NoError(test, err)
		assert.NoError(test, response.Body.Close())
		assert.DeepEqual(test, []string{"", ""}, cookies, "invalidated session should start without previous cookies")
	})
}

// -- Internals ----------------------------------------------------------------

type sessionTransport func(*http.Request) (*http.Response, error)

func (this sessionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return this(request)
}
