# Rust experiment summary

## Outcome

Case Watch was implemented in Rust and tested on Linux ARM64 on 2026-11-15.
The reference Go revision was `92ec138397d4edc6837fda3d12afe506374efee2`.
The user confirmed that live Telegram status checks worked with both the initial
Debian deployment and the final Alpine/musl deployment.

The implementation includes the Gdańsk Vaadin RPC adapter, private Telegram
conversations and Russian responses, SQLite persistence and history, durable
cooldowns, shared manual/scheduled checks, cancellation and removal fencing,
Warsaw weekday scheduling, and graceful shutdown. Go source and deployment
configuration remain unchanged. See [PLAN.md](PLAN.md) for behavior mapping and
[README.md](README.md) for reproducible commands and verification details.

## Final implementation

- Rust/Cargo 1.100.0, edition 2024, with pinned dependencies and Cargo.lock.
- Alpine 3.24 runtime; static `aarch64-unknown-linux-musl` release executable
  with symbols stripped. The retained image is `case-watch-rust:0.1.0-alpine`.
- One Tokio event loop, one SQLite worker, and at most one blocking DNS helper.
- Bundled SQLite, Rustls/AWS-LC, system/repository CA certificates, and embedded
  Warsaw timezone rules. Independent Rust database and Docker volume.
- No intentional user-facing behavior deviations. Go database compatibility
  is not a supported interface; the live trial used a validated copy, leaving
  the original Go database untouched.

## Verification

Formatting, Clippy with warnings denied, all 23 offline tests, native GNU release
compilation, and ARM64 musl release compilation passed. The Alpine Docker builder
runs formatting, Clippy, and tests before producing the release binary. Container
startup smoke checks and Compose configuration validation also passed.

Pre-PR Go verification also passed: `go test -timeout 60s ./...`, `go vet ./...`,
and a Go build, all from the repository root. `go test -race -timeout 90s ./...`
was attempted but could not start on this host: ThreadSanitizer reports
`unsupported VMA range` (`Found 47 - Supported 48`). This is a local platform
limitation; the race check is not reported as passed.

The tests cover portal fixtures and request replay, cookie isolation, external
failures, private ownership and credential deletion, polling cancellation,
conversation expiry, cache/cooldown persistence, shared checks, removal fencing,
transaction rollback, shutdown, Warsaw DST, missed slots, and scheduler overlap.
Live status checks were confirmed manually by the user; no automated checks sent
Telegram messages using live credentials.

## Observations and limits

These are ad hoc readings from this host, not controlled Go-versus-Rust benchmarks.
Image sizes use `docker image inspect` and are uncompressed; executable sizes use
`stat`.

| Artifact | Initial Debian/GNU build | Final Alpine/musl build |
| --- | ---: | ---: |
| Docker image | 164,547,146 bytes (156.9 MiB) | 27,918,767 bytes (26.6 MiB) |
| Executable | 11,208,448 bytes (10.7 MiB) | 8,541,744 bytes (8.1 MiB) |

Alpine packaging reduced image size by approximately 83%. The libc, allocator,
static linkage, and stripping choices differ, so this does not establish runtime
performance differences.

The initial Debian deployment showed 10.6 MiB RSS and peak RSS, two threads, and
approximately 0% of one CPU core over a five-second idle sample. Docker memory
accounting was unavailable on the host, so RSS came from `/proc/1/status`.
Equivalent Alpine idle/active memory readings and controlled Go comparisons were
not collected.

Observed release compilation times were 3m 45s for the initial native GNU build,
23s for a later incremental native build, and 4m 33s for the musl build. Cache
state and build environments differ; these are not comparable timing benchmarks.
The musl figure excludes formatting, Clippy, tests, downloads, package installation,
and image export. No shared benchmark harness was implemented.

## Deployment after the experiment

The Rust container is stopped. Go is running again against its original database,
and its systemd automatic-update timer is enabled and active. The updater service
completed successfully. The deployment checkout remains on `master`; PR work uses
an isolated worktree so automatic updates can continue.

The obsolete Debian Rust image was removed. The Alpine image and independent Rust
volume were retained for future experiments. No Rust observations were merged back
into the Go database.
