# Node.js experiment summary

## Outcome

Case Watch was implemented in JavaScript/Node.js and tested on Linux ARM64 on
2026-11-16. The reference Go revision was
`f6863f145fd9d77c381b666654716d182bebc5cb`.

The implementation includes the Gdańsk Vaadin RPC adapter, private Telegram
conversations and Russian responses, SQLite persistence/history, durable cache
and cooldown guards, shared manual/scheduled checks, cancellation/removal fencing,
Warsaw weekday scheduling, and graceful shutdown. Go application source and its
deployment configuration remain unchanged. See [PLAN.md](PLAN.md) for behavior
mapping and [README.md](README.md) for reproducible commands and limitations.

Offline tests and twelve controlled synthetic resource trials passed. No live
Telegram or portal checks were performed, and live case-refresh compatibility
has not been confirmed. Full equivalence with every reference Go test remains
unverified.

## Final implementation

- JavaScript ES modules, Node.js 24.22.3 LTS and npm 11.19.1. There is no compiler
  or transpiler; manifests and lockfile pin Prettier 3.6.2 as the sole development
  dependency. There are no third-party runtime dependencies.
- Alpine 3.24/musl runtime with the official Node executable, libstdc++, libatomic
  and CA certificates. Both base images are pinned by digest. npm, Yarn, tests,
  fixtures and development packages are excluded from the final image.
- One application event loop, native HTTP keepalive pools, owner-specific cookies
  and one synchronous SQLite connection. V8 heap and libuv worker settings are
  left at their defaults; synchronous database work briefly blocks the event loop.
- Bundled SQLite 3.54.1, full ICU 78.3 Warsaw timezone rules, Node's CA roots and
  the repository portal intermediate certificate. Independent database/volume;
  existing Go database files are not a supported compatibility interface.
- Retained final image: `case-watch-node:24.22.3-alpine`. The previous Debian slim
  image remains `case-watch-node:24.22.3`, with its recipe in `Dockerfile.debian`.
- Timestamps have millisecond rather than Go's nanosecond precision. The fixed-origin
  cookie jar implements domain/path/security/expiry handling without browser
  SameSite or public-suffix policy. No other intentional user-facing deviations
  are claimed.

## Verification

Locked dependency installation, Prettier, JavaScript syntax checks, all 35 offline
tests, release archive packaging and ARM64 Docker builds passed. The Alpine
Docker build runs all checks and tests in its verification stage, then runs all
35 tests again against the minimal final runtime's native libraries.

The runtime probe passed SQLite operations, Warsaw winter/summer offsets,
bundled CA-root availability and the portal intermediate's fingerprint in Node's
extra trust store. npm's absence from the final image was also verified. No live
TLS handshake against the portal was attempted.

Tests cover configuration and redaction, portal fixtures and request bounds,
cookie isolation, private ownership and credential deletion, conversation expiry,
command menus, polling/backoff and bounded intake, cache/cooldown boundaries,
restart persistence, shared checks and canceled waiters, removal fencing,
transaction rollback, shutdown, Warsaw DST, missed slots and scheduler overlap.
JavaScript has no direct equivalent of Go's race detector; deterministic tests
exercise async interleavings.

Go reference verification passed `go test -timeout 60s ./...`, `go vet ./...`
and a Go application build; the measurement launcher also compiled as a stripped
ARM64 executable.
`go test -race -timeout 90s ./...` was attempted but could not execute on this
host: ThreadSanitizer reports `unsupported VMA range` (`Found 47 - Supported 48`).
This platform limitation is not a passed race check.

## Observations and limits

Image sizes use Docker's logical image-inspect `.Size`, including the runtime and
base layers, rather than compressed registry downloads or unique disk blocks.

| Artifact     |            Initial Debian slim |           Final minimal Alpine |
| ------------ | -----------------------------: | -----------------------------: |
| Docker image | 349,505,803 bytes (333.31 MiB) | 196,075,668 bytes (186.99 MiB) |

Alpine packaging and excluding package managers reduced image size by **43.9%**.
The final image's Node executable is 128,247,648 bytes. This remains a full Node/V8
runtime with ICU and SQLite. See [the image comparison](measurements/IMAGE-SIZE.md)
for exact image IDs, build instructions and verification.

The following resource results describe the **earlier Debian image**, not Alpine.
Two repetitions per implementation/scenario used one synthetic case, fresh
independent databases and controlled local HTTP endpoints on the same ARM64 host.
Each idle trial had ten seconds of warmup plus sixty seconds of measured polling;
each active trial performed one real five-request portal exchange with the
production two-second request spacing. Active windows span approximately ten
seconds including two seconds after completion. Implementation order reverses
in repetition two.

| Metric                                            |              Go | Node.js / Debian |
| ------------------------------------------------- | --------------: | ---------------: |
| Median idle RSS                                   |       12.34 MiB |        68.23 MiB |
| Median manual-refresh RSS                         |       13.08 MiB |        73.95 MiB |
| Median scheduled-refresh RSS                      |       12.78 MiB |        73.92 MiB |
| Largest process lifetime peak RSS across trials   |       13.59 MiB |        74.81 MiB |
| Median idle CPU, percent of one core              | 0.000% observed |           0.008% |
| Median manual-refresh CPU, percent of one core    |          0.097% |           0.390% |
| Median scheduled-refresh CPU, percent of one core |          0.049% |           0.390% |
| Threads during measured windows                   |               7 |                7 |
| Swap during measured windows                      |           0 MiB |            0 MiB |

All twelve trials recorded zero diagnostics; each active trial completed exactly
one bootstrap and four RPC requests. Process counters were sampled every 250ms.
CPU accounting has 100Hz resolution, so observed zero is not zero execution cost
and tiny CPU ratios would be misleading. The driver/server and live bot are
excluded; application processes had no children. CPU and task limits were
confirmed, but the host lacks a memory cgroup controller: the requested 128MiB
limit was not enforced. RSS is not container memory or PSS.

This is a small controlled-workload trial, not a throughput/capacity benchmark.
Host activity and filesystem caches remain uncontrolled. TLS/Internet latency,
live credentials, sustained multi-owner load, production cold-start readiness
and comparable build costs were not measured. Scheduled workflow initiation uses
measurement wiring instead of waiting for a real Warsaw slot. Alpine CPU/RSS
have not been remeasured, so no runtime-memory improvement is inferred from the
smaller image. See [the report](measurements/REPORT.md),
[derived results](measurements/summary.json) and adjacent raw samples.

## Deployment after the experiment

Node was never deployed as the live bot. All synthetic trial containers and
databases were removed. The existing Go container remained running throughout;
its token, configuration, database and updater were untouched. Both Node images
are retained locally. No experiment observations were merged into Go's database.
PR preparation uses an isolated worktree so the deployment checkout stays on
`master`.
