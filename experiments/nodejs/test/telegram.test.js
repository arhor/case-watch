import test from "node:test";
import assert from "node:assert/strict";
import { Telegram, commands, formatStatus } from "../src/telegram.js";
import { fixture, server, tick, deferred } from "./support.js";
const token = "123:" + "a".repeat(35);
const message = (text, id = 1) => ({
  message_id: 10,
  from: { id, is_bot: false },
  chat: { id, type: "private" },
  text,
});
async function setup(t, handler) {
  const f = fixture(t);
  const s = await server(t, (req, res) => {
    const method = req.url.split("/").at(-1),
      body = JSON.parse(req.body);
    let result = true;
    if (handler) {
      const handled = handler(method, body, req, res);
      if (handled !== undefined) return;
    }
    if (method === "getMe") result = { id: 123 };
    if (method === "getMyCommands")
      result = commands.map((c) => ({
        description: c.description,
        command: c.command,
      }));
    res.end(JSON.stringify({ ok: true, result }));
  });
  const diagnostics = [];
  const bot = new Telegram(token, f.cases, {
    endpoint: s.url,
    now: () => f.now,
    diagnostic: (c) => diagnostics.push(c),
  });
  t.after(() => bot.close());
  return {
    f,
    s,
    bot,
    diagnostics,
    sends: () =>
      s.requests
        .filter((r) => r.url.endsWith("/sendMessage"))
        .map((r) => JSON.parse(r.body).text),
    deletes: () => s.requests.filter((r) => r.url.endsWith("/deleteMessage")),
  };
}
test("initialization only synchronizes changed default commands; nonfatal menu failure and fatal unauthorized", async (t) => {
  const { bot, s } = await setup(t);
  await bot.initialize();
  assert.deepEqual(
    s.requests.map((r) => r.url.split("/").at(-1)),
    ["getMe", "getMyCommands"],
  );
  const changed = await setup(t, (method, _body, _req, res) => {
    if (method === "getMyCommands") {
      res.end(JSON.stringify({ ok: true, result: [] }));
      return true;
    }
  });
  await changed.bot.initialize();
  assert.ok(changed.s.requests.some((r) => r.url.endsWith("/setMyCommands")));
  const failure = await setup(t, (method, _body, _req, res) => {
    if (method === "getMyCommands") {
      res.statusCode = 500;
      res.end("secret");
      return true;
    }
  });
  await failure.bot.initialize();
  assert.deepEqual(failure.diagnostics, [
    "telegram command synchronization failure",
  ]);
  const unauthorized = await setup(t, (_m, _b, _r, res) => {
    res.statusCode = 401;
    res.end("{}");
    return true;
  });
  await assert.rejects(unauthorized.bot.initialize(), { code: "unauthorized" });
});
test("private ownership, help, separate credential intake and confirmed deletion", async (t) => {
  const { f, bot, sends, deletes } = await setup(t);
  for (const bad of [
    null,
    { ...message("/start"), from: { id: 1, is_bot: true } },
    { ...message("/start"), chat: { id: 2, type: "private" } },
    { ...message("/start"), chat: { id: 1, type: "group" } },
    message("/start", -1),
  ])
    await bot.handle(bad);
  assert.equal(sends().length, 0);
  await bot.handle(message("/help"));
  assert.ok(
    commands.every((c) =>
      sends()
        .at(-1)
        .includes("/" + c.command),
    ),
  );
  await bot.handle(message("/start"));
  assert.match(sends().at(-1), /номер дела/);
  await bot.handle(message("number"));
  await bot.handle(message("password"));
  await Promise.all([...bot.tasks]);
  assert.equal(f.store.get(1).number, "number");
  assert.equal(deletes().length, 2);
  assert.match(sends().at(-1), /Готово/);
  await bot.handle(message("/start"));
  assert.match(sends().at(-1), /уже есть дело/);
  await bot.handle(message("/status"));
  assert.match(sends().at(-1), /initial/);
  await bot.handle(message("/remove"));
  await bot.handle(message("maybe"));
  assert.match(sends().at(-1), /«Да» или «Нет»/);
  assert.equal(f.cases.hasEnrollment(1), true);
  await bot.handle(message(" Нет "));
  assert.match(sends().at(-1), /Удаление отменено/);
  await bot.handle(message("/remove"));
  await bot.handle(message(" ДА "));
  assert.equal(f.cases.hasEnrollment(1), false);
  assert.match(sends().at(-1), /Дело удалено/);
});
test("byte limits, command arguments, unknown slash deletion and exact draft/removal expiry", async (t) => {
  const { f, bot, sends, deletes } = await setup(t);
  await bot.handle(message("/start secret"));
  assert.equal(deletes().length, 1);
  assert.match(sends().at(-1), /без дополнительных параметров/);
  await bot.handle(message("/start"));
  await bot.handle(message("/unknown"));
  assert.equal(deletes().length, 2);
  for (const text of ["", "a\nb", "я".repeat(129)]) {
    await bot.handle(message(text));
    assert.match(sends().at(-1), /Не удалось принять номер/);
  }
  await bot.handle(message("я".repeat(128)));
  assert.match(sends().at(-1), /Теперь отправьте пароль/);
  await bot.handle(message("я".repeat(2049)));
  assert.match(sends().at(-1), /Не удалось принять пароль/);
  f.advance(600000);
  await bot.handle(message("secret"));
  assert.match(sends().at(-1), /Время ожидания истекло/);
  assert.equal(bot.state(1).number, "");
  await bot.handle(message("/start"));
  await bot.handle(message("number"));
  f.advance(600000);
  bot.sweep();
  assert.equal(bot.state(1).number, "");
  await f.cases.enroll(1, "n", "p");
  await bot.handle(message("/remove"));
  f.advance(600000);
  await bot.handle(message("да"));
  assert.equal(f.cases.hasEnrollment(1), true);
  assert.match(sends().at(-1), /попробовать удалить/);
});
test("cancel interrupts detached enrollment and late success never commits", async (t) => {
  const { f, bot, sends } = await setup(t);
  const gate = deferred(),
    entered = deferred();
  f.portal.check = async (_o, _n, _p, s) => {
    entered.resolve(s);
    await gate.promise;
    return { stage: "late" };
  };
  await bot.handle(message("/start"));
  await bot.handle(message("n"));
  await bot.handle(message("p"));
  const signal = await entered.promise;
  await bot.handle(message("/cancel"));
  assert.equal(signal.aborted, true);
  gate.resolve();
  await Promise.all([...bot.tasks]);
  assert.equal(f.cases.hasEnrollment(1), false);
  assert.ok(sends().some((s) => s === "Действие отменено."));
});
test("failed replies are one attempt, 401 suppresses concurrent sends, canceled calls do not send", async (t) => {
  const rate = await setup(t, (_m, _b, _r, res) => {
    res.statusCode = 429;
    res.end(JSON.stringify({ ok: false, parameters: { retry_after: 100 } }));
    return true;
  });
  await rate.bot.reply(1, "a");
  await rate.bot.reply(1, "b");
  assert.equal(rate.s.requests.length, 2);
  const unauthorized = await setup(t, (_m, _b, _r, res) => {
    res.statusCode = 401;
    res.end("{}");
    return true;
  });
  await Promise.all([
    unauthorized.bot.reply(1, "a"),
    unauthorized.bot.reply(2, "b"),
  ]);
  assert.equal(unauthorized.s.requests.length, 1);
  const good = await setup(t);
  const c = new AbortController();
  c.abort();
  await good.bot.reply(1, "secret", c.signal);
  await good.bot.reply(0, "secret");
  assert.equal(good.s.requests.length, 0);
});
test("polling uses ordered intake, offsets, bounded backoff, and aborts outstanding poll", async (t) => {
  const c = new AbortController();
  let polls = 0;
  const { bot, s, sends } = await setup(t, (method, body, _req, res) => {
    if (method === "sendMessage") {
      setImmediate(() => c.abort());
    }
    if (method === "getUpdates") {
      polls++;
      assert.equal(body.timeout, 59);
      assert.deepEqual(body.allowed_updates, ["message"]);
      if (polls === 1) {
        assert.equal(body.offset, 1);
        res.end(
          JSON.stringify({
            ok: true,
            result: [{ update_id: 5, message: message("/help") }],
          }),
        );
      } else {
        assert.equal(body.offset, 6);
        // Abort after the queued help has reached the API.
      }
      return true;
    }
  });
  await bot.run(c.signal);
  assert.equal(polls, 2);
  assert.match(sends()[0], /CaseWatch/);
  await assert.rejects(bot.run(c.signal), { code: "polling" });
  assert.equal(
    s.requests.filter((r) => r.url.endsWith("/getUpdates")).length,
    2,
  );
});
test("poll failures expose fixed diagnostics and honor retry_after without retrying replies", async (t) => {
  const c = new AbortController();
  let polls = 0;
  const { bot, diagnostics } = await setup(t, (method, _b, _r, res) => {
    if (method === "getUpdates") {
      polls++;
      if (polls === 1) {
        res.statusCode = 429;
        res.end(
          JSON.stringify({
            ok: false,
            description: "secret",
            parameters: { retry_after: 2 },
          }),
        );
      } else {
        c.abort();
        res.end('{"ok":true,"result":[]}');
      }
      return true;
    }
  });
  const waits = [];
  bot.waiting = async (ms) => {
    waits.push(ms);
  };
  await bot.run(c.signal);
  assert.deepEqual(waits, [2000]);
  assert.deepEqual(diagnostics, ["telegram polling failure"]);
});
test("status formatting preserves stale observations, fresh and failure states, and Warsaw DST", () => {
  assert.match(formatStatus({}, new Error()), /Пока нет данных/);
  assert.match(
    formatStatus({
      stage: "",
      checkedAt: Date.parse("2026-07-01T12:00Z"),
      fresh: true,
    }),
    /Не указан\nПоследняя проверка: 01.07.2026, 14:00/,
  );
  assert.match(
    formatStatus({
      stage: "old",
      checkedAt: Date.parse("2026-01-01T12:00Z"),
      inProgress: true,
      error: new Error(),
    }),
    /Проверено: 01.01.2026, 13:00/,
  );
  assert.match(
    formatStatus({ stage: "old", checkedAt: 1 }),
    /Последняя успешная проверка/,
  );
});

test("API deadlines and polling queue remain bounded under slow ordered intake", async (t) => {
  const f = fixture(t),
    c = new AbortController(),
    entered = deferred(),
    gate = deferred();
  let polls = 0,
    handled = 0;
  const seen = [];
  const http = {
    async request(url, options) {
      seen.push([url.split("/").at(-1), options]);
      if (url.endsWith("/getUpdates")) {
        polls++;
        return {
          status: 200,
          body: JSON.stringify({
            ok: true,
            result: Array.from({ length: 1100 }, (_, i) => ({
              update_id: i + 1,
              message: message("/help"),
            })),
          }),
        };
      }
      return {
        status: 200,
        body: JSON.stringify({
          ok: true,
          result: url.endsWith("/getMyCommands") ? commands : true,
        }),
      };
    },
  };
  const bot = new Telegram(token, f.cases, { http });
  t.after(() => bot.close());
  await bot.initialize();
  await bot.reply(1, "hello");
  assert.equal(
    seen.find(([method]) => method === "sendMessage")[1].timeout,
    15000,
  );
  bot.handle = async () => {
    handled++;
    entered.resolve();
    await gate.promise;
  };
  const running = bot.run(c.signal);
  await entered.promise;
  await tick();
  assert.equal(polls, 1);
  assert.equal(handled, 1);
  assert.equal(
    seen.find(([method]) => method === "getUpdates")[1].timeout,
    60000,
  );
  c.abort();
  gate.resolve();
  await running;
});
