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
                    PortalClient().use { portal ->
                        val cases = CaseService(store, portal)
                        Telegram(config.telegramBotToken, cases).use { telegram ->
                            try {
                                telegram.initialize()
                                coroutineScope {
                                    launch { telegram.run() }
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
