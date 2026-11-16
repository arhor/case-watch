import { setTimeout as delay } from "node:timers/promises";
export const fail = (code) => Object.assign(new Error(code), { code });
export function active(signal) {
  signal?.throwIfAborted();
}
export function bounded(signal, ms) {
  const controller = new AbortController();
  const abort = () => controller.abort(signal.reason);
  if (signal?.aborted) abort();
  else signal?.addEventListener("abort", abort, { once: true });
  const timer = setTimeout(() => controller.abort(fail("deadline")), ms);
  return {
    signal: controller.signal,
    close() {
      clearTimeout(timer);
      signal?.removeEventListener("abort", abort);
    },
  };
}
export const wait = (ms, signal) =>
  delay(Math.max(0, ms), undefined, { signal });
export function ownerAllowed(owner) {
  if (!Number.isSafeInteger(owner) || owner <= 0) throw fail("denied");
}
