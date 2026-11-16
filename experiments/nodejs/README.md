# Case Watch for Node.js

An isolated JavaScript implementation of the Go service at `f6863f145fd9d77c381b666654716d182bebc5cb`. The [plan](PLAN.md) maps behavior and tests to the reference. The Go application is unchanged.

## Setup and run

Pinned toolchain: **Node.js 24.22.3 LTS**, **npm 11.19.1** (bundled); Prettier 3.6.2 is the sole development dependency. There are no third-party runtime dependencies or compilation steps. Native SQLite 3.54.1 and ICU 78.3 come with this Node release. Use an official full-ICU build. Production architecture is Linux ARM64; local verification also used ARM64.

From `experiments/nodejs`:

```sh
npm ci --ignore-scripts
npm run format:check
npm run check
npm test
npm run build
```

`check` syntax-checks every JavaScript source/test/script with `node --check`; `test` runs deterministic unit and loopback integration tests with a 15s per-test timeout. `build` checks syntax and writes `/tmp/case-watch-nodejs-0.1.0.tgz` with `npm pack`. Normal production runs need no `node_modules` directory.

Set `CASE_WATCH_TELEGRAM_BOT_TOKEN` and `CASE_WATCH_DB_PATH` in the environment, then run:

```sh
export CASE_WATCH_DB_PATH=/absolute/existing/directory/case-watch-node.db
export NODE_EXTRA_CA_CERTS=../../certificates/certum-dv-tls-g2-r39-ca.crt
node src/main.js
```

Supply the token through your normal secret mechanism; the same token syntax as Go is required. Environment files are never automatically loaded. The database parent directory must already exist. Use a separate database from Go, Rust, and Kotlin; schema compatibility is not supported. SQLite recovery files are ignored by Git. The portal needs the repository's [public intermediate certificate](../../certificates/README.md), added above without disabling TLS verification.

SIGINT/SIGTERM stop polling and scheduling, cancel and drain active case work, clear drafts and cookie sessions, release HTTP pools, then close SQLite. Configuration/startup/service errors are redacted. Telegram credentials and remote response text are never logged.

## Behavior

The bot preserves the five Russian commands and conversations, private-chat ownership, credential deletion, ten-minute conversation deadlines, confirmation before removal, minute-based freshness and persistent attempt guards, atomic transition history, and shared manual/scheduled checks. One case per owner is tracked. The live portal adapter bootstraps Vaadin state and sends all four authentication RPCs with discovered component IDs, cookies, CSRF token and updated counters. Each check has a 60s deadline, 10s request deadlines, two-second request spacing and six-request budget. Responses are capped at 4MiB; redirects and network retries are disabled.

Checks run on Warsaw weekdays at 08:00, 10:00, 12:00, 14:00, 16:00 and 18:00, sequentially without notifications or catch-up. Recent cache/guards cause slots to be skipped without queuing retries. All ownership and eligibility decisions go through the case service. SQLite uses one synchronous connection with WAL, FULL synchronization, foreign keys, and a five-second busy timeout; network I/O occurs outside transactions.

## Containers

From repository root:

```sh
docker build --platform linux/arm64 \
  -f experiments/nodejs/Dockerfile -t case-watch-node:24.22.3-alpine .
docker run --rm --platform linux/arm64 --stop-timeout 90 \
  --cpus 0.25 --memory 128m --pids-limit 64 \
  -e CASE_WATCH_TELEGRAM_BOT_TOKEN \
  -e CASE_WATCH_DB_PATH=/data/case-watch-node.db \
  -v case-watch-node-data:/data case-watch-node:24.22.3-alpine
```

Or export the token and run `docker compose -f experiments/nodejs/compose.yaml up --build -d`. Compose uses an independent named volume and the same limits as Go. Do not run two consumers against the same Telegram bot token at once. Docker's context must be the repository root so verification can reuse the sanitized portal fixtures and install the certificate. The default build uses Node 24.22.3 on Alpine 3.24, with both base images pinned by manifest digest. The final stage copies only the musl Node executable and installs its runtime libraries and certificates; npm, Yarn, build dependencies and fixtures stay in verification stages. The build runs formatting, syntax checks, all tests, packaging and all 35 tests again in the final Alpine runtime plus SQLite, Warsaw DST and certificate probes; no development packages or test fixtures are shipped in the final image.

## Verification

Verified locally on Linux ARM64 with the pinned Node/npm versions:

- `npm ci --ignore-scripts`: passed using the lockfile.
- `npm run format:check`: passed.
- `npm run check`: passed.
- `npm test`: 35 tests passed, including offline portal and Telegram HTTP integration, cache/cooldown boundaries, restart persistence, transaction rollback, concurrency/cancellation/removal, bounded polling intake, DST, missed slots and shutdown.
- `npm run build`: passed; production archive generated.
- The original Debian ARM64 release build passed all 35 tests and its final-runtime probe. The Alpine build verification is recorded in [the image comparison](measurements/IMAGE-SIZE.md).

Loopback servers require an environment that permits local socket binding. Correctness verification used no live credentials, portal requests, or Telegram messages. JavaScript has no equivalent of Go's race detector; deterministic tests interleave async waiters, cancellation, removal and shutdown. Live portal compatibility can still change independently of this implementation.

## Differences and comparison considerations

- JavaScript timestamps have millisecond precision; Go records nanoseconds. Submillisecond eligibility distinctions are not comparable.
- Synchronous SQLite briefly blocks the event loop; there is one application thread and no application worker pool. SQLite is bundled native code, not Go's SQLite implementation.
- Full ICU provides Warsaw timezone rules instead of Go's embedded tzdata. Pin runtime/ICU and compare DST rules when updating.
- The small cookie jar handles domain, path, secure, expiry and max-age for the fixed portal origin. It does not implement a browser's public-suffix list, SameSite policy or third-party-cookie rules. There are no cross-origin requests or redirects. Go's configured cookie jar also has no public-suffix list.
- Soft deletion matches Go: credentials, cached observation, history and case cooldown are cleared; the tombstone retains owner ID and enrollment ID, and the owner's manual cooldown remains. This does not provide forensic erasure of SQLite pages.
- Node's default V8 heap, libuv worker pool, native allocation, connection pools and runtime JIT differ from Go. No heap tuning or process flags are applied. These costs must be included in resource comparisons.

The previous Debian recipe remains available as `Dockerfile.debian`; build it from repository root with `docker build --platform linux/arm64 -f experiments/nodejs/Dockerfile.debian -t case-watch-node:24.22.3-debian .`. Existing CPU/RSS measurements apply to that runtime, not Alpine.

## Measured resource use

The subsequent [controlled Go/Node resource trial](measurements/REPORT.md) records two repetitions of idle polling, manual refresh and scheduled refresh with synthetic endpoints. Node used 68.23 MiB median idle RSS and about 73.95 MiB during refreshes; its uncompressed image is 333.31 MiB. Raw samples, source/image hashes, reproduction commands and limitations are included. The host could not enforce the requested memory cap. Those CPU/RSS results and the 333.31 MiB image size describe the earlier Debian image; see [the Alpine image comparison](measurements/IMAGE-SIZE.md) for the smaller default image.

## Later resource comparison checklist

The isolated trial above covers one synthetic owner; broader comparisons still need the following controls:

- Record Go/reference and Node implementation commits, clean/dirty state, runtime/compiler and dependency versions, base-image digests, architecture, kernel, CPU, RAM, OS, cgroup limits, timezone data, certificate setup and configuration. Retain workload scripts, raw samples and variation between repetitions.
- Reproduce the commands above; launch production directly with `node src/main.js` (container PID 1), without npm, watchers, inspectors or coverage. Use one process/event loop, default V8 heap and `UV_THREADPOOL_SIZE` (default 4); record any later flags and apply equivalent disclosed limits. Build costs are separate from runtime costs.
- Use identical hardware, limits, database seed sizes/owner counts, fixture responses, latency and failure rates. Inject controlled endpoints through the constructors used by tests (not production environment settings); use the same adapter configuration for each stack. Keep portal request spacing and Telegram polling timeout equal. Never compare two implementations competing for a real bot's polling stream.
- Define startup, steady idle polling, manual status and scheduled-check scenarios separately. Specify owner count, rates, duration, warmup and repetitions before running. Include cache-hit and refresh workloads separately. Force equivalent starting cooldown/cache state, including WAL state and history volume.
- Startup readiness means `getMe` and command synchronization have completed and the first `getUpdates` has reached the controlled endpoint. Measure spawn-to-readiness separately from warmed steady-state. Record elapsed nanoseconds around spawn/readiness with the controller's monotonic clock. For whole-process peak memory/runtime diagnostics use `/usr/bin/time -v node src/main.js`; its wall time covers the complete process lifetime, not readiness alone.
- For process CPU/RSS use `pidstat -u -r -p "$PID" 1`; preserve `%CPU` and RSS KiB samples, duration and tool version. Use `/usr/bin/time -v` maximum RSS in KiB as a separate peak statistic. Include the entire process tree (direct Node launch has no application children), and distinguish host process metrics from cgroup metrics.
- For containers use `docker stats --no-stream case-watch-node` at a defined one-second cadence and read the applicable cgroup's `memory.peak` and `cpu.stat`; record units (bytes and CPU microseconds), resets, sampling window and measurement overhead. Resolve the actual container name/ID before sampling. Do not equate cached filesystem/cgroup memory with process RSS.
- Record production archive bytes with `stat -c %s /tmp/case-watch-nodejs-0.1.0.tgz`, full Node runtime bytes separately, and uncompressed image bytes with `docker image inspect case-watch-node:24.22.3-alpine --format '{{.Size}}'`. State compressed versus uncompressed units and whether shared layers are counted. Include the runtime when comparing Go binaries to JavaScript artifacts.
