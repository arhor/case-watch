---
status: accepted
date: 2026-11-08
decision-makers: Repository maintainer
---

# Consolidate the Telegram bot

## Context and Problem Statement

The production Telegram integration is split across a bot, a raw client, a
transport interface and a reply sender even though the service has one bot and
one construction path. The maintainer used `internal/telegram_bot.go` to show the
desired direct, structured style and explicitly approved replacing ADR 0019's
separate client boundary. Existing command behavior, credential safeguards and
failure policies remain required.

## Decision

Construct one `internal/bot.Bot` from its token, case service, clock and diagnostic
callback. It owns the `go-telegram/bot` instance, default update handler, startup
validation, polling, replies, deletion, send serialization and in-memory rate
limit state. Exact commands dispatch through a map of small handler methods.
`internal/application` constructs and runs that object directly.

Remove the `Client`, `ClientConfig`, `Transport` and `replySender` types. Keep API
test options on `bot.Config` so synthetic HTTP fixtures remain offline. Do not
change commands, persistence, scheduling, configuration variables, dependencies
or the experimental playground file.

## Consequences

- Production Telegram construction and behavior are visible through one type and
  one file instead of three cooperating production types.
- Bot API details remain contained in `internal/bot`, as required by ADR 0003.
- Tests configure the consolidated bot rather than mocking a production transport
  boundary; application lifecycle tests retain a private bot stub.
- The bot remains intentionally stateful and single-use, so it is not a reusable
  generic Telegram client.

## Implementation Plan

- **Affected paths**: `internal/bot/`, `internal/application/application.go`,
  their tests, `internal/domain/contracts.go`, and maintained architecture and
  Telegram guides.
- **Dependencies**: retain `github.com/go-telegram/bot` v1.27.0; add none.
- **Pattern to follow**: configure the library and default handler together in
  `bot.New`, following the playground's direct construction style.
- **Patterns to avoid**: no separate raw-client wrapper, transport interface,
  reply service or application forwarding method.
- **Migration**: replace application construction with one `bot.New` call and
  delete the superseded types; no database or configuration migration.

### Verification

- [x] Existing enrollment, status, removal, cancellation, privacy and silent
  scheduling tests pass.
- [x] Synthetic tests cover validation, polling, reply rate limits, invalid-token
  shutdown and sanitized diagnostics through the consolidated bot.
- [x] Searches find no production `ClientConfig`, `Transport`, `replySender` or
  domain `Sender` abstraction.
- [ ] `go test ./...`, `go test -race ./...`, `go vet ./...` and the root build pass.
- [x] Maintained documentation matches construction and lifecycle behavior.

## Alternatives Considered

- Keep the split client/transport/reply design: preserves narrow mocking seams but
  retains the indirection the maintainer asked to remove.
- Move command handling into the application: reduces the bot type but leaks
  Telegram models and conversation state across the package boundary.

## More Information

This decision narrows the Telegram-specific structure selected by
[ADR 0019](0019_simplify-application-structure.md). Its case-service, scheduler,
repository and application-lifecycle decisions remain accepted.

## Verification evidence — 2026-11-08

Uncached ordinary tests, vet, the root build, structural searches and whitespace
checks passed with Go 1.27.3. Existing localhost-only portal fixtures required
sandbox permission; all exchanges remained synthetic. The race suite could not
start tests in this ARM64 environment because ThreadSanitizer rejected its
47-bit virtual-memory layout (the runtime supports 48-bit layouts), so the
combined verification item remains unchecked. No live Telegram/portal or
deployment acceptance was performed.
