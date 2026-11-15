# Kotlin experiment summary

## Outcome

Case Watch was implemented in Kotlin/JVM and tested on Linux ARM64 on
2026-11-15. The reference Go revision was
`6d04438aea4934187935586e33f3dee898a4a5c4`.

The implementation includes the Gdańsk Vaadin adapter, Telegram conversations
and Russian responses, SQLite persistence/history, cache and cooldown guards,
shared checks, cancellation/removal fencing, Warsaw scheduling and lifecycle
handling. Go source and deployment configuration remain unchanged. See
[PLAN.md](PLAN.md) for the behavior mapping and [README.md](README.md) for commands.
Full Go test-matrix equivalence remains unverified.

The corrected application completed a five-minute live idle-polling trial with
one copied case, zero logged failures and zero restarts. No active `/status` or
scheduled portal check occurred; live case-refresh behavior is not confirmed by
this trial.

## Final implementation

- Kotlin 2.4.21, Gradle Wrapper 9.7.0 and locked dependencies: coroutines 1.11.0,
  serialization JSON 1.13.0, OkHttp 4.12.0 and SQLite JDBC 3.54.1.0.
- Alpine 3.24/musl, Temurin Java 25.0.5+8 and a compressed `jlink` runtime with
  eight modules. Debug information, headers and manuals are stripped.
- JVM bytecode, requiring the bundled Java runtime; no standalone native executable
  or GraalVM native-image compilation.
- Explicit Telegram Bot API calls, owner-specific portal cookies, coroutine
  workflows and one SQLite worker/connection. JVM/HTTP pool sizing and heap/GC
  ergonomics remain at their defaults.
- Bundled ARM64 musl SQLite library, JVM timezone rules and repository certificates
  imported into JVM trust. Independent SQLite database and Docker volumes.
- Retained release image: `case-watch-kotlin:0.1.0-alpine`. The original Ubuntu
  image is also retained as `case-watch-kotlin:0.1.0`.

## Verification and live-trial fix

Formatting, compilation with warnings treated as errors, all twelve offline
tests, release distribution packaging and ARM64 Alpine container compilation
passed. The Docker build runs `formatCheck check installDist distTar`.

A standalone probe passed on the trimmed JVM in both the builder and final
minimal OS. It exercises SQLite transactions/restart/removal, Warsaw DST,
coroutine cancellation, repository trust anchors and an EC-certificate TLS
handshake. Network-disabled startup smoke checks returned the expected sanitized
errors. Test classes and synthetic keys are excluded from the final image.

The first five-minute live run logged 21 polling failures: OkHttp's default
ten-second read timeout interrupted Telegram's 59-second long poll despite the
65-second overall call timeout. The read timeout is now explicitly 65 seconds.
An eleven-second delayed-update regression test verifies the fix. The rebuilt
image's repeat trial logged zero failures. Failed-run data is retained separately
and excluded from the results below. An earlier measurement-script error aborted
before sampling and automatically restored Go.

Pre-PR Go verification passed: `go test -timeout 60s ./...`, `go vet ./...`
and a Go build. The required `go test -race -timeout 90s ./...` was attempted but
could not execute on this host: ThreadSanitizer reports `unsupported VMA range`
(`Found 47 - Supported 48`). This platform limitation is not a passed race check.

## Observations and limits

These are ad hoc observations, not controlled Go-versus-Kotlin benchmarks.
Current packaging sizes were read with `docker image inspect`, `stat` and
`du -sk`; Docker image accounting and directory block allocation are different
measurements and should not be added together.

| Artifact | Size |
| --- | ---: |
| Original Ubuntu/full-JRE image | 527,834,388 bytes (503.38 MiB) |
| Alpine/trimmed-JVM image, including polling fix | 125,484,513 bytes (119.67 MiB) |
| Application JAR | 174,957 bytes (170.86 KiB) |
| Application distribution with dependencies | 16.87 MiB on disk |
| Trimmed Java runtime | 43.29 MiB on disk |

Alpine and runtime trimming reduced the reported image size by 76.23%. The libc,
module set, compression and debug stripping differ, so this does not establish
runtime performance differences.

The corrected trial ran from 21:48:35 to 21:53:35 UTC. Samples read main-process
Linux procfs counters every five seconds, including all process threads. The
table compares a fresh 60-second Go idle baseline with Kotlin's final 240 seconds,
excluding its first minute.

| Metric | Go idle | Kotlin post-startup idle |
| --- | ---: | ---: |
| Median RSS | 13.56 MiB | 81.61 MiB |
| RSS range | 13.55–13.56 MiB | 81.53–81.78 MiB |
| Process lifetime peak RSS | 15.36 MiB | 101.95 MiB |
| Average CPU, percent of one core | 0.00% observed | 0.029% |
| Threads | 7 | 26–29 |
| Swap | 0.25 MiB | 0 MiB |

Kotlin's first minute added 2.51 CPU-seconds; total process CPU time at trial end
was 2.62 seconds. Lifetime RSS peaks cover different process ages and are not
matched cold-start measurements. Go's zero CPU delta is limited by 100 Hz tick
resolution. Thread names were not captured, so the JVM thread count has no
measured per-pool breakdown.

Both services requested 0.25 CPU, 128 MiB memory and 64 tasks. CPU and PID limits
were enforced, but this host lacks the memory cgroup controller: Docker memory
statistics were unavailable and the 128 MiB memory limit was unenforced. RSS is
not container memory or proportional shared-memory accounting. These results do
not establish operation under a 128 MiB hard cap.

Workload durations/process ages differ; host activity and live network latency
were uncontrolled. Active checks, sustained-load JVM warmup/GC, matched readiness
latency, repeated trials and comparable build costs were not measured. See the
[measurement report](measurements/REPORT.md), [derived results](measurements/summary.json)
and adjacent raw samples for methodology and provenance.

## Deployment after the experiment

At experiment close, Kotlin was stopped, Go was verified running against its
original image/database, and its systemd user automatic-update timer was enabled
and active. Independent Kotlin volumes and both Kotlin images were retained.
Temporary credential-bearing snapshots were removed. No Kotlin observations were
merged into Go's database.
