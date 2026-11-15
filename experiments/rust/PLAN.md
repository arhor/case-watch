# Rust Case Watch

## Identity

Baseline: `92ec138397d4edc6837fda3d12afe506374efee2`, clean when inspected.
Rust 1.100.0, edition 2024, Cargo; Linux ARM64 deployment and native development.
All experiment files remain here. Use a separate SQLite database. Do not change Go.
Use idiomatic Rust while preserving behavior defined by Go source and tests.

## Stack and interfaces

Pin Tokio 1.54.1, tokio-util 0.7.20, reqwest 0.13.5, serde 1.0.229,
serde_json 1.0.154, rusqlite 0.43.0 (bundled), chrono 0.4.45,
chrono-tz 0.10.4, uuid 1.29.2; commit Cargo.lock.
Official references: https://docs.rs/ (each crate's versioned documentation).
Use one Tokio worker and one SQLite worker, typed Telegram HTTP calls,
Rustls/system trust, embedded migrations and timezone rules. Disable HTTP retries.
Prefer these over a Telegram framework, SQLx pool, or cron runtime to retain
explicit cancellation, ordering, and single-connection transaction semantics.
Use std environment parsing and fixed diagnostic categories. Inject Portal,
Clock, wait functions and HTTP endpoints. Domain contracts cannot depend on adapters.

## Ordered milestones and acceptance

1. Scaffolding/configuration: preserve CASE_WATCH_TELEGRAM_BOT_TOKEN and
   CASE_WATCH_DB_PATH validation without dotenv. Match application/config_test.go.
2. Persistence: single connection; foreign keys, WAL, FULL synchronous, 5000ms
   busy timeout. Atomic observation/history and cooldown reservations. Removal
   nulls secrets and observations, retains tombstone/manual cooldown, deletes
   history/case cooldown. Match database and repository tests including restart.
3. Portal: translate bootstrap and incremental Vaadin RPC, semantic discovery,
   four authentication exchanges, verbatim stages, isolated owner cookies and
   shared connections. Six requests maximum, 2s start spacing, 10s requests,
   60s overall, 4MiB bodies, no redirects/retries. Reuse sanitized fixtures and
   port clients/*_test.go, including malformed messages and stale connections.
4. Workflows: match services/case_service_test.go. Completion-based 60s cache,
   durable 60s guards, manual/scheduled single flight, waiter cancellation,
   stale cache on failure, atomic history, removal fencing and shutdown drain.
5. Telegram: match telegram tests and exact Russian strings. Private ownership,
   command synchronization, /start /status /remove /cancel /help, byte limits,
   credential deletion, 10-minute conversations, minute sweeps, Да/Нет removal,
   ordered intake, cancellable enrollment, polling offsets and errors,
   serialized 15s reply attempts and unauthorized suppression.
6. Scheduling/lifecycle: match schedule tests. Warsaw weekdays at
   08/10/12/14/16/18, strictly future slots, no replay/catch-up, accept only
   scheduled-minute jitter, sequential owners, reject overlap, drain cancellation.
   Fatal storage/token errors stop application; close storage after task drain.
7. Verification/packaging: README with results and Linux ARM64 Dockerfile using
   repository CA certificates, independent volume and 90s shutdown grace.

## Checks

Run cargo fmt --check; cargo clippy --locked --all-targets -- -D warnings;
cargo test --locked --all-targets; cargo build --locked --release.
Build container with docker build --platform linux/arm64 -f Dockerfile ../..
from this directory. Use deterministic fake HTTP endpoints and synthetic data.
Test DST, exact boundaries, rollback, restart, cancellation, removal, and shutdown.
Never report unrun checks as passed.

## Comparison

No benchmarks or harness here. Document release launch, allocator, threads,
TLS/SQLite/timezone versions, hardware/container limits, identical workloads,
database sizes, endpoint latency, readiness and measurement boundaries,
startup versus warm steady state, repetitions, CPU/RSS/peak memory/artifact
units and tooling, raw results and variation in the eventual comparison.

## Dependency decisions

| Purpose | Pinned dependency and official reference | Decision / alternative |
| --- | --- | --- |
| Runtime, tests | [Tokio 1.54.1](https://docs.rs/tokio/1.54.1/tokio/) | Single event loop; avoid thread-per-request execution. Built-in test timers and TCP fakes. |
| Cancellation / draining | [tokio-util 0.7.20](https://docs.rs/tokio-util/0.7.20/tokio_util/) | CancellationToken and TaskTracker rather than detached tasks. |
| HTTP / Telegram / cookies | [reqwest 0.13.5](https://docs.rs/reqwest/0.13.5/reqwest/) | Rustls, shared pools, no retry policy, manual owner cookie jars; typed application adapter instead of teloxide dispatch policies. |
| Serialization / RPC | [serde 1.0.229](https://docs.rs/serde/1.0.229/serde/), [serde_json 1.0.154](https://docs.rs/serde_json/1.0.154/serde_json/) | Semantic JSON tree instead of executing JavaScript or scraping rendered HTML. |
| SQLite / migrations | [rusqlite 0.43.0](https://docs.rs/rusqlite/0.43.0/rusqlite/) | Bundled native SQLite and embedded SQL; avoid SQLx connection pools. Requires C build tools. |
| Calendar / timezone | [chrono 0.4.45](https://docs.rs/chrono/0.4.45/chrono/), [chrono-tz 0.10.4](https://docs.rs/chrono-tz/0.10.4/chrono_tz/) | Enumerate six local weekday slots, avoiding a general cron runtime. Embedded timezone data. |
| Identities | [uuid 1.29.2](https://docs.rs/uuid/1.29.2/uuid/) | Random v4 enrollment identity; no externally meaningful ordering. |
| Config / diagnostics | Rust standard library | Explicit environment parsing and fixed stderr categories; no dotenv or structured logger dependency. |

## Behavior mapping

| Requirement | Reference | Rust acceptance coverage |
| --- | --- | --- |
| Syntax-only startup validation, secret-free errors | application/config.go and config_test.go | config unit tests, startup smoke check |
| Private ownership, commands, exact Russian text, credential deletion, confirmation | telegram/bot.go and bot_test.go | tests/telegram.rs; embedded messages copied verbatim |
| Default command menu, polling, unauthorized errors | telegram/commands_test.go, bot_test.go, pinned Go bot polling source | Telegram startup/integration tests and polling implementation |
| Cache, durable guards, history, enrollment, removal, shared flights | services/case_service.go and case_service_test.go | tests/workflows.rs |
| Caller cancellation, last waiter, drain, shutdown | services/case_service_test.go | concurrent waiter, removal and shutdown tests |
| Live Vaadin protocol, fresh state, isolated cookie reuse, limits | clients/gdansk_portal_client.go and tests | tests/portal.rs, portal state/spacing unit tests, bounded deadline tests |
| WAL/FULL, migrations, rollback, restart and deletion | database and repositories tests | workflow restart, rollback, history and deletion tests |
| Warsaw DST, strict future slots, weekdays, no catch-up | schedule/jobs.go and jobs_test.go | schedule unit/integration tests |

Layout: src/{config,domain,storage,portal,service,telegram,schedule}.rs plus main.rs
and lib.rs; migrations/ contains embedded SQL; tests/ contains fake-server and
cross-layer tests. Store owns its SQLite connection on a dedicated thread and
returns owned domain values over oneshot channels. Case coordination holds a
short-lived state lock over database decisions, never across portal I/O. Flight
completion uses watch channels so completion cannot be missed. Removal marks the
owner before canceling work, drains it, then commits deletion. Clock and endpoint
constructors are test seams; Tokio timers support controlled deadline tests.

## Implementation completion

The runtime uses one event-loop thread, one SQLite worker, and at most one
Tokio blocking helper for platform DNS. No configurable worker pool is added.
The `Store::memory` constructor is an offline test seam; configured database
paths are resolved as filesystem paths, preserving Go path semantics.
Scheduler waiting is injected through `Wait`, with clocks and fake endpoints
available to integration tests. Release settings and platform differences are
listed in README.md. No intentional user-facing behavior deviations are introduced.

- [x] Identity, dependency decisions, behavior mapping, interfaces and milestones.
- [x] Isolated Rust implementation and independent persistent storage.
- [x] Formatter, Clippy, 23 offline tests and native release build.
- [x] Container/Compose instructions and resource-comparison checklist.
- [x] Final ARM64 container build and startup smoke check recorded in README.md.

## Alpine packaging revision

At the user's request, replace Debian runtime packaging with Alpine 3.24 and
build against `aarch64-unknown-linux-musl` using Rust 1.100.0-alpine3.24.
Enable `[profile.release] strip = "symbols"`; keep optimization, panic and LTO
settings otherwise unchanged. Run formatting, Clippy, all offline tests and
release compilation inside the musl builder. Verify static linkage and startup
before deploying `case-watch-rust:0.1.0-alpine` against the existing independent
Rust volume. Preserve `case-watch-rust:0.1.0` for rollback. This changes libc,
allocator implementation, symbol availability and image size; disclose those
differences in later resource comparisons.

Alpine verification completed: formatting, Clippy, all 23 tests, static stripped
release compilation, and the network-disabled startup smoke check passed.
Image and executable size readings are recorded in README.md. Compose was
updated to the Alpine image tag and redeployed against the existing Rust volume.

## Experiment closeout

The user confirmed the Alpine deployment behaved as expected. At their request,
the obsolete Debian image was removed, Rust was stopped, and Go was restored
with its original database and enabled automatic updater. The independent Rust
volume and Alpine image are retained. SUMMARY.md records verification, ad hoc
observations and comparison limits. Pull request work uses an isolated worktree
while the deployment checkout stays on master.
