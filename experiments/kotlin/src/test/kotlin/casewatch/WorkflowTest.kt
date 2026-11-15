package casewatch

import kotlinx.coroutines.*
import java.nio.file.Files
import java.time.*
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*

class WorkflowTest {
    private class MutableClock(
        var value: Instant,
    ) : Clock() {
        override fun instant() = value

        override fun getZone(): ZoneId = ZoneOffset.UTC

        override fun withZone(zone: ZoneId): Clock = this
    }

    @Test fun sharedCheckSurvivesOneCanceledWaiterAndRemovalDrains() =
        runBlocking {
            val directory = Files.createTempDirectory("case-watch-workflows")
            val clock = MutableClock(Instant.parse("2026-10-01T10:00:00Z"))
            val calls = AtomicInteger()
            val entered = CompletableDeferred<Unit>()
            val finish = CompletableDeferred<Unit>()
            val portal =
                object : Portal {
                    override suspend fun check(
                        owner: Long,
                        number: String,
                        password: String,
                    ): Observation {
                        if (calls.incrementAndGet() > 1) {
                            entered.complete(Unit)
                            finish.await()
                        }
                        return Observation("stage-${calls.get()}", clock.instant())
                    }

                    override fun forgetSession(owner: Long) {}

                    override fun close() {}
                }
            try {
                Storage(directory.resolve("test.db").toString()).use { store ->
                    val cases = CaseService(store, portal, clock)
                    try {
                        cases.enroll(1, "synthetic", "password")
                        assertTrue(cases.status(1).status.fresh)
                        assertEquals(1, calls.get())
                        clock.value = clock.value.plusSeconds(60)
                        val first = async { cases.status(1) }
                        entered.await()
                        val second = async(start = CoroutineStart.UNDISPATCHED) { cases.status(1) }
                        first.cancelAndJoin()
                        finish.complete(Unit)
                        assertEquals(
                            "stage-2",
                            second
                                .await()
                                .status.observation
                                ?.stage,
                        )
                        assertEquals(2, calls.get())
                        assertEquals(1, store.history(1).size)
                        cases.remove(1)
                        assertFalse(cases.hasEnrollment(1))
                    } finally {
                        cases.shutdown()
                    }
                }
            } finally {
                directory.toFile().deleteRecursively()
            }
        }

    @Test fun warsawScheduleSkipsWeekendAndDstAndExactSlot() =
        runBlocking {
            val directory = Files.createTempDirectory("case-watch-schedule")
            val portal =
                object : Portal {
                    override suspend fun check(
                        owner: Long,
                        number: String,
                        password: String,
                    ): Observation = error("unexpected portal call")

                    override fun forgetSession(owner: Long) {}

                    override fun close() {}
                }
            try {
                Storage(directory.resolve("test.db").toString()).use { store ->
                    val cases = CaseService(store, portal)
                    try {
                        val schedule = Schedule(cases)
                        assertEquals(Instant.parse("2026-10-26T07:00:00Z"), schedule.next(Instant.parse("2026-10-23T16:00:00Z")))
                        assertEquals(Instant.parse("2026-03-30T06:00:00Z"), schedule.next(Instant.parse("2026-03-27T17:00:00Z")))
                        assertEquals(Instant.parse("2026-10-26T09:00:00Z"), schedule.next(Instant.parse("2026-10-26T07:00:00Z")))
                    } finally {
                        cases.shutdown()
                    }
                }
            } finally {
                directory.toFile().deleteRecursively()
            }
        }
}
