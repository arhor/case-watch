// Package portal provides administration-specific clients for checking case stages.
package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/models"
	"github.com/arhor/case-watch/internal/utils"
)

type Kind string

const (
	Authentication Kind = "authentication"
	Transport      Kind = "transport"
	Protocol       Kind = "protocol"
	Canceled       Kind = "canceled"
	Deadline       Kind = "deadline"
)

// Error contains only a stable classification, never remote text or credentials.
type Error struct {
	Kind Kind
}

// GdanskPortalClientOptions injects endpoints and dependencies for offline operation. Transport must
// perform a single exchange without retries. Nil dependencies use production defaults.
// Wait must honor cancellation; production callers should leave it nil.
type GdanskPortalClientOptions struct {
	BaseURL   string
	Transport http.RoundTripper
	Clock     utils.Clock
	Wait      func(context.Context, time.Duration) error
}

// GdanskPortalClient checks case stages through the Gdańsk administration portal.
// Each Check reuses user cookies with fresh RPC state over a shared connection
// pool and performs no automatic retries. Call Close on shutdown to release idle connections.
type GdanskPortalClient struct {
	base       string
	httpClient *utils.HttpClient
	clock      utils.Clock
	wait       func(context.Context, time.Duration) error
	interval   time.Duration
	overall    time.Duration
	perRequest time.Duration
	budget     int
}

var _ models.Portal = (*GdanskPortalClient)(nil)

// NewGdanskPortalClient creates a client with bounded requests and production defaults.
func NewGdanskPortalClient(options GdanskPortalClientOptions) *GdanskPortalClient {
	base := options.BaseURL
	if base == "" {
		base = "https://klient.gdansk.uw.gov.pl"
	}
	clock := options.Clock
	if clock == nil {
		clock = utils.NewClock()
	}
	wait := options.Wait
	if wait == nil {
		wait = waitContext
	}
	return &GdanskPortalClient{
		base:       strings.TrimRight(base, "/"),
		httpClient: utils.NewHttpClient(utils.HttpClientOptions{Transport: options.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}),
		clock:      clock,
		wait:       wait,
		interval:   2 * time.Second,
		overall:    60 * time.Second,
		perRequest: 10 * time.Second,
		budget:     6,
	}
}

// Check returns the portal's stage text and the completion time of one bounded check.
func (c *GdanskPortalClient) Check(ctx context.Context, ownerID int64, number, password string) (stage string, checkedAt time.Time, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.overall)
	defer cancel()
	defer func() { err = classify(err) }()

	defer func() {
		if err != nil {
			c.ForgetSession(ownerID)
		}
	}()
	session := c.newSession()
	session.userID = strconv.FormatInt(ownerID, 10)
	if err := session.bootstrap(ctx); err != nil {
		return "", time.Time{}, err
	}
	if _, stageErr := session.state.stage(); stageErr != nil {
		if err := session.authenticate(ctx, number, password); err != nil {
			return "", time.Time{}, err
		}
	}
	stage, err = session.state.stage()
	if err != nil {
		return "", time.Time{}, err
	}
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, err
	}
	return stage, c.clock.Now(), nil
}

// Session state belongs to a single check, never to the reusable client.
type gdanskPortalSession struct {
	config      *GdanskPortalClient
	userID      string
	lastRequest time.Time
	requests    int
	uiID        float64
	state       gdanskRPCState
}

func (c *GdanskPortalClient) newSession() *gdanskPortalSession {
	return &gdanskPortalSession{
		config: c,
		state:  gdanskRPCState{props: map[float64]map[string]any{}, children: map[float64][]float64{}},
	}
}

// Close releases idle connections of the transport created by the client. An injected
// transport belongs to the caller and is left untouched. It is idempotent and should be
// called after in-flight checks have drained.
func (c *GdanskPortalClient) Close() {
	c.httpClient.Close()
}

func (c *GdanskPortalClient) ForgetSession(ownerID int64) {
	c.httpClient.InvalidateSession(strconv.FormatInt(ownerID, 10))
}

func (s *gdanskPortalSession) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.requests >= s.config.budget {
		return nil, errors.New("request budget exhausted")
	}
	if !s.lastRequest.IsZero() {
		if err := s.config.wait(ctx, s.lastRequest.Add(s.config.interval).Sub(s.config.clock.Now())); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, err
		}
	}
	requestCtx, requestCancel := context.WithTimeout(ctx, s.config.perRequest)
	defer requestCancel()
	// A non-nil body of unknown length without GetBody makes every request, including
	// the initial GET, non-replayable, so net/http never resends it on a stale pooled connection.
	req, err := http.NewRequestWithContext(requestCtx, method, s.config.base+path, io.NopCloser(&buf))
	if err != nil {
		return nil, &Error{Kind: Protocol}
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	s.requests++
	s.lastRequest = s.config.clock.Now()
	resp, err := s.config.httpClient.Do(s.userID, req)
	if err != nil {
		if requestCtx.Err() != nil {
			return nil, requestCtx.Err()
		}
		return nil, &Error{Kind: Transport}
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, &Error{Kind: Authentication}
	}
	if resp.StatusCode != 200 {
		return nil, &Error{Kind: Transport}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		if requestCtx.Err() != nil {
			return nil, requestCtx.Err()
		}
		return nil, &Error{Kind: Transport}
	}
	if len(b) > 4<<20 {
		return nil, errors.New("portal response exceeds limit")
	}
	return b, nil
}

func (s *gdanskPortalSession) bootstrap(ctx context.Context) error {
	b, err := s.request(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return err
	}
	uidl, err := gdanskBootstrapObject(b, "var uidl =")
	if err != nil {
		return err
	}
	config, err := gdanskBootstrapObject(b, "var config =")
	if err != nil {
		return err
	}
	id, ok := config["v-uiId"].(float64)
	if !ok || !gdanskRPCInteger(id) {
		return errors.New("missing UI identifier")
	}
	s.uiID = id
	if err := s.state.apply(uidl); err != nil {
		return err
	}
	if _, err := s.state.stage(); err == nil {
		return nil
	}
	if _, _, _, err := s.state.loginComponents(); err != nil {
		return err
	}
	if s.state.token == "" {
		return errors.New("missing security token")
	}
	return nil
}

func (s *gdanskPortalSession) authenticate(ctx context.Context, number, password string) error {
	path := "/?v-r=uidl&v-uiId=" + url.QueryEscape(fmt.Sprint(s.uiID))
	for step := 0; step < 4; step++ {
		// Each reply may replace login components.
		num, pass, button, err := s.state.loginComponents()
		if err != nil {
			return err
		}
		var rpc []any
		switch step {
		case 0:
			rpc = []any{gdanskRPCEvent(pass, "value-changed")}
		case 1:
			rpc = gdanskRPCChange(num, number, "change")
		case 2:
			rpc = gdanskRPCChange(pass, password, "value-changed")
		case 3:
			rpc = []any{gdanskRPCEvent(button, "click")}
		}
		b, err := s.request(ctx, http.MethodPost, path, map[string]any{
			"csrfToken": s.state.token, "syncId": s.state.sync, "clientId": s.state.client, "rpc": rpc,
		})
		if err != nil {
			return err
		}
		if err := s.state.applyResponse(b); err != nil {
			return err
		}
	}
	return nil
}

func gdanskRPCEvent(node float64, name string) map[string]any {
	data := map[string]any{}
	if name == "click" {
		for _, key := range []string{"shiftKey", "metaKey", "ctrlKey", "altKey"} {
			data["event."+key] = false
		}
		for _, key := range []string{"detail", "clientX", "clientY", "button", "screenX", "screenY"} {
			data["event."+key] = 0
		}
	}
	return map[string]any{"type": "event", "node": node, "event": name, "data": data}
}

func gdanskRPCChange(node float64, value, event string) []any {
	return []any{
		map[string]any{"type": "mSync", "node": node, "feature": 1, "property": "value", "value": value},
		gdanskRPCEvent(node, event),
	}
}

type gdanskRPCState struct {
	props        map[float64]map[string]any
	children     map[float64][]float64
	sync, client any
	token        string
}

func (s *gdanskRPCState) applyResponse(b []byte) error {
	var messages []map[string]any
	if err := json.Unmarshal(bytes.TrimPrefix(b, []byte("for(;;);")), &messages); err != nil || len(messages) == 0 {
		return errors.New("invalid RPC response")
	}
	for _, message := range messages {
		if err := s.apply(message); err != nil {
			return err
		}
	}
	return nil
}

func (s *gdanskRPCState) loginComponents() (number, password, button float64, err error) {
	number, err = s.find("vaadin-text-field", "Number of application")
	if err != nil {
		return
	}
	password, err = s.find("vaadin-password-field", "Password")
	if err != nil {
		return
	}
	button, err = s.find("vaadin-button", "")
	return
}

func gdanskBootstrapObject(b []byte, marker string) (map[string]any, error) {
	i := bytes.Index(b, []byte(marker))
	if i < 0 {
		return nil, errors.New("bootstrap marker missing")
	}
	var v map[string]any
	err := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(b[i+len(marker):]))).Decode(&v)
	return v, err
}

func (s *gdanskRPCState) apply(v map[string]any) error {
	sync, sok := v["syncId"].(float64)
	client, cok := v["clientId"].(float64)
	if !sok || !cok || !gdanskRPCInteger(sync) || !gdanskRPCInteger(client) {
		return errors.New("invalid RPC synchronization state")
	}
	s.sync, s.client = sync, client
	if t, ok := v["Vaadin-Security-Key"].(string); ok {
		s.token = t
	}
	rawChanges, exists := v["changes"]
	if !exists {
		return nil
	}
	cs, ok := rawChanges.([]any)
	if !ok {
		return errors.New("invalid RPC changes")
	}
	for _, raw := range cs {
		c, ok := raw.(map[string]any)
		if !ok {
			return errors.New("invalid RPC change")
		}
		n, ok := c["node"].(float64)
		if !ok || !gdanskRPCInteger(n) {
			return errors.New("invalid RPC node")
		}
		if s.props[n] == nil {
			s.props[n] = map[string]any{}
		}
		feat, _ := c["feat"].(float64)
		switch c["type"] {
		case "attach":
		case "put":
			k, ok := c["key"].(string)
			if !ok {
				return errors.New("invalid property key")
			}
			// Only semantic properties participate in discovery; styles and listeners must not overwrite them.
			if feat == 0 || feat == 1 || feat == 7 {
				s.props[n][k] = c["value"]
			}
		case "remove":
			if feat == 0 || feat == 1 || feat == 7 {
				k, _ := c["key"].(string)
				delete(s.props[n], k)
			}
		case "detach":
			delete(s.props, n)
			delete(s.children, n)
		case "clear", "empty":
			if feat == 2 {
				delete(s.children, n)
			}
		case "splice":
			if feat != 2 {
				continue
			}
			old := s.children[n]
			i, ok := c["index"].(float64)
			if !ok || !gdanskRPCInteger(i) {
				return errors.New("invalid child index")
			}
			r, _ := c["remove"].(float64)
			if !gdanskRPCInteger(r) || i > float64(len(old)) || i+r > float64(len(old)) {
				return errors.New("invalid child splice")
			}
			next := append([]float64{}, old[:int(i)]...)
			if raw, exists := c["addNodes"]; exists {
				added, ok := raw.([]any)
				if !ok {
					return errors.New("invalid child nodes")
				}
				for _, x := range added {
					f, ok := x.(float64)
					if !ok || !gdanskRPCInteger(f) {
						return errors.New("invalid child node")
					}
					next = append(next, f)
				}
			}
			s.children[n] = append(next, old[int(i+r):]...)
		default:
			return errors.New("unsupported RPC change")
		}
	}
	return nil
}

func (s *gdanskRPCState) find(tag, label string) (float64, error) {
	var found []float64
	for n, p := range s.props {
		if p["tag"] == tag && (label == "" || p["label"] == label) {
			if tag == "vaadin-button" {
				matches := false
				for _, child := range s.children[n] {
					if s.props[child]["text"] == "Log in" {
						matches = true
					}
				}
				if !matches {
					continue
				}
			}
			found = append(found, n)
		}
	}
	if len(found) != 1 {
		return 0, errors.New("missing or ambiguous login component")
	}
	return found[0], nil
}

func (s *gdanskRPCState) stage() (string, error) {
	// Locate the field in the same row as the stage caption.
	var contains func(float64, string, map[float64]bool) bool
	contains = func(n float64, text string, seen map[float64]bool) bool {
		if seen[n] {
			return false
		}
		seen[n] = true
		p := s.props[n]
		if p["text"] == text || p["nodeValue"] == text {
			return true
		}
		for _, ch := range s.children[n] {
			if contains(ch, text, seen) {
				return true
			}
		}
		return false
	}
	for n := range s.children {
		if !contains(n, "Stage of your case", map[float64]bool{}) {
			continue
		}
		var values []string
		var walk func(float64, map[float64]bool)
		walk = func(id float64, seen map[float64]bool) {
			if seen[id] {
				return
			}
			seen[id] = true
			p := s.props[id]
			if p["tag"] == "vaadin-text-field" {
				if v, ok := p["value"].(string); ok && strings.TrimSpace(v) != "" {
					values = append(values, v)
				}
			}
			for _, ch := range s.children[id] {
				walk(ch, seen)
			}
		}
		walk(n, map[float64]bool{})
		if len(values) == 1 {
			return values[0], nil
		}
	}
	if _, err := s.find("vaadin-password-field", "Password"); err == nil {
		return "", &Error{Kind: Authentication}
	}
	return "", &Error{Kind: Protocol}
}

func gdanskRPCInteger(v float64) bool {
	return v >= 0 && v <= 1<<53-1 && math.Trunc(v) == v
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (e *Error) Error() string {
	return "portal check failed: " + string(e.Kind)
}

func (e *Error) Is(target error) bool {
	return e.Kind == Canceled && target == context.Canceled || e.Kind == Deadline && target == context.DeadlineExceeded
}

func classify(err error) error {
	if err == nil {
		return nil
	}
	var safe *Error
	if errors.As(err, &safe) {
		return safe
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Kind: Canceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: Deadline}
	}
	return &Error{Kind: Protocol}
}
