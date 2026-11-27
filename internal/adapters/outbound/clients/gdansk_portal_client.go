package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/domain"
	realclock "github.com/arhor/case-watch/internal/platform/clock"
	"github.com/arhor/case-watch/internal/platform/httpclient"
	"github.com/arhor/case-watch/internal/platform/vaadin"
)

const (
	Authentication Kind = "authentication"
	Transport      Kind = "transport"
	Protocol       Kind = "protocol"
	Canceled       Kind = "canceled"
	Deadline       Kind = "deadline"
)

// Session state belongs to a single check, never to the reusable client.
type Kind string

// Error contains only a stable classification, never remote text or credentials.
type Error struct {
	Kind Kind
}

func (this *Error) Error() string {
	return "portal check failed: " + string(this.Kind)
}

func (this *Error) Is(target error) bool {
	return this.Kind == Canceled && target == context.Canceled || this.Kind == Deadline && target == context.DeadlineExceeded
}

// GdanskPortalClientOptions injects endpoints and dependencies for offline operation. Transport must
// perform a single exchange without retries. Nil dependencies use production defaults.
// Wait must honor cancellation; production callers should leave it nil.
type GdanskPortalClientOptions struct {
	BaseURL   string
	Transport http.RoundTripper
	Clock     domain.Clock
	Wait      func(context.Context, time.Duration) error
}

var _ domain.Portal = (*GdanskPortalClient)(nil)

// GdanskPortalClient checks case stages through the Gdańsk administration portal.
// Each Check reuses user cookies with fresh RPC state over a shared connection
// pool and performs no automatic retries. Call Close on shutdown to release idle connections.
type GdanskPortalClient struct {
	base       string
	httpClient *httpclient.HttpClient
	clock      domain.Clock
	wait       func(context.Context, time.Duration) error
	interval   time.Duration
	overall    time.Duration
	perRequest time.Duration
	budget     int
}

// NewGdanskPortalClient creates a client with bounded requests and production defaults.
func NewGdanskPortalClient(options GdanskPortalClientOptions) *GdanskPortalClient {
	base := options.BaseURL
	if base == "" {
		base = "https://klient.gdansk.uw.gov.pl"
	}
	clock := options.Clock
	if clock == nil {
		clock = realclock.NewClock()
	}
	wait := options.Wait
	if wait == nil {
		wait = waitContext
	}
	return &GdanskPortalClient{
		base:       strings.TrimRight(base, "/"),
		httpClient: httpclient.NewHttpClient(httpclient.HttpClientOptions{Transport: options.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}),
		clock:      clock,
		wait:       wait,
		interval:   2 * time.Second,
		overall:    60 * time.Second,
		perRequest: 10 * time.Second,
		budget:     6,
	}
}

// Check returns the portal's stage text and the completion time of one bounded check.
func (this *GdanskPortalClient) Check(ctx context.Context, ownerID int64, number, password string) (snapshot domain.PortalSnapshot, err error) {
	ctx, cancel := context.WithTimeout(ctx, this.overall)
	defer cancel()
	defer func() { err = classify(err) }()

	defer func() {
		if err != nil {
			this.ForgetSession(ownerID)
		}
	}()
	session := this.newSession()
	session.userID = strconv.FormatInt(ownerID, 10)
	if err := session.bootstrap(ctx); err != nil {
		return domain.PortalSnapshot{}, err
	}
	if _, stageErr := session.state.stage(); stageErr != nil {
		if err := session.authenticate(ctx, number, password); err != nil {
			return domain.PortalSnapshot{}, err
		}
	}
	stage, err := session.state.stage()
	if err != nil {
		return domain.PortalSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.PortalSnapshot{}, err
	}
	html := ""
	roots, selectionErr := selectGdanskCaseContent(session.state.tree, stage)
	if selectionErr != nil {
		log.Print("portal HTML extraction unavailable: selection")
	} else if len(roots) > 0 {
		rendered, renderErr := vaadin.RenderHTML(session.state.tree, roots...)
		if renderErr != nil {
			log.Print("portal HTML extraction unavailable: rendering")
		} else if strings.TrimSpace(rendered) == "" {
			log.Print("portal HTML extraction unavailable: empty output")
		} else {
			html = rendered
		}
	}
	if err := ctx.Err(); err != nil {
		return domain.PortalSnapshot{}, err
	}
	return domain.PortalSnapshot{Stage: stage, HTML: html, CheckedAt: this.clock.Now()}, nil
}

// Close releases idle connections of the transport created by the client. An injected
// transport belongs to the caller and is left untouched. It is idempotent and should be
// called after in-flight checks have drained.
func (this *GdanskPortalClient) Close() {
	this.httpClient.Close()
}

func (this *GdanskPortalClient) ForgetSession(ownerID int64) {
	this.httpClient.InvalidateSession(strconv.FormatInt(ownerID, 10))
}

// -- Internals ----------------------------------------------------------------
type gdanskPortalSession struct {
	config      *GdanskPortalClient
	userID      string
	lastRequest time.Time
	requests    int
	uiID        uint64
	state       gdanskRPCState
}

type gdanskRPCState struct {
	tree         *vaadin.StateTree
	sync, client uint64
	token        string
}

func (this *GdanskPortalClient) newSession() *gdanskPortalSession {
	return &gdanskPortalSession{config: this, state: gdanskRPCState{tree: vaadin.NewStateTree()}}
}

func (this *gdanskPortalSession) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if this.requests >= this.config.budget {
		return nil, errors.New("request budget exhausted")
	}
	if !this.lastRequest.IsZero() {
		if err := this.config.wait(ctx, this.lastRequest.Add(this.config.interval).Sub(this.config.clock.Now())); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, err
		}
	}
	requestCtx, requestCancel := context.WithTimeout(ctx, this.config.perRequest)
	defer requestCancel()
	// A non-nil body of unknown length without GetBody makes every request, including
	// the initial GET, non-replayable, so net/http never resends it on a stale pooled connection.
	req, err := http.NewRequestWithContext(requestCtx, method, this.config.base+path, io.NopCloser(&buf))
	if err != nil {
		return nil, &Error{Kind: Protocol}
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	this.requests++
	this.lastRequest = this.config.clock.Now()
	resp, err := this.config.httpClient.Do(this.userID, req)
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

func (this *gdanskPortalSession) bootstrap(ctx context.Context) error {
	b, err := this.request(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return err
	}
	bootstrap, err := vaadin.DecodeBootstrap(b)
	if err != nil {
		return err
	}
	this.uiID = bootstrap.UIID
	if err := this.state.apply(bootstrap.Message); err != nil {
		return err
	}
	if _, err := this.state.stage(); err == nil {
		return nil
	}
	if _, _, _, err := this.state.loginComponents(); err != nil {
		return err
	}
	if this.state.token == "" {
		return errors.New("missing security token")
	}
	return nil
}

func (this *gdanskPortalSession) authenticate(ctx context.Context, number, password string) error {
	path := "/?v-r=uidl&v-uiId=" + url.QueryEscape(fmt.Sprint(this.uiID))
	for step := 0; step < 4; step++ {
		// Each reply may replace login components.
		num, pass, button, err := this.state.loginComponents()
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
		b, err := this.request(ctx, http.MethodPost, path, map[string]any{
			"csrfToken": this.state.token, "syncId": this.state.sync, "clientId": this.state.client, "rpc": rpc,
		})
		if err != nil {
			return err
		}
		messages, err := vaadin.DecodeResponse(b)
		if err != nil {
			return err
		}
		for _, message := range messages {
			if err := this.state.apply(message); err != nil {
				return err
			}
		}
	}
	return nil
}

func gdanskRPCEvent(node vaadin.NodeID, name string) map[string]any {
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

func gdanskRPCChange(node vaadin.NodeID, value, event string) []any {
	return []any{
		map[string]any{"type": "mSync", "node": node, "feature": vaadin.PropertiesFeature, "property": "value", "value": value},
		gdanskRPCEvent(node, event),
	}
}

func (this *gdanskRPCState) apply(message vaadin.Message) error {
	if err := this.tree.Apply(message.Changes); err != nil {
		return err
	}
	this.sync, this.client = message.SyncID, message.ClientID
	if message.SecurityKey != nil {
		this.token = *message.SecurityKey
	}
	return nil
}

func (this *gdanskRPCState) loginComponents() (number, password, button vaadin.NodeID, err error) {
	number, err = this.find("vaadin-text-field", "Number of application")
	if err != nil {
		return
	}
	password, err = this.find("vaadin-password-field", "Password")
	if err != nil {
		return
	}
	button, err = this.find("vaadin-button", "")
	return
}

func (this *gdanskRPCState) find(tag, label string) (vaadin.NodeID, error) {
	var found []vaadin.NodeID
	for _, node := range this.tree.Nodes() {
		property, _ := node.Property(vaadin.PropertiesFeature, "label")
		if node.Tag() != tag || (label != "" && property != label) {
			continue
		}
		if tag == "vaadin-button" {
			matches := false
			for _, child := range this.tree.Children(node.ID()) {
				if gdanskNodeHasText(child, "Log in") {
					matches = true
				}
			}
			if !matches {
				continue
			}
		}
		found = append(found, node.ID())
	}
	if len(found) != 1 {
		return 0, errors.New("missing or ambiguous login component")
	}
	return found[0], nil
}

func (this *gdanskRPCState) stage() (string, error) {
	// Portal-specific stage semantics stay outside the generic state tree.
	for _, node := range this.tree.Nodes() {
		if len(node.ChildIDs()) == 0 {
			continue
		}
		containsCaption := false
		var values []string
		for _, descendant := range this.tree.Subtree(node.ID()) {
			if gdanskNodeHasText(descendant, "Stage of your case") {
				containsCaption = true
			}
			if descendant.Tag() == "vaadin-text-field" {
				property, _ := descendant.Property(vaadin.PropertiesFeature, "value")
				if value, ok := property.(string); ok && strings.TrimSpace(value) != "" {
					values = append(values, value)
				}
			}
		}
		if containsCaption && len(values) == 1 {
			return values[0], nil
		}
	}
	if _, err := this.find("vaadin-password-field", "Password"); err == nil {
		return "", &Error{Kind: Authentication}
	}
	return "", &Error{Kind: Protocol}
}

// Keep the previous adapter's text/nodeValue compatibility across semantic
// features without flattening properties in the generic state representation.
func gdanskNodeHasText(node vaadin.Node, text string) bool {
	for _, feature := range []vaadin.FeatureID{vaadin.ElementFeature, vaadin.PropertiesFeature, vaadin.TextFeature} {
		for _, key := range []string{"text", "nodeValue"} {
			value, _ := node.Property(feature, key)
			if value == text {
				return true
			}
		}
	}
	return false
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

func classify(err error) error {
	if err == nil {
		return nil
	}
	if safe, ok := errors.AsType[*Error](err); ok {
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
