# Telegram bot

`internal/application` constructs one `internal/repositories/telegram.Client`
without network requests (`WithSkipGetMe`) and injects it into `internal/bot.New`
along with the case service and clock. The bot consumes a small local `Cases`
interface for enrollment, status, removal and enrollment existence checks.
It receives no repository reader or application runtime object.

The application validates the token with the client's bounded `getMe` call before
starting either the bot or scheduler. Any validation error prevents startup.
Use one poller per token. `Bot.Run(ctx)` supplies its handler to `Client.Poll`,
which binds the handler before polling and rejects a second polling lifetime.
Only message updates are requested. Private sender/chat identity is validated
before command handling or enrollment input. Membership events do no application
work. Cancellation drains intake and enrollment work and clears drafts.

The bot privately owns its reply sender in `internal/bot/replies.go`. This sender
serializes one bounded send attempt across recipients, applying a 15-second
request timeout. Telegram rate limits establish an in-memory deadline using a
positive `retry_after`, or 30 seconds otherwise. Cancellation does not lose an
observed limit; restart clears it. Replies are never persisted or retried.
Invalid-token sends close send admission and report `telegram_invalid_token`,
which stops the application. Polling and credential-message deletion report
structured token rejection through the same diagnostic category.

`internal/repositories/telegram.Client` owns the Bot API library and transport
configuration. It returns sanitized accepted/failed/rate-limited/invalid-token
send outcomes. Raw errors, tokens and message text never enter diagnostics.
Messages use plain text without parse mode, preserving portal stage text.

Enrollment runs asynchronously so `/cancel` can cancel its check. A further
`/enroll` waits for cancellation to finish. Status and removal run synchronously.
Drafts hold the case number in memory; passwords are passed directly to enrollment.
Each message checks exact ten-minute inactivity/confirmation deadlines; a minute
sweep also clears expired drafts. Restart discards them. Best-effort deletion of
credential messages does not guarantee Telegram platform-copy deletion, and Go
reference clearing does not guarantee secure memory erasure.

Scheduled/manual checks save cache/history without automatic messages. `/status`
returns the result under the existing cache and cooldown rules. The bot's help
explains silent tracking. No incoming command durability is claimed.

Offline tests cover conversations, ownership, silent scheduled changes followed
by `/status`, token rejection, bounded reply handling and cooldowns. Live
portal/Telegram and target-device behavior remain unverified. See
[application structure](architecture.md), [ADR 0019](adr/0019_simplify-application-structure.md)
and [product contract](product-contract.md).
