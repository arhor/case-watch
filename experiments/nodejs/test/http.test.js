import test from "node:test";
import assert from "node:assert/strict";
import { Http } from "../src/http.js";
import { server, tick } from "./support.js";
test("HTTP aborts hung request, rejects oversized body, and never retries closed sockets", async (t) => {
  const s = await server(t, (req, res) => {
    if (req.url === "/close") req.socket.destroy();
    else if (req.url === "/big") res.end("x".repeat(100));
    else if (req.url === "/ok") res.end("ok");
  });
  const http = new Http();
  t.after(() => http.close());
  assert.equal((await http.request(s.url + "/ok")).body, "ok");
  await assert.rejects(http.request(s.url + "/close"), { code: "transport" });
  assert.equal(s.requests.length, 2);
  await assert.rejects(http.request(s.url + "/big", { limit: 10 }), {
    code: "protocol",
  });
  const c = new AbortController();
  const hung = http.request(s.url + "/hung", { signal: c.signal });
  await tick();
  c.abort(new Error("cancel"));
  await assert.rejects(hung, /cancel/);
  await assert.rejects(http.request(s.url + "/hung", { timeout: 10 }), {
    code: "deadline",
  });
});
