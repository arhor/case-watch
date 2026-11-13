// Package bot implements the private command transport selected by ADR 0003.
// Library types remain here. Incoming commands are not durably acknowledged.
package bot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	_ "time/tzdata"

	telegramApi "github.com/go-telegram/bot"
	telegramModels "github.com/go-telegram/bot/models"

	"github.com/arhor/case-watch/internal/models"
	"github.com/arhor/case-watch/internal/services/cases"
	"github.com/arhor/case-watch/internal/utils"
)

var (
	ErrInvalidToken = errors.New("telegram token rejected")
	errPolling      = errors.New("telegram polling already started")
)

type commandHandler func(context.Context, int64, *state, time.Time)

type Bot struct {
	handlers      map[string]commandHandler
	client        *telegramApi.Bot
	polling       atomic.Bool
	caseService   *cases.Service
	replyMu       sync.Mutex
	repliesFailed bool
	config        Config
	owners        map[int64]*state
	mu            sync.Mutex
	wg            sync.WaitGroup
}

type Config struct {
	Token       string
	Endpoint    string
	HTTPClient  *http.Client
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

// New constructs the bot and validates its token using ctx before returning.
func New(ctx context.Context, c Config) (*Bot, error) {
	if c.CaseService == nil || c.Clock == nil {
		return nil, errors.New("invalid Telegram configuration")
	}
	bot := &Bot{
		caseService: c.CaseService,
		config:      c,
		owners:      make(map[int64]*state),
	}
	opts := []telegramApi.Option{
		telegramApi.WithSkipGetMe(),
		telegramApi.WithNotAsyncHandlers(),
		telegramApi.WithAllowedUpdates(telegramApi.AllowedUpdates{"message"}),
		telegramApi.WithErrorsHandler(func(err error) {
			if !bot.tokenRejected(err) {
				bot.diagnostic("telegram polling failure")
			}
		}),
	}
	if c.Endpoint != "" {
		opts = append(opts, telegramApi.WithServerURL(c.Endpoint))
	}
	if c.HTTPClient != nil {
		opts = append(opts, telegramApi.WithHTTPClient(30*time.Second, c.HTTPClient))
	}
	client, err := telegramApi.New(c.Token, opts...)
	if err != nil {
		return nil, errors.New("invalid Telegram configuration")
	}
	bot.client = client
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := bot.client.GetMe(bounded); err != nil {
		if bot.tokenRejected(err) {
			return nil, ErrInvalidToken
		}
		bot.diagnostic("telegram validation failure")
		return nil, errors.New("telegram validation failed")
	}
	bot.handlers = map[string]commandHandler{
		"/cancel": bot.onCancel,
		"/help":   bot.onHelp,
		"/start":  bot.onStart,
		"/status": bot.onStatus,
		"/remove": bot.onRemove,
	}
	return bot, nil
}

// Cancellation drains library handlers, cancels enrollment work and clears drafts.
func (this *Bot) Run(ctx context.Context) error {
	if !this.polling.CompareAndSwap(false, true) {
		return errPolling
	}
	this.client.RegisterHandlerMatchFunc(
		func(*telegramModels.Update) bool { return true },
		func(ctx context.Context, _ *telegramApi.Bot, update *telegramModels.Update) { this.handle(ctx, update) },
	)
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
	this.client.Start(ctx)
	cancel()
	<-done
	this.Close()
	return ctx.Err()
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
	expiredEnrollment := !s.deadline.IsZero() && !now.Before(s.deadline)
	expiredRemoval := !s.removal.IsZero() && !now.Before(s.removal)
	if expiredEnrollment {
		s.number = ""
		s.deadline = time.Time{}
	}
	if expiredRemoval {
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
		this.reply(ctx, id, "Команду нужно отправить без дополнительных параметров.\n" +
		"\n" +
		"Чтобы начать заново, используйте /start.")
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
	if expiredRemoval {
		this.reply(ctx, id, "Время ожидания истекло. Чтобы попробовать удалить дело ещё раз, отправьте /remove.")
		return
	}
	if expiredEnrollment {
		this.reply(ctx, id, "Время ожидания истекло. Чтобы начать заново, отправьте /start.")
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
	this.reply(ctx, id, "Действие отменено.")
}

func (this *Bot) onHelp(ctx context.Context, id int64, s *state, now time.Time) {
	this.reply(ctx, id,
		"CaseWatch помогает следить за ходом рассмотрения вашего дела.\n" +
		"\n" +
		"Доступные команды:\n" +
		"/start — добавить дело для отслеживания\n" +
		"/status — проверить статус дела\n" +
		"/remove — удалить дело и связанные с ним данные\n" +
		"/cancel — отменить текущее действие\n" +
		"/help — показать эту справку")
}

func (this *Bot) onStart(ctx context.Context, id int64, s *state, now time.Time) {
	if s.cancel != nil {
		s.cancel()
		this.reply(ctx, id, "Отменяю предыдущую попытку добавления дела.\n" +
		"\n" +
		"После завершения попробуйте ещё раз: /start.")
		return
	}
	s.number = ""
	s.deadline = time.Time{}
	s.removal = time.Time{}
	exists, e := this.exists(ctx, id)
	if e != nil {
		this.reply(ctx, id, "Не удалось проверить сохранённые данные.\n" +
		"\n" +
		"Попробуйте ещё раз позже.")
		return
	}
	if exists {
		this.reply(ctx, id, "У вас уже есть дело для отслеживания.\n" +
		"\n" +
		"Чтобы добавить другое, сначала удалите текущее с помощью /remove.")
		return
	}
	s.deadline = now.Add(10 * time.Minute)
	this.reply(ctx, id, "Давайте добавим ваше дело для отслеживания.\n" +
		"\n" +
		"Для начала отправьте номер дела отдельным сообщением. Затем я попрошу пароль для доступа к информации о деле.\n" +
		"\n" +
		"Для отмены используйте /cancel.")
}

func (this *Bot) onStatus(ctx context.Context, id int64, s *state, now time.Time) {
	s.mu.Unlock()
	status, e := this.caseService.Status(ctx, id)
	s.mu.Lock()
	if errors.Is(e, cases.ErrNotFound) {
		this.reply(ctx, id, "У вас пока нет дела для отслеживания.\n" +
		"\n" +
		"Чтобы добавить дело, отправьте /start.")
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
		this.reply(ctx, id, "Не удалось проверить сохранённые данные.\n" +
		"\n" +
		"Попробуйте ещё раз позже.")
		return
	}
	if !exists {
		this.reply(ctx, id, "У вас пока нет дела для отслеживания.\n" +
		"\n" +
		"Чтобы добавить дело, отправьте /start.")
		return
	}
	s.removal = now.Add(10 * time.Minute)
	this.reply(ctx, id, "Вы уверены, что хотите удалить дело из отслеживания?\n" +
		"\n" +
		"Все сохранённые данные, включая номер дела, пароль и историю изменений статуса, будут безвозвратно удалены.\n" +
		"\n" +
		"Для подтверждения ответьте «Да», для отмены — «Нет». Ответ нужно отправить в течение 10 минут.")
}

func (this *Bot) onUnknownCommand(ctx context.Context, m *telegramModels.Message, s *state) {
	if !s.deadline.IsZero() {
		this.erase(ctx, m.Chat.ID, m.ID)
	}
	this.reply(ctx, m.Chat.ID, "Неизвестная команда. Список доступных команд: /help.")
}

func (this *Bot) onText(ctx context.Context, m *telegramModels.Message, s *state, now time.Time) {
	id, text := m.Chat.ID, m.Text
	if !s.removal.IsZero() {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "нет":
			s.removal = time.Time{}
			this.reply(ctx, id, "Удаление отменено.")
			return
		case "да":
			// Proceed with the confirmed deletion.
		default:
			this.reply(ctx, id, "Пожалуйста, ответьте «Да» или «Нет».")
			return
		}
		s.removal = time.Time{}
		s.mu.Unlock()
		e := this.caseService.Remove(ctx, id)
		s.mu.Lock()
		if e != nil {
			this.reply(ctx, id, "Не удалось завершить удаление дела.\n\nПопробуйте ещё раз с помощью /remove.")
		} else {
			this.reply(ctx, id, "Дело удалено из отслеживания. Все связанные с ним данные удалены.")
		}
		return
	}
	if s.deadline.IsZero() {
		this.reply(ctx, id, "Чтобы добавить дело для отслеживания, отправьте /start.\n" +
		"\n" +
		"Список доступных команд: /help.")
		return
	}
	this.erase(ctx, id, m.ID)
	if s.number == "" {
		if strings.TrimSpace(text) == "" || len(text) > 256 || strings.ContainsAny(text, "\r\n") {
			this.reply(ctx, id, "Не удалось принять номер дела.\n" +
		"\n" +
		"Проверьте его и отправьте ещё раз одним текстовым сообщением, без переносов строк.")
			return
		}
		s.number = text
		s.deadline = now.Add(10 * time.Minute)
		this.reply(ctx, id, "Теперь отправьте пароль для доступа к вашему делу отдельным сообщением.")
		return
	}
	if text == "" || len(text) > 4096 {
		this.reply(ctx, id, "Не удалось принять пароль.\n" +
		"\n" +
		"Проверьте его и отправьте ещё раз отдельным сообщением.")
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
	this.reply(ctx, id, "Проверяю данные и подключаю отслеживание вашего дела.")
	go func(number, password string) {
		defer this.wg.Done()
		_, _, e := this.caseService.Enroll(work, id, number, password)
		cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cancel = nil
		if e != nil {
			this.reply(ctx, id, "Не удалось добавить дело для отслеживания.\n" +
		"\n" +
		"Проверьте номер дела и пароль или попробуйте ещё раз позже.\n" +
		"\n" +
		"Для повторной попытки отправьте /start.")
			return
		}
		this.reply(ctx, id, "Готово! Ваше дело добавлено для отслеживания.\n" +
		"\n" +
		"Чтобы узнать текущий статус, отправьте /status.")
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
	_, err := this.client.SendMessage(bounded, &telegramApi.SendMessageParams{ChatID: id, Text: text})
	cancel()
	if errors.Is(err, telegramApi.ErrorUnauthorized) {
		this.repliesFailed = true
		this.diagnostic("telegram_invalid_token")
		return
	}
	if err != nil {
		this.diagnostic("telegram reply failure")
	}
}

func (this *Bot) erase(ctx context.Context, id int64, message int) {
	_, err := this.client.DeleteMessage(ctx, &telegramApi.DeleteMessageParams{ChatID: id, MessageID: message})
	if err != nil && !this.tokenRejected(err) {
		this.diagnostic("telegram credential deletion failure")
	}
}

func (this *Bot) tokenRejected(err error) bool {
	if errors.Is(err, telegramApi.ErrorUnauthorized) {
		this.diagnostic("telegram_invalid_token")
		return true
	}
	return false
}

func (this *Bot) diagnostic(s string) {
	if this.config.Diagnostic != nil {
		this.config.Diagnostic(s)
	}
}

func (this *Bot) exists(ctx context.Context, id int64) (bool, error) {
	return this.caseService.HasEnrollment(ctx, id)
}

// privateOwner binds conversations to Telegram transport metadata.
func privateOwner(sender *telegramModels.User, chat telegramModels.Chat) (int64, bool) {
	if sender == nil || sender.IsBot || sender.ID <= 0 || chat.Type != telegramModels.ChatTypePrivate || chat.ID != sender.ID {
		return 0, false
	}
	return sender.ID, true
}

func formatStatus(s models.Status, err error) string {
	var parts []string
	if err != nil {
		parts = append(parts, "Не удалось обновить статус дела.")
	}
	if s.CheckedAt.IsZero() {
		parts = append(parts, "Пока нет данных об успешных проверках вашего дела.")
	} else {
		stage := s.Stage
		if stage == "" {
			stage = "Не указан"
		}
		checkedAt := formatWarsawTime(s.CheckedAt)
		switch {
		case err != nil:
			parts = append(parts, fmt.Sprintf(
				"Показываю данные последней успешной проверки:\n\nСтатус дела: %s\nПроверено: %s",
				stage, checkedAt,
			))
		case s.Fresh:
			parts = append(parts, fmt.Sprintf(
				"Статус дела: %s\nПоследняя проверка: %s\n\nДанные успешно обновлены.",
				stage, checkedAt,
			))
		default:
			parts = append(parts, fmt.Sprintf(
				"Статус дела: %s\nПоследняя успешная проверка: %s\n\nПоказываю последние сохранённые данные.",
				stage, checkedAt,
			))
		}
	}
	if s.InProgress {
		parts = append(parts, "Проверка статуса ещё выполняется.")
	}
	if !s.NextEligibleAt.IsZero() {
		parts = append(parts, "Повторное обновление будет доступно после "+formatWarsawTime(s.NextEligibleAt)+".")
	}
	return strings.Join(parts, "\n\n")
}

func formatWarsawTime(t time.Time) string {
	location, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		return t.UTC().Format("02.01.2006, 15:04 UTC")
	}
	return t.In(location).Format("02.01.2006, 15:04")
}

func withLock(mu *sync.Mutex, fn func()) {
	mu.Lock()
	defer mu.Unlock()
	fn()
}
