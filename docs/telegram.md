# Telegram bot handoff

`internal/application` constructs one `internal/repositories/telegram.Client`
using ADR 0003's library v1.27.0 without network requests (`WithSkipGetMe`). It
injects the client into `internal/services/replies` as its raw sender and into
`internal/entrypoints/telegram.New` for polling and credential-message deletion.
After local configuration and database migration, the application calls the
client's `Validate(ctx)` before runtime loops start. Validation uses a
context-cancellable `getMe` request bounded to 15 seconds. Any error prevents
startup; only structured unauthorized errors are classified as token rejection.
Use one poller per token. The bot's `Run(ctx)` supplies its handler to
`Client.Poll`, which registers it before library polling and rejects a second
polling lifetime. Cancellation drains library intake and enrollment goroutines
and clears drafts. Wait for Run before closing services and repositories.
`Close` is also available for offline/manual use after stopping intake; do not
call it concurrently with starting new intake.

The bot's `Config` requires the polling/deletion client, tracking service, clock
and direct-reply callback. Tracking provides enrollment existence checks; intake
receives no repository reader. Optional HTTP client/endpoint settings belong to
the repository client's Config. Sanitized diagnostics are optional. Membership
updates cause no application work: there are no automatic sends, recipient pauses
or delivery recovery. Only ordinary private messages from a non-bot sender with
positive matching sender/chat IDs enter command handling.

`internal/repositories/telegram.Client` performs each Bot API operation and
classifies sends as accepted, failed, rate-limited or invalid-token using
sanitized `domain.SendOutcome` values. Other API/network failures use one generic
failed category; no delivery IDs or retry-specific categories are retained. Replies and deletion attempts have no
application retry. Plain text is sent without parse mode; portal stage text is
preserved. Library debugging is disabled and raw errors never reach diagnostics.

`Config.DirectSend(ctx, owner, text)` is wired to `replies.Sender.Send`. It
serializes admission, one bounded raw transport attempt and in-memory cooldown
updates across recipients. Positive Telegram `retry_after` is honored; otherwise
the cooldown is 30 seconds. Cancellation does not lose an observed rate limit.
Restart clears the cooldown. Replies are never persisted or retried. An invalid
token closes send admission and stops the service. Structured unauthorized errors
during polling or credential-message deletion emit `telegram_invalid_token`,
which the application also treats as fatal. Raw `Client.Send` remains the
one-attempt transport primitive. See [ADR 0018](adr/0018_remove-automatic-notifications.md)
and [ADR 0012](adr/0012_fail-fast-on-invalid-telegram-token.md).

Library intake runs in update order. Enrollment runs asynchronously so `/cancel`
can cancel the coordinator check; subsequent `/enroll` asks the user to wait for
that work to finish. Status/removal run synchronously. Drafts hold only the number,
never a retained password; inactivity expires at exactly ten minutes. A minute
sweep clears idle expired plaintext, and each message checks exact deadlines.
Removal confirmation expires ten minutes from its prompt. Restart discards both.
Go reference clearing is not secure memory erasure, and best-effort Telegram
deletion does not guarantee platform-copy deletion. No incoming command durability
or exactly-once semantics are claimed.

Scheduled and manual checks persist cache/history without outbound notifications.
`/status` returns the latest observation under the existing freshness and cooldown
rules. Help explicitly explains silent scheduled tracking.

Offline tests inject synthetic HTTP responses through the real library. They
cover enrollment/removal, silent scheduled changes followed by `/status`, token
rejection and reply cooldowns. Live portal/Telegram and target-device behavior
remain unverified.
