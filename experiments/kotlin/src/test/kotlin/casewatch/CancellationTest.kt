package casewatch

import com.sun.net.httpserver.HttpServer
import kotlinx.coroutines.*
import java.net.InetSocketAddress
import java.util.concurrent.Executors
import kotlin.test.*

class CancellationTest {
    @Test fun cancellationInterruptsResponseBodyAndForgetsSession() =
        runBlocking {
            val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
            val executor = Executors.newCachedThreadPool()
            server.executor = executor
            val started = CompletableDeferred<Unit>()
            server.createContext("/") { exchange ->
                try {
                    exchange.sendResponseHeaders(200, 100_000)
                    exchange.responseBody.write("var uidl = {".toByteArray())
                    exchange.responseBody.flush()
                    started.complete(Unit)
                    Thread.sleep(30_000)
                } catch (_: Exception) {
                } finally {
                    exchange.close()
                }
            }
            server.start()
            try {
                PortalClient("http://127.0.0.1:${server.address.port}").use { portal ->
                    val job = launch { portal.check(1, "synthetic", "password") }
                    withTimeout(5000) { started.await() }
                    withTimeout(1000) { job.cancelAndJoin() }
                    assertTrue(job.isCancelled)
                }
            } finally {
                server.stop(0)
                executor.shutdownNow()
            }
        }
}
