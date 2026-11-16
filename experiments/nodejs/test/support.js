import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer } from "node:http";
import { Storage } from "../src/storage.js";
import { CaseService } from "../src/service.js";
import { fail } from "../src/common.js";
export function deferred() {
  let resolve, reject;
  const promise = new Promise((a, b) => {
    resolve = a;
    reject = b;
  });
  return { promise, resolve, reject };
}
export const tick = () => new Promise((resolve) => setImmediate(resolve));
export function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "case-watch-node-"));
  const path = join(dir, "test.db");
  let store = new Storage(path);
  let now = Date.parse("2026-01-01T12:00:00Z"),
    calls = 0,
    forgot = [];
  const portal = {
    async check() {
      calls++;
      return { stage: "initial" };
    },
    forget(owner) {
      forgot.push(owner);
    },
  };
  const make = () => new CaseService(store, portal, { now: () => now });
  let cases = make();
  t.after(async () => {
    await cases.shutdown();
    store.close();
    rmSync(dir, { recursive: true, force: true });
  });
  return {
    get store() {
      return store;
    },
    get cases() {
      return cases;
    },
    portal,
    path,
    get calls() {
      return calls;
    },
    forgot,
    get now() {
      return now;
    },
    advance(ms) {
      now += ms;
    },
    restart() {
      store.close();
      store = new Storage(path);
      cases = make();
    },
    failPortal() {
      portal.check = async () => {
        calls++;
        throw fail("secret_remote_error");
      };
    },
  };
}
export async function server(t, handler) {
  const requests = [];
  const s = createServer(async (req, res) => {
    try {
      const chunks = [];
      for await (const c of req) chunks.push(c);
      const body = Buffer.concat(chunks).toString();
      const request = {
        method: req.method,
        url: req.url,
        headers: req.headers,
        body,
        socket: req.socket,
      };
      requests.push(request);
      await handler(request, res);
    } catch {
      res.destroy();
    }
  });
  await new Promise((resolve) => s.listen(0, "127.0.0.1", resolve));
  t.after(() => {
    s.closeAllConnections();
    s.close();
  });
  return { url: `http://127.0.0.1:${s.address().port}`, requests };
}
