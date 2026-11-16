import test from "node:test";
import assert from "node:assert/strict";
import { fixture, deferred, tick } from "./support.js";
import { fail } from "../src/common.js";
test("completion freshness and exact cache boundary do not consume guards", async (t) => {
  const f = fixture(t);
  await f.cases.enroll(1, "n", "p");
  f.advance(59999);
  const cached = await f.cases.status(1);
  assert.equal(cached.fresh, false);
  assert.equal(f.calls, 1);
  assert.equal(
    f.store.db.prepare("SELECT count(*) AS n FROM cooldowns").get().n,
    0,
  );
  f.advance(1);
  const refreshed = await f.cases.status(1);
  assert.equal(refreshed.fresh, true);
  assert.equal(f.calls, 2);
  assert.equal(refreshed.nextEligibleAt, f.now + 60000);
});
test("failed refresh preserves successful cache and persistent guards across restart", async (t) => {
  const f = fixture(t);
  await f.cases.enroll(1, "n", "p");
  const checked = f.now;
  f.advance(60000);
  f.failPortal();
  const failed = await f.cases.status(1);
  assert.equal(failed.checkedAt, checked);
  assert.equal(failed.fresh, false);
  assert.equal(failed.error.code, "check");
  f.restart();
  const cached = await f.cases.status(1);
  assert.equal(cached.error, undefined);
  assert.equal(f.calls, 2);
  f.advance(60000);
  assert.equal((await f.cases.scheduled(1)).error.code, "check");
  assert.equal((await f.cases.status(1)).error, undefined);
  assert.equal(f.calls, 3);
});
test("scheduled guard preserves manual guard and recent enrollment is reused after restart", async (t) => {
  const f = fixture(t);
  await f.cases.enroll(1, "n", "p");
  f.restart();
  await f.cases.scheduled(1);
  assert.equal(f.calls, 1);
  f.advance(60000);
  await f.cases.scheduled(1);
  assert.deepEqual(
    f.store.db
      .prepare("SELECT subject_type FROM cooldowns")
      .all()
      .map((r) => r.subject_type),
    ["case_check"],
  );
  await f.cases.status(1);
  assert.equal(f.calls, 2);
});
test("shared refresh survives one canceled waiter; final waiter cancellation aborts work", async (t) => {
  const f = fixture(t);
  await f.cases.enroll(1, "n", "p");
  f.advance(60000);
  const gate = deferred(),
    entered = deferred();
  let calls = 0;
  f.portal.check = async (_o, _n, _p, signal) => {
    calls++;
    entered.resolve(signal);
    await gate.promise;
    return { stage: "new" };
  };
  const a = new AbortController(),
    b = new AbortController();
  const first = f.cases.status(1, a.signal),
    second = f.cases.status(1, b.signal);
  const signal = await entered.promise;
  a.abort(fail("canceled"));
  assert.equal((await first).inProgress, true);
  assert.equal(signal.aborted, false);
  gate.resolve();
  assert.equal((await second).fresh, true);
  assert.equal(calls, 1);
  f.advance(60000);
  const ended = deferred();
  f.portal.check = async (_o, _n, _p, s) => {
    s.addEventListener("abort", () => ended.resolve(), { once: true });
    await ended.promise;
    throw fail("canceled");
  };
  const c = new AbortController();
  const last = f.cases.status(1, c.signal);
  await tick();
  c.abort(fail("canceled"));
  assert.equal((await last).inProgress, true);
  await ended.promise;
  await tick();
});
test("removal fences late refresh and enrollment results, drains before deletion", async (t) => {
  const f = fixture(t);
  await f.cases.enroll(1, "n", "p");
  f.advance(60000);
  const gate = deferred();
  f.portal.check = async () => {
    await gate.promise;
    return { stage: "late" };
  };
  const check = f.cases.status(1);
  await tick();
  let removed = false;
  const removal = f.cases.remove(1).then(() => {
    removed = true;
  });
  await tick();
  assert.equal(removed, false);
  await assert.rejects(f.cases.status(1), { code: "removing" });
  gate.resolve();
  await removal;
  assert.ok((await check).error);
  assert.equal(f.cases.hasEnrollment(1), false);
  const gate2 = deferred();
  f.portal.check = async () => {
    await gate2.promise;
    return { stage: "late" };
  };
  const enroll = f.cases.enroll(1, "n", "p");
  await tick();
  const remove = f.cases.remove(1);
  gate2.resolve();
  await assert.rejects(enroll);
  await assert.rejects(remove, { code: "not_found" });
  assert.equal(f.cases.hasEnrollment(1), false);
});
test("canceled removal retains enrollment and reopens admission after drain", async (t) => {
  const f = fixture(t);
  await f.cases.enroll(1, "n", "p");
  f.advance(60000);
  const gate = deferred();
  f.portal.check = async () => {
    await gate.promise;
    return { stage: "late" };
  };
  const check = f.cases.status(1);
  await tick();
  const c = new AbortController();
  const removal = f.cases.remove(1, c.signal);
  c.abort(fail("canceled"));
  gate.resolve();
  await assert.rejects(removal, { code: "canceled" });
  await check;
  assert.equal(f.cases.hasEnrollment(1), true);
  await f.cases.status(1);
});
test("shutdown closes admission and drains all owners", async (t) => {
  const f = fixture(t);
  await f.cases.enroll(1, "n", "p");
  f.advance(60000);
  const gate = deferred();
  f.portal.check = async (_o, _n, _p, s) => {
    await gate.promise;
    assert.equal(s.aborted, true);
    return { stage: "late" };
  };
  const check = f.cases.status(1);
  await tick();
  const shutdown = f.cases.shutdown();
  await assert.rejects(f.cases.status(1), { code: "closed" });
  gate.resolve();
  await shutdown;
  assert.ok((await check).error);
  assert.equal(f.store.get(1).stage, "initial");
});
test("owner authorization, invalid enrollment, duplicate admission and failed enrollment forget sessions", async (t) => {
  const f = fixture(t);
  for (const owner of [0, -1, NaN, 1.5]) {
    await assert.rejects(f.cases.enroll(owner, "n", "p"), { code: "denied" });
    await assert.rejects(f.cases.status(owner), { code: "denied" });
    await assert.rejects(f.cases.remove(owner), { code: "denied" });
    assert.throws(() => f.cases.hasEnrollment(owner), { code: "denied" });
  }
  await assert.rejects(f.cases.enroll(1, "n", "   "), { code: "invalid" });
  assert.equal(f.calls, 0);
  f.failPortal();
  await assert.rejects(f.cases.enroll(1, "n", "p"), { code: "check" });
  assert.equal(f.cases.hasEnrollment(1), false);
  assert.deepEqual(f.forgot, [1]);
});
test("observation failure retains cache, storage errors reported without leaking collaborator error", async (t) => {
  const f = fixture(t);
  let reported = 0;
  f.cases.onStorageError = () => reported++;
  await f.cases.enroll(1, "n", "p");
  f.advance(60000);
  f.portal.check = async () => ({ stage: "new" });
  f.store.db.exec(
    "CREATE TRIGGER reject_history BEFORE INSERT ON history BEGIN SELECT RAISE(ABORT,'secret'); END;",
  );
  const result = await f.cases.status(1);
  assert.equal(result.stage, "initial");
  assert.equal(result.error.message, "storage");
  assert.equal(reported, 1);
  assert.equal(f.store.get(1).stage, "initial");
});

test("scheduled cancellation drains its shared flight; history includes only actual transitions", async (t) => {
  const f = fixture(t);
  await f.cases.enroll(1, "n", "p");
  f.advance(60000);
  const c = new AbortController(),
    gate = deferred(),
    entered = deferred();
  f.portal.check = async (_o, _n, _p, signal) => {
    entered.resolve(signal);
    await gate.promise;
    return { stage: "late" };
  };
  const check = f.cases.scheduled(1, c.signal);
  await entered.promise;
  c.abort();
  let completed = false;
  check.then(() => {
    completed = true;
  });
  await tick();
  assert.equal(completed, false);
  gate.resolve();
  await check;
  assert.equal(f.store.get(1).stage, "initial");
  f.advance(60000);
  f.portal.check = async () => ({ stage: "second" });
  await f.cases.scheduled(1);
  f.advance(60000);
  await f.cases.status(1);
  f.advance(60000);
  f.portal.check = async () => ({ stage: "third" });
  await f.cases.status(1);
  assert.deepEqual(
    f.store.db
      .prepare("SELECT previous_stage,stage FROM history ORDER BY sequence")
      .all()
      .map((r) => [r.previous_stage, r.stage]),
    [
      ["initial", "second"],
      ["second", "third"],
    ],
  );
});
