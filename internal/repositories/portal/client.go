package portal

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/domain"
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
type Error struct{ Kind Kind }

func (e *Error) Error() string { return "portal check failed: " + string(e.Kind) }
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

// Options injects endpoints and dependencies for offline operation. Transport must
// perform a single exchange without retries. Nil dependencies use production defaults.
// Wait must honor cancellation; production callers should leave it nil.
type Options struct {
	BaseURL   string
	Transport http.RoundTripper
	Now       func() time.Time
	Wait      func(context.Context, time.Duration) error
}

type Client struct {
	base                          string
	transport                     http.RoundTripper
	now                           func() time.Time
	wait                          func(context.Context, time.Duration) error
	interval, overall, perRequest time.Duration
	budget                        int
}

var _ domain.Portal = (*Client)(nil)

func NewClient(options Options) *Client {
	base := options.BaseURL
	if base == "" {
		base = "https://klient.gdansk.uw.gov.pl"
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	wait := options.Wait
	if wait == nil {
		wait = waitContext
	}
	return &Client{base: strings.TrimRight(base, "/"), transport: options.Transport, now: now, wait: wait, interval: 2 * time.Second, overall: 60 * time.Second, perRequest: 10 * time.Second, budget: 6}
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
func (c *Client) Check(ctx context.Context, credentials domain.Credentials) (domain.Observation, error) {
	result, err := c.run(ctx, credentials.Number, credentials.Password)
	if err != nil {
		return domain.Observation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Observation{}, classify(err)
	}
	return domain.Observation{Stage: result.Stage, CheckedAt: c.now()}, nil
}
