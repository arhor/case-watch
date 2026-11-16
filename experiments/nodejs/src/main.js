import { pathToFileURL } from "node:url";
import { loadConfig } from "./config.js";
import { Storage } from "./storage.js";
import { Portal } from "./portal.js";
import { CaseService } from "./service.js";
import { Telegram } from "./telegram.js";
import { Schedule } from "./schedule.js";
import { fail } from "./common.js";

export async function run({
  env = process.env,
  signal,
  makeStorage = (path) => new Storage(path),
  makePortal = () => new Portal(),
  makeTelegram = (token, cases, options) => new Telegram(token, cases, options),
  makeSchedule = (cases, options) => new Schedule(cases, options),
  report = (category) => console.error(category),
} = {}) {
  const config = loadConfig(env),
    scope = new AbortController();
  const abort = () => scope.abort(signal.reason);
  if (signal?.aborted) abort();
  else signal?.addEventListener("abort", abort, { once: true });
  const fatal = () => scope.abort(fail("service_operation_failed"));
  const diagnostic = (category) => {
    report(category);
    if (["telegram_invalid_token", "inventory_failed"].includes(category))
      fatal();
  };
  let store, portal, cases, telegram;
  let started = false;
  try {
    store = makeStorage(config.dbPath);
    portal = makePortal();
    cases = new CaseService(store, portal, { onStorageError: fatal });
    telegram = makeTelegram(config.token, cases, { diagnostic });
    await telegram.initialize(scope.signal);
    if (scope.signal.aborted) return;
    const schedule = makeSchedule(cases, { report: diagnostic });
    started = true;
    const loop = async (fn) => {
      try {
        await fn();
        if (!scope.signal.aborted) fatal();
      } catch {
        if (!scope.signal.aborted) fatal();
      }
    };
    const loops = [
      loop(() => telegram.run(scope.signal)),
      loop(() => schedule.run(scope.signal)),
    ];
    if (!scope.signal.aborted)
      await new Promise((resolve) =>
        scope.signal.addEventListener("abort", resolve, { once: true }),
      );
    await cases.shutdown();
    await Promise.allSettled(loops);
    if (scope.signal.reason?.code === "service_operation_failed")
      throw fail("service_operation_failed");
  } catch {
    if (!signal?.aborted)
      throw fail(
        started
          ? "service_operation_failed"
          : "service_startup_validation_failed",
      );
  } finally {
    scope.abort(fail("canceled"));
    signal?.removeEventListener("abort", abort);
    let cleanupFailed = false;
    for (const cleanup of [
      () => cases?.shutdown(),
      () => telegram?.close(),
      () => portal?.close(),
      () => store?.close(),
    ]) {
      try {
        await cleanup();
      } catch {
        cleanupFailed = true;
      }
    }
    if (cleanupFailed) throw fail("service_operation_failed");
  }
}
if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const controller = new AbortController();
  const stop = () => controller.abort(fail("canceled"));
  process.once("SIGINT", stop);
  process.once("SIGTERM", stop);
  try {
    await run({ signal: controller.signal });
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
