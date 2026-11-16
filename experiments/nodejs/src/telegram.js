import { readFileSync } from "node:fs";
import { Http } from "./http.js";
import { active, bounded, fail, wait } from "./common.js";
const messages = JSON.parse(
  readFileSync(new URL("./messages.json", import.meta.url), "utf8"),
);
export const commands = [
  { command: "start", description: "Добавить дело для отслеживания" },
  { command: "status", description: "Проверить статус дела" },
  { command: "remove", description: "Удалить дело и связанные с ним данные" },
  { command: "cancel", description: "Отменить текущее действие" },
  { command: "help", description: "Показать эту справку" },
];
export function formatStatus(s = {}, error = s.error) {
  const parts = [];
  if (error) parts.push("Не удалось обновить статус дела.");
  if (s.checkedAt === undefined || s.checkedAt === null)
    parts.push("Пока нет данных об успешных проверках вашего дела.");
  else {
    const stage = s.stage || "Не указан";
    const p = Object.fromEntries(
      new Intl.DateTimeFormat("en-GB", {
        timeZone: "Europe/Warsaw",
        day: "2-digit",
        month: "2-digit",
        year: "numeric",
        hour: "2-digit",
        minute: "2-digit",
        hourCycle: "h23",
      })
        .formatToParts(s.checkedAt)
        .map((x) => [x.type, x.value]),
    );
    const time = `${p.day}.${p.month}.${p.year}, ${p.hour}:${p.minute}`;
    parts.push(
      error
        ? `Показываю данные последней успешной проверки:\n\nСтатус дела: ${stage}\nПроверено: ${time}`
        : s.fresh
          ? `Статус дела: ${stage}\nПоследняя проверка: ${time}\n\nДанные успешно обновлены.`
          : `Статус дела: ${stage}\nПоследняя успешная проверка: ${time}`,
    );
  }
  if (s.inProgress) parts.push("Проверка статуса ещё выполняется.");
  return parts.join("\n\n");
}
export class Telegram {
  constructor(
    token,
    cases,
    {
      endpoint = "https://api.telegram.org",
      http,
      now = Date.now,
      diagnostic = () => {},
      waiting = wait,
    } = {},
  ) {
    if (!cases) throw fail("invalid_telegram_configuration");
    Object.assign(this, {
      token,
      cases,
      endpoint,
      http: http ?? new Http(),
      now,
      diagnostic,
      waiting,
    });
    this.owned = !http;
    this.states = new Map();
    this.tasks = new Set();
    this.replyTail = Promise.resolve();
    this.repliesFailed = false;
    this.polling = false;
  }
  async api(method, body = {}, signal, timeout = 15000) {
    const r = await this.http.request(
      `${this.endpoint}/bot${this.token}/${method}`,
      {
        method: "POST",
        body: JSON.stringify(body),
        headers: { "Content-Type": "application/json" },
        signal,
        timeout,
        limit: 16 << 20,
      },
    );
    let data;
    try {
      data = JSON.parse(r.body);
    } catch {
      if (r.status !== 401) throw fail("transport");
    }
    if (r.status === 401 || data?.error_code === 401) {
      this.diagnostic("telegram_invalid_token");
      throw fail("unauthorized");
    }
    if (r.status !== 200 || !data?.ok)
      throw Object.assign(fail("telegram"), {
        retryAfter: data?.parameters?.retry_after,
      });
    return data.result;
  }
  async initialize(signal) {
    const b = bounded(signal, 15000);
    try {
      await this.api("getMe", {}, b.signal);
      try {
        const current = await this.api("getMyCommands", {}, b.signal);
        if (
          !Array.isArray(current) ||
          current.length !== commands.length ||
          !current.every(
            (c, i) =>
              c.command === commands[i].command &&
              c.description === commands[i].description,
          )
        ) {
          if (!(await this.api("setMyCommands", { commands }, b.signal)))
            throw fail("telegram");
        }
      } catch (error) {
        if (error.code === "unauthorized") throw error;
        this.diagnostic("telegram command synchronization failure");
      }
    } finally {
      b.close();
    }
  }
  reply(id, text, signal) {
    const send = async () => {
      if (this.repliesFailed || signal?.aborted) {
        this.diagnostic("telegram reply failure");
        return;
      }
      if (id <= 0) return;
      try {
        await this.api("sendMessage", { chat_id: id, text }, signal);
      } catch (error) {
        if (error.code === "unauthorized") this.repliesFailed = true;
        else this.diagnostic("telegram reply failure");
      }
    };
    this.replyTail = this.replyTail.then(send);
    return this.replyTail;
  }
  async erase(id, message, signal) {
    try {
      await this.api(
        "deleteMessage",
        { chat_id: id, message_id: message },
        signal,
        60000,
      );
    } catch (error) {
      if (error.code !== "unauthorized")
        this.diagnostic("telegram credential deletion failure");
    }
  }
  state(id) {
    if (!this.states.has(id))
      this.states.set(id, { number: "", deadline: 0, removal: 0 });
    return this.states.get(id);
  }
  async handle(message, signal) {
    const m = message,
      id = m?.from?.id;
    if (
      !Number.isSafeInteger(id) ||
      id <= 0 ||
      m.from.is_bot ||
      m.chat?.type !== "private" ||
      m.chat.id !== id
    )
      return;
    const s = this.state(id),
      now = this.now();
    const expired = s.deadline > 0 && now >= s.deadline,
      expiredRemoval = s.removal > 0 && now >= s.removal;
    if (expired) {
      s.number = "";
      s.deadline = 0;
    }
    if (expiredRemoval) s.removal = 0;
    const text = typeof m.text === "string" ? m.text : "",
      fields = text.trim().split(/\s+/).filter(Boolean),
      command = fields[0]?.startsWith("/") ? fields[0] : "";
    const say = (key) => this.reply(id, messages[key], signal);
    if (command && fields.length > 1) {
      s.number = "";
      s.deadline = 0;
      await this.erase(id, m.message_id, signal);
      return say("invalid_command");
    }
    switch (command) {
      case "/cancel":
        s.controller?.abort(fail("canceled"));
        s.number = "";
        s.deadline = s.removal = 0;
        return say("canceled");
      case "/help":
        return this.reply(
          id,
          "CaseWatch помогает следить за ходом рассмотрения вашего дела.\n\nДоступные команды:\n" +
            commands.map((c) => `/${c.command} — ${c.description}`).join("\n"),
          signal,
        );
      case "/start": {
        if (s.controller) {
          s.controller.abort(fail("canceled"));
          return say("cancel_previous");
        }
        s.number = "";
        s.deadline = s.removal = 0;
        try {
          if (this.cases.hasEnrollment(id)) return say("already_enrolled");
        } catch {
          return say("storage_failure");
        }
        s.deadline = now + 600000;
        return say("start");
      }
      case "/status": {
        try {
          return this.reply(
            id,
            formatStatus(await this.cases.status(id, signal)),
            signal,
          );
        } catch (error) {
          return error.code === "not_found"
            ? say("not_enrolled")
            : this.reply(id, formatStatus({}, error), signal);
        }
      }
      case "/remove": {
        s.controller?.abort(fail("canceled"));
        s.number = "";
        s.deadline = 0;
        try {
          if (!this.cases.hasEnrollment(id)) return say("not_enrolled");
        } catch {
          return say("storage_failure");
        }
        s.removal = now + 600000;
        return say("confirm_remove");
      }
    }
    if (command) {
      if (s.deadline) await this.erase(id, m.message_id, signal);
      return say("unknown_command");
    }
    if (expiredRemoval) return say("expired_removal");
    if (expired) return say("expired_enrollment");
    if (s.removal) {
      const answer = text.trim().toLowerCase();
      if (answer === "нет") {
        s.removal = 0;
        return say("remove_canceled");
      }
      if (answer !== "да") return say("answer_yes_no");
      s.removal = 0;
      try {
        await this.cases.remove(id, signal);
        return say("removed");
      } catch {
        return say("remove_failed");
      }
    }
    if (!s.deadline) return say("idle");
    await this.erase(id, m.message_id, signal);
    if (!s.number) {
      if (!text.trim() || Buffer.byteLength(text) > 256 || /[\r\n]/.test(text))
        return say("invalid_number");
      s.number = text;
      s.deadline = now + 600000;
      return say("ask_password");
    }
    if (!text || Buffer.byteLength(text) > 4096) return say("invalid_password");
    const number = s.number;
    s.number = "";
    s.deadline = 0;
    const controller = new AbortController();
    s.controller = controller;
    const abort = () => controller.abort(signal.reason);
    signal?.addEventListener("abort", abort, { once: true });
    if (signal?.aborted) abort();
    await say("checking");
    const task = (async () => {
      let success = false;
      try {
        await this.cases.enroll(id, number, text, controller.signal);
        success = true;
      } catch {
        /* fixed response only */
      } finally {
        s.controller = undefined;
        signal?.removeEventListener("abort", abort);
      }
      await say(success ? "enrolled" : "enroll_failed");
    })();
    this.tasks.add(task);
    task.finally(() => this.tasks.delete(task));
  }
  sweep() {
    for (const s of this.states.values()) {
      if (s.deadline && this.now() >= s.deadline) {
        s.number = "";
        s.deadline = 0;
      }
      if (s.removal && this.now() >= s.removal) s.removal = 0;
    }
  }
  async run(signal) {
    if (this.polling) throw fail("polling");
    this.polling = true;
    const scope = new AbortController(),
      abort = () => scope.abort(signal.reason);
    if (signal?.aborted) abort();
    else signal?.addEventListener("abort", abort, { once: true });
    const work = scope.signal,
      queue = [];
    let changed, space;
    const notify = () => {
      changed?.();
      changed = undefined;
    };
    work.addEventListener("abort", () => {
      notify();
      space?.();
    });
    const untilChange = () =>
      new Promise((resolve) => {
        changed = resolve;
      });
    const producer = (async () => {
      let offset = 1,
        backoff = 0;
      while (!work.aborted) {
        try {
          if (backoff) await this.waiting(backoff, work);
          const updates = await this.api(
            "getUpdates",
            { offset, timeout: 59, allowed_updates: ["message"] },
            work,
            60000,
          );
          if (!Array.isArray(updates)) throw fail("telegram");
          let stuck = false;
          for (const update of updates) {
            if (
              !Number.isSafeInteger(update?.update_id) ||
              update.update_id === 0
            ) {
              stuck = true;
              this.diagnostic("telegram polling failure");
              continue;
            }
            stuck = false;
            offset = update.update_id + 1;
            while (queue.length >= 1024 && !work.aborted)
              await new Promise((resolve) => {
                space = resolve;
              });
            active(work);
            queue.push(update);
            notify();
          }
          backoff = stuck ? Math.min(backoff ? backoff * 2 : 100, 5000) : 0;
        } catch (error) {
          if (work.aborted) break;
          if (error.code === "unauthorized") {
            scope.abort(error);
            break;
          }
          this.diagnostic("telegram polling failure");
          backoff =
            error.retryAfter > 0
              ? error.retryAfter * 1000
              : Math.min(backoff ? backoff * 2 : 100, 5000);
        }
      }
      notify();
    })();
    const ticker = setInterval(() => this.sweep(), 60000);
    try {
      while (!work.aborted) {
        if (!queue.length) {
          await untilChange();
          continue;
        }
        const update = queue.shift();
        space?.();
        space = undefined;
        if (update.message) await this.handle(update.message, work);
      }
    } finally {
      scope.abort(fail("canceled"));
      clearInterval(ticker);
      signal?.removeEventListener("abort", abort);
      await producer;
      await this.close();
    }
  }
  async close() {
    for (const s of this.states.values()) {
      s.controller?.abort(fail("canceled"));
      s.number = "";
      s.deadline = s.removal = 0;
    }
    await Promise.allSettled([...this.tasks]);
    await this.replyTail;
    if (this.owned) this.http.close();
  }
}
