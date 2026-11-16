import test from "node:test";
import assert from "node:assert/strict";
import { nextSlot, Schedule } from "../src/schedule.js";
import { deferred, tick } from "./support.js";
const at = Date.parse;
test("strictly future Warsaw slots across DST, weekends and holidays", () => {
  for (const [before, expected] of [
    ["2026-01-05T06:59:59Z", "2026-01-05T07:00:00Z"],
    ["2026-01-05T07:00:00Z", "2026-01-05T09:00:00Z"],
    ["2026-03-27T17:00:00Z", "2026-03-30T06:00:00Z"],
    ["2026-10-23T16:00:00Z", "2026-10-26T07:00:00Z"],
    ["2026-12-25T06:59:00Z", "2026-12-25T07:00:00Z"],
    ["2026-07-04T00:00:00Z", "2026-07-06T06:00:00Z"],
  ])
    assert.equal(
      new Date(nextSlot(at(before))).toISOString(),
      new Date(expected).toISOString(),
    );
});
test("sequential runs skip crossed slots and reject concurrent Run", async () => {
  let now = at("2026-01-05T06:00:00Z");
  const c = new AbortController(),
    gate = deferred(),
    entered = deferred(),
    waits = [],
    owners = [];
  const cases = {
    scheduledOwners: () => [1, 2],
    async scheduled(owner) {
      owners.push(owner);
      if (owner === 1) {
        entered.resolve();
        await gate.promise;
        now = at("2026-01-05T12:00:00Z");
      }
      return {};
    },
  };
  const schedule = new Schedule(cases, {
    now: () => now,
    waiting: async (ms) => {
      waits.push(now + ms);
      if (waits.length === 1) now += ms;
      else c.abort(new Error("stop"));
    },
  });
  const run = schedule.run(c.signal);
  await entered.promise;
  await assert.rejects(schedule.run(c.signal), { code: "scheduler" });
  gate.resolve();
  await assert.rejects(run, /stop/);
  assert.deepEqual(owners, [1, 2]);
  assert.deepEqual(waits, [
    at("2026-01-05T07:00:00Z"),
    at("2026-01-05T13:00:00Z"),
  ]);
});
test("missed minute skipped, subminute jitter accepted and cancellation drains current call", async () => {
  let now = at("2026-01-05T06:00:00Z"),
    waits = 0,
    calls = 0;
  const c = new AbortController(),
    gate = deferred(),
    entered = deferred();
  const cases = {
    scheduledOwners: () => [1, 2],
    async scheduled(_owner, signal) {
      calls++;
      entered.resolve();
      await gate.promise;
      assert.equal(signal.aborted, true);
      return {};
    },
  };
  const schedule = new Schedule(cases, {
    now: () => now,
    waiting: async (ms) => {
      now += ms + (++waits === 1 ? 60000 : 500);
    },
  });
  const run = schedule.run(c.signal);
  await entered.promise;
  assert.equal(waits, 2);
  c.abort(new Error("stop"));
  let drained = false;
  run.catch(() => {
    drained = true;
  });
  await tick();
  assert.equal(drained, false);
  gate.resolve();
  await assert.rejects(run, /stop/);
  assert.equal(calls, 1);
});
