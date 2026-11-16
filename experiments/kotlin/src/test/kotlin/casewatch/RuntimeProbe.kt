package casewatch

import kotlinx.coroutines.*
import java.net.InetAddress
import java.nio.file.Files
import java.nio.file.Path
import java.security.KeyStore
import java.security.cert.CertificateFactory
import java.time.Instant
import java.time.ZoneId
import java.util.concurrent.FutureTask
import java.util.concurrent.TimeUnit
import javax.net.ssl.KeyManagerFactory
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLServerSocket
import javax.net.ssl.SSLSocket
import javax.net.ssl.TrustManagerFactory

/** Standalone packaging check: executes on the shipped jlink JVM, without Gradle or JUnit. */
object RuntimeProbe {
    @JvmStatic
    fun main(args: Array<String>) =
        runBlocking {
            val directory = Files.createTempDirectory("case-watch-runtime-probe")
            try {
                val now = Instant.parse("2026-10-01T10:00:00Z")
                val database = directory.resolve("probe.db").toString()
                Storage(database).use { store ->
                    store.create(Enrollment("probe", 1, "synthetic", "password", Observation("first", now)))
                    store.observe(1, "probe", Observation("second", now.plusSeconds(1)))
                    check(store.history(1).size == 1)
                }
                Storage(database).use { store ->
                    check(store.enrollment(1)?.observation?.stage == "second")
                    store.remove(1, now.plusSeconds(2))
                    check(store.enrollment(1) == null)
                }
                val warsaw = ZoneId.of("Europe/Warsaw")
                check(Instant.parse("2026-03-29T00:30:00Z").atZone(warsaw).hour == 1)
                check(Instant.parse("2026-03-29T01:30:00Z").atZone(warsaw).hour == 3)
                check(
                    Instant
                        .parse("2026-10-25T00:30:00Z")
                        .atZone(warsaw)
                        .offset.totalSeconds == 7200,
                )
                check(
                    Instant
                        .parse("2026-10-25T01:30:00Z")
                        .atZone(warsaw)
                        .offset.totalSeconds == 3600,
                )
                val job = launch { delay(Long.MAX_VALUE) }
                withTimeout(1000) { job.cancelAndJoin() }
                verifyTrust(Path.of(args[1]))
                verifyTls(Path.of(args[0]))
                println("trimmed-runtime probe passed: SQLite/restart/removal, Warsaw DST, cancellation, trust anchors, EC TLS")
            } finally {
                directory.toFile().deleteRecursively()
            }
        }

    private fun verifyTrust(certificates: Path) {
        val trust = KeyStore.getInstance("JKS")
        Files.newInputStream(Path.of(System.getProperty("java.home"), "lib/security/cacerts")).use {
            trust.load(it, "changeit".toCharArray())
        }
        check(trust.size() > 1)
        Files.list(certificates).use { paths ->
            paths.filter { it.toString().endsWith(".crt") }.forEach { path ->
                val certificate = Files.newInputStream(path).use { CertificateFactory.getInstance("X.509").generateCertificate(it) }
                check(trust.getCertificateAlias(certificate) != null)
            }
        }
    }

    private fun verifyTls(keyStorePath: Path) {
        val password = "synthetic-probe".toCharArray()
        val store = KeyStore.getInstance("PKCS12")
        Files.newInputStream(keyStorePath).use { store.load(it, password) }
        val keys = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm())
        keys.init(store, password)
        val trust = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm())
        trust.init(store)
        val context = SSLContext.getInstance("TLS")
        context.init(keys.keyManagers, trust.trustManagers, null)
        (context.serverSocketFactory.createServerSocket(0, 1, InetAddress.getLoopbackAddress()) as SSLServerSocket).use { server ->
            server.soTimeout = 5000
            val response =
                FutureTask {
                    (server.accept() as SSLSocket).use { socket ->
                        socket.soTimeout = 5000
                        socket.startHandshake()
                        socket.outputStream.write(42)
                    }
                }
            val thread =
                Thread(response).also {
                    it.isDaemon = true
                    it.start()
                }
            try {
                (context.socketFactory.createSocket(server.inetAddress, server.localPort) as SSLSocket).use { client ->
                    client.soTimeout = 5000
                    client.startHandshake()
                    check(client.inputStream.read() == 42)
                }
                response.get(5, TimeUnit.SECONDS)
            } finally {
                thread.interrupt()
            }
        }
    }
}
