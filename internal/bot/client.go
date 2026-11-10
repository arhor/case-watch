package bot

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	telegramApi "github.com/go-telegram/bot"
	telegramModels "github.com/go-telegram/bot/models"
)

var (
	ErrInvalidToken = errors.New("telegram token rejected")
	errPolling      = errors.New("telegram polling already started or missing handler")
)

type ClientConfig struct {
	Token      string
	Endpoint   string
	HTTPClient *http.Client
	Diagnostic func(string)
}

type Client struct {
	polling    atomic.Bool
	bot        *telegramApi.Bot
	diagnostic func(string)
}

type SendCategory string

const (
	SendAccepted     SendCategory = "accepted"
	SendFailed       SendCategory = "failed"
	SendInvalidToken SendCategory = "invalid_token"
)

type SendOutcome struct {
	Category SendCategory
}

func NewClient(c ClientConfig) (*Client, error) {
	client := &Client{diagnostic: c.Diagnostic}
	opts := []telegramApi.Option{
		telegramApi.WithSkipGetMe(),
		telegramApi.WithNotAsyncHandlers(),
		telegramApi.WithAllowedUpdates(telegramApi.AllowedUpdates{"message"}),
		telegramApi.WithErrorsHandler(func(err error) {
			if !client.tokenRejected(err) {
				client.report("telegram polling failure")
			}
		}),
	}
	if c.Endpoint != "" {
		opts = append(opts, telegramApi.WithServerURL(c.Endpoint))
	}
	if c.HTTPClient != nil {
		opts = append(opts, telegramApi.WithHTTPClient(30*time.Second, c.HTTPClient))
	}
	b, err := telegramApi.New(c.Token, opts...)
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
	if errors.Is(err, telegramApi.ErrorUnauthorized) {
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
func (c *Client) Poll(ctx context.Context, handle func(context.Context, *telegramModels.Update)) error {
	if handle == nil || !c.polling.CompareAndSwap(false, true) {
		return errPolling
	}
	c.bot.RegisterHandlerMatchFunc(
		func(*telegramModels.Update) bool { return true },
		func(ctx context.Context, _ *telegramApi.Bot, update *telegramModels.Update) { handle(ctx, update) },
	)
	c.bot.Start(ctx)
	return ctx.Err()
}

func (c *Client) DeleteMessage(ctx context.Context, chatID int64, messageID int) error {
	_, err := c.bot.DeleteMessage(ctx, &telegramApi.DeleteMessageParams{ChatID: chatID, MessageID: messageID})
	if err == nil {
		return nil
	}
	if c.tokenRejected(err) {
		return ErrInvalidToken
	}
	return errors.New("telegram message deletion failed")
}

// Send makes exactly one API call and returns a sanitized outcome.
func (c *Client) Send(ctx context.Context, id int64, text string) (SendOutcome, error) {
	_, err := c.bot.SendMessage(ctx, &telegramApi.SendMessageParams{ChatID: id, Text: text})
	if err == nil {
		return SendOutcome{Category: SendAccepted}, nil
	}
	out := SendOutcome{Category: SendFailed}
	if errors.Is(err, telegramApi.ErrorUnauthorized) {
		out.Category = SendInvalidToken
	}
	return out, errors.New("telegram send failed")
}
