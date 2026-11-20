package httpclient

import (
	"net/http"
	"testing"

	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestHttpClientDo(test *testing.T) {
	test.Run("should isolate cookies when users share transport", func(test *testing.T) {
		// Given
		var sentCookies []string
		client := newCookieRecordingClient(test, &sentCookies)
		firstRequest := newSessionRequest(test)
		repeatedRequest := newSessionRequest(test)
		otherSessionRequest := newSessionRequest(test)

		// When
		firstResponse, firstError := client.Do("session-1", firstRequest)
		repeatedResponse, repeatedError := client.Do("session-1", repeatedRequest)
		otherResponse, otherError := client.Do("session-2", otherSessionRequest)
		cleanupResponses(test, firstResponse, repeatedResponse, otherResponse)

		// Then
		assert.NoError(test, firstError)
		assert.NoError(test, repeatedError)
		assert.NoError(test, otherError)
		assert.DeepEqual(test, []string{"", "session=value", ""}, sentCookies, "each session should retain only its own cookies")
	})
}

func TestHttpClientInvalidateSession(test *testing.T) {
	test.Run("should discard cookies when session is invalidated", func(test *testing.T) {
		// Given
		var sentCookies []string
		client := newCookieRecordingClient(test, &sentCookies)
		initialRequest := newSessionRequest(test)
		requestAfterInvalidation := newSessionRequest(test)
		initialResponse, setupError := client.Do("session-1", initialRequest)
		assert.NoError(test, setupError)
		cleanupResponses(test, initialResponse)

		// When
		client.InvalidateSession("session-1")
		response, actualError := client.Do("session-1", requestAfterInvalidation)
		cleanupResponses(test, response)

		// Then
		assert.NoError(test, actualError)
		assert.DeepEqual(test, []string{"", ""}, sentCookies, "invalidated session should start without previous cookies")
	})
}

// -- Internals ----------------------------------------------------------------

func newCookieRecordingClient(test *testing.T, sentCookies *[]string) *HttpClient {
	test.Helper()

	client := NewHttpClient(HttpClientOptions{
		Transport: sessionTransport(func(request *http.Request) (*http.Response, error) {
			*sentCookies = append(*sentCookies, request.Header.Get("Cookie"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Set-Cookie": []string{"session=value; Path=/"}},
				Body:       http.NoBody,
			}, nil
		}),
	})
	test.Cleanup(client.Close)
	return client
}

func newSessionRequest(test *testing.T) *http.Request {
	test.Helper()

	request, err := http.NewRequest(http.MethodGet, "https://example.test/", nil)
	assert.NoError(test, err)
	return request
}

func cleanupResponses(test *testing.T, responses ...*http.Response) {
	test.Helper()

	test.Cleanup(func() {
		for _, response := range responses {
			if response != nil {
				assert.NoError(test, response.Body.Close())
			}
		}
	})
}

type sessionTransport func(*http.Request) (*http.Response, error)

func (this sessionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return this(request)
}
