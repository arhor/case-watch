package casewatch

import kotlinx.coroutines.*
import java.time.*
import java.util.concurrent.atomic.AtomicBoolean

class Schedule(
    private val cases: CaseService,
    private val clock: Clock = Clock.systemUTC(),
    private val wait: suspend (Instant) -> Unit = { deadline ->
        delay(
            Duration.between(clock.instant(), deadline).toMillis().coerceAtLeast(0),
        )
    },
) {
    private val running = AtomicBoolean()

    fun next(after: Instant): Instant {
        val zone = ZoneId.of("Europe/Warsaw")
        var date = after.atZone(zone).toLocalDate()
        while (true) {
            if (date.dayOfWeek !in listOf(DayOfWeek.SATURDAY, DayOfWeek.SUNDAY)) {
                for (hour in listOf(8, 10, 12, 14, 16, 18)) {
                    val slot = date.atTime(hour, 0).atZone(zone).toInstant()
                    if (slot > after) return slot
                }
            }
            date = date.plusDays(1)
        }
    }

    suspend fun run() {
        check(running.compareAndSet(false, true))
        try {
            while (currentCoroutineContext().isActive) {
                val slot = next(clock.instant())
                wait(slot)
                currentCoroutineContext().ensureActive()
                val now = clock.instant()
                if (now < slot || now >= slot.plusSeconds(60)) continue
                for (owner in cases.scheduledOwners()) {
                    currentCoroutineContext().ensureActive()
                    try {
                        cases.status(owner, false)
                    } catch (error: Failure) {
                        if (error.kind == Failure.Kind.STORAGE) throw error
                        System.err.println("check_failed")
                    }
                }
            }
        } finally {
            running.set(false)
        }
    }
}
