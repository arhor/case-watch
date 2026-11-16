import { randomUUID } from "node:crypto";
import { active, bounded, fail, ownerAllowed } from "./common.js";
export class CaseService {
  constructor(
    store,
    portal,
    { now = Date.now, onStorageError = () => {} } = {},
  ) {
    Object.assign(this, { store, portal, now, onStorageError });
    this.states = new Map();
    this.closed = false;
  }
  storage(fn) {
    try {
      return fn();
    } catch (error) {
      if (["not_found", "exists"].includes(error.code)) throw error;
      this.onStorageError();
      throw fail("storage");
    }
  }
  state(owner) {
    if (!this.states.has(owner)) this.states.set(owner, {});
    return this.states.get(owner);
  }
  admit(owner, signal) {
    ownerAllowed(owner);
    active(signal);
    if (this.closed) throw fail("closed");
    const state = this.state(owner);
    if (state.removing) throw fail("removing");
    return state;
  }
  hasEnrollment(owner) {
    ownerAllowed(owner);
    try {
      this.storage(() => this.store.get(owner));
      return true;
    } catch (error) {
      if (error.code === "not_found") return false;
      throw error;
    }
  }
  scheduledOwners() {
    return this.storage(() => this.store.owners());
  }
  async baseline(owner, number, password, signal) {
    const bound = bounded(signal, 60000);
    try {
      const result = await this.portal.check(
        owner,
        number,
        password,
        bound.signal,
      );
      active(bound.signal);
      return { stage: result.stage, checkedAt: this.now() };
    } catch {
      if (bound.signal.aborted) throw bound.signal.reason;
      throw fail("check");
    } finally {
      bound.close();
    }
  }
  async enroll(owner, number, password, signal) {
    ownerAllowed(owner);
    active(signal);
    if (!number.trim() || !password.trim()) throw fail("invalid");
    const state = this.admit(owner, signal);
    if (state.flight) throw fail("busy");
    if (this.hasEnrollment(owner)) throw fail("exists");
    const controller = new AbortController();
    const abort = () => controller.abort(signal.reason);
    signal?.addEventListener("abort", abort, { once: true });
    const flight = { controller, enrollment: true };
    state.flight = flight;
    flight.done = Promise.resolve().then(async () => {
      let committed = false;
      try {
        const result = await this.baseline(
          owner,
          number,
          password,
          controller.signal,
        );
        if (state.removing) throw fail("removing");
        active(controller.signal);
        this.storage(() =>
          this.store.create({
            id: randomUUID(),
            owner,
            number,
            password,
            ...result,
          }),
        );
        committed = true;
        return result;
      } finally {
        if (!committed) this.portal.forget(owner);
        state.flight = undefined;
        signal?.removeEventListener("abort", abort);
      }
    });
    return flight.done;
  }
  status(owner, signal) {
    return this.check(owner, true, signal);
  }
  scheduled(owner, signal) {
    return this.check(owner, false, signal);
  }
  async check(owner, manual, signal) {
    const state = this.admit(owner, signal);
    let flight = state.flight;
    if (flight?.enrollment) throw fail("busy");
    if (!flight) {
      const e = this.storage(() => this.store.get(owner));
      const status = {
        stage: e.stage,
        checkedAt: e.checkedAt,
        fresh: false,
        inProgress: false,
        nextEligibleAt: 0,
      };
      const now = this.now();
      if (now < e.checkedAt + 60000) {
        status.nextEligibleAt = Math.max(
          e.checkedAt + 60000,
          this.storage(() => this.store.eligibility(owner, e.id, manual)),
        );
        return status;
      }
      const reservation = this.storage(() =>
        this.store.reserve(owner, e.id, manual, now),
      );
      status.nextEligibleAt = reservation.nextEligibleAt;
      if (!reservation.reserved) return status;
      flight = { controller: new AbortController(), waiters: 0, status };
      state.flight = flight;
      flight.done = Promise.resolve().then(async () => {
        try {
          const result = await this.baseline(
            owner,
            e.number,
            e.password,
            flight.controller.signal,
          );
          if (state.removing) throw fail("removing");
          active(flight.controller.signal);
          this.storage(() =>
            this.store.observe(owner, e.id, result.stage, result.checkedAt),
          );
          flight.status = {
            ...status,
            ...result,
            fresh: true,
            nextEligibleAt: Math.max(
              status.nextEligibleAt,
              result.checkedAt + 60000,
            ),
          };
        } catch (error) {
          flight.status = { ...status, error };
        } finally {
          state.flight = undefined;
        }
        return flight.status;
      });
    }
    flight.waiters++;
    return this.waitFor(flight, signal, !manual);
  }
  async waitFor(flight, signal, drain) {
    let canceled = false,
      abort;
    const cancellation = new Promise((resolve) => {
      abort = () => {
        canceled = true;
        flight.waiters--;
        if (flight.waiters === 0) flight.controller.abort(fail("canceled"));
        resolve({
          ...flight.status,
          inProgress: true,
          error: signal.reason ?? fail("canceled"),
        });
      };
      if (signal?.aborted) abort();
      else signal?.addEventListener("abort", abort, { once: true });
    });
    try {
      const result = await Promise.race([flight.done, cancellation]);
      if (canceled && drain) await flight.done;
      return result;
    } finally {
      signal?.removeEventListener("abort", abort);
    }
  }
  async remove(owner, signal) {
    const state = this.admit(owner, signal);
    state.removing = true;
    const flight = state.flight;
    flight?.controller.abort(fail("canceled"));
    state.removalDone = (async () => {
      try {
        if (flight) await flight.done.catch(() => {});
        active(signal);
        const e = this.storage(() => this.store.get(owner));
        this.storage(() => this.store.remove(owner, e.id, Date.now()));
        this.portal.forget(owner);
      } finally {
        state.removing = false;
        state.removalDone = undefined;
      }
    })();
    return state.removalDone;
  }
  async shutdown() {
    this.closed = true;
    const pending = [];
    for (const state of this.states.values()) {
      if (state.flight) {
        state.flight.controller.abort(fail("canceled"));
        pending.push(state.flight.done);
      }
      if (state.removalDone) pending.push(state.removalDone);
    }
    await Promise.allSettled(pending);
  }
}
