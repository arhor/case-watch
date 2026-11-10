// Package bot implements the private command transport selected by ADR 0003.
// Library types remain here. Incoming commands are not durably acknowledged.
package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	telegramModels "github.com/go-telegram/bot/models"

	"github.com/arhor/case-watch/internal/models"
	"github.com/arhor/case-watch/internal/services/cases"
	"github.com/arhor/case-watch/internal/utils"
)

type commandHandler func(context.Context, int64, *state, time.Time)

type Bot struct {
	handlers      map[string]commandHandler
	client        *Client
	caseService   *cases.Service
	replyMu       sync.Mutex
	repliesFailed bool
	config        Config
	owners        map[int64]*state
	mu            sync.Mutex
	wg            sync.WaitGroup
}

type Config struct {
	Client      *Client
	CaseService *cases.Service
	Clock       utils.Clock
	Diagnostic  func(string)
}

type state struct {
	mu                sync.Mutex
	number            string
	deadline, removal time.Time
	cancel            context.CancelFunc
}

func New(c Config) (*Bot, error) {
	if c.Client == nil || c.CaseService == nil || c.Clock == nil {
		return nil, errors.New("invalid Telegram configuration")
	}
	b := &Bot{
		client:      c.Client,
		caseService: c.CaseService,
		config:      c,
		owners:      make(map[int64]*state),
	}
	b.handlers = map[string]commandHandler{
		"/cancel": b.onCancel,
		"/start":  b.onHelp,
		"/help":   b.onHelp,
		"/enroll": b.onEnroll,
		"/status": b.onStatus,
		"/remove": b.onRemove,
	}
	return b, nil
}

// Run must only be called after token validation.
// Cancellation drains library handlers, cancels enrollment work and clears drafts.
func (this *Bot) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				this.Sweep()
			}
		}
	}()
	err := this.client.Poll(ctx, this.handle)
	cancel()
	<-done
	this.Close()
	return err
}

// -- Internal implementation ---------------------------------------------------------------------

func (this *Bot) handle(ctx context.Context, u *telegramModels.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	id, ok := privateOwner(m.From, m.Chat)
	if !ok {
		return
	}
	s := this.owner(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := this.config.Clock.Now()
	if !s.deadline.IsZero() && !now.Before(s.deadline) {
		s.number = ""
		s.deadline = time.Time{}
	}
	if !s.removal.IsZero() && !now.Before(s.removal) {
		s.removal = time.Time{}
	}
	text := m.Text
	fields := strings.Fields(text)
	command := ""
	if len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
		command = fields[0]
	}
	if command != "" && len(fields) > 1 {
		s.number = ""
		s.deadline = time.Time{}
		this.erase(ctx, id, m.ID)
		this.reply(ctx, id, "Commands take no arguments. Send /enroll to restart enrollment.")
		return
	}
	if handler, ok := this.handlers[command]; ok {
		handler(ctx, id, s, now)
		return
	}
	if command != "" {
		this.onUnknownCommand(ctx, m, s)
		return
	}
	this.onText(ctx, m, s, now)
}

// Command and conversation handlers run with the owner's state locked.
func (this *Bot) onCancel(ctx context.Context, id int64, s *state, now time.Time) {
	if s.cancel != nil {
		s.cancel()
	}
	s.number = ""
	s.deadline = time.Time{}
	s.removal = time.Time{}
	this.reply(ctx, id, "Conversation cancelled.")
}

func (this *Bot) onHelp(ctx context.Context, id int64, s *state, now time.Time) {
	this.reply(ctx, id, help)
}

func (this *Bot) onEnroll(ctx context.Context, id int64, s *state, now time.Time) {
	if s.cancel != nil {
		s.cancel()
		this.reply(ctx, id, "Enrollment cancellation requested. Send /enroll again after it finishes.")
		return
	}
	s.number = ""
	s.deadline = time.Time{}
	s.removal = time.Time{}
	exists, e := this.exists(ctx, id)
	if e != nil {
		this.reply(ctx, id, "Unable to read tracking repositories. Try again later.")
		return
	}
	if exists {
		this.reply(ctx, id, "You already track a case. Use /remove before enrolling another.")
		return
	}
	s.deadline = now.Add(10 * time.Minute)
	this.reply(ctx, id, "Send the case number in a separate text message. Telegram is not end-to-end encrypted; copies may remain. Deletion is best effort. Use /cancel to stop.")
}

func (this *Bot) onStatus(ctx context.Context, id int64, s *state, now time.Time) {
	s.mu.Unlock()
	status, e := this.caseService.Status(ctx, id)
	s.mu.Lock()
	if errors.Is(e, cases.ErrNotFound) {
		this.reply(ctx, id, "No tracked case. Use /enroll.")
		return
	}
	this.reply(ctx, id, formatStatus(status, e))
}

func (this *Bot) onRemove(ctx context.Context, id int64, s *state, now time.Time) {
	if s.cancel != nil {
		s.cancel()
	}
	s.number = ""
	s.deadline = time.Time{}
	exists, e := this.exists(ctx, id)
	if e != nil {
		this.reply(ctx, id, "Unable to read tracking repositories.")
		return
	}
	if !exists {
		this.reply(ctx, id, "No tracked case. Use /enroll.")
		return
	}
	s.removal = now.Add(10 * time.Minute)
	this.reply(ctx, id, "Send the exact separate text REMOVE within 10 minutes to delete your credentials and history. They cannot be reconstructed. Use /cancel to stop.")
}

func (this *Bot) onUnknownCommand(ctx context.Context, m *telegramModels.Message, s *state) {
	if !s.deadline.IsZero() {
		this.erase(ctx, m.Chat.ID, m.ID)
	}
	this.reply(ctx, m.Chat.ID, "Unknown command. Use /help.")
}

func (this *Bot) onText(ctx context.Context, m *telegramModels.Message, s *state, now time.Time) {
	id, text := m.Chat.ID, m.Text
	if !s.removal.IsZero() {
		if text != "REMOVE" {
			this.reply(ctx, id, "Send REMOVE exactly to confirm, or /cancel.")
			return
		}
		s.removal = time.Time{}
		s.mu.Unlock()
		e := this.caseService.Remove(ctx, id)
		s.mu.Lock()
		if e != nil {
			this.reply(ctx, id, "Removal failed. Use /remove to try again.")
		} else {
			this.reply(ctx, id, "Tracking data removed.")
		}
		return
	}
	if s.deadline.IsZero() {
		this.reply(ctx, id, "Use /enroll to begin, or /help.")
		return
	}
	this.erase(ctx, id, m.ID)
	if s.number == "" {
		if strings.TrimSpace(text) == "" || len(text) > 256 || strings.ContainsAny(text, "\r\n") {
			this.reply(ctx, id, "Invalid case number. Send a nonempty single-line number separately.")
			return
		}
		s.number = text
		s.deadline = now.Add(10 * time.Minute)
		this.reply(ctx, id, "Send the password in a separate text message.")
		return
	}
	if text == "" || len(text) > 4096 {
		this.reply(ctx, id, "Invalid password. Send a nonempty password separately.")
		return
	}
	number := s.number
	s.number = ""
	s.deadline = time.Time{}
	work, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	// The intake handler remains ordered while collection can be cancelled by
	// a later command. The case service owns bounded work and commit fencing.
	this.wg.Add(1)
	go func(number, password string) {
		defer this.wg.Done()
		_, _, e := this.caseService.Enroll(work, id, number, password)
		cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cancel = nil
		if e != nil {
			this.reply(ctx, id, "Enrollment failed. Send /enroll to restart.")
			return
		}
		this.reply(ctx, id, "Tracking enrolled. Use /status to view your case.")
	}(number, text)
}

// Sweep drops expired plaintext drafts even when no further update arrives.
func (this *Bot) Sweep() {
	withLock(&this.mu, func() {
		now := this.config.Clock.Now()
		for _, state := range this.owners {
			withLock(&state.mu, func() {
				if !state.deadline.IsZero() && !now.Before(state.deadline) {
					state.number = ""
					state.deadline = time.Time{}
				}
				if !state.removal.IsZero() && !now.Before(state.removal) {
					state.removal = time.Time{}
				}
			})
		}
	})
}

func (this *Bot) Close() {
	withLock(&this.mu, func() {
		for _, state := range this.owners {
			withLock(&state.mu, func() {
				if state.cancel != nil {
					state.cancel()
				}
				state.number = ""
				state.deadline = time.Time{}
				state.removal = time.Time{}
			})
		}
	})
}

func (this *Bot) owner(id int64) *state {
	this.mu.Lock()
	defer this.mu.Unlock()
	s := this.owners[id]
	if s == nil {
		s = &state{}
		this.owners[id] = s
	}
	return s
}

// reply makes one bounded attempt for a user interaction. Replies are never queued.
func (this *Bot) reply(ctx context.Context, id int64, text string) {
	this.replyMu.Lock()
	defer this.replyMu.Unlock()
	if this.repliesFailed || ctx.Err() != nil {
		this.diagnostic("telegram reply failure")
		return
	}
	if id <= 0 {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	out, err := this.client.Send(bounded, id, text)
	cancel()
	if out.Category == SendInvalidToken {
		this.repliesFailed = true
		this.diagnostic("telegram_invalid_token")
		return
	}
	if err != nil {
		this.diagnostic("telegram reply failure")
	}
}

func (this *Bot) erase(ctx context.Context, id int64, message int) {
	err := this.client.DeleteMessage(ctx, id, message)
	if err != nil && !errors.Is(err, ErrInvalidToken) {
		this.diagnostic("telegram credential deletion failure")
	}
}

func (this *Bot) diagnostic(s string) {
	if this.config.Diagnostic != nil {
		this.config.Diagnostic(s)
	}
}

func (this *Bot) exists(ctx context.Context, id int64) (bool, error) {
	return this.caseService.HasEnrollment(ctx, id)
}

const help = "Track one case privately. Commands: /enroll /status /remove /cancel /help. " +
	"Automatic checks run Monday-Friday at 08:00, 10:00, 12:00, 14:00, 16:00 and 18:00 Europe/Warsaw, including holidays. " +
	"Checks do not send automatic notifications; use /status to view your case. Telegram enrollment is not end-to-end encrypted; " +
	"platform copies may remain. Credential-message deletion is best effort."

// privateOwner binds conversations to Telegram transport metadata.
func privateOwner(sender *telegramModels.User, chat telegramModels.Chat) (int64, bool) {
	if sender == nil || sender.IsBot || sender.ID <= 0 || chat.Type != telegramModels.ChatTypePrivate || chat.ID != sender.ID {
		return 0, false
	}
	return sender.ID, true
}

func formatStatus(s models.Status, err error) string {
	text := ""
	if err != nil {
		text = "Refresh failed. "
	}
	if s.CheckedAt.IsZero() {
		text += "No successful observation available."
	} else {
		kind := "Cached"
		if s.Fresh && err == nil {
			kind = "Fresh"
		}
		text += fmt.Sprintf("%s stage: %s\nLast successful check: %s", kind, s.Stage, s.CheckedAt.UTC().Format(time.RFC3339))
	}
	if s.InProgress {
		text += "\nA check is in progress."
	}
	if !s.NextEligibleAt.IsZero() {
		text += "\nNext check eligible: " + s.NextEligibleAt.UTC().Format(time.RFC3339)
	}
	return text
}

func withLock(mu *sync.Mutex, fn func()) {
	mu.Lock()
	defer mu.Unlock()
	fn()
}
