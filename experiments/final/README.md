# Final four-version experiment

The root [EXPERIMENT-REPORT.md](../../EXPERIMENT-REPORT.md) is generated only after
all 80 build trials and 120 runtime trials pass completeness checks. Each of the
four versions gets ten fresh builds, ten incremental builds, ten idle trials,
ten manual refreshes and ten scheduled refreshes. Startup uses the ten idle
launches, and every runtime trial records RSS, peak RSS, CPU and thread counts.

Run from the repository root on the same Linux ARM64 Docker host:

```bash
bash experiments/final/prepare-builders.sh
bash experiments/final/prepare-caches.sh
python3 experiments/final/make-probes.py
bash experiments/final/compile-probes.sh
python3 experiments/final/runtime.py --smoke
python3 experiments/final/builds.py
python3 experiments/final/runtime.py
python3 experiments/final/report.py
```

Do not run the build and runtime phases concurrently. Commands need Docker,
loopback networking, host `/proc` visibility and several GiB of temporary space.
Cgroup v2 CPU accounting (`cpu.stat`) must be available.
The existing production runtime images named in `runtime.py` must be available;
their content-addressed IDs are recorded and pinned for all trials in the run. Rebuild those images from their
respective repository Dockerfiles when reproducing on another host.

Fresh builds clear compiler outputs/caches but reuse downloaded dependencies.
The fresh Rust build recompiles dependencies too; the Go build recompiles its
standard library and dependencies. Kotlin keeps Gradle dependency/transform
caches, clears project outputs/state and compiles in the Gradle process. It uses
`installDist distTar`, with no formatter/test/runtime trimming in the timed step.
Incremental builds change an error diagnostic literal in the application
entrypoint, changing artifact content without changing dependencies or public APIs.
Each fresh build restores the original source first.
This models a small source-only edit, not a dependency or broad API change.
Node has no compiler output cache: both modes run its existing syntax-check and
npm packaging command. The incremental package includes the changed source.
Build container lifecycle and explicit output/cache deletion are outside timing;
Docker exec invocation and all build subprocesses are inside timing. Toolchain
setup/downloads and test runs are outside timing. No OS cache flush is attempted.

The runtime controller reuses the synthetic protocol server and `/proc` sampling
logic from `experiments/nodejs/measurements/measure.py`. New Rust/Kotlin probes are
generated from production entrypoints by `make-probes.py`; Go/Node use the existing
measurement launchers. All real portal, database, service, Telegram and scheduler
modules remain in use. Only endpoints, synthetic database seeding and a timed
scheduled-workflow trigger differ. Kotlin's probe jar replaces the production jar
inside the existing trimmed JVM image; Rust/Go mount their compiled probe binary.
The driver audits workload success and container liveness before accepting data.
An active refresh has exactly one bootstrap GET and four portal RPCs. Its result
must be a successful manual reply or scheduled completion marker. Trial order
rotates across versions each repetition to reduce systematic ordering effects.
The controller waits outside imminent real Warsaw schedule slots before launching
a trial, keeping the normal scheduler enabled without contaminating idle/manual
measurements with an unrelated timed refresh.

Each runtime trial starts a new process and database, warms up for ten seconds,
then measures sixty seconds of idle polling or one active refresh. This is an
early-runtime experiment; ten seconds does not establish JVM steady-state JIT
warmup. CPU uses cgroup v2 `cpu.stat` microsecond usage deltas for the complete application
container. Raw process thread-group ticks are also retained. Displayed zero CPU
can reflect rounding, not zero work. Peak RSS is
kernel lifetime high-water RSS, including startup. Process samples exclude the
fake service, Docker daemon and measurement driver. Scheduled trials include the measurement-only completion
HTTP request and its client initialization/allocation costs; manual trials include
the normal Telegram reply. Small CPU differences therefore also contain probe
overhead and should not be read as a pure service-function microbenchmark.
Runtime limits are requested
at 0.25 CPU, 128 MiB and 64 tasks; effective settings are recorded per trial.
No live bot credentials or existing databases are used or modified.

Raw counters are retained as JSONL, summaries as JSON, build commands and toolchain
identities in `build-host.json`, runtime image/source identities in `host.json`.
Ignored build/runtime logs are local diagnostics; generated probe binaries/jars
are also ignored. Failed smoke trials use repetition zero and must not be counted
in the final data. Collection scripts resume completed version/build groups and individual runtime
trials. Archive or remove prior result JSON and metadata before starting a new
experiment; do not combine runs with different source, images or parameters. A failed run raises an error rather than fabricating a row.
