package casewatch

import java.time.Instant

class Failure(
    val kind: Kind,
    val retryAfter: Long? = null,
) : RuntimeException(kind.name) {
    enum class Kind { STORAGE, NOT_FOUND, EXISTS, DENIED, BUSY, REMOVING, CLOSED, INVALID, CHECK, AUTHENTICATION, PROTOCOL, TRANSPORT }
}

data class Observation(
    val stage: String,
    val checkedAt: Instant,
)

data class Enrollment(
    val id: String,
    val owner: Long,
    val number: String,
    val password: String,
    val observation: Observation,
)

data class Status(
    val observation: Observation?,
    val fresh: Boolean = false,
    val inProgress: Boolean = false,
    val nextEligibleAt: Instant? = null,
)

data class Transition(
    val sequence: Long,
    val enrollmentId: String,
    val previousStage: String,
    val observation: Observation,
)

interface Portal : AutoCloseable {
    suspend fun check(
        owner: Long,
        number: String,
        password: String,
    ): Observation

    fun forgetSession(owner: Long)
}
