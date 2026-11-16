package casewatch

import kotlinx.coroutines.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.time.Clock
import java.util.UUID

/** Failure retains the last successful observation without relabeling it fresh. */
data class CheckResult(
    val status: Status,
    val failure: Failure? = null,
)

class CaseService(
    private val store: Storage,
    private val portal: Portal,
    private val clock: Clock = Clock.systemUTC(),
) {
    private val lock = Mutex()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    private class Flight(
        val enrollment: Boolean,
    ) {
        lateinit var job: Job
        val result = CompletableDeferred<CheckResult>()
        var waiters = 0
    }

    private class Owner {
        var flight: Flight? = null
        var removing = false
    }

    private val owners = mutableMapOf<Long, Owner>()
    private var closed = false

    private fun owner(id: Long): Owner {
        if (id <= 0) throw Failure(Failure.Kind.DENIED)
        if (closed) throw Failure(Failure.Kind.CLOSED)
        return owners.getOrPut(id) { Owner() }.also { if (it.removing) throw Failure(Failure.Kind.REMOVING) }
    }

    suspend fun hasEnrollment(id: Long): Boolean =
        lock.withLock {
            owner(id)
            store.enrollment(id) != null
        }

    suspend fun scheduledOwners(): List<Long> =
        lock.withLock {
            if (closed) throw Failure(Failure.Kind.CLOSED)
            store.owners()
        }

    suspend fun enroll(
        id: Long,
        number: String,
        password: String,
    ): Observation {
        currentCoroutineContext().ensureActive()
        val state: Owner
        val flight = Flight(true)
        lock.withLock {
            state = owner(id)
            if (number.isBlank() || password.isBlank()) throw Failure(Failure.Kind.INVALID)
            if (state.flight != null) throw Failure(Failure.Kind.BUSY)
            if (store.enrollment(id) != null) throw Failure(Failure.Kind.EXISTS)
            flight.job = currentCoroutineContext().job
            state.flight = flight
        }
        var committed = false
        try {
            val observation = withTimeout(60_000) { portal.check(id, number, password) }
            lock.withLock {
                currentCoroutineContext().ensureActive()
                if (state.removing) throw Failure(Failure.Kind.REMOVING)
                store.create(Enrollment(UUID.randomUUID().toString(), id, number, password, observation))
                committed = true
            }
            return observation
        } finally {
            withContext(NonCancellable) {
                lock.withLock {
                    state.flight = null
                    flight.result.complete(CheckResult(Status(null)))
                }
                if (!committed) portal.forgetSession(id)
            }
        }
    }

    suspend fun status(
        id: Long,
        manual: Boolean = true,
    ): CheckResult {
        currentCoroutineContext().ensureActive()
        val flight: Flight
        lock.withLock {
            val state = owner(id)
            val active = state.flight
            if (active != null) {
                if (active.enrollment) throw Failure(Failure.Kind.BUSY)
                flight = active
            } else {
                val enrollment = store.enrollment(id) ?: throw Failure(Failure.Kind.NOT_FOUND)
                val now = clock.instant()
                if (now < enrollment.observation.checkedAt.plusSeconds(60)) {
                    return CheckResult(
                        Status(
                            enrollment.observation,
                            true,
                            nextEligibleAt =
                                maxOf(
                                    enrollment.observation.checkedAt.plusSeconds(60),
                                    store.eligibility(id, enrollment.id, manual) ?: java.time.Instant.MIN,
                                ),
                        ),
                    )
                }
                val next = store.reserve(id, enrollment.id, manual, now)
                if (next != null) return CheckResult(Status(enrollment.observation, nextEligibleAt = next))
                flight = Flight(false)
                state.flight = flight
                flight.job =
                    scope.launch(start = CoroutineStart.LAZY) {
                        var result = CheckResult(Status(enrollment.observation, nextEligibleAt = now.plusSeconds(60)))
                        try {
                            val observation = withTimeout(60_000) { portal.check(id, enrollment.number, enrollment.password) }
                            lock.withLock {
                                currentCoroutineContext().ensureActive()
                                if (state.removing) throw Failure(Failure.Kind.REMOVING)
                                store.observe(id, enrollment.id, observation)
                                result =
                                    CheckResult(
                                        Status(
                                            observation,
                                            true,
                                            nextEligibleAt = maxOf(now.plusSeconds(60), observation.checkedAt.plusSeconds(60)),
                                        ),
                                    )
                            }
                        } catch (
                            error: Failure,
                        ) {
                            result = result.copy(failure = error)
                        } catch (
                            error: CancellationException,
                        ) {
                            result = result.copy(failure = Failure(Failure.Kind.CHECK))
                        } finally {
                            withContext(NonCancellable) {
                                lock.withLock {
                                    state.flight = null
                                    flight.result.complete(result)
                                }
                            }
                        }
                    }
            }
            flight.waiters++
            flight.job.start()
        }
        try {
            return flight.result.await()
        } catch (error: CancellationException) {
            withContext(NonCancellable) {
                lock.withLock {
                    flight.waiters--
                    if (flight.waiters == 0) flight.job.cancel()
                }
                if (!manual) flight.result.await()
            }
            throw error
        }
    }

    suspend fun remove(id: Long) {
        val state: Owner
        val flight: Flight?
        lock.withLock {
            state = owner(id)
            state.removing = true
            flight = state.flight
            flight?.job?.cancel()
        }
        try {
            flight?.result?.await()
            lock.withLock {
                currentCoroutineContext().ensureActive()
                store.remove(id, clock.instant())
                portal.forgetSession(id)
            }
        } finally {
            withContext(NonCancellable) { lock.withLock { state.removing = false } }
        }
    }

    suspend fun shutdown() {
        val flights =
            lock.withLock {
                closed = true
                owners.values.mapNotNull { it.flight }.also { list -> list.forEach { it.job.cancel() } }
            }
        withContext(NonCancellable) {
            flights.forEach { it.result.await() }
            scope.cancel()
        }
    }
}
