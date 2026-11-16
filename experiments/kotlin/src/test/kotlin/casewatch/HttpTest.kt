package casewatch

import com.sun.net.httpserver.HttpServer
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import java.net.InetSocketAddress
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.test.*

class HttpTest {
    @Test
    fun retryAfterZeroDoesNotReplayPortalRequest() =
        runBlocking {
            val count =
                java.util.concurrent.atomic
                    .AtomicInteger()
            val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
            server.createContext("/") { exchange ->
                count.incrementAndGet()
                exchange.responseHeaders.add("Retry-After", "0")
                exchange.sendResponseHeaders(503, -1)
                exchange.close()
            }
            server.start()
            try {
                PortalClient("http://127.0.0.1:${server.address.port}").use { portal ->
                    assertFailsWith<Failure> { portal.check(1, "synthetic", "password") }
                    assertEquals(1, count.get())
                }
            } finally {
                server.stop(0)
            }
        }

    @Test fun liveAdapterUsesRpcAndReusesOwnerCookiesWithoutRetries() =
        runBlocking {
            val requests = CopyOnWriteArrayList<Pair<String, String>>()
            val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
            var step = 0
            server.createContext("/") { exchange ->
                requests.add(exchange.requestMethod to exchange.requestBody.readAllBytes().decodeToString())
                val fixtures = Path.of("../../internal/clients/testdata")
                val content =
                    if (exchange.requestMethod == "GET") {
                        step = 0
                        exchange.responseHeaders.add("Set-Cookie", "owner=synthetic; Path=/")
                        Files.readString(fixtures.resolve("bootstrap.html"))
                    } else {
                        Files.readString(fixtures.resolve("rpc${step++}.json"))
                    }
                val bytes = content.toByteArray()
                exchange.sendResponseHeaders(200, bytes.size.toLong())
                exchange.responseBody.use { it.write(bytes) }
            }
            server.start()
            try {
                PortalClient("http://127.0.0.1:${server.address.port}", wait = {}).use { portal ->
                    assertEquals("Synthetic stage", portal.check(1, "synthetic-number", "synthetic-password").stage)
                    assertEquals(5, requests.size)
                    val payload = Json.parseToJsonElement(requests[2].second).jsonObject
                    assertEquals(
                        "synthetic-number",
                        payload
                            .getValue("rpc")
                            .jsonArray[0]
                            .jsonObject
                            .getValue("value")
                            .jsonPrimitive.content,
                    )
                    assertEquals("Synthetic stage", portal.check(1, "synthetic-number", "synthetic-password").stage)
                    assertEquals(10, requests.size)
                }
            } finally {
                server.stop(0)
            }
        }

    @Test fun telegramSynchronizesMenuAndHandlesPrivateStart() =
        runBlocking {
            val calls = CopyOnWriteArrayList<Pair<String, JsonObject>>()
            val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
            server.createContext("/") { exchange ->
                val method = exchange.requestURI.path.substringAfterLast('/')
                val body = Json.parseToJsonElement(exchange.requestBody.readAllBytes().decodeToString()).jsonObject
                calls.add(method to body)
                val result =
                    when (method) {
                        "getMe" -> "{}"
                        "getMyCommands" -> "[]"
                        else -> "true"
                    }
                val bytes = "{\"ok\":true,\"result\":$result}".toByteArray()
                exchange.sendResponseHeaders(200, bytes.size.toLong())
                exchange.responseBody.use { it.write(bytes) }
            }
            val directory = Files.createTempDirectory("case-watch-telegram")
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
                            assertEquals(listOf("getMe", "getMyCommands", "setMyCommands"), calls.map { it.first })

                            fun message(
                                owner: Long,
                                type: String,
                            ) = buildJsonObject {
                                put("from", buildJsonObject { put("id", owner) })
                                put(
                                    "chat",
                                    buildJsonObject {
                                        put("id", 1)
                                        put("type", type)
                                    },
                                )
                                put("message_id", 1)
                                put("text", "/start")
                            }
                            bot.handle(message(2, "private"), this@runBlocking)
                            bot.handle(message(1, "group"), this@runBlocking)
                            assertEquals(3, calls.size)
                            bot.handle(message(1, "private"), this@runBlocking)
                            assertTrue(
                                calls
                                    .last()
                                    .second
                                    .getValue("text")
                                    .jsonPrimitive.content
                                    .startsWith("Давайте добавим"),
                            )
                        }
                    } finally {
                        cases.shutdown()
                    }
                }
            } finally {
                server.stop(0)
                directory.toFile().deleteRecursively()
            }
        }
}
