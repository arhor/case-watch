# Go / Node.js resource comparison — 2026-11-16

Two repetitions of three scenarios per implementation completed successfully:
12 isolated trials, one synthetic case per trial. The live Go bot stayed running
and its configuration, token and database were never accessed or changed.
All temporary benchmark containers and databases were removed.

## Results

RSS values are medians of the two per-trial window medians. CPU is the median
of the two window averages, expressed as percent of **one CPU core**.

| Scenario                  | Go RSS (MiB) | Node.js RSS (MiB) |     Go CPU (%) | Node.js CPU (%) |
| ------------------------- | -----------: | ----------------: | -------------: | --------------: |
| Idle long polling         |        12.34 |             68.23 | 0.000 observed |           0.008 |
| One manual status refresh |        13.08 |             73.95 |          0.097 |           0.390 |
| One scheduled refresh     |        12.78 |             73.92 |          0.049 |           0.390 |

| Additional metric                                         |         Go |     Node.js |
| --------------------------------------------------------- | ---------: | ----------: |
| Largest kernel-recorded process peak RSS across trials    |  13.59 MiB |   74.81 MiB |
| Threads during measured windows                           |          7 |           7 |
| Swap during measured windows                              |      0 MiB |       0 MiB |
| Median synthetic process-start-to-readiness, six launches |    0.148 s |     0.759 s |
| Uncompressed Docker image bytes                           | 33,896,669 | 349,505,803 |
| Uncompressed Docker image size (MiB)                      |      32.33 |      333.31 |
| Uncompressed Docker image size (decimal MB)               |      33.90 |      349.51 |

Node used about **5.5×** Go's idle RSS, **5.7–5.8×** during refreshes, and its
image is **10.3×** larger. Images include the complete runtime and base OS:
Node's Debian slim/V8/SQLite image versus Go's Alpine/static executable image.
Image sizes are Docker's logical uncompressed `.Size`, not registry download
sizes or unique on-disk blocks. No image optimization was performed.

Idle RSS trial medians were 12.31/12.38 MiB for Go and 68.25/68.22 MiB for Node.
Manual medians were 13.08/13.08 MiB versus 73.94/73.97 MiB; scheduled medians
were 12.78/12.78 MiB versus 73.88/73.97 MiB. Peak RSS includes startup and is not
a sampled median or a hard memory requirement.

## CPU interpretation

Each idle window sampled about 60.34 seconds after a ten-second warmup and
included a completed 59-second long poll plus a second polling request.
Go added no observable CPU tick in either idle window; Node added 0.01 and
0.00 CPU-seconds. Each active window covered about 10.27 seconds, including the
roughly eight-second five-request exchange and two seconds after completion.
Node added 0.04 CPU-seconds in each active window. Go added 0.00/0.02 seconds
for manual and 0.01/0.00 seconds for scheduled checks.

The kernel accounts CPU at 100Hz, so these tiny differences have coarse
resolution. An observed zero is not zero execution cost; precise ratios of
idle CPU would be misleading. This is a small-workload observation, not a
throughput or multi-owner capacity benchmark. Active CPU averages include
intentional network spacing waits.

## Environment and equivalence

- Linux ARM64 Raspberry Pi host, four CPUs, 4,146,240 KiB physical RAM; kernel
  `6.18.50+rpt-rpi-2712`. Go 1.27.3; Node 24.22.3, bundled SQLite 3.54.1 and
  ICU 78.3. Full runtime versions and source hashes are in `host.json`.
- Reference HEAD: `f6863f145fd9d77c381b666654716d182bebc5cb`.
  Node and benchmark files were uncommitted; hashes identify the measured
  production sources and launcher. No application source changed during trials.
- Requested container limits: 0.25 CPU, 128MiB memory, 64 tasks. Effective
  `cpu.max` was `25000 100000`; `pids.max` was `64` in every trial.
  **This host has no memory cgroup controller.** Docker discarded the memory
  limit (`HostConfig.Memory=0`), and no `memory.max`/`memory.peak` files existed.
  These results do not establish behavior under an enforced 128MiB cap.
- Both use one fresh SQLite database with one synthetic enrollment, no history,
  no cooldowns and an observation initially two minutes old. Real WAL/FULL
  persistence and production adapters/service coordination are exercised.
- The local fake server serves the same sanitized portal fixtures. Every active
  trial made exactly one bootstrap GET and four RPC requests, retained the real
  two-second spacing, and completed successfully. All trials recorded zero
  diagnostic lines. Idle trials made no portal requests.
- Manual requests enter through the real Telegram polling/intake/reply path.
  Scheduled checks traverse the same production inventory/check service calls,
  initiated by measurement-only wiring ten seconds after startup. The ordinary
  scheduler also runs; no real Warsaw slot occurred during these trials.
- Production sources are wired to local HTTP endpoints with a synthetic token.
  The Go probe replaces the executable in the existing Go container image;
  Node imports its production modules using a mounted measurement launcher.
  Internet/TLS costs, live-service variability, credential intake and enrollment
  are excluded. The readiness metric includes migration and synthetic seeding
  and uses warmed image/filesystem caches, so it is not a cold-start benchmark.
- `/proc/PID/stat` and `/proc/PID/status` are sampled every 250ms. CPU includes
  the process's thread-group user/system time; RSS includes file mappings and
  shared libraries. No application child processes existed. The Python driver,
  fake server, Docker daemon and live bot are excluded from application counters.
  PSS, cgroup memory and total host energy are not measured.
- Implementation order reverses in repetition two. No V8 heap flags, Go runtime
  tuning, worker tuning, cache flushing or host isolation was applied. Host
  background activity remains a source of variation; load averages are recorded.

Measured image IDs:

- Go: `sha256:2d1cdfeef681bcc3d06956e98786a33663a5de4382de0371c5db0b97290d8db7`
- Node: `sha256:792db207b956ed55a359e90b069364dc591bc252c3d85bf2c843f10da94ae7d9`

## Reproduction and validation

See [the harness instructions](README.md) for the exact build/run commands.
`measure.py` records `host.json`, twelve raw JSONL sample files and
`results.json`; `summarize.py` derives `summary.json`. Setup/smoke runs are
excluded from this final dataset. All twelve raw sample files and workload
counts were checked after collection, and no synthetic container remained.

The normal Go suite and `go vet ./...` passed. The race-enabled suite was
attempted but failed before tests could run because ThreadSanitizer does not
support this host's VMA layout (`Found 47 - Supported 48`). It is not reported
as a passing race check. The Node image's 35 tests, formatting, syntax and
packaging checks passed before measurement. This task adds only measurement
wiring/documentation; application behavior is unchanged.
