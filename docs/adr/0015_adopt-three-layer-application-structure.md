---
status: accepted
date: 2026-11-05
decision-makers: Repository maintainer
---

# Adopt a three-layer application structure

## Context and Problem Statement

Case Watch currently spreads process construction, business workflows and infrastructure across packages named `application`, `service`, `tracking`, `notifications`, `data`, `telegram` and `scheduler`. Those names obscure the dependency direction: Telegram and scheduled execution initiate use cases, services enforce business behavior, and repositories persist state or communicate with external systems. The application is an undeployed personal work in progress, so understandable package ownership is more valuable than speculative abstraction.

ADR 0011 moved transaction boundaries out of repositories but retained the old package layout. This decision keeps its transaction contract while replacing its package-boundary guidance. The experimental persistent SQLite queue is intentionally retained, but it must not influence the production application structure.

## Decision Drivers

- Make the dependency direction visible from package paths.
- Keep business rules and transaction orchestration together.
- Prevent Telegram and scheduled execution from querying repositories directly.
- Avoid ports/adapters, hexagonal and Clean Architecture terminology and scaffolding.
- Preserve all current behavior without introducing HTTP or a command-line layer.
- Preserve persistent-queue experimentation without presenting it as production behavior.

## Considered Options

- Keep the existing packages and document their responsibilities.
- Adopt entry points, services and repositories with a small application bootstrap package.
- Adopt a ports-and-adapters or Clean Architecture package hierarchy.

## Decision Outcome

Use three application layers with dependencies flowing `entrypoints -> services -> repositories`.

- `internal/entrypoints/telegram` owns Telegram update intake and bot lifecycle.
- `internal/entrypoints/scheduler` owns Warsaw-time invocation of scheduled checks.
- `internal/services/tracking` owns enrollment, checking, removal, owner serialization and tracking transactions.
- `internal/services/notifications` owns durable notification delivery, retry policy, membership effects and notification transactions.
- `internal/repositories` owns SQLite, migrations, entity repositories and repository models.
- `internal/repositories/portal` owns the external case-portal client.
- `internal/app` constructs dependencies, monitors failures and owns process startup/shutdown.
- `internal/experimental` owns the persistent SQLite queue experiment directly, without another nested package.

Shared business values and narrow collaborator interfaces live in `internal/domain`; it is not an application layer. Interfaces remain only where required for tests or external communication. Do not add interface/implementation pairs merely to mirror layers.

Entry points call services only. The scheduler obtains its scheduled workload through a service operation rather than reading enrollment repositories. Telegram asks the tracking service whether an enrollment exists rather than receiving a repository reader. Services may use `repositories.Store.RunInTx`; external portal and Telegram calls never execute inside a database transaction.

There is no HTTP entry point, CLI command layer, ports/adapters directory, dependency-injection framework, event bus, or separate domain framework. `cmd/case-watch/main.go` remains a minimal call to `app.Run`.

### Consequences

- Package paths communicate ownership and dependency direction directly.
- Transaction orchestration remains explicit in services as required by ADR 0011.
- Moving packages creates a broad import and test diff even though product behavior is unchanged.
- The bootstrap package still coordinates several runtime components; it is lifecycle composition, not a business-service layer.
- Experimental queue code may use lower-level SQLite APIs and evolve independently, but production packages must not import it.

## Implementation Plan

- Move `internal/data` to `internal/repositories`, preserving `Store`, granular repositories, migrations and transaction behavior.
- Move the portal client to `internal/repositories/portal`.
- Move `internal/tracking` and `internal/notifications` beneath `internal/services` and update imports/tests.
- Move Telegram and the Warsaw scheduler beneath `internal/entrypoints` and update imports/tests.
- Add tracking-service operations for enrollment existence and scheduled checking so entry points have no repository dependency.
- Move process lifecycle code from `internal/service` to `internal/app`; keep path/lock validation there until it becomes independently reusable.
- Move `internal/data/scheduler.go` to `internal/experimental`, change its package, and keep its table in the initial migration. Correct the malformed primary-key declaration and align its column names with the experiment.
- Update `cmd/case-watch/main.go`, maintained architecture/storage/Telegram documentation and ADR 0011 cross-references. Do not add dependencies or configuration.

### Verification

- [x] Production business access follows `entrypoints -> services -> repositories`; Telegram delegates only Bot API transport operations to `internal/repositories/telegram` and does not access persistence repositories.
- [x] `internal/app` is the only production package constructing the complete runtime graph.
- [x] Telegram enrollment checks and scheduled inventory traversal go through tracking service operations.
- [x] SQL and migrations remain under `internal/repositories`; business transaction sequencing remains under `internal/services`.
- [x] Persistent queue code exists under `internal/experimental`, no production package imports it, and its schema can initialize.
- [x] Enrollment, manual and scheduled checks, removal, notification retries, membership recovery, cooldowns and shutdown behavior retain test coverage.
- [x] `go test ./...`, `go test -race ./...`, `go vet ./...` and `go build ./cmd/case-watch` pass with Go 1.27.3.
- [x] Maintained documentation matches the package structure, Markdown links resolve and `git diff --check` passes.

## Pros and Cons of the Options

Keeping the existing packages minimizes file movement, but documentation cannot make misleading dependency boundaries obvious at call sites and imports.

The chosen three-layer structure matches the maintainer's mental model and current application size. It incurs a one-time broad rename and retains a concrete Store transaction dependency in services.

Ports-and-adapters or Clean Architecture could isolate every dependency behind formal boundaries, but would introduce terminology, interfaces and indirection explicitly rejected for this application.

## More Information

The maintainer confirmed the structure after removing encryption and the former CLI commands. The persistent queue experiment belongs directly in `internal/experimental`; it is not scheduled for deletion.

### 2026-11-05: Implementation verification

Implemented the package moves, service-owned enrollment/scheduling queries, Telegram Bot API repository, direct `app.Run` startup and experimental queue isolation. Full ordinary and race-enabled tests, vet, the native application build, structural import checks, Markdown links and whitespace checks passed with Go 1.27.3. Existing localhost-only portal fixtures required sandbox escalation; no live endpoints were contacted.
