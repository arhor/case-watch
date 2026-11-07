---
status: accepted
date: 2026-11-01
decision-makers: Repository maintainer
---

# Choose weekday polling and rate-limited fresh status checks

## Context and Problem Statement

Case Watch needs a predictable automatic polling schedule and on-demand `/status` behavior without making unnecessary requests to the Gdańsk portal. The existing Go probe completes one logical case check through several HTTP requests; the successful live experiment used five requests.

[ADR 0004](0004_choose-sqlite-persistence.md) defines successful observations, transition history, and pending notifications. [ADR 0005](0005_choose-user-authorization-and-enrollment.md) requires authorization for commands and background work. Neither defines check timing or freshness. The maintainer proposed six weekday runs and a one-minute per-user manual limit, and confirmed that public holidays remain included and a recent scheduled result satisfies `/status`.

## Decision Drivers

- Check regularly during the Polish office day without frequent background traffic.
- Let users explicitly request fresh information while bounding repeated manual attempts.
- Clearly distinguish cached results, fresh results, and failed attempts.
- Share check coordination between manual and scheduled callers.
- Preserve the established atomic comparison and notification behavior.

## Considered Options

- Fixed weekday office-day schedule with rate-limited fresh manual checks.
- Continuous interval polling throughout the day and night.
- Scheduled polling with cache-only `/status`.
- Unrestricted fresh checks for every `/status` invocation.

## Decision Outcome

### Automatic schedule

Check active, authorized case associations Monday–Friday at 08:00, 10:00, 12:00, 14:00, 16:00, and 18:00 in the IANA time zone `Europe/Warsaw`. Include public holidays; do not introduce a holiday calendar.

The equivalent five-field cron expression is:

```cron
0 8,10,12,14,16,18 * * 1-5
```

Evaluate these as local wall-clock times in Warsaw, not fixed UTC offsets or elapsed two-hour intervals. Bundle or otherwise ensure the required time-zone data is available at runtime; host/container default time zones must not change the schedule. A scheduled run begins at each slot and processes its cases sequentially, so not every case necessarily starts at the exact slot time.

Skip missed slots after downtime; do not replay them in a burst. Startup alone does not trigger a catch-up run. Enrollment baseline checks and manual checks remain independent of the weekday schedule. Authorized users can request `/status` outside scheduled hours, including weekends.

### Manual checks and cached responses

Apply a 60-second cooldown per Telegram user to actual manual refresh attempts, measured from attempt start, including failed attempts. Cached replies do not start or extend this cooldown. `/status` that is eligible for refresh starts a logical case check without waiting for the next scheduled slot, subject to existing in-flight work and per-case coordination.

| Situation | Behavior |
| --- | --- |
| Manual refresh eligible and no recent/in-flight case check | Start a fresh logical check |
| User's manual attempt started less than 60 seconds ago | Make no new portal request; return the cached result with its last successful check time and next manual eligibility time |
| Case was successfully checked less than 60 seconds ago, including by the scheduler | Return that cached result with its successful check time; do not perform a redundant check |
| Check for the same tracked case is running | Join/reuse the in-flight result instead of starting another check; repeated calls do not create queued duplicate refreshes |
| No successful cached result exists and a new check is not permitted | Explain that no successful result is available and indicate that checking is in progress or when refresh is allowed |
| A fresh attempt fails | Report the failure; show the last successful cached result with its timestamp if one exists |

The limit applies to complete checks, not each HTTP request in the Vaadin exchange. A failed attempt consumes the cooldown, so repeated errors cannot bypass it. Enforce a corresponding 60-second minimum between attempt starts for the same tracked case across manual and scheduled callers; a recent failed scheduled attempt therefore also prevents an immediate manual retry. Do not label its older cached value as recently refreshed. When a scheduled case is inside this guard, skip it for that slot rather than scheduling an immediate retry.

Measure successful-result freshness from successful completion, separately from attempt-start cooldowns. Persist user manual-attempt and case attempt-start timestamps so a process restart does not reset these guards. Reserve attempts atomically before portal access; cached responses and joining existing work do not consume a new manual attempt. No cross-user ownership access is granted by sharing check coordination. This decision does not require identifying duplicate enrollments by decrypting or indexing case numbers.

### Coordination and observations

Allow at most one in-flight check per tracked case. Prevent scheduled runs from overlapping; skip a new scheduled run if the previous one is still running rather than accumulate a backlog. Recheck authorization before executing queued background work, as required by ADR 0005.

Use the same observation transaction for scheduled and manual results. Every successful observation updates the cached successful-check timestamp even when the stage is unchanged; unchanged stages create no transition history or notification. Failed checks do not overwrite the last successful stage or its timestamp.

A manual check that detects a transition creates history and pending change notifications under ADR 0004, just like a scheduled check. The direct `/status` reply does not acknowledge or replace the durable change notification; users may receive both. The first successful observation remains a baseline rather than a fabricated change.

Do not introduce automatic portal retries in this initial policy. After failure, the next eligible manual request or normal schedule may try again. Telegram notification delivery retries are a separate decision. Existing HTTP bounds for a single portal check remain independent from this scheduling policy.

### Consequences

- The automatic workload is six logical checks per active tracked case per weekday, before skips, plus eligible manual/enrollment checks. Each logical check involves multiple HTTP requests.
- Public holidays and manual weekend checks remain simple and explicit.
- Recent scheduled results and in-flight coordination reduce redundant traffic.
- A user can receive a cached result even though their own manual limit has not been consumed, because the per-case guard also applies.
- Persistent guard timestamps and separate successful-observation timestamps are required. A crash after reservation may delay retry until the guard expires, which is accepted to avoid request bursts.
- No missed-slot replay means downtime may delay discovery until the next scheduled or manual check. Intermediate portal transitions can remain unobserved.
- Sequential background work favors low load over exact per-case wall-clock execution times.

## Scope and Non-goals

This decision defines scheduling and command freshness behavior, not a cron library or deployment mechanism. Concrete `/status` syntax and selection among multiple enrolled cases remain command-design work; the same user-wide manual guard applies regardless of which case is selected. Notification retry timing, portal-wide quotas for public use, and enrollment flows are not selected here. No scheduler, runtime dependency, or live requests are introduced by this record. Existing ADRs remain accepted.

## Implementation Plan

These tasks remain pending:

- [x] Implement Warsaw-local slot calculation and sequential scheduling under `internal/scheduler/`, with explicit missed-slot and overlap handling and available time-zone data.
- [x] Add check coordination under `internal/tracking/` shared by scheduler and Telegram callers: per-user cooldown, per-case attempt guard, and in-flight result sharing. Inject clock and portal interfaces for offline testing.
- [x] Extend `internal/storage/` with atomic attempt reservation and persisted user/case attempt timestamps, plus the last successful observation timestamp distinct from transition history. Follow ADR 0004 migrations and transaction boundaries.
- [x] Connect `/status` under `internal/telegram/` to the shared coordinator after authorization, formatting fresh/cached provenance, timestamps, next eligibility, and failures without exposing credentials.
- [x] Route successful manual and scheduled observations through the same transition/pending-notification transaction; never hold database transactions across portal access.
- [x] Document the schedule and command behavior. Retain live validation limits; use fake clocks and synthetic portal outcomes for implementation tests.

### Verification

- [x] All six slots occur Monday–Friday in Warsaw time, including public holidays; none occur automatically on weekends. Winter and summer cases follow local time independently of the host time zone.
- [x] Restart does not replay missed slots or reset persisted cooldowns; a slow scheduled run does not overlap or accumulate another run.
- [x] A manual attempt at t=0 blocks a new attempt at t=59 seconds and permits one at t=60 seconds, including when the first attempt failed. Another case selection does not bypass the user guard.
- [x] Cached replies and joins do not extend the manual cooldown. Per-case guards prevent redundant manual/scheduled attempts, including immediately after failed scheduled attempts.
- [x] Recent scheduled success satisfies `/status`; freshness and attempt timestamps are evaluated separately and older results are not presented as newly successful.
- [x] Concurrent callers for one tracked case result in one portal check. A missing cache or failed check produces the documented response without a false stage update.
- [x] Successful unchanged checks refresh the cache timestamp without history/notifications; transitions from either source follow ADR 0004 and the direct reply does not mark pending notifications delivered.
- [x] Authorization gates scheduled execution and manual access. Tests use synthetic data and make no live Telegram or government-portal requests.

## Pros and Cons of the Options

### Weekday slots and rate-limited fresh manual checks

Chosen because it combines predictable low background frequency with user-controlled freshness. Requires coordination, timestamps, and clear cached-result responses; automatic detection can lag by two hours or overnight/weekends.

### Continuous interval polling

Simple interval-based execution can shorten automatic discovery delay. Not chosen because checking outside the proposed office-day slots adds traffic without an established need.

### Cache-only status commands

Avoids command-triggered portal traffic and provides quick replies. Not chosen because the maintainer explicitly wants fresh on-demand checks when permitted.

### Unrestricted fresh status commands

Easy to explain as always fetching live data. Rejected because repeated commands and failures can create unnecessary portal traffic.

## More Information

The maintainer confirmed the weekday/public-holiday interpretation and reuse of recent scheduled results on 2026-11-01. This policy complements [the portal client's request bounds](../portal-client.md); the probe's five-request success is not evidence of long-running scheduler reliability. Implementation, repeated live checks, and target-device validation remain pending.

### 2026-11-01: Independent implementation evidence

`scheduler.Next/Run` and embedded `time/tzdata` implement the six Warsaw weekday slots, holidays, DST, missed-slot skipping and sequential execution. `tracking.Coordinator` and storage reservations share in-flight checks and persistent start guards, distinguish completion-based cache freshness, preserve failed-cache provenance, and atomically enqueue both manual and scheduled changes. Tests cover exact boundaries, cross-restart guards, cancellation/joins, failed attempts, late wakes and slow-run overlap. The current product has one selected case per owner; the persisted owner guard is independent of enrollment identity and survives removal/re-enrollment. No live or target-device schedule validation is claimed.

Checklist evidence maps to the reviewed implementation and offline tests in [the independent audit](../implementation-audit.md) and exact command results in [implementation progress](../implementation-progress.md). Unchecked composite items retain their pending, conditional or explicitly deferred clauses; acceptance of this ADR is not a claim of live, Docker or Pi validation.

### 2026-11-07: User authorization removed

[ADR 0017](0017_remove-user-authorization.md) removes user eligibility checks and
authorization suppression. Scheduling, cooldowns, retry ordering, blocked-recipient
gates, ownership and retained pending work continue to follow this record.
