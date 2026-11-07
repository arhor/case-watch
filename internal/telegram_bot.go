package internal

import (
	"context"
	"log"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// -- Experimental code, don't touch --------------------------------------------------------------

type commandHandler func(context.Context, *models.Message)

// Bot is an experimental command bot, separate from the application bot.
type Bot struct {
	client   *bot.Bot
	handlers map[string]commandHandler
}

// NewBot creates a bot and validates its token through Telegram's getMe API.
func NewBot(token string) (*Bot, error) {
	b := &Bot{}
	b.handlers = map[string]commandHandler{
		"/help":   b.onHelp,
		"/start":  b.onStart,
		"/enroll": b.onEnroll,
		"/setup":  b.onSetup,
		"/status": b.onStatus,
		"/remove": b.onRemove,
		"/cancel": b.onCancel,
	}
	client, err := bot.New(
		token,
		bot.WithAllowedUpdates(bot.AllowedUpdates{"message"}),
		bot.WithDefaultHandler(b.handleMessage),
	)
	if err != nil {
		return nil, err
	}
	b.client = client
	return b, nil
}

// Run polls for messages until ctx is canceled.
func (b *Bot) Run(ctx context.Context) {
	b.client.Start(ctx)
}

func (b *Bot) handleMessage(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}
	message := update.Message
	if message.From == nil || message.From.IsBot || message.From.ID <= 0 ||
		message.Chat.Type != models.ChatTypePrivate || message.Chat.ID != message.From.ID {
		return
	}
	if handler, ok := b.handlers[message.Text]; ok {
		handler(ctx, message)
		return
	}
	if strings.HasPrefix(message.Text, "/") {
		b.onUnknownCommand(ctx, message)
	} else if message.Text != "" {
		b.onText(ctx, message)
	}
}

func (b *Bot) onHelp(ctx context.Context, message *models.Message) {
	b.reply(ctx, message.Chat.ID, "Case Watch experiment. Commands: /start, /help, /enroll (/setup), /status, /remove, /cancel. Tracking actions are stubs; do not send credentials. Send ping to receive pong.")
}

func (b *Bot) onStart(ctx context.Context, message *models.Message) {
	b.onHelp(ctx, message)
}

func (b *Bot) onSetup(ctx context.Context, message *models.Message) {
	b.onEnroll(ctx, message)
}

func (b *Bot) onEnroll(ctx context.Context, message *models.Message) {
	// TODO: Start an owner-bound conversation collecting case number and password separately.
	b.reply(ctx, message.Chat.ID, "Enrollment is not implemented yet. Do not send credentials.")
}

func (b *Bot) onStatus(ctx context.Context, message *models.Message) {
	// TODO: Fetch the owner's status, respecting cache freshness and check cooldowns.
	b.reply(ctx, message.Chat.ID, "Status is not implemented yet.")
}

func (b *Bot) onRemove(ctx context.Context, message *models.Message) {
	// TODO: Request expiring REMOVE confirmation before coordinated deletion.
	b.reply(ctx, message.Chat.ID, "Removal is not implemented yet. No data has been deleted.")
}

func (b *Bot) onCancel(ctx context.Context, message *models.Message) {
	// TODO: Clear the owner's draft/confirmation and cancel enrollment work.
	b.reply(ctx, message.Chat.ID, "Cancellation is not implemented yet. No conversation state is stored in this experiment.")
}

func (b *Bot) onText(ctx context.Context, message *models.Message) {
	// TODO: Route by the owner's conversation step: case number, password, or removal confirmation.
	// Enforce expiry and attempt credential-message deletion without echoing input.
	b.reply(ctx, message.Chat.ID, "Conversation input is not implemented yet. Use /help; do not send credentials.")
}

func (b *Bot) onUnknownCommand(ctx context.Context, message *models.Message) {
	b.reply(ctx, message.Chat.ID, "Unknown command. Use /help. Commands take no arguments.")
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string) {
	_, err := b.client.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	})
	if err != nil {
		log.Print("experimental Telegram reply failed")
	}
}
