# Kotlin/JVM Case Watch

Baseline: 6d04438aea4934187935586e33f3dee898a4a5c4 (clean).
Target: Kotlin 2.4.21, Java 25, Gradle 9.7.0, Linux ARM64.
Independent database; Go and Rust remain unchanged.

## Dependencies

Kotlin JVM and serialization compiler 2.4.21; coroutines 1.11.0;
serialization JSON 1.13.0; OkHttp 4.12.0; SQLite JDBC 3.54.1.0.
Official documentation: https://kotlinlang.org/docs/gradle-configure-project.html,
https://github.com/Kotlin/kotlinx.coroutines,
https://github.com/Kotlin/kotlinx.serialization,
https://square.github.io/okhttp/, https://github.com/xerial/sqlite-jdbc.
Use explicit Telegram API rather than a framework, JDBC rather than ORM/pool,
java.time rather than cron, environment parsing rather than dotenv.
JVM supplies timezone and TLS; import repository certificates into JVM trust.
SQLite JDBC requires its bundled ARM64 native library and writable extraction directory.

## Ordered milestones and behavior mapping

1. Configuration/scaffolding: preserve application/config.go validation and tests.
2. Storage: port database/repositories tests; one connection, WAL, FULL,
   foreign keys, 5000ms busy timeout, versioned migrations, atomic observations
   and history, restart, rollback, deletion tombstone and retained manual guard.
3. Portal: clients/gdansk_portal_client.go and tests; real Vaadin adapter,
   semantic discovery, four auth exchanges, fresh RPC state, owner cookies,
   six requests, two-second spacing, ten-second request and sixty-second check
   deadlines, 4MiB limit, no redirects/retries. Reuse sanitized fixtures.
4. Workflows: services/case_service_test.go; positive owners, one case,
   sixty-second completion freshness and durable guards, stale failure cache,
   shared flights, waiter cancellation, removal fencing and shutdown drain.
5. Telegram: bot_test.go and commands_test.go; exact Russian strings, private
   ownership, six commands/menu synchronization, UTF-8 byte limits, deletion,
   ten-minute expiry, minute sweeps, ordered polling, enrollment cancellation,
   fifteen-second serialized replies, unauthorized handling.
6. Schedule/lifecycle: schedule/jobs_test.go; Warsaw weekday slots
   08/10/12/14/16/18, strict future, scheduled-minute jitter, no catch-up,
   sequential owners, reject overlap, DST and drain. Fatal storage/token errors stop.
7. Packaging: ARM64 Java 25 container, independent volume, 90s shutdown grace;
   README with actual checks and resource-comparison prerequisites.

## Contracts and concurrency

Domain exposes typed status/enrollment/transition values and suspendable Portal
and Cases contracts. Adapters depend on domain. Inject clock, wait, HTTP transport
and endpoints. One SQLite worker serializes transactional operations. Short-lived
owner locks coordinate shared checks without covering portal I/O. Last waiter
cancels the flight. Removal fences commits, cancels/drains, then deletes.

## Acceptance

Deterministic fake endpoints, clocks and synthetic fixtures; no credentials.
Run ./gradlew --no-daemon formatCheck check installDist distTar.
Cover boundaries, failures, rollback/restart, concurrent waiters, cancellation,
late removal completion, DST/missed slots and shutdown.
No intentional behavior deviations. No benchmarks or deployment in this task.
Record toolchain/image versions, heap/GC/workers, controlled workloads,
startup versus JVM warmup, readiness, CPU/RSS/peak/artifact units and repetitions.

## Selected dependency details

| Purpose | Selection | Alternative and rationale | Runtime requirements |
| --- | --- | --- | --- |
| Runtime/build | Kotlin 2.4.21, Gradle 9.7.0, Java 25 | JVM selected by user over Native/GraalVM AOT; standard Gradle application distribution | Java 25, JVM warmup and default GC |
| Cancellation/concurrency | kotlinx-coroutines 1.11.0 | Structured coroutines over unmanaged futures or thread-per-owner | JVM coroutine dispatcher threads |
| Telegram/HTTP/session | OkHttp 4.12.0, explicit Bot API adapter | Avoid framework dispatch/retry policies; owner-specific cookie jars share pools | JVM trust store; callbacks canceled through complete body consumption |
| Portal JSON | kotlinx-serialization-json 1.13.0 | Semantic JSON trees over browser automation or JavaScript execution | JVM only |
| SQLite/migrations | sqlite-jdbc 3.54.1.0, embedded versioned SQL | Single JDBC connection instead of ORM/pool; user_version prevents unknown schema use | Bundled native SQLite, writable temporary directory |
| Schedule/time/config/logging | java.time and standard library | Six explicit weekday slots rather than cron; fixed diagnostics rather than HTTP logging | JVM timezone database |
| Verification | kotlin-test, coroutines-test 1.11.0, JDK HttpServer, ktlint 1.5.0 | Local deterministic endpoints rather than live credentials | Build-time tooling only |

## Implemented test mapping

- AdaptersTest: config sanitization, fixtures, restart/history/cooldowns/deletion,
  rollback when transition insertion fails.
- WorkflowTest: completion cache, shared manual flights, one canceled waiter,
  persisted transition, removal, strict-future schedule and both Warsaw DST changes.
- HttpTest: actual bootstrap/RPC HTTP adapter, credential payload, command menu,
  private ownership, Russian start text, no replay for 503 Retry-After: 0.
- CancellationTest: cancellation while the response body is stalled.

The source adapters expose clocks, endpoint and wait constructors for additional
boundary scenarios. Full live-service verification remains separate and requires
operator credentials; no live behavior claim is based solely on sanitized fixtures.

## Alpine minimal-runtime packaging revision

At the user's request, replace the Ubuntu JRE image with Alpine 3.24 and a
jlink runtime produced by the ARM64 Temurin 25 musl JDK. Keep the JVM target
and application dependencies unchanged. Include jdeps-discovered modules plus
the dynamically selected EC TLS provider; strip debug data, headers and manuals,
and compress runtime resources. Retain JVM timezone rules and repository trust
anchors. Install only libstdc++, zlib and CA certificates in the runtime image.

Acceptance: build ARM64 image, run formatter/full offline suite on musl, and run
a standalone probe using the actual trimmed JVM for SQLite transactions,
Warsaw DST, EC TLS handshake, trust anchors and cancellation. Preserve the old
image tag for comparison/rollback; use case-watch-kotlin:0.1.0-alpine for the new
image. Record exact modules, runtime/image sizes and verification in README.
No deployment or benchmarks are included.

Alpine revision acceptance completed: formatter, compiler checks, eleven offline
tests and release packaging passed under musl. The trimmed JVM probe passed in
both build and final OS stages. Network-disabled launcher smoke checks passed.
Final image is 119.67 MiB by Docker image-inspect accounting, versus 503.38 MiB
for the preserved Ubuntu image. README records modules, digests and exact results.

## Live measurement follow-up

User authorized temporary replacement of Go for resource measurements. Preserve
Go's volume, pause/re-enable its user-systemd updater, seed a fresh independent
Kotlin measurement volume, sample main-process procfs counters, and restore Go.
The host lacks the memory cgroup controller; CPU quota and PID limits are enforced.

The first live trial uncovered the default OkHttp 10s read timeout on Telegram
long polls. Explicitly set it to 65s and add an 11s delayed-update regression.
All 12 offline tests and trimmed-runtime probes passed before the repeat trial.
Report both the failed run and corrected measurements with their limitations.

Corrected five-minute trial completed with zero logged failures/restarts. Go and
its updater were restored and verified. Median post-startup RSS was 81.61 MiB,
peak RSS 101.95 MiB, post-startup CPU 0.029% of one core. Fresh Go baseline median
RSS was 13.56 MiB. No active case refresh occurred; report does not claim active
workload measurements. See measurements/REPORT.md and credential-free raw data.
