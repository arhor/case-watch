# Live Kotlin resource trial — 2026-11-15

Go was temporarily replaced by Kotlin, then restored with its original database
and image. The systemd user updater is enabled and active again. Kotlin is stopped;
its independent volumes are retained. No experiment observations were merged into
Go's database. Temporary credential-bearing database snapshots were removed.

## Corrected-run results

| Metric | Go idle baseline | Kotlin after first 60 seconds |
| --- | ---: | ---: |
| Sample duration | 60 seconds | 240 seconds |
| Median resident memory (RSS) | 13.56 MiB | 81.61 MiB |
| RSS range | 13.55–13.56 MiB | 81.53–81.78 MiB |
| Process lifetime peak RSS | 15.36 MiB | 101.95 MiB |
| Average CPU, percent of one core | 0.00% observed | 0.029% |
| CPU time added during window | 0.00 s observed | 0.07 s |
| Threads | 7 | 26–29 |
| Swap | 0.25 MiB | 0 MiB |

Kotlin ran for 300 seconds, from 21:48:35 to 21:53:35 UTC. Its first 60 seconds
added 2.51 CPU-seconds; total process CPU time at completion was 2.62 seconds.
The kernel-recorded peak captures short spikes between samples, explaining why
peak RSS exceeds the largest five-second sampled RSS. Go's lifetime peak covers
a different process age and is not a matched cold-start benchmark.

Kotlin logged zero polling/service/storage/reply failures and had zero restarts.
The database retained one active case, with unchanged observation/history and
cooldown metadata: no case-refresh attempt occurred. These are startup and idle
long-polling results. No active `/status` or scheduled portal-check measurement
is claimed. This Sunday window had no scheduled weekday checks.

## Environment and method

- Linux ARM64, four host CPUs, 4,146,240 KiB physical RAM; kernel
  `6.18.50+rpt-rpi-2712`. Kotlin uses Alpine 3.24, musl, Temurin 25.0.5+8 and the
  trimmed eight-module JVM described in the experiment README.
- Both deployments requested 0.25 CPU, 128 MiB memory and 64 tasks. Confirmed
  effective `cpu.max` was `25000 100000`, and `pids.max` was `64`.
- This host has **no memory cgroup controller**. Docker reported `0B / 0B`;
  the requested 128 MiB memory limit is unenforced. These results do not establish
  behavior under a 128 MiB hard cap. JVM heap/GC settings were not additionally tuned.
- Samples read host `/proc/PID/status` and `/proc/PID/stat` every five seconds.
  CPU uses aggregate process/thread-group user+system ticks at 100 Hz, divided
  by elapsed monotonic time. CPU percentages refer to one core, not quota share.
  RSS includes resident file mappings and shared libraries; PSS/container memory
  were not measured. Go's observed zero CPU delta is limited by tick resolution.
- A fresh, consistent snapshot of stopped Go was loaded into an independent
  SQLite schema: one enrollment, zero history rows and two cooldown rows.
  The original volume remained attached only to Go. Updater timer/service were
  paused before replacement; the trial's finally block restored Go and the timer.
- The Go deployed revision marker and repository HEAD were both
  `6d04438aea4934187935586e33f3dee898a4a5c4`; image:
  `sha256:54d4b4463b2b8b3ec691dc9cf047b69764a9bf6dc115a87b89659733d319767c`.
- Corrected Kotlin image recorded by deployment:
  `sha256:2bfe75ebee869be21ff01c13ce71e41192b1abf6354f1d619bc8927ea4ee40a2`.
  Kotlin source was uncommitted experiment work. Dependency versions are locked.
- Compose launch took 3.29 seconds until its command returned, **not** measured
  application readiness. No matched cold-start readiness benchmark was conducted.

This is one short live observation, not a controlled benchmark: durations/process
ages differ, live network latency and host activity were uncontrolled, and JVM
workload warmup/GC behavior under sustained active checks was not exercised.

## Failure discovered and corrected

The first five-minute run logged 21 polling failures. OkHttp's default read timeout
was ten seconds while Telegram long polling requested 59 seconds. An overall
65-second call timeout did not override that read timeout. Its measurements are
retained under `polling-timeout-trial/` and excluded from the table above.

The client now explicitly sets a 65-second read timeout. `PollingTest` delivers a
fake `/help` update after eleven seconds and verifies the response, detecting the
old timeout behavior. All twelve offline tests, formatting, release packaging
and both musl/trimmed-runtime probes passed before the repeat live deployment.
The repeat run logged zero failures. An earlier phase-event script error aborted
before sampling and automatically restored Go; `aborted-events.json` records it.

## Artifacts and commands

- `go-idle.jsonl`, `kotlin-live.jsonl`: raw, credential-free process counters.
- `events.json`: deployment, phase, log-audit and restoration timestamps.
- `host.json`: cgroup/host metadata; `workload.json`: aggregate workload audit.
- `summary.json`: derived windows, CPU calculations and RSS statistics.

From the repository root:

```sh
python3 experiments/kotlin/measurements/sample.py --pid PID --seconds 60 --interval 5 --output OUTPUT.jsonl
python3 experiments/kotlin/measurements/summarize.py
```

`trial.py` is the live orchestration used for the corrected run. It uses the
measurement Compose overlay and refuses to overwrite an existing measurement
database. Repeating it requires choosing a new independent volume first; do not
run it casually because it stops the live Go service. It never prints tokens or
case credentials. The application image was built using:

```sh
docker build --platform linux/arm64 -f experiments/kotlin/Dockerfile -t case-watch-kotlin:0.1.0-alpine .
```
