# Case Watch in Rust

Independent Rust implementation of the Go baseline recorded in [PLAN.md](PLAN.md).
The Go application and its database are unchanged. Do not point this experiment at
an existing Go database.

## Build and run

Install Rust 1.100.0 using rustup; the toolchain file also installs rustfmt and
Clippy. Native build requirements: a C compiler, CMake, make, and system CA
certificates. SQLite is compiled into the application; Rustls uses system trust
and AWS-LC. No OpenSSL or external SQLite library is required at runtime.

From `experiments/rust`:

```sh
cargo build --locked --release
export CASE_WATCH_DB_PATH="$PWD/case-watch-rust.db"
# Set CASE_WATCH_TELEGRAM_BOT_TOKEN in your shell using your local secret source.
./target/release/case-watch-rust
```

Both environment variables are required. The application does not load `.env`.
The database parent directory must already exist. SIGINT and SIGTERM stop polling
and scheduling, cancel and drain case work, and close SQLite. Diagnostics contain
fixed categories, never remote errors, request URLs, credentials, or payloads.

```sh
cargo fmt --check
cargo clippy --locked --all-targets -- -D warnings
cargo test --locked --all-targets
```

Tests use the repository's sanitized portal fixtures and loopback HTTP servers;
no live credentials or network services are needed. A sandbox must allow binding
loopback ports. Fixture paths intentionally refer to the Go baseline testdata.

## Container

Run from the repository root:

```sh
docker build --platform linux/arm64 -t case-watch-rust:0.1.0-alpine -f experiments/rust/Dockerfile .
docker run --rm --name case-watch-rust --stop-timeout 90 \
  --env CASE_WATCH_TELEGRAM_BOT_TOKEN \
  --env CASE_WATCH_DB_PATH=/data/case-watch-rust.db \
  --mount type=volume,src=case-watch-rust-data,dst=/data \
  case-watch-rust:0.1.0-alpine
```

Alternatively, with the token exported, run
`docker compose -p case-watch-rust -f experiments/rust/compose.yaml up --build -d`.
Compose uses an independent volume, versioned local image, ARM64 platform,
90-second shutdown grace, and the reference CPU/memory/PID limits. The image
uses Alpine 3.24 with a stripped, static ARM64 musl executable and installs
system trust and repository CA certificates. The builder runs formatting, Clippy,
and all offline tests on musl before producing the release binary. Warsaw timezone rules are
embedded via chrono-tz, so runtime tzdata packages are unnecessary.

## Verification and differences

Verified on 2026-11-15, native `aarch64-unknown-linux-gnu`, Rust/Cargo 1.100.0:

| Check | Result |
| --- | --- |
| `cargo fmt --check` | Passed |
| `cargo clippy --locked --all-targets -- -D warnings` | Passed |
| `cargo test --locked --all-targets` | Passed: 23 tests |
| `cargo build --locked --release` | Passed |
| Native startup smoke checks | Missing token and inaccessible database rejected with exit 1; diagnostics omit values |
| Compose `config --quiet` with synthetic token | Passed |
| `docker build --platform linux/arm64 -t case-watch-rust:0.1.0 -f experiments/rust/Dockerfile .` (repository root) | Passed |
| `docker run --rm --network none case-watch-rust:0.1.0` | Passed: executable starts and rejects missing configuration with exit 1 |

The tests cover protocol fixtures and request replay, cookies and failures,
private conversations and credential deletion, polling cancellation, expiry,
cache/guard persistence, concurrent checks, removal fencing, history rollback,
fatal storage errors, shutdown, Warsaw DST, missed slots, and scheduler overlap.
These original correctness checks used offline services. The application was
subsequently deployed with live credentials, and ad hoc resource readings were
taken at the user’s request; no controlled benchmarks have been run.

The database has independent migration bookkeeping (`user_version`) and uses the
baseline logical tables and transaction boundaries. Rust uses bundled native
SQLite, Rustls/AWS-LC instead of Go TLS, and chrono-tz's embedded timezone data.
The process runs one Tokio event-loop thread plus one dedicated SQLite thread;
Tokio also permits at most one blocking helper thread for DNS resolution. There
is no runtime heap limit or configurable concurrency setting.
The default release profile uses optimization level 3, unwind panics, no LTO,
symbol stripping, and the system allocator (musl inside the Alpine image; glibc
for native GNU builds). These choices must be disclosed in comparisons.

## Alpine packaging verification

Verified on 2026-11-15 for `aarch64-unknown-linux-musl`, using
`rust:1.100.0-alpine3.24` and `alpine:3.24`:

- Docker builder: formatting and Clippy passed; all 23 offline tests passed.
- Release build passed in 4m 33s (compilation only; excludes earlier checks,
  image downloads, package installation and export).
- `file` reports a statically linked, stripped ARM64 ELF executable.
- Network-disabled image startup smoke check passed: missing configuration
  produces the expected error and exit code 1.
- Compose configuration validated with the existing deployment environment.

Ad hoc size readings using `docker image inspect` and `stat` (uncompressed image
size, not download size):

| Artifact | Previous Debian/GNU image | Alpine/musl image |
| --- | ---: | ---: |
| Docker image | 164,547,146 bytes (156.9 MiB) | 27,918,767 bytes (26.6 MiB) |
| Executable | 11,208,448 bytes (10.7 MiB) | 8,541,744 bytes (8.1 MiB) |

The image is approximately 83% smaller. This is a packaging comparison; changing
libc also changes the allocator, so CPU/memory behavior needs separate measurement.
The existing `case-watch-rust-data` volume is reused on deployment. The Debian
image was removed after the live trial; Go and its automatic updater were restored.
The Alpine image and independent Rust data volume are retained. See
[SUMMARY.md](SUMMARY.md) for the experiment outcome and measurement limits.

## Future resource comparison checklist

Do not infer resource equivalence from correctness tests. No measurements or
shared benchmark harness are included in this experiment.

- Record both Git revisions, Cargo.lock, `rustc -Vv`, `cargo -V`, Go version,
  image digests, architecture, OS/kernel, CPU model and container limits.
- Use the release binary above with no watchers or development servers. Keep
  runtime flags, allocator, thread counts and resource limits in the run record.
- Match database seed size, owner count, manual request rate, external responses
  and latency, duration, warmup and repetitions. Use controlled Telegram/portal
  endpoints through the injected adapter constructors in a later harness.
- Cover startup, idle long polling, manual status checks and sequential scheduled
  runs. Define readiness as successful startup validation/menu synchronization
  and entry into polling; specify measurement boundaries in the later harness.
- Separate cold startup from warmed runs. Include the full process tree and
  distinguish host process measurements from container accounting.
- Proposed tools for the later harness: `/usr/bin/time -v` around a controlled
  startup/shutdown run (elapsed seconds and peak RSS KiB); `pidstat -u -r -p PID 1`
  for sampled CPU percent and RSS KiB; `docker stats --no-stream` for separately
  labeled container statistics; `stat -c %s target/release/case-watch-rust` for
  artifact bytes and `docker image inspect --format '{{.Size}}' case-watch-rust:0.1.0-alpine`
  for image bytes. Define readiness timing explicitly rather than treating the
  whole startup/shutdown lifetime as startup latency. Report build costs separately.
- Retain raw samples, workload definitions, units, warmup, repetitions and
  variation. Disclose TLS, SQLite, timezone, compiler and tuning differences.
