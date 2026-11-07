// Package telegram owns communication with the Telegram Bot API.
package telegram

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

var (
	ErrInvalidToken = errors.New("telegram token rejected")
	errPolling      = errors.New("telegram polling already started or missing handler")
)

type Config struct {
	Token      string
	Endpoint   string
	HTTPClient *http.Client
	Diagnostic func(string)
}

type Client struct {
	polling    atomic.Bool
	bot        *bot.Bot
	diagnostic func(string)
}

func New(c Config) (*Client, error) {
	client := &Client{diagnostic: c.Diagnostic}
	opts := []bot.Option{
		bot.WithSkipGetMe(),
		bot.WithNotAsyncHandlers(),
		bot.WithAllowedUpdates(bot.AllowedUpdates{"message", "my_chat_member"}),
		bot.WithErrorsHandler(func(err error) {
			if !client.tokenRejected(err) {
				client.report("telegram polling failure")
			}
		}),
	}
	if c.Endpoint != "" {
		opts = append(opts, bot.WithServerURL(c.Endpoint))
	}
	if c.HTTPClient != nil {
		opts = append(opts, bot.WithHTTPClient(30*time.Second, c.HTTPClient))
	}
	b, err := bot.New(c.Token, opts...)
	if err != nil {
		return nil, errors.New("invalid Telegram configuration")
	}
	client.bot = b
	return client, nil
}

func (c *Client) report(category string) {
	if c.diagnostic != nil {
		c.diagnostic(category)
	}
}

func (c *Client) tokenRejected(err error) bool {
	if errors.Is(err, bot.ErrorUnauthorized) {
		c.report("telegram_invalid_token")
		return true
	}
	return false
}

func (c *Client) Validate(ctx context.Context) error {
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := c.bot.GetMe(bounded); err != nil {
		if c.tokenRejected(err) {
			return ErrInvalidToken
		}
		c.report("telegram validation failure")
		return errors.New("telegram validation failed")
	}
	return nil
}

// Poll binds the intake handler before starting ordered library polling. A client
// supports one polling lifetime; raw sends and deletion use the same bot.
func (c *Client) Poll(ctx context.Context, handle func(context.Context, *models.Update)) error {
	if handle == nil || !c.polling.CompareAndSwap(false, true) {
		return errPolling
	}
	c.bot.RegisterHandlerMatchFunc(func(*models.Update) bool { return true },
		func(ctx context.Context, _ *bot.Bot, update *models.Update) { handle(ctx, update) })
	c.bot.Start(ctx)
	return ctx.Err()
}

func (c *Client) DeleteMessage(ctx context.Context, chatID int64, messageID int) error {
	_, err := c.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chatID, MessageID: messageID})
	if err == nil {
		return nil
	}
	if c.tokenRejected(err) {
		return ErrInvalidToken
	}
	return errors.New("telegram message deletion failed")
}

// Send makes exactly one API call and returns a sanitized outcome.
func (c *Client) Send(ctx context.Context, id int64, text string) (domain.SendOutcome, error) {
	m, err := c.bot.SendMessage(ctx, &bot.SendMessageParams{ChatID: id, Text: text})
	if err == nil {
		return domain.SendOutcome{Category: domain.SendAccepted, MessageID: int64(m.ID)}, nil
	}
	out := domain.SendOutcome{Category: domain.SendTransient}
	var rate *bot.TooManyRequestsError
	switch {
	case errors.As(err, &rate):
		out.Category = domain.SendRateLimited
		out.RetryAfter = time.Duration(rate.RetryAfter) * time.Second
	case errors.Is(err, bot.ErrorUnauthorized):
		out.Category = domain.SendInvalidToken
	case errors.Is(err, bot.ErrorForbidden):
		out.Category = domain.SendPermanent
		if strings.Contains(strings.ToLower(err.Error()), "bot was blocked by the user") {
			out.Category = domain.SendBlocked
		}
	case errors.Is(err, bot.ErrorBadRequest), errors.Is(err, bot.ErrorNotFound):
		out.Category = domain.SendPermanent
	}
	return out, errors.New("telegram send failed")
}
