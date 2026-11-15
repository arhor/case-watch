package casewatch

import com.sun.net.httpserver.HttpServer
import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import java.net.InetSocketAddress
import java.nio.file.Files
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test

class PollingTest {
    @Test
    fun longPollingAcceptsResponsesDelayedBeyondDefaultReadTimeout() =
        runBlocking {
            val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
            val executor = Executors.newCachedThreadPool()
            server.executor = executor
            val requests = AtomicInteger()
            val replied = CompletableDeferred<Unit>()
            server.createContext("/") { exchange ->
                try {
                    val method = exchange.requestURI.path.substringAfterLast('/')
                    exchange.requestBody.readAllBytes()
                    val result =
                        when (method) {
                            "getMe" -> "{}"
                            "getMyCommands" -> "[]"
                            "getUpdates" -> {
                                if (requests.incrementAndGet() == 1) {
                                    Thread.sleep(11_000)
                                    """[{"update_id":1,"message":{"from":{"id":1,"is_bot":false},"chat":{"id":1,"type":"private"},"message_id":1,"text":"/help"}}]"""
                                } else {
                                    Thread.sleep(60_000)
                                    "[]"
                                }
                            }
                            else -> "true"
                        }
                    val bytes = "{\"ok\":true,\"result\":$result}".toByteArray()
                    exchange.sendResponseHeaders(200, bytes.size.toLong())
                    exchange.responseBody.use { it.write(bytes) }
                    if (method == "sendMessage") replied.complete(Unit)
                } catch (_: Exception) {
                    // The pending long poll is expected to be interrupted at shutdown.
                } finally {
                    exchange.close()
                }
            }
            val directory = Files.createTempDirectory("case-watch-polling")
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
            server.start()
            try {
                Storage(directory.resolve("test.db").toString()).use { store ->
                    val cases = CaseService(store, portal)
                    try {
                        Telegram("123:" + "a".repeat(35), cases, endpoint = "http://127.0.0.1:${server.address.port}").use { bot ->
                            bot.initialize()
                            val polling = launch { bot.run() }
                            try {
                                withTimeout(20_000) { replied.await() }
                            } finally {
                                polling.cancelAndJoin()
                            }
                        }
                    } finally {
                        cases.shutdown()
                    }
                }
            } finally {
                server.stop(0)
                executor.shutdownNow()
                directory.toFile().deleteRecursively()
            }
        }
}
