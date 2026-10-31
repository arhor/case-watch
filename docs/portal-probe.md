# Portal probe

Initial Go RPC experiment governed by ADR 0002. A single live validation succeeded on 2026-10-31.

Use Go 1.27.3. From the repository root, run `nvm use` before repository tooling.

Set CASE_WATCH_CASE_NUMBER and CASE_WATCH_PASSWORD in your environment, then run:

```sh
go run ./cmd/portal-probe --live
```

Credentials are used only in memory. Do not supply credentials as command-line arguments or reuse HAR cookies.

The command fetches bootstrap state, discovers login components, sends the recorded field events and click, and attempts stage extraction. It makes at most six requests, waits at least two seconds between requests, and does not follow redirects or intentionally retry requests. It downloads no static assets. The overall deadline is 60 seconds; individual requests time out after 10 seconds.

Success prints JSON containing stage and request count. Failures exit nonzero without dumping request or response bodies. Current component selection supports the English labels in the recording. Unexpected layouts stop the probe.

## Validation

Run `go test ./...` for offline checks and `GOOS=linux GOARCH=arm64 go build -o /tmp/portal-probe ./cmd/portal-probe` to cross-build.

Offline checks cover the sanitized HAR sequence, fresh cookies and synchronization counters, changed node identifiers, authentication rejection, malformed responses, expired sessions, unsupported changes, redirect refusal, cancellation, and request-budget exhaustion. Fixtures contain synthetic credentials, session values, and case data. The UI-state reducer handles the recorded semantic properties and child-list operations; it is intentionally not a complete Vaadin implementation.

Offline tests and Linux ARM64 cross-compilation have passed. On 2026-10-31, one user-authorized live invocation authenticated from a fresh session and extracted a nonempty case stage in five HTTP requests. Credentials were supplied in memory from the local HAR with explicit authorization; they were not saved in application files. No retries or additional live runs were performed.

Repeated checks, session renewal, and Pi measurements remain pending. Do not run repeated live attempts during implementation; the agreed allowance is one invocation, up to six requests total.
