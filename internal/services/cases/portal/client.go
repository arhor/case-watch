package portal

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/domain"
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

// Options injects endpoints and dependencies for offline operation. Transport must
// perform a single exchange without retries. Nil dependencies use production defaults.
// Wait must honor cancellation; production callers should leave it nil.
type Options struct {
	BaseURL   string
	Transport http.RoundTripper
	Clock     utils.Clock
	Wait      func(context.Context, time.Duration) error
}

type Client struct {
	base       string
	transport  http.RoundTripper
	clock      utils.Clock
	wait       func(context.Context, time.Duration) error
	interval   time.Duration
	overall    time.Duration
	perRequest time.Duration
	budget     int
}

var _ domain.Portal = (*Client)(nil)

func (e *Error) Error() string {
	return "portal check failed: " + string(e.Kind)
}

func (e *Error) Is(target error) bool {
	return e.Kind == Canceled && target == context.Canceled || e.Kind == Deadline && target == context.DeadlineExceeded
}

func NewClient(options Options) *Client {
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
	return &Client{
		base:       strings.TrimRight(base, "/"),
		transport:  options.Transport,
		clock:      clock,
		wait:       wait,
		interval:   2 * time.Second,
		overall:    60 * time.Second,
		perRequest: 10 * time.Second,
		budget:     6,
	}
}

func (c *Client) Check(ctx context.Context, Number, Password string) (string, time.Time, error) {
	result, err := c.run(ctx, Number, Password)
	if err != nil {
		return "", time.Time{}, err
	}
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, classify(err)
	}
	return result.Stage, c.clock.Now(), nil
}

// -- Internal implementation ---------------------------------------------------------------------

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
