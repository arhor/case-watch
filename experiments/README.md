# Case Watch technology experiments

Use this template to implement equivalent Case Watch services in different technologies so their computer resource usage can later be compared with Go. Preserve observable behavior while using idiomatic code and libraries for each stack.

This document is an instruction template for an agent. Follow the steps in order, producing a technology-specific plan before implementation. Benchmark tooling and actual measurements are separate tasks.

The template applies to any language or technology stack. Rust, Kotlin, and Node.js are examples, not a fixed set of supported technologies. Add another technology by following the same steps in a new experiment directory; no changes to this template are required.

## Example requests

- Implement Case Watch in `<technology>` following `experiments/README.md`.
- Implement Case Watch in Rust following `experiments/README.md`.
- Implement Case Watch in Kotlin following `experiments/README.md`.
- Implement Case Watch in Node.js following `experiments/README.md`.

For a planning-only task, prefix the request with “Create the implementation plan only.”

## 1. Define the experiment

Create `experiments/<technology>/`, choosing a descriptive lowercase directory name for the selected stack (for example, `rust`, `kotlin`, or `nodejs`). Keep all implementation files and technology-specific documentation there; leave the Go application intact.

Record the target language, runtime, compiler where applicable, package manager, versions, and deployment architecture. Explicitly resolve stack variants that affect the experiment, such as JavaScript versus TypeScript for Node.js or the runtime target for Kotlin. Choose supported versions and explain decisions that affect resource usage. Record the reference Go Git revision and any relevant uncommitted baseline changes.

**Deliverable:** an experiment identity section in `experiments/<technology>/PLAN.md`.

## 2. Establish the behavior baseline

Inspect the current implementation and its tests rather than relying on this template as a complete behavior specification:

- [Application and configuration](../internal/application), [entry point](../main.go), and [example environment](../.env.example).
- [Telegram conversations](../internal/entrypoints/telegram) and [scheduled checks](../internal/entrypoints/schedule).
- [Case workflows](../internal/services), [domain contracts](../internal/models), and [HTTP utilities](../internal/utils).
- [Portal client](../internal/clients) and [sanitized fixtures](../internal/clients/testdata/README.md).
- [Database, repositories, and migrations](../internal/database).
- [Build/run documentation](../README.md), [Dockerfile](../Dockerfile), and [Compose configuration](../compose.yaml).

Build a behavior checklist mapping each requirement to reference source/tests and planned target tests. Cover:

- Private-chat ownership, command menu synchronization, `/start`, `/status`, `/remove`, `/cancel`, `/help`, Russian messages, input validation, conversation expiry, and credential-message deletion.
- Enrollment, one tracked case per owner, cached status and freshness, failed refreshes, history, cooldowns, concurrent manual/scheduled checks, cancellation, and removal fencing.
- Actual Gdańsk portal protocol, session lifecycle, parsing, stage text, request limits, timeouts, and retry behavior. Fixtures support offline tests; they do not replace the live adapter.
- SQLite transactions, migrations, restart persistence, deletion semantics, and cleanup.
- Warsaw timezone and daylight saving, weekday schedule, missed slots, sequential execution, and shutdown.
- Startup validation, Telegram polling, bounded external work, errors, and diagnostics that do not expose secrets.

Derive exact timings and edge cases from the reference. Source and tests are authoritative; flag contradictions or missing specifications and resolve material ambiguities before coding. Do not silently simplify behavior or add retries, queues, or notifications absent from the baseline.

**Deliverable:** the behavior checklist in `PLAN.md`, including any explicit deviations and their comparison impact.

## 3. Identify and select dependencies

Research supported libraries using current official documentation. Cover Telegram long polling, HTTP and cookie sessions, portal protocol handling, SQLite and migrations, timezone-aware scheduling, configuration, logging, and testing. Standard library solutions are valid selections.

Write a dependency table with purpose, selected library/version, official documentation link, rationale, alternatives considered, and runtime/native-library requirements. Verify that the Telegram library and HTTP stack can support baseline cancellation, timeout, ordering, and test-endpoint behavior. Check packaging requirements for certificates, timezone data, and SQLite.

**Deliverable:** explicit dependency decisions in `PLAN.md`; pin dependencies using the ecosystem's manifests and lockfiles during implementation.

## 4. Define project layout and interfaces

Propose an idiomatic directory tree covering startup, domain workflows, Telegram, scheduling, portal access, storage, and tests. Preserve layered responsibilities without requiring a literal translation of Go packages or interfaces.

Specify the key contracts, data flow, error handling, transaction boundaries, cancellation, and concurrency model. Define test seams for clocks, waiting, Telegram endpoints, and portal responses.

Preserve `CASE_WATCH_TELEGRAM_BOT_TOKEN` and `CASE_WATCH_DB_PATH` and their baseline configuration behavior. Use an independent experiment database: compatibility with existing Go database files is not required, but persistence semantics are. Never commit credentials, `.env` files, or generated databases.

**Deliverable:** layout, interface decisions, configuration, and exact build/run/test commands in `PLAN.md`.

## 5. Write the implementation plan

Complete `experiments/<technology>/PLAN.md` before coding. Consolidate the preceding deliverables and add ordered milestones: scaffolding/configuration, persistence, portal integration, case workflows, Telegram, scheduling/lifecycle, verification, and packaging/documentation. For each milestone, identify its output and acceptance checks.

Resolve dependency and architecture choices so implementation can proceed without further design decisions. Record assumptions and unavoidable deviations. Ask the user only about material ambiguities that repository inspection cannot resolve. For an implementation request, continue after writing the plan; a planning-only request ends here.

## 6. Implement and verify equivalence

Implement incrementally against the plan. Add focused unit and integration tests mapped to the behavior checklist, including success, invalid inputs, external failures, cache/cooldown boundaries, concurrency/cancellation, removal, database restart, scheduling across DST, and shutdown.

Reuse sanitized portal fixtures where applicable and fake external services for deterministic verification. Use synthetic data; offline correctness checks must not require live credentials. Run the ecosystem's formatter, static checks, tests, and release build. Include concurrency checks where supported and relevant.

**Deliverables:** a working implementation and `experiments/<technology>/README.md` with setup, pinned toolchain requirements, commands, configuration, verification results, and known deviations. Never present unrun checks as passed. Correctness must be established before resource comparisons.

## 7. Prepare for resource comparison

Document a comparison checklist in the experiment README; do not implement a shared harness or run benchmarks as part of this template:

- Reproducible release build and launch commands, including container build/run instructions, architecture, runtime flags, heap limits, worker counts, and concurrency settings. Exclude development servers and watchers.
- Identical hardware, OS/container limits, workload, database seed size, external-service responses/latency, and measurement boundaries for every implementation, including Go. Prefer controlled external endpoints to avoid live-service variability.
- Scenarios for startup, steady idle polling, manual status requests, and scheduled checks. Record owner counts, request rates, duration, warmup, and repetitions when defining the later benchmark.
- Separate startup and warmed steady-state results, including JVM JIT and other runtime warmup effects. Account for the complete application process tree and distinguish process measurements from container measurements.
- Measurement tools and exact commands for startup time, idle/active CPU, resident and peak memory, and artifact/container size. Define units, readiness criteria, and sampling boundaries; report build costs separately from runtime costs if measured.
- Record reference and implementation revisions, dependency/toolchain versions, workload parameters, environment, raw measurements, and variation across runs. Disclose tuning and deviations that could change comparability.

## Completion checklist

- [ ] Technology-specific plan records the baseline revision, behavior mapping, dependencies, layout, milestones, and acceptance checks.
- [ ] Implementation is isolated under its experiment directory and preserves baseline behavior or documents deviations.
- [ ] Deterministic tests and required checks pass; exact commands and results are recorded.
- [ ] Release build, configuration, launch, and container instructions are reproducible.
- [ ] Resource-comparison prerequisites are documented; measurements remain a separate task.
