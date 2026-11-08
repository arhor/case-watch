# Implementation progress and handoff

2026-11-01: Step 1 defines the [product contract](product-contract.md), scoped ADR amendments and ordinary Go/context contracts in `internal/application/contracts.go`. It adds no adapters, persistence, key material or runtime behavior. Existing probe code remains an experiment. Read every accepted ADR, including dated amendments, before continuing.

| Step | Deliverable | Governing ADRs | State |
| --- | --- | --- | --- |
| 1 | Product commands, enrollment/removal contract, application boundaries | 0002–0009 | Implemented; offline Go verification recorded in task handoff |
| 2 | Strict startup configuration and shared authorization | 0005, 0006, 0008 | Implemented; focused offline tests and vet pass |
| 3 | AES-256-GCM versioned keyring, AAD binding, explicit provisioning | 0006, 0008 | Implemented; focused offline tests, race and vet pass |
| 4 | SQLite modernc v1.61.1, migrations, ownership and atomic baseline/observations | 0004–0006 | Implemented; offline tests, race, vet and ARM64 storage compilation pass |
| 5 | Persistent attempt reservations, cooldowns, ordered queues and recipient/global gates | 0004, 0007, 0009 | Implemented; focused offline tests, race and vet pass |
| 6 | Reusable bounded, cancellable portal adapter; no retry | 0002, 0007 | Implemented; offline portal tests, race and vet pass |
| 7 | Shared tracking coordinator, in-flight joins, ephemeral enrollment, fenced removal | 0004–0007, 0009 | Implemented; focused offline race tests and full vet pass |
| 8 | Telegram v1.27.0 long polling, private identity, English command flow and best-effort message deletion | 0003, 0005, 0006 | Implemented; offline adapter race tests and full vet pass |
| 9 | Durable ordered delivery, retries, unblock/recovery and sanitized error classification | 0004, 0005, 0009 | Implemented; offline worker/storage/tracking/Telegram race tests and full vet pass |
| 10 | Warsaw six-slot sequential scheduler, missed/overlap skips | 0007 | Implemented; offline scheduler/tracking race tests and focused vet pass |
| 11 | Startup validation, shutdown/draining and explicit operator maintenance | 0004, 0006, 0008, 0009 | Implemented; independent focused race tests passed |
| 12 | Non-root ARM64 Docker/Compose, persistent named volume and separate read-only key | 0008 | Artifacts and offline acceptance implemented; Docker lifecycle unverified (daemon unavailable) |
| 13 | Independent audit of contract, ADR compliance, security boundaries and offline evidence | 0002–0009 | Completed; three findings corrected and independently re-reviewed |

Contract names are intentionally small: atomic `Store.Enroll/Observe`, immutable `EnrollmentID`, ciphertext-only `SealedCredentials`, one-attempt `Sender.Send` with sanitized `SendOutcome` (category/message ID/retry-after), `Portal.Check`, `Policy.Allows`, `Clock.Now`, owner-facing `Tracking.Enroll/Status/Remove`, transient `Credentials`, `Observation` and `Status`. SQL and Telegram types stay in adapters. Extend storage reservation/removal/queue interfaces only when their consumers exist; the atomic enrollment/observation and one-attempt sending boundaries define this milestone without freezing a speculative repository API. `Tracking.Remove` requires prior explicit confirmation at the conversation boundary; its implementation must enforce ownership, cancellation/draining and durable identity fencing. The step 8 command adapter is implemented; runtime wiring remains step 11. Drafts and confirmation tokens are ephemeral, while cooldowns and recipient/global gates survive removal and restart.

Repository implementation and independent audit are complete. Docker image/lifecycle, live integration and target-device acceptance remain pending as recorded below and in [the independent audit](implementation-audit.md). Follow ADR 0008's no-backup amendment rather than historical backup requirements. Do not read or modify untracked HTML/HAR captures, introduce live checks, commit/push or deploy without separate authorization. Before repository tooling run `nvm use` (source `$NVM_DIR/nvm.sh` if necessary). Use a writable temporary Go cache if the sandbox denies the default cache.

## Step 2 evidence (2026-11-01)

Added `internal/config` strict environment syntax parsing, `internal/auth` shared eligibility and transport-independent private sender/owner validation, `cmd/case-watch run`, `.env.example` and `docs/configuration.md`. The command validates environment before any runtime operation and explicitly fails with `service lifecycle is not implemented`; it cannot pretend the service is running or implicitly provision keys. Path syntax validation does not read files. Later steps must enforce key-file separation/permissions/content and database/ciphertext validation before networking. Future storage operations must independently apply owner predicates; helper tests establish identity isolation, not a storage implementation.

Exact verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build gofmt -w internal/config internal/auth cmd/case-watch && GOCACHE=/private/tmp/case-watch-go-build go test ./internal/config ./internal/auth ./cmd/case-watch && GOCACHE=/private/tmp/case-watch-go-build go vet ./internal/config ./internal/auth ./cmd/case-watch`. All three test packages passed. Tests cover default restricted mode, whitespace/deduplication, invalid/missing settings and sanitized errors, malformed allowlists even under allow-all, shared eligibility, private sender validation, bot/group/channel/missing/mismatched identity rejection, ownership isolation, and explicit command failure. No dependencies, network calls, live secrets, key material, commits or deployments were introduced.

Additional focused verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build go test -race ./internal/config ./internal/auth ./cmd/case-watch && git diff --check`. All three race-enabled package tests passed and diff whitespace checks reported no errors.

## Step 3 evidence (2026-11-01)

Added `internal/credentials`, explicit `case-watch provision-key PATH KEY_ID`, and [key format/security documentation](credentials.md). Strict versioned JSON parsing rejects duplicate members/IDs, unknown members, empty IDs, absent active keys, invalid base64/key lengths, trailing JSON and unsupported versions. Loading pins a restricted directory and rejects symlinks, insecure permissions and special files; errors reveal no supplied paths, key values or credentials. AES-256-GCM uses standard-library random-prefixed nonces with purpose/version/key/destination-identity AAD. Provisioning creates random 32-byte keys exclusively without output or overwriting. The runtime placeholder still fails explicitly; startup record authentication, path separation checks, storage/enrollment integration and rotation lifecycle remain future work. No backups, real secrets or live requests were introduced.

Exact verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build gofmt -w internal/credentials cmd/case-watch && GOCACHE=/private/tmp/case-watch-go-build go test ./internal/credentials ./internal/config ./internal/auth ./cmd/case-watch && GOCACHE=/private/tmp/case-watch-go-build go test -race ./internal/credentials ./cmd/case-watch && GOCACHE=/private/tmp/case-watch-go-build go vet ./internal/credentials ./cmd/case-watch && git diff --check`. Tests cover synthetic roundtrip/fresh nonces, ciphertext plaintext exclusion, tampering, wrong/unknown key, changed record/key IDs, unsupported versions, truncation, strict malformed documents, explicit provisioning/no overwrite, missing files, insecure directory/file permissions, file/directory symlinks and nonblocking FIFO rejection. Test fixtures are synthetic and confined to temporary directories.


## Step 4 evidence (2026-11-01)

Added `internal/storage`, pinned `modernc.org/sqlite` v1.61.1 and transitive checksums, SQLite artifact ignores and [storage handoff](storage.md). One serialized connection uses DELETE journaling, FULL synchronization, foreign keys and a busy timeout. Schema v1 migration is transactional and rejects newer versions without reset. Immutable opaque IDs and a persistent identity registry support later deletion fencing. Owner predicates protect observations/reads; enrollment atomically stores ciphertext and baseline and refuses replacement. Observations atomically update cache/time, append individual history occurrences and enqueue durable owner-recipient records; unchanged stages refresh only time. Ordinary-value inventory/history APIs support later startup and coordination without exposing SQL types. No delivery, reservation, cooldown, removal or live-service integration is claimed.

Offline tests cover baseline without changes, unchanged refresh time, A → B → A ordering, identical concurrent observations, pending-insertion fault rollback of cache/history/queue, baseline-failure rollback of credentials/identity, duplicate-owner rejection, ownership isolation, durable reopen, future-schema rejection without file changes, concrete durable settings, ciphertext destination binding, and scanning the database plus an active rollback journal for synthetic plaintext credentials. Tests seal fixtures through `credentials.Seal`; no plaintext credential values enter SQL.

Exact verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test -race ./internal/storage && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test ./... && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go vet ./... && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /private/tmp/case-watch-step04-arm64 ./cmd/case-watch && git diff --check`.

After strengthening active-journal inspection, exact final verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod gofmt -w internal/storage && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test -count=1 -race ./internal/storage && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go vet ./... && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -o /private/tmp/case-watch-storage-step04-arm64.test ./internal/storage && git diff --check`. The storage test binary compilation specifically includes modernc; cross-compilation does not execute tests on ARM64 or measure Pi resource use. Dependency download used the authorized pinned-dependency network escalation. No portal/Telegram calls, real keys, raw capture access, commits, deployment or backups occurred.

## Step 5 evidence (2026-11-01)

Added transactional schema v1→v2 runtime migration, atomic persisted user/case attempt guards, ordered delivery reservations with cycle/lifetime fencing, sanitized outcomes and message IDs, retry deadlines, recipient/global pauses, startup uncertain-attempt recovery, explicit maintenance resume, block-evidence recovery, and atomic owner/identity removal retaining registry/user/recipient/global gates. SQL types stay private. Storage enforces mechanics; authorization, in-process check joins, send coordination, startup recovery invocation and maintenance entry points remain later steps. See [storage handoff](storage.md) for exact APIs and the requirement to serialize or retry membership recovery when a recipient send is active.

Focused offline tests cover concurrent check and send reservations; 59/60-second boundaries across reopen; crash recovery without budget reset or overtaking; stale completion rejection; sixth-failure retention and explicit resume preserving lifetime; rate gate retention through unblock; atomic removal with retained user/token/recipient gates, immutable identity reuse rejection and late observation/ack fencing; v1 history/queue migration; and injected rollback failures for manual guard, outcome/global gate and removal transactions. Earlier future-version and baseline/observation tests remain active.

Exact final verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod gofmt -w internal/storage && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test -count=1 -race ./internal/storage && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go vet ./internal/storage && git diff --check`. No network, raw captures, backups, commits, deployment or new dependencies.


## Step 6 evidence (2026-11-01)

Added `portal.Client` implementing `application.Portal`, with injectable endpoint, one-exchange transport, clock and cancellation-aware wait. Every check owns a fresh cookie jar, a 60-second overall deadline and 10-second request deadlines; production defaults retain six requests maximum and two-second start spacing. No redirects, automatic application retries or HTTP replayable request bodies are allowed; production transports disable connection reuse. The diagnostic Probe API shares the same implementation. Login nodes are rediscovered after each response, counters and numeric identifiers are validated as bounded integers, and bodies remain capped at 4 MiB. Successful observations preserve stage text and use completion time. Errors expose only authentication/transport/protocol/canceled/deadline categories and preserve context matching without wrapping remote errors.

Exact final verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test -race ./internal/portal && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go vet ./internal/portal ./cmd/portal-probe && git diff --check`. Portal race tests passed (11.611s), vet and whitespace checks passed. Localhost-only escalation was needed for existing httptest listeners; no live requests were made. New injected-transport tests cover consecutive fresh sessions, completion timestamps, spacing, request deadlines, non-replayable bodies, sanitized transport/authentication/protocol/oversize failures, owned overall/per-request deadlines and cancellation before/during request spacing. Existing fixture exchange, dynamic IDs, synchronization, malformed responses, expired sessions, request budget, redirects and diagnostic probe tests remain passing. No dependencies or raw captures were read or added. Repeated live/Pi validation remains outstanding.


## Step 7 evidence and consumer handoff (2026-11-01)

Added `internal/tracking.Coordinator`, implementing `application.Tracking`, with injected store/vault/portal/policy/clock, owner eligibility before data access, one bounded non-retried enrollment check, encrypted atomic baseline enrollment, serialized attempt reservation, and shared manual/scheduled flights. Failed checks retain the old successful stage/time. Completion time comes from the injected clock. Recent successful cache is reused by both entry points; persisted case/manual attempt guards apply at exactly 60 seconds. Cached replies and joins do not reserve again. `storage.CheckEligibility` is a read-only guard lookup for cached replies. Plaintext references are cleared on exit; Go does not promise secure erasure.

`tracking.New(store, vault, portal, policy, clock)` requires all dependencies and one shared instance for the process. `Status(ctx, owner)` is manual; `Scheduled(ctx, owner)` is the future scheduler entry point and applies the case guard without consuming the manual guard. The scheduler must also authorize its inventory before scheduling. `Status.Fresh` means this call received a successful refresh (including a joined refresh); cached replies are false even when the cache is recent. `NextEligibleAt` combines the relevant persisted start guards with successful-cache reuse deadlines. A failed actual attempt returns both an error and the previous observation. One canceled waiter does not cancel other waiters; the last departing waiter cancels the underlying bounded flight. The future service shutdown must cancel/drain its caller contexts.

`Remove(ctx, owner)` requires the transport's prior explicit unexpired confirmation. It blocks new owner work, cancels/drains checks and registered owner callbacks, then deletes the exact immutable enrollment through storage. It awaits draining even if its caller cancels; cancellation then prevents deletion and reopens work. Failed deletion never reports success. Late observations cannot commit into a removed or later enrollment. Storage retains the manual/recipient/global guards and immutable identity registry.

**Step 9 must use the same coordinator's `RunOwnerWork(ctx, owner, enrollmentID, callback)` for every send and membership/recovery operation.** This gate serializes callbacks for each owner, including queued events, and registers cancellation/draining with removal. Checks may run alongside sends. Pass the exact enrollment ID for sends (old IDs are rejected), or an empty ID for owner-level membership updates, which remain possible after removal. Callbacks must honor context cancellation, return only after network work drains, fence acknowledgements by immutable enrollment identity, and return sanitized errors. They must not recursively call the gate or `Remove`. Reserve/send/complete inside the callback, with short storage calls and network I/O outside transactions. Recheck current eligibility immediately before send. Retry membership events rejected/canceled during removal after removal finishes rather than dropping unblock evidence. The gate does not implement delivery, retry timing, startup recovery, Telegram handlers/drafts, or scheduling.

Offline tests cover completion-based 59/60-second cache reuse, failed manual/scheduled guards across reopen, cached/no-reservation behavior, concurrent manual/scheduled joins beyond 60 seconds, independent waiter cancellation, stale-cache preservation on portal/credential/storage errors, duplicate enrollment refusal, enrollment failure rollback, owner isolation and authorization withdrawal before commit, A → B → A durable changes from both sources, canceled enrollment/removal, portal and send draining, new-work refusal during removal, immutable identity fencing after re-enrollment, retained cooldown deadlines, serialized send/membership recovery, and membership updates without a current enrollment. Synthetic credentials and temporary SQLite only; no live calls, captures, backups, commits, or deployment.

Exact focused verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod gofmt -w internal/tracking internal/storage/runtime.go && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test -count=1 -race -timeout 45s ./internal/tracking ./internal/storage`. Separate static verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go vet ./... && git diff --check`.

The additional `go test ./...` run passed the tracking/storage and other non-portal packages but failed in existing `internal/portal.TestProbeRejectsUnexpectedBootstrap` because the sandbox denied its loopback listener (`bind: operation not permitted`). This is a local-test permission limitation, not a full-suite success claim; the focused coordinator suite makes no network listeners.


## Step 8 evidence (2026-11-01)

Added `internal/telegram` and pinned `github.com/go-telegram/bot` v1.27.0 (approved dependency fetch only). Commands authenticate ordinary private numeric senders before reading input or owner state. Ordered intake, separate enrollment fields, exact draft/confirmation deadlines, periodic draft cleanup, cancellation of enrollment, best-effort deletion and plain-text English status/removal are implemented. Confirmation follows coordinator commit; direct replies have no retry. The Sender maps structured rate-limit/token/permanent failures and distinguishes definite blocking from other 403 responses. Diagnostics contain only fixed categories. See [Telegram lifecycle/delivery handoff](telegram.md). No runnable-service wiring, live bot/portal validation, backups, commits or deployment.

Offline tests use the real Telegram library with an injected in-memory HTTP endpoint/transport, synthetic portal and temporary SQLite/coordinator/key fixtures. Coverage includes enrollment baseline without notification, cached original-language/plain stage, owner-isolated removal, exact ten-minute expiry, cancellation, invalid/argument/slash input deletion with failed deletion responses, no echoed credentials, API classification/retry-after, polling updates/failures/cancellation and cancellation of an in-flight enrollment.

Verification commands: `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test -count=1 -race ./internal/telegram && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test ./... && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go vet ./... && git diff --check` (focused race tests passed; the sandboxed full suite was blocked by existing portal loopback fixtures, then the full suite, vet and diff check passed with approved loopback escalation). Telegram HTTP fixtures run without listening sockets or network access.


## Step 9 evidence (2026-11-01)

Added `internal/notifications` consumer-owned ordinary interfaces, injected clock, deterministic `Step`, startup-only uncertain recovery and cancellable `Run`. Each recipient uses the shared tracking owner gate and durable oldest-first reservation; all sends serialize conservatively so persisted global rate/token failures gate the next recipient. Six failed attempts retain the exhausted head, with fixed backoff from completion, and explicit recovery preserves lifetime counts. Membership evidence queues before application and retries after interrupted removal; unblock and proven-block interaction recovery do not require `/start`. Authorization suppression consumes no attempts. Accepted outcomes persist with bounded cancellation-independent cleanup before removal drains/deletes; persistence failure stops the worker for restart recovery. Diagnostics receive only fixed categories. Direct reply gate/outcome hooks share global state without queue acknowledgement. No automatic portal retry or new dependency was added.

Offline temporary-SQLite tests cover all six attempts/deadline boundaries, no overtaking and other-recipient progress, exhaustion across new observations/restart, explicit resume/lifetime counts, rate/token gates including structured outcomes accompanied by errors, blocked/unblocked recovery during an active send, authorization suppression, accepted-before-ack uncertainty/recovery, fatal acknowledgement-write faults, and removal cancellation/draining retaining block gates. Existing storage/tracking tests also exercise late acknowledgements and identity fencing.

Exact verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod gofmt -w internal/notifications internal/storage/runtime.go internal/telegram/telegram.go && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test -race ./internal/notifications ./internal/storage ./internal/tracking ./internal/telegram && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go vet ./... && git diff --check`. No live endpoints, captures, real secrets, backup tooling, commits or deployment were used.


## Step 10 evidence (2026-11-01)

Added `internal/scheduler` using only the standard library, bundled `time/tzdata`, and ordinary-value consumer interfaces `Inventory.Enrollments` and `Tracking.Scheduled`. `New(Config)` validates collaborators; `Next(after)` calculates the strictly future Warsaw weekday slot; `Run(ctx)` waits without startup catch-up, inventories each run, rechecks policy immediately before each sequential coordinator call, and computes the next future slot after completion. Concurrent `Run` calls are rejected. Wait is injectable as an absolute-deadline function; its default uses a stopped-on-return timer. Timer jitter is accepted within the scheduled cron minute; waking at or after the following minute skips that missed slot. Slow runs skip crossed slots without backlog. Public holidays remain included. No SQL or Telegram types enter the consumer contracts; inventory uses the existing ordinary `storage.Enrollment` value.

Scheduled calls now drain the precise shared tracking flight they joined after cancellation, preserving manual coordination and avoiding a replacement-flight race. `Status` cancellation behavior is unchanged. Process-wide shutdown/draining across all owners and notification activities remains step 11 work; runtime wiring must share one coordinator among manual, scheduled, and delivery callers.

Offline tests cover every slot, summer/winter UTC offsets, both DST weekends, weekends, public holidays, explicit foreign input offsets, startup exact boundaries and restart with missed slots, queued authorization withdrawal, sequential continuation after check failure, a slow run crossing multiple slots without overlap/backlog, rejected concurrent runs, cancellation waiting for an active callback, cron-minute missed-wake boundaries and ordinary jitter. A real SQLite/vault/tracking integration verifies a manual attempt immediately before a scheduled slot prevents another portal call; existing tracking tests cover failed attempts and persisted guards.

Exact final verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod gofmt -w internal/scheduler && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test -count=1 -race ./internal/scheduler ./internal/tracking && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go vet ./internal/scheduler ./internal/tracking && git diff --check`. No dependencies, network calls, live credentials, captures, backup tooling, commits or deployment were introduced.

## Step 11 evidence (2026-11-01)

Wired `cmd/case-watch run` through `internal/service`: configuration/path/key/schema validation and authentication of every stored credential precede transport construction; uncertain sends recover before polling. One coordinator serves Telegram, notifications and scheduling, with direct-reply global gates and membership recovery wired. Fixed-category diagnostics are emitted; fatal storage/worker failures cancel the process. `Coordinator.Shutdown` closes admission and cancels/drains flights, sends and removal operations before database closure. SIGINT/SIGTERM cancel the service. An exclusive persistent `.lock` file protects service and maintenance use; final database symlinks/hardlinks and insecure file/directory permissions are rejected.

Added stopped-service `rotate-key`, `queue`, `resume-recipient` and `resume-token` commands without requiring Telegram environment configuration. Rotation safely persists old/new keys before one credential rewrite transaction, verifies every row, then atomically activates the new key; interrupted retries reuse the persisted inactive key. Queue inspection emits metadata rather than notification text; recovery preserves history/lifetime attempts and never acknowledges unsent work. No backup or implicit reset/provisioning was added. See [operations](operations.md), [configuration](configuration.md) and [credentials](credentials.md).

Parent independent verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use` followed by `GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod GOPROXY=off GOSUMDB=off go test -count=1 -race -timeout 60s ./internal/service ./internal/credentials ./internal/tracking ./cmd/case-watch`. All four packages passed. Tests cover validation before transport creation, undecryptable rows, key path separation, service/maintenance exclusion, detached-flight shutdown/reopen, startup uncertain recovery, unexpected poller exit, interrupted rotation boundaries/stable identities and explicit queue recovery preserving lifetime attempts. No live requests, deployment, commits or raw captures were used. Full integrated verification and container availability checks follow in step 12.


## Step 12 evidence (2026-11-01)

Added multi-stage `Dockerfile`, default-deny `.dockerignore`, single-service `compose.yaml`, and [deployment runbook](deployment.md). The Go 1.27.3 build explicitly disables CGO and targets Linux ARM64 via BuildKit platform arguments. The scratch image contains the executable, CA trust bundle, dedicated UID/GID 10001, and private owned `/data` and `/keys` directories; scheduler imports embedded Go timezone data. Compose uses the fixed `case-watch-data` named volume with initial image directory copy-up, separately binds the existing key file read-only without host-path creation, and configures `unless-stopped` plus 90-second stop grace. No root runtime bootstrap/supervisor, automatic key generation, reset or backups are included. Deployment documentation covers protected host key provisioning, ownership, Docker boot behavior, versioned non-overlapping upgrades, stopped-service maintenance/rotation with writable key-directory access, atomic file-mount replacement/recreation, and explicit accepted loss/re-enrollment.

Added `TestConversationShouldPreserveOrderedTransitionsAndGatesWhenDeliveryRestartsBeforeRemoval` in `internal/telegram/telegram_test.go`. It exercises the real conversation/coordinator/SQLite/notification worker with fake time, an in-memory Telegram transport and synthetic portal/sender: separate-message enrollment, manual transition and cached reply, scheduled reverse transition, retained ordered history/queue, global rate limit across database reopen, recipient block/unblock recovery without overtaking, and confirmed removal deleting retained work. Existing storage tests additionally verify synthetic plaintext exclusion from the database and active rollback journal; existing service tests verify startup refusal before transport construction, single-process locking, uncertainty recovery before polling, cancellation/draining and reopenability. None of this establishes Docker-level lifecycle properties.

Focused verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod gofmt -w internal/telegram/telegram_test.go && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test -count=1 ./internal/telegram -run TestConversationShouldPreserve`.

Full fresh verification (exit 0): `source "$NVM_DIR/nvm.sh" && nvm use && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test -count=1 ./... && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go test -count=1 -race ./... && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod go vet ./... && GOCACHE=/private/tmp/case-watch-go-build GOMODCACHE=/private/tmp/case-watch-go-mod CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /private/tmp/case-watch-step12-arm64 ./cmd/case-watch && file /private/tmp/case-watch-step12-arm64 && git diff --check`. The ordinary sandbox first denied the legacy portal probe test's loopback listener; the full sequence was rerun with approved local-test escalation and all tested packages passed, including race-enabled packages. `file` confirmed an ARM aarch64 statically linked Linux ELF. Cross-compilation does not execute on ARM64 or measure Pi resources.

Compose syntax validation (exit 0), using synthetic values only: `CASE_WATCH_IMAGE=case-watch:synthetic CASE_WATCH_HOST_KEY_FILE=/private/tmp/synthetic-key.json CASE_WATCH_TELEGRAM_BOT_TOKEN=123:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa CASE_WATCH_ALLOWED_USER_IDS=101 docker compose config --quiet`. This parses configuration without creating mounts, containers or keys.

`docker version` (exit 1) returned the Darwin ARM64 client, then `Cannot connect to the Docker daemon at unix:///Users/maksimburyshynets/.docker/run/docker.sock`. No daemon was started. Image build, actual non-root/file permissions and initial volume ownership copy-up, CA/timezone behavior in the image, container recreation with retained state, SIGTERM/forced-kill recovery, restart policy and host reboot behavior remain **unverified**. The deployment runbook records the required synthetic Docker acceptance and cautions that the production CLI intentionally has no fake-endpoint overrides; an offline container test harness must inject factories/build a test binary. No live portal/Telegram calls, real keys, capture reads, commits, pushes, Pi provisioning, deployment or backups occurred.

## Step 13 audit and final verification (2026-11-01)

A fresh independent agent reviewed every accepted ADR, dated amendment, product behavior, implementation and critical tests. [The audit](implementation-audit.md) maps requirements to evidence and records three corrected findings: direct/durable send admission now shares one gate/send/outcome critical section; known direct rate/token outcomes persist with bounded cancellation-independent contexts and real persistence failures remain fatal; explicit unblock evidence resumes a retained cycle even if the earlier block update was missed, while unrelated interaction cannot clear exhaustion/permanent pauses. The original delivery agent implemented corrections and regression tests; the parent and auditor independently reviewed and race-tested them. No actionable code defect remained from that review. ADR checkboxes now reflect supported evidence rather than assuming Docker/live/Pi or deferred backup acceptance.

Final parent verification after every code correction (exit 0): run `source "$NVM_DIR/nvm.sh"` and `nvm use`, then export `GOCACHE=/private/tmp/case-watch-go-build`, `GOMODCACHE=/private/tmp/case-watch-go-mod`, `GOPROXY=off`, `GOSUMDB=off`; execute `go test -count=1 ./...`, `go test -count=1 -race ./...`, `go vet ./...`, `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o /private/tmp/case-watch-final-arm64 ./cmd/case-watch`, `file /private/tmp/case-watch-final-arm64`, and `git diff --check`. All test packages passed in both runs; vet and diff checks passed; the binary is a statically linked ARM aarch64 Linux ELF. Approved escalation was used solely for existing offline loopback HTTP fixtures. An initial final-verification attempt did not execute because approval review hit a usage limit; after the user reported the reset, this full sequence executed successfully.

No commits, pushes, deployment, live portal/Telegram checks, real key provisioning, raw capture reads or backups were performed. The pre-existing untracked HTML/HAR captures remain untouched. Docker daemon access remains unavailable; actual image builds, container permissions/persistence/termination/restart and host boot behavior are unverified. Additional live integration and Pi resource measurements remain separately authorized environment acceptance tasks, not claimed successes.

### 2026-11-02: golang-migrate replacement

Replaced custom migration sequencing with pinned golang-migrate v4.20.1,
embedded up SQL, and the modernc SQLite adapter. The consolidated baseline uses
schema_migrations version 1. Legacy user_version databases are rejected unchanged;
no compatibility conversion or automatic reset is supplied. Added metadata
rejection, ordered-upgrade and transactional failure/dirty-state tests. Proposed
ADR 0010 records the dependency decision.

Verification: Go 1.27.3; go test ./... passed; go test -race ./... passed on
rerun (the first race run failed the existing service uncertainty-recovery test);
go vet ./... passed; native and CGO_ENABLED=0 Linux ARM64 application builds
passed. Documentation relative links and git diff --check passed. Sandbox
loopback/cache restrictions required elevated verification. Docker client exists
but its daemon is unreachable, so image build and container behavior remain
unverified. No live endpoints, deployment, or data reset were performed.

### 2026-11-02: Migration change review

Reviewed migration initialization, legacy/dirty/future-schema refusal, embedded
SQL, connection ownership, cancellation and Docker source inclusion against the
implementation plan. Added a regression test confirming that an unversioned
populated database is rejected byte-for-byte unchanged. With Go 1.27.3,
`go test -count=1 ./...`, `go test -count=1 -race ./...`, `go vet ./...`, native
build, CGO-disabled Linux ARM64 build and `git diff --check` all passed. Go tests
used local loopback fixtures and a temporary build cache under approved sandbox
access. The Docker daemon remains unavailable; image contents were inspected
through the Dockerfile and `.dockerignore`, not a container build.

### 2026-11-02: Initial migration SQL review

Folded the old v2 `pending` column additions into the fresh database's initial
`CREATE TABLE` and removed the now-empty recipient copy. Compared the old
two-step schema with the revised initial SQL using SQLite introspection: tables,
columns, foreign keys, indexes, triggers and delivery-gate seed matched. With
Go 1.27.3, `go test -count=1 ./...`, `go test -count=1 -race ./...`,
`go vet ./...`, native and CGO-disabled Linux ARM64 builds, and
`git diff --check` passed. Docker execution remains unverified without a daemon.

### 2026-11-02: Initial SQL formatting

Reformatted the initial migration for readable table definitions, constraints,
index, triggers and seed data. SQLite introspection confirmed the resulting
tables, columns, foreign keys, indexes and gate seed match the former two-step
schema. `go test -count=1 ./...`, `go test -count=1 -race ./...`, `go vet ./...`,
native and CGO-disabled Linux ARM64 builds, and `git diff --check` passed.

### 2026-11-02: Data access package refactor

Moved the SQLite adapter to `internal/data` with persistence models, entity repositories, and a transaction service for multi-entity operations. Updated runtime wiring and the maintained storage guide without changing the schema. Offline verification: `GOCACHE=/private/tmp/case-watch-go-build go test ./...`, `go test -race ./...`, `go vet ./...`, `go build -o /private/tmp/case-watch-data-refactor ./cmd/case-watch`, and `git diff --check` all exited 0. The build printed a non-fatal module stat-cache write warning in the restricted environment. No live portal or Telegram acceptance was performed.

### 2026-11-02: Enrollment soft deletion

Replaced `enrollment_ids` with soft-deleted enrollment rows and an active-owner partial unique index in the initial WIP schema. Removal retains ID/owner/deletion time, clears credentials and baseline, and transactionally deletes history, pending work and case guards. Active reads, observations, check guards and credential rewrites exclude deleted rows. Added reopen/scrubbing/exclusion coverage and retained rollback, late-result and gate tests. Earlier version-1 development databases require recreation; no production migration was needed.

With Node v26.13.0 selected and Go 1.27.3, `GOCACHE=/private/tmp/case-watch-go-build go test ./...`, `go test -race ./...`, `go vet ./...`, `go build -o /private/tmp/case-watch-soft-delete ./cmd/case-watch`, and `git diff --check` exited 0. The first full-suite attempt could not access the default build cache; rerunning with the writable temporary cache passed. The build emitted a non-fatal module stat-cache write warning. Verification is offline; no live portal/Telegram or deployment acceptance was performed.

### 2026-11-02: Service-owned transactions (ADR 0011)

Accepted [ADR 0011](adr/0011_move-transaction-boundaries-to-services.md). Store.RunInTx binds six named DBTX repositories to one transaction and rejects nested transactions and transaction-Store closure. Removed data.Transactions and its repository compositions. Tracking and notification services now compose atomic enrollment, observations, removal, check/delivery reservations, outcomes, recovery and recipient changes. Credential rewrites are composed in the maintenance service. SQL remains in data; schema and dependencies are unchanged.

Moved workflow rollback/concurrency/reopen tests into the service package and retained migration/durability tests in data. Added transaction tests for multi-repository commits and rollback, callback error identity, panic propagation, cancellation, nested lifecycle rejection, closed-database begin failure and deferred-constraint commit failure.

The first full-suite attempt encountered the restricted default Go cache; using a writable temporary cache then exposed a sandbox restriction on local HTTP listeners. Elevated offline verification allowed those listeners. The next normal-suite run reproduced an intermittent startup test failure also recorded in earlier migration evidence: recovery had completed, but cancellation during a concurrent tracking storage read was reported as a fatal infrastructure error. Tracking monitoring now ignores failures from canceled operations, consistent with monitored enrollment reads. Its focused cancellation regression failed before the guard and passed afterward; the startup and monitor tests passed 100 repetitions after the fix.

With Node v26.13.0 selected through nvm and Go 1.27.3, final commands all exited 0:

- GOCACHE=/tmp/case-watch-go-cache go test -count=1 ./...
- GOCACHE=/tmp/case-watch-go-cache go test -count=1 -race ./...
- GOCACHE=/tmp/case-watch-go-cache go vet ./...
- GOCACHE=/tmp/case-watch-go-cache go build -o /tmp/case-watch-adr-0011 ./cmd/case-watch

Documentation relative links, ADR status/index consistency, formatting of changed Go files and git diff --check passed. An existing formatting difference in untouched internal/scheduler/scheduler_test.go remains outside this change. Verification used synthetic data and local HTTP fixtures. No live portal/Telegram, container execution, deployment, target-Pi checks, or new Linux cross-build was performed for this refactor.

### 2026-11-03: Command entry point readability

Refactored command dispatch to a switch with explicit argument-count checks and
named command arguments. Replaced abbreviated local names, extracted queue
reporting, and used time.RFC3339 for the existing UTC timestamp format. Command
syntax, errors and diagnostic fields are unchanged. Expanded command tests for
missing/extra arguments, unknown commands and invalid recipient IDs.

With Node v26.13.0 selected through nvm and Go 1.27.3,
`GOCACHE=/private/tmp/case-watch-go-build go test ./cmd/case-watch`,
`go test ./...`, `go test -race ./...`, `go vet ./...`, and
`go build -o /private/tmp/case-watch-cli-refactor ./cmd/case-watch` passed
using the same temporary build cache. The full normal and race suites initially
hit sandbox loopback restrictions; both passed with elevated access for local
HTTP fixtures. The build emitted a non-fatal module stat-cache write warning.
Formatting of the changed Go files and `git diff --check` passed. Verification
used synthetic data; no live portal/Telegram or deployment acceptance was run.

### 2026-11-03: Test-only command wrapper

Moved the run wrapper from main.go to main_test.go, retaining its signature,
background context and stdout behavior. Existing test calls and the production
entry point are unchanged.

With Node v26.13.0 selected through nvm and Go 1.27.3, verification passed:
`go test ./cmd/case-watch`, `go test ./...`, `go test -race ./...`,
`go vet ./...`, and
`go build -o /private/tmp/case-watch-test-helper ./cmd/case-watch`, all using
`GOCACHE=/private/tmp/case-watch-go-build`. The full suites ran with elevated
loopback access for local HTTP fixtures. The build emitted a non-fatal module
stat-cache write warning. Changed Go files are gofmt-clean and
`git diff --check` passed. No live integration or deployment checks were run.

### 2026-11-03: Command router and handlers

Reduced execute to empty-input validation and command routing. Each command
calls its own onRun, onProvisionKey, onRotateKey, onResumeToken, onQueue or
onResumeRecipient handler. Handlers own argument validation and command work;
queue and recipient resumption no longer share a conditional callback.
Command syntax, validation errors and output are unchanged.

With Node v26.13.0 selected through nvm and Go 1.27.3, the existing command tests,
`go test ./...`, `go test -race ./...`, `go vet ./...`, and
`go build -o /private/tmp/case-watch-command-router ./cmd/case-watch` passed
using `GOCACHE=/private/tmp/case-watch-go-build`. Full suites used elevated
loopback access for local HTTP fixtures. The build emitted a non-fatal module
stat-cache write warning. The changed Go file is gofmt-clean and
`git diff --check` passed. No live integration or deployment checks were run.

### 2026-11-03: Double-dash CLI commands

Replaced all six bare command names with double-dash commands, preserving
positional arguments and handler behavior. Bare and single-dash spellings
return usage without reading the environment. Updated CLI tests, the Docker
default command and maintained operator examples. Existing operator scripts
must adopt the new spellings.

With Node v26.13.0 selected through nvm and Go 1.27.3, verification passed:
`go test ./cmd/case-watch`, `go test ./...`, `go test -race ./...`,
`go vet ./...`, and
`go build -o /private/tmp/case-watch-double-dash ./cmd/case-watch`,
using `GOCACHE=/private/tmp/case-watch-go-build`. The build emitted a
non-fatal module stat-cache write warning. Changed Go files are gofmt-clean;
maintained documentation local links, code fences and command spellings,
and `git diff --check` passed. No Docker runtime, live portal/Telegram or
deployment acceptance checks were run.

### 2026-11-03: Recipient recovery SQL script

Removed `--resume-recipient`, its handler and the operator-only recipient
recovery API. Token maintenance now calls `ResumeToken` directly. Automatic
Telegram unblock recovery remains in the notification worker.
Added [resume-recipient.sql (removed by ADR 0018)](adr/0018_remove-automatic-notifications.md) and updated the
operations/storage guides. The transaction validates a bound recipient ID,
rejects any outstanding reservation, clears the recipient pause/block and
resets only the oldest undelivered notification's retry cycle. Startup remains
responsible for uncertain-reservation recovery.

With Node v26.13.0 selected through nvm and Go 1.27.3, `go test ./...`,
`go test -race ./...`, `go vet ./...`, and
`go build -o /private/tmp/case-watch-resume-recipient ./cmd/case-watch` passed
using `GOCACHE=/private/tmp/case-watch-go-build`. The initial default-cache
test invocation was blocked by sandbox permissions; the build emitted a
non-fatal module stat-cache write warning. SQL integration tests verify retained
history, lifetime attempts, delivered/later records and global gates, plus
rollback for invalid IDs and reservations belonging to another recipient.
The documented system `sqlite3 -bail` invocation passed against a temporary
database; a missing recipient binding was rejected. Changed Go files are
gofmt-clean; documentation links/fences and `git diff --check` passed.
No live portal/Telegram or deployment acceptance checks were run.


## 2026-11-03: Fail-fast Telegram token handling (ADR 0012)

Implemented [ADR 0012](adr/0012_fail-fast-on-invalid-telegram-token.md), replacing only ADR 0009's token suspension policy. Startup validates the token through a context-cancellable getMe request bounded to 15 seconds before recovery, polling, delivery or scheduling. Any validation error prevents startup. Structured runtime unauthorized responses during polling, sending or credential-message deletion stop and drain the whole service, including portal checks. Correct the token environment and restart normally.

Removed --resume-token, token queue metadata, application token gates and the token state API. The initial schema omits delivery_gates.token_paused; the application is not deployed and requires no upgrade path. Explicit unauthorized send rejection restores cycle/lifetime counters without acknowledging a notification. Tests cover the sixth-attempt boundary, subsequent send refusal, refusal even if outcome persistence fails, and normal retry through a fresh worker. Existing uncertain recovery, recipient and rate-limit tests remain applicable.

With Node v26.13.0 selected using nvm and Go 1.27.3, final go test ./..., go test -race ./..., go vet ./..., and go build -o /tmp/case-watch-token-policy ./cmd/case-watch passed using GOCACHE=/tmp/case-watch-go-cache. Initial sandboxed full tests were blocked because existing portal HTTP fixtures could not bind a localhost port; rerunning with sandbox escalation passed. The default build cache was also sandbox-restricted, so checks used /tmp. Changed Go files are gofmt-clean; documentation links and git diff --check passed. No live Telegram/portal or deployment acceptance checks were run.


### 2026-11-03: Initial-schema verification

The maintainer confirmed the application is not deployed. Token suspension is omitted directly from the initial schema; upgrade-specific coverage is unnecessary. The ADR index retains its established `(status, date)` entry endings. With Node v26.13.0 selected through nvm and Go 1.27.3, `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build -o /tmp/case-watch-initial-schema ./cmd/case-watch` passed using `GOCACHE=/tmp/case-watch-go-cache` and sandbox escalation for existing localhost test fixtures. A fresh SQLite schema check confirmed delivery gates contain only singleton and rate_until. Markdown links/fences, ADR index format and `git diff --check` passed. No live Telegram/portal or deployment checks were performed.

### 2026-11-03: In-memory Telegram send cooldown (ADR 0013)

The maintainer approved replacing the persisted global rate-limit deadline with one deadline owned by the notification worker. ADR 0013 supersedes ADR 0009 while retaining ordered durable retries and ADR 0012's token behavior. ADR 0009's historical contents remain intact apart from supersession metadata and its replacement link.

Removed GlobalGateRepository, GlobalState, Store.Gates and delivery_gates from the existing initial migration; no new migration was added. Both notification sends and direct replies honor the shared in-memory cooldown. Positive retry_after is honored, otherwise 30 seconds is used. Observed limits survive caller cancellation and notification outcome-write failure within the worker; restart clears the shared deadline. Per-notification retry deadlines and attempt budgets remain durable. Stopped-service queue inspection drops rate_until. Maintained storage, Telegram, operations, product and deployment guides describe the new lifetime.

With Node v26.13.0 selected through nvm and Go 1.27.3, `GOCACHE=/tmp/case-watch-go-cache go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build -o /tmp/case-watch-adr-0013 ./cmd/case-watch` passed (the same GOCACHE was used for all Go checks). Offline fixtures cover cross-recipient and direct/queued suppression, exact expiry, positive and absent/nonpositive delays, cancellation, failed outcome storage, restart reset with retained notification deadlines, recipient recovery and fresh schema absence of delivery_gates. Markdown links and fences across 26 files, reciprocal ADR links/index, historical ADR preservation, a single initial migration and `git diff --check` passed. No live Telegram/portal calls, deployment, Docker or target-device acceptance was performed.


### 2026-11-03: Generic cooldown persistence

Replaced case_guards and manual_guards in the initial schema with cooldowns keyed by type and subject_id, retaining Unix-nanosecond attempt starts. Store.Cooldowns exposes an entity-based CooldownRepository with FindByTypeAndSubjectID, Upsert, and idempotent Delete. Missing entities return the existing ErrNotFound sentinel, whose message is now record not found. Tracking validates active enrollment ownership within service transactions, preserves atomic manual/case reservations, and explicitly removes case cooldowns while retaining owner cooldowns. Subject references are application-enforced; no polymorphic foreign key or automatic expiry cleanup is introduced. Earlier development databases require explicit recreation, without conversion or automatic reset.

With Node v26.13.0 selected through nvm and Go 1.27.3, go test ./..., go test -race ./..., go vet ./..., and go build -o /tmp/case-watch-cooldowns ./cmd/case-watch passed using GOCACHE=/tmp/case-watch-cooldowns-go-cache. Initial checks encountered sandbox restrictions on the default build cache and localhost HTTP fixtures; final checks used the temporary cache and sandbox escalation for local listeners. Coverage includes entity round trips, Unix epoch timestamps, extensible types and subject isolation, upserts, missing entities, idempotent deletion, invalid input, reopen persistence, identity rejection without writes, removal cleanup and retained manual cooldowns, and rollback when a manual cooldown insert fails. Existing tests retain timing-boundary, concurrency, cache/join, failed-check and re-enrollment coverage. Storage guide links, Markdown fences, gofmt and git diff --check passed. No live Telegram/portal, deployment, Docker or target-device acceptance checks were performed.


### 2026-11-03: Cooldown input validation in Go

Removed the blank-string CHECK expressions from cooldowns.type and cooldowns.subject_id in the initial migration. CooldownRepository continues to reject blank keys with strings.TrimSpace and zero write timestamps; SQLite retains NOT NULL columns and the composite primary key. The storage guide now states this validation boundary explicitly. Existing invalid-input tests exercise repository rejection without database CHECK support.

With Node v26.13.0 selected through nvm and Go 1.27.3, go test ./..., go test -race ./..., go vet ./..., and go build -o /tmp/case-watch-cooldowns ./cmd/case-watch passed using GOCACHE=/tmp/case-watch-cooldowns-go-cache and sandbox escalation for existing localhost HTTP fixtures. Storage guide links and git diff --check passed. No live service or deployment acceptance was performed.


### 2026-11-03: Repository validation replaces remaining SQL checks

Removed the remaining CHECK constraints from the initial migration: positive enrollment owners, positive pending recipients, and active/deleted enrollment field completeness. Existing Go methods enforce positive identifiers and own creation, observation, credential updates and soft deletion. Creation and credential updates now share sealed-credential validation, rejecting unsupported versions, invalid identities, blank key IDs and ciphertext shorter than 28 bytes. This also closes the former credential-update gap that accepted blank key IDs and short nonempty ciphertext. Primary keys, required columns, foreign keys, uniqueness, the recipient-creation trigger and atomic identity predicates remain in SQLite. The storage guide documents the boundary and the need for direct SQL callers to preserve application invariants.

Added repository integration tests for invalid owner IDs, observation/deletion timestamps, credential identities/versions/keys/payloads, and pending history/recipient IDs. They assert ErrInvalid and unchanged persisted state, verify valid writes still work, and exercise complete active records, scrubbed tombstones and rejection of late observation/credential updates after deletion without SQL CHECK constraints. Existing rollback, removal/reopen and concurrency tests remain in place.

Using Node v26.13.0 selected via nvm and Go 1.27.3 with GOCACHE=/tmp/case-watch-cooldowns-go-cache, targeted repository validation tests, go test ./..., go test -race ./..., go vet ./..., and go build -o /tmp/case-watch-go-validation ./cmd/case-watch passed. Full checks used sandbox escalation for existing localhost HTTP fixtures. Storage guide links/fences, absence of CHECK constraints in the initial migration, gofmt and git diff --check passed. Initial-schema changes follow the existing predeployment recreation policy; no upgrade/conversion was added. No live service or deployment acceptance was performed.


### 2026-11-03: Explicit cooldown subject type naming

Renamed cooldowns.type to subject_type and Cooldown.Type to SubjectType. Repository lookups now use FindBySubjectTypeAndSubjectID; SQL, parameters, tracking callers, tests and the manual-upsert fault trigger use the corresponding subject naming. Updated the storage guide; historical verification entries retain their original names. This revises the initial schema under the existing predeployment recreation policy without an upgrade migration.

With Node v26.13.0 selected via nvm and Go 1.27.3, go test ./..., go test -race ./..., go vet ./..., and go build -o /tmp/case-watch-subject-type ./cmd/case-watch passed using GOCACHE=/tmp/case-watch-cooldowns-go-cache and sandbox escalation for localhost fixtures. Existing entity round-trip, type isolation, rollback and tracking tests passed with the renamed field and column. Storage guide links/fences, gofmt and git diff --check passed. No live service or deployment acceptance was performed.

### 2026-11-05: Plaintext WIP credentials (ADR 0014)

The maintainer confirmed that Case Watch is an undeployed WIP application with no existing databases and chose to defer credential encryption until a concrete requirement exists. ADR 0014 supersedes ADR 0006. The initial schema now stores case number and password directly on enrollments; opaque enrollment identities remain for removal fencing. Removed the encryption/keyring package, sealed-credential model, startup authentication, key configuration and mount, provisioning, rotation and their tests. Queue inspection now requires only database path and recipient ID. Telegram draft expiry, best-effort credential-message deletion, owner scoping and sanitized diagnostics remain.

With Node v26.13.0 selected through nvm and Go 1.27.3, `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build -o /private/tmp/case-watch-plaintext ./cmd/case-watch` passed using `GOCACHE=/private/tmp/case-watch-go-cache`. The initial sandboxed test run could not bind existing localhost HTTP fixtures; the escalated rerun passed. Maintained configuration, storage, Telegram, operations and deployment guides describe plaintext WIP storage. No live Telegram/portal, Docker, deployment or target-device acceptance checks were run.

### 2026-11-05: Remove queue inspection command

Removed the `--queue` CLI command, its queue formatting and inspection code, and the application-level maintenance database opener that existed only for that command. The executable now accepts only `--run`. Recipient recovery remains an explicit stopped-service SQL procedure; durable notification queueing used by normal delivery is unchanged.

With Node v26.13.0 selected through nvm and Go 1.27.3, `go test ./cmd/case-watch`, `go vet ./...`, `go build -o /private/tmp/case-watch-no-queue ./cmd/case-watch`, and `git diff --check` passed. The required full `go test ./...` stopped on the existing invalid experimental `scheduled_jobs` declaration (`INTEGER NOT PRIMARY KEY AUTOINCREMENT`) in the initial schema, before the race suite could run. That unrelated scheduler/schema work was left unchanged for the planned architecture cleanup.

### 2026-11-05: Remove the run argument

Removed the redundant `--run` argument after it became the executable's only command. Running `case-watch` starts the service. The container entrypoint now invokes the executable without a default argument.

With Go 1.27.3, `go test ./cmd/case-watch`, `go vet ./...`, `go build -o /private/tmp/case-watch-no-command ./cmd/case-watch`, and `git diff --check` passed. Full repository tests remain blocked by the unrelated existing malformed experimental `scheduled_jobs` schema recorded above; no live service, container, Telegram or portal checks were run.

The remaining argument check and command dispatcher were subsequently removed. `main` now calls `service.Run` directly, and the obsolete command-package tests were deleted; service startup validation remains covered in `internal/service`.

After that simplification, `go test ./cmd/case-watch`, `go vet ./...`, `go build -o /private/tmp/case-watch-direct-main ./cmd/case-watch`, and `git diff --check` passed with Node v26.13.0 selected through nvm and Go 1.27.3.

### 2026-11-05: Three-layer package structure (ADR 0015)

Adopted the explicit `entrypoints -> services -> repositories` structure. Telegram and Warsaw scheduling moved under `internal/entrypoints`; tracking and durable notification workflows moved under `internal/services`; SQLite repositories, migrations, portal communication and Telegram Bot API communication moved under `internal/repositories`. `internal/app` now owns construction and process lifetime, while `cmd/case-watch` only calls `app.Run`. Shared values and the few test/external collaborator interfaces moved from `internal/application` to neutral `internal/domain`.

Telegram now asks the tracking service whether an enrollment exists instead of reading the enrollment repository. The scheduler obtains authorized owner IDs from the tracking service rather than inventorying SQLite directly. Transaction orchestration remains in tracking and notification services. The persistent SQLite queue experiment was preserved directly under `internal/experimental`; no production package imports it. Its initial-schema primary key and `attempts` column were corrected so fresh databases initialize and match the experiment's queries.

With Node v26.13.0 selected through nvm and Go 1.27.3, fresh `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build -o /private/tmp/case-watch-three-layer ./cmd/case-watch`, Markdown relative-link validation and `git diff --check` passed. Ordinary and race suites required approved loopback access for existing portal `httptest` listeners. No live Telegram/portal, Docker, deployment or target-device checks were run.


### 2026-11-05: Cron-based polling slots

Replaced the handwritten Warsaw weekday/hour traversal in `internal/entrypoints/scheduler` with `github.com/robfig/cron/v3` v3.0.1 and the ADR 0007 expression `CRON_TZ=Europe/Warsaw 0 8,10,12,14,16,18 * * 1-5`. The entry point retains sequential execution, missed-minute suppression, completion-based selection of the next slot, cancellation/draining, and the injectable clock/wait used by deterministic tests. The experimental persistent queue and its schema remain unchanged under ADR 0015. The operations guide documents the implementation.

With Node v26.13.0 selected via nvm and Go 1.27.3, focused scheduler/tracking tests, `go test -timeout 60s ./...`, `go test -race -timeout 90s ./...`, `go vet ./...`, and `go build -o /tmp/case-watch-cron ./cmd/case-watch` passed. Scheduling coverage includes Warsaw summer/winter offsets, both DST weekends, all daily slots, weekends, public holidays, foreign input offsets, exact and subsecond boundaries, year rollover, skipped slots, overlap rejection and cancellation/draining. Formatting, changed-document relative links and `git diff HEAD --check` passed. Verification used offline fixtures and localhost HTTP servers; no live service or deployment acceptance was performed.

## 2026-11-07 — Root process entry point

Moved `cmd/case-watch/main.go` unchanged to root `main.go`. Updated Docker build inputs, CI, build instructions and agent verification commands; ADR 0015 has a dated location amendment.

With Node v26.13.0 selected through nvm and Go 1.27.3, `go test -timeout 60s ./...`, `go test -race -timeout 90s ./...`, `go vet ./...`, `go build -o /tmp/case-watch-root .`, and `git diff --check` passed. Go checks used the prepared workspace module/build caches and approved sandbox escalation for existing localhost HTTP fixtures. No live service or Docker acceptance checks were performed.

### 2026-11-07: Encapsulated application bootstrap (ADR 0016)

Moved the executable to repository-root main.go and renamed internal/app to
internal/application. Main calls application.Run(ctx). Dependency construction
and injection live in private wiring code; private runtime code owns supervision,
failure propagation and cleanup. Test factories, storage path/lock helpers,
clock and tracking monitor are unexported. The single raw Telegram client is
constructed before the notification worker and intake, eliminating callbacks
capturing an uninitialized worker. Replies require Worker.SendDirect, membership
callbacks are required, and polling binds the update handler at runtime and
rejects another polling lifetime. Service transaction ownership is unchanged.

Updated CI, Dockerfile, Docker context filtering, build commands, operational
guides and architecture decisions. Corrected the old migration-directory rule
in .dockerignore so embedded SQL enters the image build context.

With Node v26.13.0 selected through nvm and Go 1.27.3, full ordinary and race
tests, vet, the native root build and a static Linux ARM64 cross-build passed.
Offline integration tests verify startup ordering, partial startup cleanup,
fatal cancellation, detached tracking drain, acknowledgment persistence before
storage close, shared reply gating and ordered message/membership dispatch.
Localhost portal tests required approved socket access outside the sandbox.
Markdown relative links, formatting, production dependency boundaries and Docker
build-context inputs were checked. A local image build passed using a temporary
Dockerfile with build-only secret mounts for the cloud proxy CA; the unmodified
build recipe could not download modules through that proxy without its CA.
No application was deployed, and no live Telegram/portal or target-device
acceptance was performed. See ADR 0016 for verification details.

### 2026-11-07: Preserve main.go layout and deferred signal cleanup

Restored the maintainer's original multiline signal-context setup and
`defer stop()`. The entry-point diff now contains only the application import
and the call to `application.Run(ctx)`. Updated ADR 0016 to clarify that deferred
signal cleanup is skipped on error exit; application cleanup completes before
Run returns. Go ordinary and race tests passed from cache, and vet, the native
root build and whitespace checks passed with Go 1.27.3 and Node v26.13.0 selected
through nvm. No live endpoints were contacted.

### 2026-11-07: Telegram bot naming

Renamed the private application intake interface and runtime field to
`telegramBot`, and the Telegram entry-point type from `telegram.Adapter` to
`telegram.Bot`. Updated wiring, test references and the maintained Telegram
guide. The library's `bot.Bot` remains in the separate repository package, so
there is no naming conflict. Runtime behavior is unchanged.

With Node v26.13.0 selected through nvm and Go 1.27.3, focused application and
Telegram tests, `go test -timeout 60s ./...`, `go test -race -timeout 90s ./...`,
`go vet ./...`, `go build -o /tmp/case-watch .`, changed-document relative links
and `git diff --check` passed. Full ordinary and race tests required approved
socket access for existing localhost portal HTTP fixtures. No live Telegram or
portal endpoints were contacted.

## 2026-11-07: Remove user authorization

Updated the checkout to origin/master (`894d779`) before implementing
[ADR 0017](adr/0017_remove-user-authorization.md). Removed `internal/auth`, the
shared eligibility interface, allowlist/access-mode configuration and eligibility
checks from Telegram intake, tracking, scheduling inventory and notification
delivery. All ordinary private-chat users can enroll and manage their own case;
transport sender validation, owner scoping, enrollment identity fencing, portal
login and bot-token validation remain. Existing access environment values are
ignored. No schema change or data deletion is required. Previously excluded
owners and their pending work become eligible subject to existing work/delivery
gates. Updated maintained configuration, product, operations and deployment docs.

Verification with Go 1.27.3:

- Focused configuration, tracking, notifications, Telegram and application tests passed.
- `go test -timeout 60s ./...` passed.
- `go test -race -timeout 90s ./...` passed.
- `go vet ./...` passed.
- `go build -o /tmp/case-watch .` passed.
- `docker compose config --quiet` passed with a synthetic image/token and no user access variables.
- Relative links in changed Markdown files resolve; `git diff --check` passed.

Initial sandboxed full tests could not open the localhost sockets used by existing
portal fixtures. Both full suites passed outside the sandbox with synthetic
fixtures. New regression coverage exercises configuration without access policy,
private sender validation for messages/membership, arbitrary-owner enrollment,
scheduled refresh and delivery, plus preserved ownership isolation. No live
portal/Telegram calls, database migration, deployment or target-device checks
were performed.

## 2026-11-08: Remove automatic notifications

Implemented [ADR 0018](adr/0018_remove-automatic-notifications.md). Removed the
notification worker, retry/recovery state, delivery repositories, recipient
membership handling and recovery SQL script. Scheduled and manual checks retain
atomic cache/history updates; `/status` continues to return results. Telegram
subscribes to message updates only. Direct replies retain serialized bounded
sends, an in-memory rate-limit cooldown and invalid-token shutdown through
`internal/services/replies`. Removed the coordinator's unused delivery activity
gate while retaining check cancellation/draining and enrollment identity fencing.
Help and removal text, maintained guides and affected ADR amendments are updated.

Added immutable migration 2 to discard the legacy queue, recipient gates and
queue trigger. Migration coverage starts with a version 1 database containing a
reserved delivery and verifies that credentials, enrollment, cached observation,
history and manual cooldown survive. No application database was migrated in
this session. Added silent scheduled change followed by cached `/status` coverage
and reply cooldown boundary/cancellation and invalid-token admission tests.
Existing manual/scheduled A → B → A history and scheduler behavior tests pass.

Verification with Go 1.27.3 after `nvm use` (Node 26.13.0):

- `go test ./...` passed.
- `go test -race ./...` passed.
- `go vet ./...` passed.
- `go build -o /tmp/case-watch .` passed (exit 0).
- Local links in changed Markdown documents and `git diff --check` passed.

Go checks used `GOCACHE=/tmp/case-watch-go-cache` because the default build cache
was restricted. Build emitted a non-fatal module stat-cache write warning for the
restricted module cache; compilation completed successfully. Tests used temporary
databases and synthetic transports/HTTP fixtures. No live portal/Telegram calls,
deployment or target-device acceptance was performed.

## 2026-11-08: Review and simplify after notification removal

Reviewed tracking, persistence, Telegram transport, reply sending and runtime
wiring for obsolete delivery assumptions. Simplified history insertion to return
only an error, removed its unused insert-ID lookup, and made removal wait directly
for its single active check. Removed unused `ErrNotEligible`, `ErrSchema` and
`instant` storage helpers. Simplified send outcomes to accepted/failed/rate-limited/
invalid-token, removing unused Telegram message IDs and blocking/permanent/transient
retry classifications. Updated migration fixture setup and ordered-message polling
coverage. Corrected stale domain comments and the maintained Telegram guide.
Reply serialization and runtime supervision remain necessary for asynchronous
enrollment replies, rate-limit coordination and orderly shutdown.

Fresh verification with Go 1.27.3 after `nvm use` passed: affected package tests,
`go test ./...`, `go test -race ./...`, `go vet ./...`, and
`go build -o /tmp/case-watch .`. Changed-document relative links and
`git diff --check` passed. Checks used `GOCACHE=/tmp/case-watch-go-cache`;
the build again exited 0 with the non-fatal restricted module stat-cache warning
recorded above. No live portal/Telegram calls, deployment or target-device
acceptance was performed.

## 2026-11-08: Uncached correctness review of notification removal

Re-reviewed the full affected implementation: migration drop ordering, tracking
cache/history transaction boundaries, single-flight removal and shutdown drains,
reply cooldown/token handling, and the absence of automatic send paths. No new
production defect was identified. Added a real SQLite history-insert failure test
that verifies cache stage/time rollback, supplementing the existing substituted
storage-failure test. Temporarily bypassing the transaction made the new test fail
with an incorrectly updated cache; restoring the implementation made it pass.
Strengthened migration coverage to verify both manual/case cooldowns, complete
credentials, and clean schema version 2 metadata.

Fresh verification with Go 1.27.3 after `nvm use`:

- Focused rollback and version 1 migration tests passed without cached results.
- `go test -count=1 ./...` passed.
- `go test -race -count=1 ./...` passed.
- `go vet ./...` and `go build -o /tmp/case-watch .` passed.
- Changed-document relative links and `git diff --check` passed.

The initial uncached full suites hit the sandbox's local-socket restriction in
existing portal HTTP fixtures. Both suites then passed with approved local socket
access. Earlier verification runs had reused cached portal package results; these
uncached runs execute that package too. Checks used the temporary Go build cache;
the build emitted the previously recorded non-fatal module-cache warning. All
network exchanges were synthetic fixtures. Live portal/Telegram, deployment and
target-device acceptance remain unverified.
