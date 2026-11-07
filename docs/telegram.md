# Telegram bot handoff

`internal/application` constructs one `internal/repositories/telegram.Client`
using ADR 0003's library v1.27.0 without network requests (`WithSkipGetMe`). It
injects the client into the notification worker as its raw sender and into
`internal/entrypoints/telegram.New` to construct a `telegram.Bot` for polling
and credential-message deletion.
After local configuration and database validation, the application calls the
client's `Validate(ctx)` before recovery or runtime loops start. Validation uses
a context-cancellable `getMe` request bounded to 15 seconds. Any error prevents
startup; only structured unauthorized errors are classified as token rejection.
Use one poller per token. The bot's `Run(ctx)` supplies its handler to
`Client.Poll`, which registers it before library polling and rejects a second
polling lifetime. Cancellation drains library intake and enrollment goroutines
and clears drafts. Wait for Run before closing services and repositories.
`Close` is also available for offline/manual use after stopping intake; do not
call it concurrently with starting new intake.

The bot's `Config` requires the polling/deletion client, shared auth policy, tracking
service, clock, direct-reply callback and membership callback. Tracking provides
enrollment existence checks; intake receives no repository reader. Optional
HTTP client/endpoint settings belong to the repository client's Config. The
sanitized diagnostic callback is optional. Ordinary `Membership` values identify
authenticated private owner evidence: explicit block/unblock and incoming
interaction. Application wiring connects them to delivery recovery, which
serializes membership effects with recipient sends, respects context cancellation,
and preserves permanent/exhausted pauses without block evidence. The synchronous
callback must not run an outbound send or wait indefinitely: command intake,
including removal, follows it. The application owns the delivery worker's lifetime.

`internal/repositories/telegram.Client` performs each Bot API operation and classifies sends into sanitized `domain.SendOutcome` values. It distinguishes definite blocked descriptions from other forbidden errors; structured unauthorized, rate-limit and permanent API errors map separately. The notification service owns retries and rate gates under [ADR 0013](adr/0013_keep-telegram-send-cooldown-in-memory.md) and the token rejection policy in [ADR 0012](adr/0012_fail-fast-on-invalid-telegram-token.md). Replies and deletion attempts have no application retry. Plain text is sent without parse mode; portal stage text is preserved. Library debugging is disabled and raw errors never reach diagnostics.

Library intake runs in update order. Enrollment runs asynchronously so `/cancel` can cancel the coordinator check; subsequent `/enroll` asks the user to wait for that work to finish. Status/removal run synchronously. Drafts hold only the number, never a retained password; inactivity expires at the exact ten-minute deadline. A minute sweep clears idle expired plaintext, and each incoming message checks exact deadlines. Confirmation expires ten minutes from its prompt. Restart discards both. Go reference clearing is not secure memory erasure, and best-effort Telegram deletion does not guarantee platform-copy deletion. No incoming command durability or exactly-once semantics are claimed.

Offline tests inject an in-memory HTTP transport through the real library. They never contact Telegram or the portal. Service startup, persistent delivery and maintenance are implemented and tested separately; live bot validation and target-device behavior remain unverified.

Delivery integration: `Config.DirectSend(ctx, owner, text)` applies only to direct command replies and is wired to `notifications.Worker.SendDirect`. It serializes gate admission, the send and in-memory cooldown updates with durable sends. Known rate-limit outcomes update the shared in-memory deadline even if intake is canceled. Positive retry_after is honored; otherwise the cooldown is 30 seconds. Restart clears the shared cooldown, while queued notification retry deadlines remain durable. Invalid-token send outcomes stop the worker and service. Structured unauthorized errors during polling or credential-message deletion emit `telegram_invalid_token`, which the application treats as fatal. Raw `Client.Send` remains the one-attempt transport primitive, preventing recursion. The bot exposes no Send method or raw-send fallback. Direct replies never acknowledge notification rows. Application wiring maps `Membership` fields to ordinary `notifications.Event` and calls `Worker.Membership`; explicit unblock evidence can resume a cycle even if the earlier block update was missed, while an ordinary interaction only clears a known block. Events interrupted by removal/cancellation remain queued for the next worker step. Start delivery recovery before incoming polling. Recovery depends on observed evidence and is not guaranteed after arbitrary downtime.
