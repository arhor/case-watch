---
status: accepted
date: 2026-10-31
decision-makers: Repository maintainer
---

# Choose Go for the Case Watch service

## Context and Problem Statement

Case Watch needs a programming language before application implementation begins. It is a private Telegram bot for the maintainer and his wife to track Polish administrative cases, initially through https://klient.gdansk.uw.gov.pl/. Users supply a case number and password; the intended service securely stores them, periodically checks for changes, sends notifications, and supports an on-demand `/status` command.

The deployment target is a Raspberry Pi 5 with 4 GB RAM running 64-bit Raspberry Pi OS (Linux ARM64). Available memory, other workloads, and application resource consumption have not been measured.

The maintainer has experience with Java, Kotlin, JavaScript, TypeScript, Scala, and Python, including Kotlin concurrency primitives, and wants to learn Go. Coding agents will do much of the implementation; the maintainer will review it and learn enough Go to assess authentication, credential handling, concurrency, cancellation, and failures.

This record follows [the ADR adoption decision](0001_adopt-architecture-decision-records.md).

## Decision Drivers

- Reliable status retrieval and straightforward maintenance take priority over executable size and startup speed, provided deployment fits the Pi.
- Prefer a simple deployment and modest runtime resource consumption.
- Learning Go is an explicit benefit, separate from unverified performance expectations.
- Support one portal and two users first.
- Prefer direct portal requests to avoid browser resource consumption.

## Considered Options

- Go.
- Kotlin or Java with Quarkus native image compilation.

## Decision Outcome

Choose Go for the service. The maintainer explicitly accepted this language choice after discussing its tradeoffs. Implementation and resource validation remain outstanding; acceptance of this record does not claim those checks have passed.

Attempt the portal integration using direct Vaadin Flow remote procedure calls (RPC) first. The inspected HAR (HTTP Archive) shows stateful JSON UI synchronization carrying a CSRF token, UI node identifiers, and synchronization counters. It does not establish a supported external API contract. The maintainer accepts maintaining this integration when the portal changes.

Use browser automation only if direct RPC proves unreliable or impractical to maintain, rather than requiring proof that it is technically impossible. Keep the browser mechanism and packaging undecided until needed.

Revisit the language choice if the integration experiment establishes a concrete reliability or maintenance advantage for Kotlin/Java with Quarkus. A portal integration failure alone does not establish such an advantage.

### Consequences

- The maintainer must learn Go conventions and review agent-generated code; prior concurrency experience helps but does not replace that work.
- Direct RPC requires session and UI synchronization handling, with accepted maintenance work when the portal changes.
- Deployment simplicity and resource efficiency motivate the choice but must be verified on the target device; no memory or binary-size claims have been measured.
- Kotlin/Java expertise is available as an alternative, but choosing it now would give up the explicit Go learning benefit without an established integration advantage.

## Scope and Non-goals

This decision selects the service language and the first integration approach. It does not choose a Go version, Telegram library, persistence system, credential encryption scheme, polling interval, browser library, or deployment packaging. Those choices require implementation context. Public access and support for additional portals are outside the initial scope.

## Implementation Plan

The following tasks remain pending; recording this ADR does not implement the application.

- [ ] Initialize `go.mod` and `go.sum` (if dependencies are required), selecting and documenting a supported Go version at implementation time. Add no dependencies for this documentation change.
- [ ] Build an isolated Go integration experiment under `cmd/portal-probe/`, with portal protocol handling under `internal/portal/`. No existing application patterns or runtime require migration.
- [ ] Demonstrate fresh-session authentication and status extraction, discovering current UI identifiers rather than hard-coding identifiers or cookies from a recording.
- [ ] Add behavior-focused protocol tests alongside the portal implementation using sanitized fixtures in `internal/portal/testdata/`. Keep actual credentials, session secrets, and personal case details out of fixtures and logs; use runtime configuration for probe credentials.
- [ ] Run repeated checks and exercise expired sessions, invalid credentials, and malformed or unexpected responses. Record results and resource measurements in `docs/portal-probe.md`.
- [ ] Measure idle and peak memory and check duration on the target Pi, including any child processes. Record available host memory and other workloads, then agree a resource budget before claiming deployment suitability.
- [ ] If RPC is unreliable or impractical to maintain, evaluate browser fallback and measure its full resource use before selecting its mechanism or packaging.
- [ ] After validation, implement the Telegram service in Go and reference this ADR from its entry point. Resolve persistence, credential protection, and scheduling separately.

### Verification

- [ ] The Go experiment builds for Linux ARM64 and executes on the target Pi.
- [ ] A fresh session authenticates and extracts status matching the portal display without captured session cookies or hard-coded UI node identifiers.
- [ ] Repeated checks and session renewal succeed; invalid credentials and protocol failures are reported as errors rather than status changes.
- [ ] Protocol tests pass with sanitized fixtures and no live credentials embedded in code or test data.
- [ ] Target-device resource measurements and an explicitly agreed resource budget are recorded in `docs/portal-probe.md`.
- [ ] The integration findings are reviewed for any concrete reason to revisit Go before proceeding with the full service.

## Pros and Cons of the Options

### Go

- Good: aligns with the maintainer's explicit learning goal and preference for a compiled service with simple deployment.
- Good: allows the integration experiment and eventual bot to use the same language.
- Bad: introduces unfamiliar language conventions into code the maintainer must review and debug.
- Bad: desired runtime efficiency is still an assumption for this application until measured.

### Kotlin or Java with Quarkus native image compilation

- Good: builds on the maintainer's existing language and concurrency experience.
- Good: offers a native executable deployment approach without changing to Go.
- Bad: adds a native image build and compatibility workflow that needs validation for Linux ARM64 and the selected dependencies.
- Bad: does not meet the Go learning goal, and no concrete portal integration advantage has been established.

## More Information

The maintainer confirmed the Go decision on 2026-10-31. Portal integration, resource suitability, and application implementation remain unverified. Local HTML and HAR recordings informed the discussion; this record intentionally contains no captured credentials or case data.

### 2026-10-31: Direct RPC experiment succeeded

The Go probe authenticated against the live Gdańsk portal from a fresh session and extracted the case stage in five HTTP requests, within the agreed six-request limit, with no retries. This validates the initial feasibility of the direct Vaadin Flow RPC approach selected above. Continue with direct RPC for this portal; the existing browser fallback criterion remains unchanged. See [the probe report](../portal-probe.md) for implementation and validation details.

Offline tests exercise the sanitized recorded exchange, fresh session cookies, synchronization counters, changed UI node identifiers, and failure handling. Linux ARM64 cross-compilation succeeded. These results support retaining Go and direct RPC; they do not establish production reliability or resource suitability on the target device.

Repeated live checks, session renewal, execution on the Pi, resource measurements, and an agreed resource budget remain outstanding. The earlier unverified statement records the state at acceptance; this dated entry records the subsequent findings without rewriting that history.
