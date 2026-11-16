import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { Portal, RpcState, bootstrapObject } from "../src/portal.js";
import { Cookies } from "../src/http.js";
import { server } from "./support.js";
const fixtures = new URL(
  "../../../internal/clients/testdata/",
  import.meta.url,
);
const read = (name) => readFileSync(new URL(name, fixtures), "utf8");
test("live adapter speaks five-request fixture protocol, preserves stage and owner cookie isolation", async (t) => {
  let step = 0;
  const bodies = [];
  const s = await server(t, (req, res) => {
    if (req.method === "GET") {
      step = 0;
      res.setHeader("Set-Cookie", "JSESSIONID=session; Path=/");
      res.end(read("bootstrap.html"));
    } else {
      bodies.push(JSON.parse(req.body));
      assert.equal(req.headers.cookie, "JSESSIONID=session");
      res.end(read(`rpc${step++}.json`));
    }
  });
  const waits = [];
  let now = 10000;
  const portal = new Portal({
    base: s.url,
    now: () => now,
    waiting: async (ms) => {
      waits.push(ms);
      now += Math.max(0, ms);
    },
  });
  t.after(() => portal.close());
  const result = await portal.check(
    1,
    "synthetic-number",
    "synthetic-password",
  );
  assert.equal(result.stage, "Synthetic stage");
  assert.equal(result.checkedAt, 18000);
  assert.equal(s.requests.length, 5);
  assert.deepEqual(waits, [2000, 2000, 2000, 2000]);
  assert.equal(bodies[0].rpc[0].event, "value-changed");
  assert.equal(bodies[1].rpc[0].value, "synthetic-number");
  assert.equal(bodies[2].rpc[0].value, "synthetic-password");
  assert.equal(bodies[3].rpc[0].event, "click");
  assert.deepEqual(
    bodies.map((b) => b.syncId),
    [0, 1, 2, 3],
  );
  assert.ok(bodies.every((b) => b.csrfToken));
  await portal.check(1, "n", "p");
  assert.equal(s.requests[5].headers.cookie, "JSESSIONID=session");
  await portal.check(2, "n", "p");
  assert.equal(s.requests[10].headers.cookie, undefined);
  portal.forget(1);
  await portal.check(1, "n", "p");
  assert.equal(s.requests[15].headers.cookie, undefined);
  assert.equal(s.requests[0].socket, s.requests[5].socket);
});
test("portal rejects HTTP failures and redirects without retries or secrets and forgets sessions", async (t) => {
  for (const status of [401, 403, 302, 500, 503]) {
    const s = await server(t, (_req, res) => {
      res.statusCode = status;
      res.setHeader("Location", "/elsewhere");
      res.end("secret remote message");
    });
    const portal = new Portal({ base: s.url, interval: 0 });
    await assert.rejects(
      portal.check(1, "secret-number", "secret-password"),
      (error) => {
        assert.equal(
          error.code,
          [401, 403].includes(status) ? "authentication" : "transport",
        );
        assert.doesNotMatch(error.message, /secret/);
        return true;
      },
    );
    assert.equal(s.requests.length, 1);
    assert.equal(portal.sessions.size, 0);
    portal.close();
  }
});
test("portal bounds body size, request time, logical budget, cancellation and pre-aborted calls", async (t) => {
  const s = await server(t, (req, res) => {
    if (req.url === "/large") res.end("x".repeat((4 << 20) + 1));
    else res.end(read("bootstrap.html"));
  });
  const portal = new Portal({ base: s.url, interval: 0, budget: 1 });
  t.after(() => portal.close());
  await assert.rejects(portal.check(1, "n", "p"), { code: "protocol" });
  assert.equal(s.requests.length, 1);
  const abort = new AbortController();
  abort.abort(new Error("canceled"));
  await assert.rejects(portal.check(1, "n", "p", abort.signal));
  assert.equal(s.requests.length, 1);
  const hanging = await server(t, () => {});
  const timeout = new Portal({ base: hanging.url, perRequest: 20 });
  t.after(() => timeout.close());
  await assert.rejects(timeout.check(1, "n", "p"), { code: "deadline" });
  assert.equal(hanging.requests.length, 1);
  const overall = new Portal({ base: s.url, overall: 20, interval: 1000 });
  t.after(() => overall.close());
  await assert.rejects(overall.check(1, "n", "p"), { code: "deadline" });
  const large = new Portal({ base: s.url + "/large" });
  t.after(() => large.close());
  await assert.rejects(large.check(1, "n", "p"), { code: "protocol" });
});
test("RPC parsing handles dynamic IDs, semantic features, malformed synchronization/splices and stage text", () => {
  const state = new RpcState();
  state.apply(bootstrapObject(read("bootstrap.html"), "var uidl ="));
  assert.equal(state.login().length, 3);
  const shifted = new RpcState();
  const uidl = bootstrapObject(read("bootstrap.html"), "var uidl =");
  for (const c of uidl.changes) {
    c.node += 10000;
    if (c.addNodes) c.addNodes = c.addNodes.map((n) => n + 10000);
  }
  shifted.apply(uidl);
  assert.deepEqual(
    shifted.login(),
    state.login().map((n) => n + 10000),
  );
  for (const changes of [
    null,
    [null],
    [{ node: -1, type: "attach" }],
    [{ node: 1, type: "unknown" }],
    [{ node: 1, type: "put", key: 0 }],
    [{ node: 1, type: "splice", feat: 2, index: 2 }],
    [{ node: 1, type: "splice", feat: 2, index: 0, addNodes: ["bad"] }],
  ])
    assert.throws(
      () => new RpcState().apply({ syncId: 0, clientId: 0, changes }),
      { code: "protocol" },
    );
  assert.throws(() => state.apply({ syncId: 0.5, clientId: 0 }), {
    code: "protocol",
  });
  assert.throws(() => state.response("for(;;);[]"), { code: "protocol" });
  for (let i = 0; i < 4; i++) state.response(read(`rpc${i}.json`));
  assert.equal(state.stage(), "Synthetic stage");
  assert.deepEqual(
    bootstrapObject(
      'var uidl = {"a":"}\\\"{","nested":{"b":1}};',
      "var uidl =",
    ),
    { a: '}"{', nested: { b: 1 } },
  );
});
test("cookie matching handles domain/path/security, deletion and max-age precedence", () => {
  const jar = new Cookies();
  jar.set(
    "https://example.com/a/b",
    [
      "host=x; Path=/a; Secure",
      "other=y; Domain=example.com; Path=/",
      "expired=z; Max-Age=0",
      "expiry=v; Max-Age=10; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
    ],
    1000,
  );
  assert.equal(
    jar.get("https://example.com/a/b", 1001),
    "host=x; expiry=v; other=y",
  );
  assert.equal(jar.get("http://sub.example.com/a/b", 1001), "other=y");
  assert.equal(jar.get("https://example.com/ab", 12000), "other=y");
});
