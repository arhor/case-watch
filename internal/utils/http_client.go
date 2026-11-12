package utils

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"time"
)

// ------------------------------------------------------------------------------------------------
//                                        HTTP Session
// ------------------------------------------------------------------------------------------------

// Session owns the cookies of one session and is safe for concurrent use.
type Session struct {
	jar *cookiejar.Jar
}

func (this *Session) Cookies(u *url.URL) []*http.Cookie             { return this.jar.Cookies(u) }
func (this *Session) SetCookies(u *url.URL, cookies []*http.Cookie) { this.jar.SetCookies(u, cookies) }

// ------------------------------------------------------------------------------------------------
//                                  HTTP Session Manager
// ------------------------------------------------------------------------------------------------

type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewSessionManager() *SessionManager { return &SessionManager{} }

func (this *SessionManager) GetOrCreate(sessionID string) (*Session, error) {
	if sessionID == "" {
		return nil, errors.New("empty HTTP session ID")
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if s := this.sessions[sessionID]; s != nil {
		return s, nil
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	s := &Session{jar: jar}
	if this.sessions == nil {
		this.sessions = make(map[string]*Session)
	}
	this.sessions[sessionID] = s
	return s, nil
}

// Invalidate detaches a session without canceling requests already using it.
func (this *SessionManager) Invalidate(sessionID string) {
	this.mu.Lock()
	defer this.mu.Unlock()
	delete(this.sessions, sessionID)
}

// Clear detaches all sessions without canceling requests already using them.
func (this *SessionManager) Clear() {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.sessions = nil
}

// ------------------------------------------------------------------------------------------------
//                                          HTTP Client
// ------------------------------------------------------------------------------------------------

type HttpClientOptions struct {
	Transport     http.RoundTripper
	Timeout       time.Duration
	CheckRedirect func(*http.Request, []*http.Request) error
}

// HttpClient shares a connection pool while isolating cookies by session.
type HttpClient struct {
	transport     http.RoundTripper
	timeout       time.Duration
	checkRedirect func(*http.Request, []*http.Request) error
	sessions      *SessionManager
	owned         *http.Transport
}

func NewHttpClient(options HttpClientOptions) *HttpClient {
	c := &HttpClient{
		transport:     options.Transport,
		timeout:       options.Timeout,
		checkRedirect: options.CheckRedirect,
		sessions:      NewSessionManager(),
	}
	if c.transport == nil {
		c.owned = http.DefaultTransport.(*http.Transport).Clone()
		c.transport = c.owned
	}
	return c
}

// Do follows net/http semantics; callers must close the response body.
func (this *HttpClient) Do(sessionID string, req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	session, err := this.sessions.GetOrCreate(sessionID)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport:     this.transport,
		Timeout:       this.timeout,
		CheckRedirect: this.checkRedirect,
		Jar:           session,
	}
	return client.Do(req)
}

func (this *HttpClient) InvalidateSession(sessionID string) {
	this.sessions.Invalidate(sessionID)
}

// Close is idempotent. Drain requests before closing; do not reuse afterward.
func (this *HttpClient) Close() {
	this.sessions.Clear()
	if this.owned != nil {
		this.owned.CloseIdleConnections()
	}
}
