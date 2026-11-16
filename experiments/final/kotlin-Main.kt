package casewatch

import kotlinx.coroutines.*
import java.util.concurrent.CountDownLatch

fun main() {
    val storageFailed =
        java.util.concurrent.atomic
            .AtomicBoolean()
    try {
        val config = Config.load()
        runBlocking {
            val root = currentCoroutineContext().job
            val stopped = CountDownLatch(1)
            val hook =
                Thread {
                    root.cancel()
                    stopped.await()
                }
            Runtime.getRuntime().addShutdownHook(hook)
            try {
                Storage(config.dbPath) {
                    storageFailed.set(true)
                    root.cancel()
                }.use { store ->
                    PortalClient(base = System.getenv("BENCH_ENDPOINT")).use { portal ->
                        store.create(Enrollment("00000000000000000000000000000001", 1, "synthetic-number", "synthetic-password", Observation("Synthetic stage", java.time.Instant.now().minusSeconds(120))))
                        val cases = CaseService(store, portal)
                        Telegram(config.telegramBotToken, cases, endpoint = System.getenv("BENCH_ENDPOINT")).use { telegram ->
                            try {
                                telegram.initialize()
                                coroutineScope {
                                    launch { telegram.run() }
                                    if (System.getenv("BENCH_SCENARIO") == "scheduled") launch {
                                        delay(10000)
                                        for (owner in cases.scheduledOwners()) check(cases.status(owner, false).failure == null)
                                        withContext(Dispatchers.IO) {
                                            val connection = java.net.URI(System.getenv("BENCH_ENDPOINT") + "/scheduled-done").toURL().openConnection() as java.net.HttpURLConnection
                                            connection.requestMethod = "POST"
                                            connection.inputStream.close()
                                            connection.disconnect()
                                        }
                                    }
                                    launch { Schedule(cases).run() }
                                }
                            } finally {
                                withContext(NonCancellable) { cases.shutdown() }
                            }
                        }
                    }
                }
            } finally {
                stopped.countDown()
                try {
                    Runtime.getRuntime().removeShutdownHook(hook)
                } catch (_: IllegalStateException) {
                }
            }
        }
    } catch (
        _: CancellationException,
    ) {
        if (storageFailed.get()) {
            System.err.println("service operation failed")
            kotlin.system.exitProcess(1)
        }
    } catch (_: Exception) {
        System.err.println("service operation failed")
        kotlin.system.exitProcess(1)
    }
}
