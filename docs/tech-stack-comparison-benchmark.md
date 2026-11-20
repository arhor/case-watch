# Tech stack comparison benchmark

Source revision: `107c5859fbdf7a8b8ef454aedeaf3f94ee7fec0f`. Each sampled metric reports the median and observed minimum–maximum across **10 repeated trials**. Image size is fixed production-image metadata, not a repeated timing sample. All four implementations ran sequentially on the same host; no historical measurements are mixed into these results.

Rust had the lowest median idle RSS at 5.89 MiB. Among the compiled versions, Kotlin/JVM had the shortest median fresh build at 32.51 seconds. Go had the shortest median incremental compiled build at 0.98 seconds. Go and Kotlin/JVM fresh-build ranges overlap. Node.js build timings cover validation and packaging. The tables below show the tradeoffs and observed variation; these results describe one synthetic case on this host.

## Release build and startup comparison

| Version | Fresh build, s | Incremental build, s | Startup readiness, s | Runtime image, MiB |
| --- | ---: | ---: | ---: | ---: |
| Go | 33.69 (31.99–45.17) | 0.98 (0.68–7.29) | 0.225 (0.207–0.316) | 32.33 |
| Rust | 268.17 (265.11–279.38) | 10.83 (10.54–11.14) | 0.210 (0.195–0.573) | 26.63 |
| Kotlin/JVM | 32.51 (31.70–35.30) | 20.99 (20.59–26.63) | 8.533 (6.715–9.016) | 119.67 |
| Node.js | 1.48 (1.47–2.16) | 1.49 (1.47–1.51) | 0.870 (0.661–0.992) | 186.99 |

## Idle long polling

| Version | Median RSS, MiB | Lifetime peak RSS, MiB | CPU, % of one core | CPU time, s | Threads, maximum | Window, s |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Go | 12.39 (12.33–12.42) | 12.39 (12.34–12.44) | 0.00103 (0.00094–0.00122) | 0.000621 (0.000567–0.000739) | 7.0 (7.0–8.0) | 60.37 (60.36–60.37) |
| Rust | 5.89 (5.89–5.91) | 5.92 (5.92–5.94) | 0.00109 (0.00091–0.00119) | 0.000661 (0.000547–0.000716) | 2.0 (2.0–2.0) | 60.37 (60.36–60.37) |
| Kotlin/JVM | 99.84 (98.88–101.45) | 99.84 (98.88–103.39) | 0.02141 (0.01899–0.05273) | 0.012926 (0.011465–0.031829) | 27.0 (27.0–27.0) | 60.37 (60.36–60.37) |
| Node.js | 74.28 (73.89–74.45) | 74.30 (73.91–74.47) | 0.00540 (0.00530–0.00613) | 0.003261 (0.003197–0.003703) | 7.0 (7.0–7.0) | 60.37 (60.36–60.37) |

## Manual status refresh

| Version | Median RSS, MiB | Lifetime peak RSS, MiB | CPU, % of one core | CPU time, s | Threads, maximum | Window, s |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Go | 13.09 (13.03–13.14) | 13.59 (13.48–13.64) | 0.096 (0.084–0.103) | 0.010 (0.009–0.011) | 7.0 (7.0–8.0) | 10.27 (10.27–10.27) |
| Rust | 6.14 (6.14–6.16) | 6.14 (6.14–6.16) | 0.055 (0.048–0.057) | 0.006 (0.005–0.006) | 2.0 (2.0–2.0) | 10.27 (10.27–10.27) |
| Kotlin/JVM | 101.15 (100.14–102.67) | 101.65 (100.78–103.17) | 2.222 (1.603–2.268) | 0.234 (0.165–0.239) | 32.5 (32.0–33.0) | 10.52 (10.27–10.52) |
| Node.js | 79.97 (79.73–80.09) | 80.88 (80.62–81.00) | 0.471 (0.464–0.491) | 0.048 (0.048–0.050) | 7.0 (7.0–7.0) | 10.27 (10.27–10.27) |

## Scheduled case refresh

| Version | Median RSS, MiB | Lifetime peak RSS, MiB | CPU, % of one core | CPU time, s | Threads, maximum | Window, s |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Go | 12.77 (12.67–12.89) | 13.25 (13.14–13.38) | 0.082 (0.070–0.086) | 0.008 (0.007–0.009) | 7.0 (7.0–7.0) | 10.27 (10.27–10.27) |
| Rust | 6.11 (6.09–6.11) | 6.11 (6.09–6.25) | 0.112 (0.108–0.117) | 0.011 (0.011–0.012) | 2.0 (2.0–2.0) | 10.27 (10.27–10.27) |
| Kotlin/JVM | 101.54 (100.09–103.95) | 102.24 (100.77–104.67) | 1.593 (1.310–2.081) | 0.166 (0.135–0.219) | 32.0 (32.0–32.0) | 10.52 (10.27–10.52) |
| Node.js | 79.88 (79.80–80.05) | 80.52 (80.45–80.69) | 0.415 (0.407–0.431) | 0.043 (0.042–0.044) | 7.0 (7.0–7.0) | 10.27 (10.27–10.27) |

## Method and interpretation

Fresh builds remove compiler outputs and compiler caches while retaining downloaded dependencies and toolchains. They exclude network installation, tests, container construction and OS page-cache eviction. Incremental builds follow each fresh build after changing an error diagnostic literal in the entrypoint source; dependency artifacts remain available. Kotlin uses in-process compilation within a single-use Gradle daemon, retaining dependency/transform caches while clearing project outputs and state for fresh builds. Wall time includes the Docker exec launch. Node.js is interpreted: its build validates JavaScript and creates an npm package, so its build cost is packaging rather than compilation.

Runtime trials use production adapters with measurement-only launch wiring, a fresh SQLite database and one synthetic case whose last observation is two minutes old. The local Telegram/portal server serves the same sanitized fixtures to all versions. Each successful active trial audits exactly one bootstrap GET, four portal RPC calls and a completion reply/marker. Real two-second portal request spacing remains enabled. Scheduled results include the measurement-only completion HTTP request and its client initialization/allocation costs; manual results include the normal Telegram reply. Small CPU differences therefore also contain probe overhead. Traffic uses loopback HTTP; TLS, real network latency, case tracking conversations and notifications are outside this workload. No live credentials, databases or Telegram/portal traffic are used.

Readiness is the first Telegram getUpdates request relative to the container process start. Startup table uses the ten idle trials, rather than pooling 30 startups. Each trial warms up for ten seconds; idle sampling then runs for 60 seconds. Active windows begin at the sample before the first portal GET and end two seconds after the successful reply/marker. RSS is sampled every 250 ms; lifetime peak RSS includes startup. The scheduler probe triggers the normal sequential service workflow after ten seconds rather than waiting for a real Warsaw schedule slot. Before launch, the controller waits outside imminent real schedule slots to prevent unrelated automatic work inside idle/manual trials. These short JVM trials include early-runtime/JIT behavior and do not establish fully warmed sustained throughput.

CPU uses Linux cgroup v2 `cpu.stat` usage counters with microsecond units and is expressed as a percentage of one core. Raw samples also retain process user+system ticks at the host clock resolution. Rounded zero values do not prove zero CPU work. RSS counts resident shared mappings as well as private memory; PSS and complete container memory are not measured. Child-process lists are retained in raw results. Runtime containers request 0.25 CPU, 128 MiB and 64 tasks. Effective controls are retained per trial; this host lacks a memory cgroup controller, so the requested memory cap is not enforced.

Runtime image sizes are Docker logical uncompressed image sizes, not registry transfer sizes or unique disk usage. The Go/Rust launchers and Kotlin application jar are replaced with measurement-only wiring while runtime base images remain unchanged; listed sizes refer to production runtime images. Build containers use available host CPUs without the runtime CPU quota. Builds are grouped by version in Go, Rust, Kotlin, Node order. Builds and runtime collection run in separate phases; host load and filesystem caches are not controlled. The experiment measures one case and cannot establish capacity or performance under sustained multi-user traffic.

## Verification

The Go normal test suite and `go vet ./...` passed. Its race suite could not execute on this host: ThreadSanitizer reported an unsupported 47-bit VMA range (requires 48 bits); this is not a passing race check. Rust format/clippy/tests, Kotlin format/tests/packaging/runtime probes and Node syntax/format/tests passed while preparing compiler images. All four stacks passed synthetic manual and scheduled smoke checks before collection. The report generator verifies exactly ten distinct repetitions per version and measurement type.

## Environment and retained results

- Host: Raspberry Pi 5 Model B Rev 1.1, `aarch64`, Linux `6.18.50+rpt-rpi-2712`; 4 logical CPUs; 4,146,240 kB total memory.
- Toolchains: Go 1.27.3; Rust/Cargo 1.100.0; Kotlin JVM plugin 2.4.21, Gradle 9.7.0 and Temurin OpenJDK 25.0.5+8; Node.js 24.22.3 and npm 11.19.1.
- Collection comprised 80 build observations and 120 runtime observations, with ten repetitions for every version and measurement type. Raw CPU/RSS summaries were independently recomputed, source/probe identities checked, and workload success audited before the report was retained.

This document preserves the final benchmark results, summary and methodology. The experimental implementations, build and measurement tooling, probes, raw observations and environment manifests have been removed from the current tree. Historical materials remain in [commit `efd607e`](https://github.com/arhor/case-watch/tree/efd607e/experiments), including the reproduction instructions and exact image/source identities. Reproducing the experiment requires checking out that historical revision and obtaining its recorded toolchains and images.
