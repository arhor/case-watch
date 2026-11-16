import test from "node:test";
import assert from "node:assert/strict";
import { run } from "../src/main.js";
import { fixture, deferred, tick } from "./support.js";
const env = {
  CASE_WATCH_TELEGRAM_BOT_TOKEN: "123:" + "a".repeat(35),
  CASE_WATCH_DB_PATH: "ignored",
};
test("startup failure redacts collaborator secrets and releases constructed resources", async (t) => {
  const f = fixture(t),
    events = [];
  await assert.rejects(
    run({
      env,
      makeStorage: () => ({ close: () => events.push("store") }),
      makePortal: () => ({ close: () => events.push("portal") }),
      makeTelegram: () => ({
        initialize: async () => {
          throw new Error("secret");
        },
        close: async () => events.push("telegram"),
      }),
    }),
    (error) => {
      assert.equal(error.message, "service_startup_validation_failed");
      return true;
    },
  );
  assert.deepEqual(events, ["telegram", "portal", "store"]);
});
test("lifecycle cancels and drains active service work before store closure", async (t) => {
  const f = fixture(t);
  await f.cases.enroll(1, "n", "p");
  f.advance(60000);
  const entered = deferred(),
    release = deferred(),
    c = new AbortController(),
    events = [];
  let service;
  const store = new Proxy(f.store, {
    get(target, key) {
      if (key === "close") return () => events.push("store");
      const value = target[key];
      return typeof value === "function" ? value.bind(target) : value;
    },
  });
  const portal = {
    check: async (_o, _n, _p, signal) => {
      entered.resolve(signal);
      await release.promise;
      events.push("drained");
      return { stage: "late" };
    },
    forget() {},
    close() {
      events.push("portal");
    },
  };
  const runPromise = run({
    env,
    signal: c.signal,
    makeStorage: () => store,
    makePortal: () => portal,
    makeTelegram: (_token, cases) => {
      service = cases;
      return {
        initialize: async () => {},
        run: async (signal) => {
          await cases.status(1, signal);
        },
        close: async () => events.push("telegram"),
      };
    },
    makeSchedule: () => ({
      run: async (signal) => {
        if (!signal.aborted)
          await new Promise((resolve) =>
            signal.addEventListener("abort", resolve, { once: true }),
          );
      },
    }),
  });
  const signal = await entered.promise;
  c.abort();
  await tick();
  assert.equal(signal.aborted, true);
  assert.deepEqual(events, []);
  release.resolve();
  await runPromise;
  assert.equal(service.closed, true);
  assert.ok(events.indexOf("drained") < events.indexOf("store"));
  assert.equal(f.store.get(1).stage, "initial");
});

test("cleanup failures stay redacted and do not skip remaining cleanup", async () => {
  const closed = [];
  await assert.rejects(
    run({
      env,
      makeStorage: () => ({
        close() {
          closed.push("store");
          throw new Error("secret database path");
        },
      }),
      makePortal: () => ({
        close() {
          closed.push("portal");
          throw new Error("secret portal");
        },
      }),
      makeTelegram: () => ({
        initialize: async () => {
          throw new Error("secret token");
        },
        close: async () => {
          closed.push("telegram");
          throw new Error("secret reply");
        },
      }),
    }),
    { message: "service_operation_failed" },
  );
  assert.deepEqual(closed, ["telegram", "portal", "store"]);
});
