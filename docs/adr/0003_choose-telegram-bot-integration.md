---
status: accepted
date: 2026-10-31
decision-makers: Repository maintainer
---

# Choose go-telegram/bot with long polling

## Context and Problem Statement

Case Watch needs Telegram communication for commands and status notifications. The initial audience is the maintainer and his wife, with one service instance on a Raspberry Pi 5 running 64-bit Raspberry Pi OS with 4 GB RAM. [ADR 0002](0002_choose-go.md) selects Go and direct Vaadin Flow RPC for the government portal; it leaves the Telegram library undecided.

The maintainer selected maintainability over minimizing dependencies and agreed to long polling. We need to decide whether to maintain direct HTTP communication or adopt a Go library before implementing Telegram support.

## Decision Drivers

- Reduce routine API binding, polling, decoding, and error-handling work.
- Support context cancellation and configurable HTTP clients for testing.
- Receive commands without exposing a public inbound endpoint on the Pi.
- Keep case-tracking logic independent of Telegram library types.
- Avoid claiming resource advantages without measurements.

## Considered Options

- Standard-library HTTP calls to the Telegram Bot API.
- `github.com/go-telegram/bot` v1.27.0.
- `github.com/PaulSonOfLars/gotgbot/v2`.

## Decision Outcome

Use `github.com/go-telegram/bot`, pinned to v1.27.0 when Telegram implementation begins, with long polling against Telegram's hosted Bot API. The maintainer explicitly selected this option. Review dependency upgrades explicitly rather than tracking an unpinned version.

Use the library's API bindings and polling implementation. Keep Telegram models, handlers, and configuration within a future `internal/telegram/` package. Pass ordinary Go values and contexts to case-tracking logic; do not make the portal implementation depend on Telegram types.

Long polling uses outbound HTTPS and requires no public webhook endpoint. Operate one polling instance for the bot token initially. Do not introduce a local Bot API server or webhook infrastructure for this scope.

The library advances its polling offset before command handling finishes. It does not provide durable or exactly-once command processing. Application delivery guarantees, persistence, and recovery require a later decision; this ADR must not be interpreted as solving them.

Keep bot tokens, government-portal credentials, and message bodies out of diagnostic logs. Library errors and decode failures can include sensitive request or update details: provide sanitized application diagnostics rather than forwarding raw library error output, and leave payload debugging disabled.

### Consequences

- API bindings and polling maintenance are delegated to a dependency, reducing local plumbing while requiring explicit version review.
- Library polling handles backoff and Telegram's `retry_after`; application send/retry policies remain to be designed separately.
- Context-based calls and configurable HTTP clients allow cancellation and offline testing without a real bot token.
- Update acknowledgement and command completion are separate. Durable processing cannot be assumed from successful polling.
- The package boundary limits the spread of library types, but application-specific mapping and error handling remain our responsibility.
- No memory, binary-size, or Pi resource advantage has been measured for this selection.

## Scope and Non-goals

This decision selects Telegram API communication and update transport. It does not implement a bot or select command behavior, credential enrollment, storage, delivery guarantees, portal scheduling, or detailed retry policies. It does not supersede ADR 0002.

## Implementation Plan

These tasks describe later implementation; this documentation change adds no dependency or runtime configuration.

- [ ] Add `github.com/go-telegram/bot` at v1.27.0 to `go.mod` and commit the resulting `go.sum` when Telegram code is introduced.
- [ ] Implement the Telegram adapter under `internal/telegram/`, keeping library types inside that boundary and referencing this ADR from the adapter entry point.
- [ ] Configure the token through runtime configuration, graceful context cancellation, long polling, and sanitized diagnostics. Decide the runnable bot entry point and command behavior in the implementation plan for that feature.
- [ ] Add offline tests with a configurable HTTP client or local Bot API test server. Keep live tokens and real message bodies out of fixtures.
- [ ] Resolve delivery guarantees and persistence before relying on command processing across crashes; decide send/retry behavior before implementing reliable notifications.
- [ ] Build for Linux ARM64 and measure the running bot on the target Pi before claiming resource suitability.

### Verification

- [ ] The implementation pins v1.27.0 and uses long polling without a public webhook endpoint.
- [ ] Case-tracking and portal packages do not import Telegram library types.
- [ ] Offline tests cover receiving an update, sending a reply, cancellation, polling failures, and rate-limit handling without contacting Telegram or the government portal.
- [ ] Diagnostic tests confirm that tokens, credentials, and message bodies are excluded from logs on error paths.
- [ ] Delivery assumptions explicitly account for offsets advancing before command completion.
- [ ] Linux ARM64 compilation succeeds; target-device measurements are recorded before deployment suitability is asserted.

## Pros and Cons of the Options

### Standard-library HTTP

- Good: provides full control and no additional module dependency; the initial required API surface is small.
- Bad: leaves polling offsets, API types, decoding, backoff, and error handling to this project.
- Not chosen because minimizing dependencies is less important than reducing routine maintenance.

### go-telegram/bot v1.27.0

- Good: provides context-based methods, polling, configurable HTTP clients, and no third-party module dependencies at the selected release.
- Good: the reviewed polling implementation handles backoff and `retry_after`.
- Bad: library update acknowledgement and diagnostics require careful application integration; durable processing is not built in.
- Chosen because its API and configuration fit the small service without maintaining our own Telegram HTTP layer.

### gotgbot/v2

- Good: provides generated API bindings, context-aware methods, and optional updater/dispatcher support without third-party module dependencies in the reviewed source.
- Bad: adopting its API and dispatcher still requires application-specific lifecycle, delivery, and diagnostic integration.
- A credible alternative, but no concrete advantage was identified for this private service that outweighed the selected library's fit. This is not a claim that gotgbot lacks cancellation or testing support.

## More Information

Primary sources reviewed on 2026-10-31:

- [Telegram Bot API: receiving updates](https://core.telegram.org/bots/api#getting-updates).
- [go-telegram/bot v1.27.0 release](https://github.com/go-telegram/bot/releases/tag/v1.27.0).
- [Pinned polling implementation, including offset advancement and backoff](https://github.com/go-telegram/bot/blob/v1.27.0/get_updates.go).
- [Pinned configuration options](https://github.com/go-telegram/bot/blob/v1.27.0/options.go).
- [Pinned module dependencies](https://github.com/go-telegram/bot/blob/v1.27.0/go.mod).
- [gotgbot documentation](https://github.com/PaulSonOfLars/gotgbot/tree/v2) and [request implementation](https://github.com/PaulSonOfLars/gotgbot/blob/v2/request.go).

Revisit if library maintenance stalls, a required API capability is unavailable, or its polling semantics prevent the delivery guarantees selected for the application. This ADR records a source-based decision; no live Telegram validation or resource measurements were performed.
