package casewatch

import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import java.nio.file.Path
import java.sql.Connection
import java.sql.DriverManager
import java.sql.ResultSet
import java.time.Instant
import java.util.concurrent.Executors
import kotlin.coroutines.coroutineContext

/** One worker owns the connection; transactions never escape a worker operation. */
class Storage(
    path: String,
    private val onStorageFailure: () -> Unit = {},
) : AutoCloseable {
    private val dispatcher = Executors.newSingleThreadExecutor().asCoroutineDispatcher()
    private val connection: Connection = DriverManager.getConnection("jdbc:sqlite:" + Path.of(path).toAbsolutePath())

    init {
        try {
            connection.createStatement().use { statement ->
                listOf("foreign_keys=ON", "journal_mode=WAL", "synchronous=FULL", "busy_timeout=5000").forEach {
                    statement.execute("PRAGMA $it")
                }
                val version =
                    statement.executeQuery("PRAGMA user_version").use {
                        it.next()
                        it.getInt(1)
                    }
                require(version in 0..1) { "unsupported database version" }
                if (version == 0) {
                    connection.autoCommit = false
                    try {
                        val sql = requireNotNull(javaClass.getResource("/migrations/001.sql")).readText()
                        sql.split(';').filter { it.isNotBlank() }.forEach(statement::execute)
                        statement.execute("PRAGMA user_version=1")
                        connection.commit()
                    } catch (error: Exception) {
                        connection.rollback()
                        throw error
                    } finally {
                        connection.autoCommit = true
                    }
                }
            }
        } catch (error: Exception) {
            connection.close()
            dispatcher.close()
            throw Failure(Failure.Kind.STORAGE)
        }
    }

    private suspend fun <T> operation(block: () -> T): T =
        withContext(dispatcher) {
            coroutineContext.ensureActive()
            try {
                block()
            } catch (error: java.sql.SQLException) {
                onStorageFailure()
                throw Failure(Failure.Kind.STORAGE)
            }
        }

    private fun <T> transaction(block: () -> T): T {
        check(connection.autoCommit)
        connection.autoCommit = false
        try {
            val result = block()
            connection.commit()
            return result
        } catch (error: Throwable) {
            connection.rollback()
            throw error
        } finally {
            connection.autoCommit = true
        }
    }

    private fun execute(
        sql: String,
        vararg args: Any?,
    ): Int =
        connection.prepareStatement(sql).use { statement ->
            args.forEachIndexed { index, value -> statement.setObject(index + 1, value) }
            statement.executeUpdate()
        }

    private fun find(owner: Long): Enrollment? =
        connection
            .prepareStatement(
                "SELECT id, owner_id, case_number, password, stage, checked_at FROM enrollments WHERE owner_id=? AND deleted_at IS NULL",
            ).use { statement ->
                statement.setLong(1, owner)
                statement.executeQuery().use { rows -> if (rows.next()) rows.enrollment() else null }
            }

    suspend fun enrollment(owner: Long): Enrollment? = operation { find(owner) }

    suspend fun owners(): List<Long> =
        operation {
            connection.createStatement().use { statement ->
                statement.executeQuery("SELECT owner_id FROM enrollments WHERE deleted_at IS NULL ORDER BY owner_id").use { rows ->
                    buildList { while (rows.next()) add(rows.getLong(1)) }
                }
            }
        }

    suspend fun create(enrollment: Enrollment) =
        operation {
            transaction {
                if (find(enrollment.owner) != null) throw Failure(Failure.Kind.EXISTS)
                execute(
                    "INSERT INTO enrollments(id,owner_id,case_number,password,stage,checked_at) VALUES(?,?,?,?,?,?)",
                    enrollment.id,
                    enrollment.owner,
                    enrollment.number,
                    enrollment.password,
                    enrollment.observation.stage,
                    enrollment.observation.checkedAt.toString(),
                )
            }
        }

    suspend fun observe(
        owner: Long,
        id: String,
        observation: Observation,
    ) = operation {
        transaction {
            val previous = find(owner)?.takeIf { it.id == id } ?: throw Failure(Failure.Kind.NOT_FOUND)
            execute(
                "UPDATE enrollments SET stage=?,checked_at=? WHERE id=? AND deleted_at IS NULL",
                observation.stage,
                observation.checkedAt.toString(),
                id,
            )
            if (previous.observation.stage != observation.stage) {
                execute(
                    "INSERT INTO history(enrollment_id,previous_stage,stage,checked_at) VALUES(?,?,?,?)",
                    id,
                    previous.observation.stage,
                    observation.stage,
                    observation.checkedAt.toString(),
                )
            }
        }
    }

    private fun guard(
        type: String,
        id: String,
    ): Instant? =
        connection
            .prepareStatement(
                "SELECT started_at FROM cooldowns WHERE subject_type=? AND subject_id=?",
            ).use { statement ->
                statement.setString(1, type)
                statement.setString(2, id)
                statement.executeQuery().use { rows -> if (rows.next()) Instant.ofEpochSecond(0, rows.getLong(1)) else null }
            }

    suspend fun eligibility(
        owner: Long,
        id: String,
        manual: Boolean,
    ): Instant? =
        operation {
            val values = mutableListOf<Instant>()
            guard("case_check", id)?.let { values.add(it.plusSeconds(60)) }
            if (manual) guard("manual_check", owner.toString())?.let { values.add(it.plusSeconds(60)) }
            values.maxOrNull()
        }

    suspend fun reserve(
        owner: Long,
        id: String,
        manual: Boolean,
        now: Instant,
    ): Instant? =
        operation {
            transaction {
                val guards =
                    buildList {
                        guard("case_check", id)?.let { add(it.plusSeconds(60)) }
                        if (manual) guard("manual_check", owner.toString())?.let { add(it.plusSeconds(60)) }
                    }
                val next = guards.maxOrNull()
                if (next != null && now < next) {
                    next
                } else {
                    val keys = mutableListOf("case_check" to id)
                    if (manual) keys.add("manual_check" to owner.toString())
                    keys.forEach { (type, subject) ->
                        execute(
                            "INSERT INTO cooldowns VALUES(?,?,?) ON CONFLICT(subject_type,subject_id) DO UPDATE SET started_at=excluded.started_at",
                            type,
                            subject,
                            Math.addExact(Math.multiplyExact(now.epochSecond, 1_000_000_000), now.nano.toLong()),
                        )
                    }
                    null
                }
            }
        }

    suspend fun remove(
        owner: Long,
        now: Instant,
    ) = operation {
        transaction {
            val enrollment = find(owner) ?: return@transaction
            execute("DELETE FROM history WHERE enrollment_id=?", enrollment.id)
            execute("DELETE FROM cooldowns WHERE subject_type='case_check' AND subject_id=?", enrollment.id)
            execute(
                "UPDATE enrollments SET deleted_at=?,case_number=NULL,password=NULL,stage=NULL,checked_at=NULL WHERE id=?",
                now.toString(),
                enrollment.id,
            )
        }
    }

    suspend fun history(owner: Long): List<Transition> =
        operation {
            val enrollment = find(owner) ?: return@operation emptyList()
            connection
                .prepareStatement(
                    "SELECT sequence,previous_stage,stage,checked_at FROM history WHERE enrollment_id=? ORDER BY sequence",
                ).use { statement ->
                    statement.setString(1, enrollment.id)
                    statement.executeQuery().use { rows ->
                        buildList {
                            while (rows.next()) {
                                add(
                                    Transition(
                                        rows.getLong(1),
                                        enrollment.id,
                                        rows.getString(2),
                                        Observation(rows.getString(3), Instant.parse(rows.getString(4))),
                                    ),
                                )
                            }
                        }
                    }
                }
        }

    override fun close() {
        connection.close()
        dispatcher.close()
    }
}

private fun ResultSet.enrollment() =
    Enrollment(getString(1), getLong(2), getString(3), getString(4), Observation(getString(5), Instant.parse(getString(6))))
