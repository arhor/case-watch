# Controlled Go / Node.js resource trials

This harness uses synthetic credentials and databases and binds a local HTTP
server. It never reads `.env`, copies the live database, changes the live
container, or contacts Telegram/the portal. Run it on Linux with ARM64 Docker
images. The Go probe is built from the current reference source; Node imports
production modules from the existing Node image. All additional wiring is here.

Build the Go launcher from repository root:

```sh
mkdir -p experiments/nodejs/measurements/.cache/tmp
GOTMPDIR="$PWD/experiments/nodejs/measurements/.cache/tmp" \
GOCACHE="$PWD/experiments/nodejs/measurements/.cache" \
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
  -o experiments/nodejs/measurements/go-probe-bin \
  ./experiments/nodejs/measurements/go-probe
```

With `case-watch-node:24.22.3` and `case-watch-case-watch:latest` already built:

```sh
python3 experiments/nodejs/measurements/measure.py
python3 experiments/nodejs/measurements/summarize.py
```

The runner requires Docker access, permission to bind loopback sockets and read
host `/proc/PID`, and approximately eight minutes for two repetitions of all
three scenarios on both stacks. It reverses implementation order on repetition
two, uses disposable containers and fresh synthetic databases, and removes each
container/database in a `finally` block. SIGINT/SIGTERM also trigger cleanup.
The output files are overwritten on repeat runs; archive a prior result first.

Each application receives one case with a successful observation aged two
minutes. Both use their real SQLite, Telegram long polling, cookie handling,
portal RPC parsing, cooldown and history code. `/status` arrives as a Telegram
update; the reply is audited for successful refresh. The scheduled launcher
initiates the scheduler's sequential inventory/check workflow ten seconds after
startup, alongside the ordinary scheduler loop, avoiding dependence on the real
Warsaw slot. A completion marker lets the controller audit scheduled success.
The fake portal serves the repository fixtures; each active trial must make
exactly one bootstrap GET plus four RPC requests. Real two-second request
spacing is retained, producing about eight seconds of portal work. TLS, Internet
latency, credential collection/enrollment and notifications are not workloads.

The ten-second warmup is followed by a sixty-second idle window or one active
refresh. Active sampling covers the last counter sample before the first
portal GET through two seconds after the completion marker/reply. Actual sample
boundaries and durations are recorded. The sample interval is 250ms; kernel
CPU accounting is 100Hz. CPU is percent of one core, not percent of the 0.25-core
quota. Zero CPU means no additional tick was observed, not zero execution cost.
RSS and process lifetime peak RSS are in MiB (1024² bytes). The peak includes
startup; RSS medians describe the selected measurement window.

Image sizes are Docker's uncompressed logical `.Size`, in bytes, including the
runtime/base layers. They are not compressed registry transfers, uniquely used
disk blocks, or JavaScript archive sizes. The Go image's original executable is
replaced by the small reference-source measurement launcher for resource
sampling; its reported image size remains that of the existing production image.
Startup readiness is first `getUpdates` receipt minus Docker's process start
timestamp; it includes fresh database migration and synthetic seeding, and is
not a production cold-start benchmark. Warm filesystem/image caches are not
flushed.

Limits requested are 0.25 CPU, 128MiB memory, and 64 tasks. The report and raw
results identify effective cgroup settings: this host may lack memory-controller
support, in which case 128MiB is **not enforced**. The driver/server/Docker daemon
are excluded from application CPU/RSS. The application process has no children;
its thread-group counters cover its native/libuv/Go threads. Host activity and
filesystem caching are uncontrolled; two repetitions with one case are a small
trial, not a capacity or throughput benchmark.

Artifacts:

- `host.json`: versions, image IDs/sizes, requested parameters, host/controller
  information, source hashes and compiled Go launcher hash.
- `go-*.jsonl`, `node-*.jsonl`: raw counters including startup/warmup.
- `results.json`: per-trial window summaries, workload audits and cgroup state.
- `summary.json`: medians and ranges across repetitions.
- `REPORT.md`: results and qualifications.

Verification before collection: Go normal tests and `go vet ./...` passed.
The Go race-enabled suite was attempted but cannot start on this host:
`ThreadSanitizer: unsupported VMA range; Found 47 - Supported 48`. This is a
platform limitation, not a passing race check. The Node implementation's 35
tests and release image checks passed before this measurement task; the probes
were syntax checked/formatted and active scenarios were smoke-tested before
collecting the reported dataset. Failed setup/smoke runs are excluded.
