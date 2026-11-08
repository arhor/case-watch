---
status: accepted
date: 2026-11-07
decision-makers: Repository maintainer
---

# Encapsulate construction and lifecycle in application.Run

## Context

Application construction and dependency injection belong behind
`application.Run(...)`, leaving the repository-root `main.go` as a small process
entry point. The goal is a single application bootstrap API, analogous to the
entry point of a Spring Boot application, with explicit Go construction.

Before this refactor, `internal/app.Run` handled configuration, storage
acquisition, component construction and injection, Telegram validation and process
execution. Those responsibilities were interleaved in one function, test
factories were public, and Telegram construction contained a cycle resolved with
callbacks capturing an initially nil worker.

[ADR 0015](0015_adopt-three-layer-application-structure.md) already assigns
construction and lifecycle to the bootstrap package and keeps main minimal.
Retain that decision with a dated clarification for the package rename and private
bootstrap organization. Its three-layer structure and service-owned transaction
requirements remain in force.

## Decision

Rename `internal/app` to `internal/application`. Make `Run(ctx context.Context)
error` the bootstrap API used by main. The rename expresses the requested
vocabulary; it is not required for dependency injection to work.

Run owns environment configuration, dependency construction, injection, startup
validation, delivery recovery, failure monitoring, runtime supervision and resource
cleanup. Main owns signals, final error output and the process exit code.

Use direct Go constructors. No dependency-injection container, reflection,
component scanning, global application context, service lookup, lifecycle plugin
system or framework dependency is needed.

### Package and API boundaries

| Location | Responsibility |
| --- | --- |
| `main.go` (repository root) | Create signal context, call application.Run, report sanitized failure and exit. |
| `internal/application` | Construct the entire application graph and own its lifetime behind Run. |
| `internal/entrypoints` | Telegram conversations and scheduled invocation, calling services. |
| `internal/services` | Business workflows, shared owner coordination, delivery policy and transaction orchestration. |
| `internal/repositories` | SQLite, migrations, transaction binding and external-system clients. |

Keep bootstrap-only types and functions unexported. In particular:

- Replace public Factories with private dependency overrides for tests.
- Keep the transport/polling test interfaces private to their consumers.
- Keep monitoredTracking, the real clock, the failure latch and runtime owner private.
- Make DatabasePath, Acquire, Lock and bootstrap errors private where no
  cross-package caller needs them. Keep path/lock machinery in application for
  this refactor; moving it into a new public storage API has no immediate benefit.
- Retain exported constructors and collaborator contracts that genuinely cross
  existing package boundaries. Do not export bootstrap helpers merely to let main
  construct them, or merge application layers to avoid exports.

Go's `internal` rule controls which packages may import a package. It does not
make exported identifiers inaccessible to other permitted importers. This design
minimizes the bootstrap API; it cannot remove exports needed between separate
service, entry-point and repository packages.

### Private bootstrap organization

A small file organization is sufficient:

```text
internal/application/
    application.go   # Run; private test overrides and bootstrap orchestration
    wiring.go        # explicit concrete construction and dependency injection
    runtime.go       # private failure latch, goroutine supervision and draining
    tracking.go      # existing private monitoredTracking decorator
    paths.go         # existing private filesystem/lock validation
    *_test.go
```

Keep the boundary meaningful rather than extracting every block into a helper.
The wiring function must show the complete graph in one place. Runtime code owns
running and stopping already constructed collaborators; it does not construct
business components. These are private implementation boundaries within one
package, not independently published frameworks.

The intended public facade is:

```go
func Run(ctx context.Context) error {
    return run(ctx, dependencies{lookup: os.LookupEnv})
}
```

`dependencies` and `run` are private. Tests in package application inject synthetic
portal/Telegram collaborators and a reporter through that seam. Keep overrides
limited to external dependencies actually needed by tests; do not create a
factory for every service. Production callers neither supply an environment
reader nor pass empty factory options. Main never receives an application graph.

Use a private runtime owner holding only resources and collaborators needed for
startup, execution and cleanup. Configuration and intermediate construction
variables need not become fields. It is not a service locator: services receive
their specific dependencies through constructors and never receive the runtime.

### Construction and startup order

1. Parse configuration. Resolve and validate the database path, acquire its lock,
   securely create/open its file, then open the Store and apply migrations.
2. Create the shared policy, real clock, portal client and private failure latch.
   Inject the same clock into service collaborators and portal.Options.Now.
3. Construct one tracking coordinator, then its private monitoring wrapper.
4. Construct one raw Telegram repository client independently of update intake.
5. Construct one notification worker using that client as Sender, the monitored
   coordinator and notifications.Persistence bound to the root Store.
6. Construct Telegram intake with that client, monitored tracking, policy, clock,
   membership callback and direct-reply callback. Create the scheduler with the
   same monitored tracking service.
7. Validate Telegram using the existing bounded getMe operation. No incoming
   polling, delivery, scheduling or recovery starts before validation succeeds.
8. Recover uncertain delivery reservations once, then run intake, notification
   delivery and scheduling under the private supervisor.

Finish constructing the graph before starting it. Local storage acquisition and
migrations necessarily perform I/O; component constructors must not start
background loops or make Telegram/portal requests.

### Resolve the Telegram construction cycle

Today the notification worker sends through the Telegram intake Adapter, while
that adapter calls worker methods through closures assigned before the worker
exists. This depends on startup sequencing and complicates construction tests.

Use the existing repository Client as the shared raw Bot API collaborator:

```text
Store + Portal + Policy + Clock -> tracking coordinator -> monitored tracking
Store + monitored tracking + Policy + Clock + Telegram client -> worker
Telegram client + monitored tracking + worker callbacks + Policy + Clock -> intake
monitored tracking + Clock -> scheduler
```

Remove Handler from the client's constructor configuration. Its polling method
accepts a handler for that run, for example
`Poll(ctx, handle func(context.Context, *models.Update))`. Register the catch-all
handler before library polling starts. The pinned go-telegram/bot version supports
RegisterHandlerMatchFunc for both messages and membership updates. Preserve
ordered handlers, allowed update kinds, sanitized diagnostics and bounded token
validation. Reject a second polling start to prevent multiple registrations or
pollers.

The intake consumes a narrow polling/deletion interface and supplies its handler
when it runs; the worker consumes domain.Sender from the same client. Token,
endpoint and HTTP client configuration belong to the repository Client rather
than intake. Do not expose the underlying library Bot to application wiring.

Require intake's direct-reply and membership collaborators at construction.
Remove its raw Send forwarding and optional raw-send fallback. Replies always
pass through the same worker as durable delivery, preserving their shared rate
gate. Membership mapping and direct-send failure monitoring stay private in
application wiring. No mutable dependency setter or nil worker capture remains.

### Resource and failure ownership

The private bootstrap owner covers partial construction as well as normal
runtime cleanup. Register cleanup as each resource is acquired. If construction
fails, release only acquired resources in reverse dependency order, preserving
sanitized startup errors and attempting all required cleanup. Do not scatter
repeated transport.Close/coordinator.Shutdown sequences across constructor
branches. The advisory lock must remain held until SQLite has closed; report
close failures rather than discarding them.

Retain the current runtime cancellation order: cancel shared work; close tracking
admission and cancel/drain detached owner operations; join polling, intake
handlers, worker and scheduler; finish intake cleanup; close SQLite; release the
lock. Detached tracking operations and cancellation-independent delivery
acknowledgments must finish before storage close. Do not join work that requires
coordinator cancellation before invoking Shutdown.

Keep the private failure latch distinct from ordinary context cancellation, so
signals return success and fatal runtime failures return an operational error.
Preserve monitoring for tracking storage failures, scheduler inventory failures,
membership persistence failures, outbound gate failures, invalid tokens from
all current sources and worker failures. Ignore canceled tracking operations
when classifying storage failures during shutdown. Treat an unexpected component
return while the process is active as failure, including a nil return.

Store continues constructing its entity repositories and transaction-bound
counterparts. Application wires the root Store into tracking.Persistence and
notifications.Persistence; services retain RunInTx orchestration. No SQL or
business workflow moves into bootstrap code. No schema, migration, deployment
or infrastructure change is needed.

### Main

Keep context/signals and exit reporting in main. Its application interaction is
simply `application.Run(ctx)`. Preserve the existing layout and deferred signal
cleanup. os.Exit bypasses that defer on failure, which is acceptable because the
process is terminating; application resources have already been cleaned up before
Run returns. Main imports no services, repositories, entry points or bootstrap
test types.

## Implementation sequence

1. Rename internal/app to internal/application, updating main, tests, maintained
   references and the ADR 0015 clarification when accepted.
2. Add the Run(ctx) facade, make factory overrides and bootstrap helpers private,
   and adapt same-package bootstrap tests to the private run seam.
3. Separate raw Telegram client construction from handler registration and inject
   the client into intake. Update Telegram tests around this boundary.
4. Extract explicit wiring and private runtime ownership, centralizing partial
   startup cleanup and preserving failure categories and startup order.
5. Update Telegram/operations documentation where constructor and lifecycle
   descriptions change. Record verification without rewriting historical audits.

No compatibility wrappers are required: this is an undeployed private application.
Do not perform unrelated database or package hierarchy redesigns.

## Review and verification plan

The revised proposal agrees with ADR 0015's bootstrap ownership and retains the
existing service/repository boundaries. It removes the need to publish a runtime
supervisor, tracking monitor, clock or protected storage opener for main. The
remaining Telegram API change is justified by removing an actual construction
cycle; it is independent of the bootstrap's package name.

Retain and adapt existing behavior tests, rather than testing the number of
constructor calls or exposing private components to main:

- Invalid configuration/path/lock prevents network activity, and every partial
  startup failure releases acquired resources. Database close precedes unlock.
- Invalid getMe prevents recovery and all runtime loops. Delivery recovery
  completes before intake or sends begin.
- One coordinator serves all entry points and notifications; one worker owns
  direct/durable send cooldown and membership effects.
- Message and membership dispatch remain ordered after the client split;
  credential deletion and polling still propagate token rejection.
- Signal cancellation drains work successfully; runtime token/storage failure
  drains with an error; unexpected component return cancels siblings.
- Detached portal operations and active sends finish before SQLite closes.
- main references only application.Run; application exposes no test factories
  or runtime graph; services never depend on application; production never
  imports internal/experimental.
- Implementation validation: Go 1.27.3 tests, race tests, vet, native build,
  documentation links and whitespace. No live endpoints are necessary.

### 2026-11-07: Analysis verification

The earlier analysis checked production constructors, runtime callbacks,
transaction binding, shutdown, existing tests and the pinned library's handler
registration. The unchanged application passed go test ./..., go test -race
./..., go vet ./... and a native build with Go 1.27.3, after selecting Node
v26.13.0 through nvm. Portal httptest fixtures required localhost socket access
outside the default sandbox. No live endpoint was contacted.

After the maintainer's clarification, rechecked the existing app.Run, its public
test seams, monitoring wrapper and main against this revised ownership. This
revision changes proposal documentation only. Relative links and whitespace
are checked separately; baseline test results do not verify an implementation
of this proposed refactor.

### 2026-11-07: Implementation verification

Implemented on the maintainer's instruction. The root main calls
application.Run(ctx); internal/application exposes only that function. Private
wiring constructs the shared policy, clock, portal client, tracking coordinator,
raw Telegram client, notification worker, intake and scheduler. Private runtime
code supervises components and centralizes partial-startup and shutdown cleanup.
Telegram intake requires its injected client and worker callbacks, and binds its
handler only when polling starts. Store transactions and business services are
unchanged. CI, Docker build inputs, maintained guides and ADR 0015 are updated.

Go 1.27.3 checks passed with Node v26.13.0 selected through nvm:
`go test -timeout 60s ./...`, `go test -race -timeout 90s ./...`, `go vet ./...`,
`go build -o /tmp/case-watch-application .` and a static Linux ARM64 cross-build.
The suites use synthetic HTTP transports and localhost portal fixtures; socket
access was approved outside the sandbox. Added coverage includes startup cleanup,
recovery before polling, canceled-send acknowledgment before storage close,
unexpected component returns, early fatal signals, required intake dependencies,
single-use polling and ordered message/membership dispatch. Transport send and
validation assertions moved to the repository-client tests.

Markdown relative links, formatting, production import boundaries, the root
entry point and the Docker context allowlist were checked. The ordinary image
build reached Go module download but lacked the managed cloud proxy's CA inside
the build stage. A temporary Dockerfile adds build-only CA secret mounts and a
bounded Go compilation concurrency; the repository Dockerfile and final trust
store do not contain the session CA. That local image build succeeded. No live
Telegram/portal acceptance, application deployment or target-device execution
was performed.

### 2026-11-08: Automatic notifications removed

[ADR 0018](0018_remove-automatic-notifications.md) replaces notification-specific
requirements in this record. Scheduled and manual tracking keep cache/history
without enqueueing outbound work. The runtime now supervises Telegram intake and
the scheduler; direct replies retain rate limits and invalid-token shutdown.
Migration 2 discards legacy delivery state while preserving tracking data.
