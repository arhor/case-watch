# Case Watch Kotlin/JVM experiment

See [SUMMARY.md](SUMMARY.md) for the experiment outcome, verification, measured
resource usage, limitations and deployment state at close.

Kotlin implementation isolated from the production Go application. See PLAN.md
for the baseline, architecture and acceptance mapping. Uses Kotlin 2.4.21,
Java 25, Gradle 9.7.0 and locked dependencies. Development verification uses
Oracle GraalVM JDK 25.0.3+9.1 on Linux ARM64, running ordinary JVM bytecode.
It does not use native-image compilation.

## Build and run

From this directory, with Java 25 installed:

```sh
./gradlew --no-daemon formatCheck check installDist distTar
export CASE_WATCH_TELEGRAM_BOT_TOKEN='your-token'
export CASE_WATCH_DB_PATH='/absolute/path/to/independent-kotlin.db'
build/install/case-watch-kotlin/bin/case-watch-kotlin
```

No dotenv loader. Required configuration follows Go validation. Parent database
directories must exist. Never use the production Go database for this experiment.
The launcher enables Java native access for SQLite JDBC. JDBC extracts its
bundled platform library into the JVM temporary directory, which must be writable.
Dependencies include their native SQLite library; no separately installed SQLite
is required. Java supplies timezone rules and TLS trust. Logging uses fixed
categories and never prints HTTP response bodies, tokens or credentials.

From the repository root:

```sh
docker build --platform linux/arm64 -f experiments/kotlin/Dockerfile -t case-watch-kotlin:0.1.0-alpine .
```

The image imports repository CA certificates into JVM trust. For deployment,
export the token and use `docker compose -f experiments/kotlin/compose.yaml up -d`.
Compose uses the independent `case-watch-kotlin-data` volume and 90s shutdown grace.
Deployment is a separate operator action. Dockerfile base images are pinned by digest; record these together with the
resolved Java patch version before any resource comparison.

## Verification

Tests use local HTTP servers, synthetic credentials and the sanitized Go portal
fixtures. No live portal credentials are needed. Coverage includes configuration,
RPC component discovery, live adapter requests, Telegram startup/menu/private
ownership, transactional history, restart persistence, cooldown boundaries,
shared checks and canceled waiters, removal and Warsaw DST.

Verified on 2026-11-15:

- `formatApply formatCheck check installDist distTar`: passed using Gradle 9.7.0.
- Twelve offline tests: passed, zero failures/errors/skips after the live long-poll timeout fix.
- Compiler warnings are errors. ktlint 1.5.0 passes; its embedded compiler emits
  a Java 25 Unsafe deprecation notice during formatter startup.
- Packaged launcher with missing configuration: expected exit 1 and fixed
  `service operation failed` diagnostic.
- Dependencies were resolved with `--write-locks`; the wrapper distribution
  checksum is recorded in its properties.

The environment required network-enabled tool execution for Gradle and Docker;
no credentials or live portal calls were used for verification.

## Comparison prerequisites

Do not compare resource usage until the behavior checklist has been verified.
This directory contains no benchmark harness or measurements.

- Record implementation and Go revisions, dependency locks, JDK vendor/patch,
  native SQLite version, timezone rules and container image digests.
- Launch installed distributions directly; exclude Gradle, compilers and watchers.
  Record JAVA_OPTS, GC, heap limits and processor counts. The default experiment
  uses JVM ergonomics, coroutine default dispatchers, one SQLite worker and
  OkHttp dispatchers/connection pools. These differ from Go and must be disclosed.
- Use identical hardware, OS/container CPU/memory/PID limits, seed database size,
  owner count, request rates, controlled endpoint responses and network latency.
- Define readiness after Telegram validation/menu synchronization. Measure process
  creation to readiness separately from warmed JVM steady state; specify warmup,
  workload duration, repetitions and sampling boundaries.
- Include startup, idle long polling, manual checks and scheduled checks. Account
  for all JVM threads/processes; distinguish process RSS from container memory.
- Later commands may use `/usr/bin/time -v` for startup/peak RSS, `pidstat -u -r`
  for CPU/RSS sampling and `docker stats` for container observations. Record exact
  commands, units, interval, raw samples, variation and tool versions. Use `du -b`
  for distribution bytes and `docker image inspect` for image size. Report build
  costs separately from runtime costs.

## Remaining verification limits

Offline tests are not proof of full baseline equivalence. No live-credential test
or benchmark has been run. The eleven focused tests do not exhaust the full
Go failure/concurrency matrix; extend that parity coverage before resource comparison.

## Original Ubuntu container verification

The original Linux ARM64 image `case-watch-kotlin:0.1.0` built successfully with
`docker build --platform linux/arm64 -f experiments/kotlin/Dockerfile -t case-watch-kotlin:0.1.0 .`.
Image manifest: `sha256:bc7d56f88ad95beb06b7e8aea1f3b50f43ce4e71dfd81743f8e0f42994303136`.
Runtime: Eclipse Temurin OpenJDK 25.0.5+8 LTS.

Both startup smoke checks produced expected exit 1 with only the fixed diagnostic:

```sh
docker run --rm --network none case-watch-kotlin:0.1.0
docker run --rm --network none --tmpfs /data \
  -e CASE_WATCH_TELEGRAM_BOT_TOKEN=123:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  -e CASE_WATCH_DB_PATH=/data/test.db case-watch-kotlin:0.1.0
```

The second check uses a synthetic token and an ephemeral database; Telegram
startup validation cannot succeed with networking disabled. No deployment,
live-credential test, resource measurements or production database changes occurred.


## Minimal Alpine runtime

The current Dockerfile builds on pinned Temurin Java 25 Alpine, then copies a
jlink runtime into pinned Alpine 3.24. The application and locked dependencies
remain unchanged. The image tag is `case-watch-kotlin:0.1.0-alpine`; Compose uses
this tag. The original Ubuntu image remains available as `case-watch-kotlin:0.1.0`.

The runtime includes modules discovered by jdeps and the dynamically selected
`jdk.crypto.ec` provider. jlink strips debug information, headers and manuals and
compresses resources. JVM timezone data and default trust anchors remain, with
repository certificates imported into JVM trust. Native OS support consists of
musl, libstdc++/libgcc, zlib and CA certificates. SQLite JDBC supplies its ARM64
musl library; its temporary extraction directory must remain writable.

Docker builds run formatting, compiler checks, all offline tests and release
packaging on the musl JDK. A standalone RuntimeProbe then runs on the trimmed
JVM and again in the final minimal OS to exercise SQLite transactions/restart/
removal, Warsaw DST, coroutine cancellation, repository trust anchors and an
EC-certificate TLS handshake. Test classes and synthetic keys are excluded from
the final image. No credentials, live portal requests or benchmarks are needed.

For later comparisons, disclose the change from glibc to musl and from the full
JRE to a compressed module subset. JVM GC, heap and coroutine settings are
otherwise unchanged. Update the pinned JDK/base digests and rebuild when applying
runtime security updates; a custom runtime does not update itself.

### Alpine verification results (2026-11-15)

- ARM64 Docker build: passed, including `formatCheck check installDist distTar`
  and all eleven offline tests on the Alpine/musl JDK.
- Standalone trimmed-JVM probe: passed in both builder and final minimal OS.
- Final launcher smoke checks with `--network none`: missing configuration and
  synthetic valid configuration/ephemeral database both returned expected exit 1
  with only `service operation failed`. These do not claim successful live login.
- Runtime: Eclipse Temurin 25.0.5+8, eight modules: `java.base`, `java.instrument`,
  `java.logging`, `java.sql`, `java.transaction.xa`, `java.xml`, `jdk.crypto.ec`,
  `jdk.unsupported`.
- Final manifest: `sha256:b42c2bfe3db180f4a4db508b725b410f15902e0c09f6bf2aef0ff0ff51147565`.

| Artifact | Size |
| --- | ---: |
| Original Ubuntu image | 527,834,388 bytes (503.38 MiB) |
| Alpine + jlink image | 125,484,487 bytes (119.67 MiB) |
| Trimmed Java runtime | 44,332 KiB on disk (43.29 MiB) |
| Application distribution | 17,276 KiB on disk (16.87 MiB) |

Image sizes use the same local Docker `image inspect --format '{{.Size}}'`
measurement; the reduction is 76.23%. Directory sizes use `du -sk` inside the
container and include filesystem block allocation, so they differ from apparent
file byte counts and do not sum to Docker's image accounting. This records
packaging sizes only, not CPU/memory benchmarks. The old Ubuntu image and all
production deployments remain untouched.

## Live resource trial

See [measurements/REPORT.md](measurements/REPORT.md) and the adjacent raw procfs
samples for the live deployment trial. The trial temporarily replaces Go, uses a
fresh independent copy of its data, pauses the updater, and restores Go afterward.
Memory cgroup support is absent on this host, so configured 128 MiB limits cannot
be enforced; reported memory is process RSS rather than Docker's unavailable
container-memory metric.

The first trial exposed OkHttp's default ten-second read timeout conflicting
with 59-second Telegram long polling. The client now explicitly allows a
65-second read timeout while retaining the existing overall request/coroutine
bounds. PollingTest verifies an eleven-second delayed update is handled. All
12 offline tests and Alpine runtime probes pass. Earlier trial data is retained
under measurements/polling-timeout-trial and excluded from the healthy idle
comparison. Docker image tag 0.1.0-alpine was rebuilt with the fix.
