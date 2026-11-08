# Application structure

Case Watch has one case service, a Telegram bot, a weekday scheduler and repositories.
`main.go` supplies the signal context to `application.Run(ctx)`.

```text
internal/
  application/       construction, startup, shutdown and database file protection
  bot/               Telegram API client, commands, enrollment drafts and replies
  scheduler/         the six Warsaw weekday checks
  services/cases/    enrollment, status checks, history and removal
    portal/          external case-status API client
  repositories/      SQLite repositories, transactions and migrations
  domain/            shared values and small external collaborator interfaces
```

`application/application.go` shows the complete construction and launch sequence:
open storage, construct the case service and clients, construct the bot and
scheduler, validate the Telegram token, and start the two loops. A cancellation
cause distinguishes service failure from ordinary shutdown. Normal Go defers close
resources in reverse order, keeping the database lock until SQLite is closed.

The bot and scheduler receive the same `cases.Service`. Their small local interfaces
describe the methods they call. The service receives the concrete repository Store;
its transaction methods use `Store.RunInTx` directly. There is no persistence adapter,
monitoring decorator, component registry or separate runtime object.

The case service owns the few pieces of shared state needed for correctness: one
active check per owner, callers sharing its result, and cancellation/draining during
removal or shutdown. A manual request and a scheduled check therefore cannot race
to overwrite each other. Check cooldowns persist in SQLite; successful cache age is
measured from completion. Portal requests run outside database transactions.
Storage failures invoke the application's cancellation callback directly.

The bot package owns its Telegram API client, conversation drafts and removal confirmation. Its private reply sender
serializes Telegram sends and records rate limits in memory. Invalid tokens stop
the application. Scheduled checks update cache/history silently; results are viewed
with `/status`.

`internal/telegram_bot.go` is a separate experiment explicitly marked “don't touch”;
production does not import it. See [ADR 0019](adr/0019_simplify-application-structure.md)
for the refactor decision and [product contract](product-contract.md) for behavior.
