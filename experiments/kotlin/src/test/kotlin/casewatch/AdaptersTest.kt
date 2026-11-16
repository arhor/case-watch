package casewatch

import kotlinx.coroutines.runBlocking
import java.nio.file.Files
import java.time.Instant
import kotlin.test.*

class AdaptersTest {
    @Test
    fun observationRollsBackWhenHistoryInsertFails() =
        runBlocking {
            val directory = Files.createTempDirectory("case-watch-rollback")
            val path = directory.resolve("test.db").toString()
            val now = Instant.parse("2026-10-01T10:00:00Z")
            try {
                Storage(path).use { store ->
                    store.create(Enrollment("identity", 1, "synthetic", "password", Observation("first", now)))
                    java.sql.DriverManager.getConnection("jdbc:sqlite:$path").use { connection ->
                        connection.createStatement().use {
                            it.execute(
                                "CREATE TRIGGER reject_history BEFORE INSERT ON history BEGIN SELECT RAISE(ABORT, 'synthetic failure'); END",
                            )
                        }
                    }
                    assertFailsWith<Failure> { store.observe(1, "identity", Observation("second", now.plusSeconds(1))) }
                    assertEquals("first", store.enrollment(1)?.observation?.stage)
                    assertTrue(store.history(1).isEmpty())
                }
            } finally {
                directory.toFile().deleteRecursively()
            }
        }

    @Test fun configurationRejectsSecretsWithoutEchoing() {
        val secret = "secret-token"
        val error = assertFailsWith<IllegalArgumentException> { Config.load { secret } }
        assertFalse(error.message.orEmpty().contains(secret))
        assertTrue(error.message.orEmpty().contains("CASE_WATCH_TELEGRAM_BOT_TOKEN"))
    }

    @Test fun configurationPreservesPathAndValidToken() {
        val token = "123:" + "a".repeat(35)
        assertEquals(
            Config(token, " relative.db "),
            Config.load {
                if (it.endsWith("TOKEN")) token else " relative.db "
            },
        )
    }

    @Test fun portalFixturesDiscoverLoginAndStage() {
        val fixtures =
            java.nio.file.Path
                .of("../../internal/clients/testdata")
        val state = RpcState()
        state.apply(bootstrap(Files.readString(fixtures.resolve("bootstrap.html")), "var uidl ="))
        state.find("vaadin-text-field", "Number of application")
        state.find("vaadin-password-field", "Password")
        state.find("vaadin-button", "")
        assertNull(state.stageOrNull())
        repeat(4) { index ->
            kotlinx.serialization.json.Json
                .parseToJsonElement(
                    Files.readString(fixtures.resolve("rpc$index.json")).removePrefix("for(;;);"),
                ).let { it as kotlinx.serialization.json.JsonArray }
                .forEach {
                    state.apply(it as kotlinx.serialization.json.JsonObject)
                }
        }
        assertEquals("Synthetic stage", state.stageOrNull())
    }

    @Test fun storagePersistsTransitionsAndGuardsAcrossRestart() =
        runBlocking {
            val directory = Files.createTempDirectory("case-watch-kotlin-test")
            val path = directory.resolve("test.db").toString()
            val now = Instant.parse("2026-10-01T10:00:00Z")
            try {
                Storage(path).use { store ->
                    store.create(Enrollment("identity", 10, "synthetic", "password", Observation("first", now)))
                    store.observe(10, "identity", Observation("second", now.plusSeconds(1)))
                    store.observe(10, "identity", Observation("second", now.plusSeconds(2)))
                    assertEquals(1, store.history(10).size)
                    assertNull(store.reserve(10, "identity", true, now))
                }
                Storage(path).use { store ->
                    assertEquals("second", store.enrollment(10)?.observation?.stage)
                    assertEquals(now.plusSeconds(60), store.reserve(10, "identity", true, now.plusSeconds(59)))
                    store.remove(10, now.plusSeconds(59))
                    assertNull(store.enrollment(10))
                    assertTrue(store.history(10).isEmpty())
                    assertEquals(now.plusSeconds(60), store.reserve(10, "replacement", true, now.plusSeconds(59)))
                    assertNull(store.reserve(10, "replacement", true, now.plusSeconds(60)))
                }
            } finally {
                directory.toFile().deleteRecursively()
            }
        }
}
