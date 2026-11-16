// Measurement-only wiring: production modules, synthetic endpoints and database.
import { run } from "../src/main.js";
import { Storage } from "../src/storage.js";
import { Portal } from "../src/portal.js";
import { Telegram } from "../src/telegram.js";
import { Schedule } from "../src/schedule.js";
import { Http } from "../src/http.js";
import { wait } from "../src/common.js";
const stop = new AbortController();
process.once("SIGTERM", () => stop.abort());
process.once("SIGINT", () => stop.abort());
const endpoint = process.env.BENCH_ENDPOINT;
await run({
  signal: stop.signal,
  env: {
    CASE_WATCH_DB_PATH: "/data/bench.db",
    CASE_WATCH_TELEGRAM_BOT_TOKEN: "123:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  },
  makeStorage: (path) => {
    const db = new Storage(path);
    db.create({
      owner: 1,
      id: "00000000000000000000000000000001",
      number: "synthetic-number",
      password: "synthetic-password",
      stage: "Synthetic stage",
      checkedAt: Date.now() - 120000,
    });
    return db;
  },
  makePortal: () => new Portal({ base: endpoint }),
  makeTelegram: (token, cases, options) =>
    new Telegram(token, cases, { ...options, endpoint }),
  makeSchedule: (cases, options) => {
    const schedule = new Schedule(cases, options);
    if (process.env.BENCH_SCENARIO !== "scheduled") return schedule;
    return {
      async run(signal) {
        const http = new Http();
        const trigger = (async () => {
          await wait(10000, signal);
          for (const owner of cases.scheduledOwners()) {
            const status = await cases.scheduled(owner, signal);
            if (status.error) throw status.error;
          }
          await http.request(endpoint + "/scheduled-done", {
            method: "POST",
            signal,
          });
        })();
        try {
          await Promise.all([schedule.run(signal), trigger]);
        } finally {
          http.close();
        }
      },
    };
  },
});
