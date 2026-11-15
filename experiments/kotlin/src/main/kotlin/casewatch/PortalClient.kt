package casewatch

import kotlinx.coroutines.delay
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.*
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.time.Clock
import java.time.Duration
import java.util.concurrent.ConcurrentHashMap
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/** Keep cancellation attached until response headers and body have both drained. */
internal suspend fun <T> Call.consume(block: (Response) -> T): T =
    suspendCancellableCoroutine { continuation ->
        continuation.invokeOnCancellation { cancel() }
        enqueue(
            object : Callback {
                override fun onFailure(
                    call: Call,
                    e: IOException,
                ) {
                    if (continuation.isActive) continuation.resumeWithException(Failure(Failure.Kind.TRANSPORT))
                }

                override fun onResponse(
                    call: Call,
                    response: Response,
                ) {
                    try {
                        val result = response.use(block)
                        if (continuation.isActive) continuation.resume(result)
                    } catch (error: Exception) {
                        if (continuation.isActive) {
                            continuation.resumeWithException(
                                if (error is Failure || error is IllegalStateException) error else Failure(Failure.Kind.TRANSPORT),
                            )
                        }
                    }
                }
            },
        )
    }

class PortalClient(
    private val base: String = "https://klient.gdansk.uw.gov.pl",
    private val clock: Clock = Clock.systemUTC(),
    private val wait: suspend (Long) -> Unit = { delay(it) },
) : Portal {
    private val cookies = ConcurrentHashMap<Long, MutableList<Cookie>>()
    private val client =
        OkHttpClient
            .Builder()
            .retryOnConnectionFailure(false)
            .addNetworkInterceptor { chain ->
                val response = chain.proceed(chain.request())
                // OkHttp otherwise follows 503 Retry-After: 0 even with connection retries disabled.
                if (response.code == 503) response.newBuilder().removeHeader("Retry-After").build() else response
            }.followRedirects(false)
            .followSslRedirects(false)
            .callTimeout(Duration.ofSeconds(10))
            .build()

    override suspend fun check(
        owner: Long,
        number: String,
        password: String,
    ): Observation {
        try {
            return withTimeout(60_000) {
                val state = RpcState()
                var requests = 0
                var last: java.time.Instant? = null

                suspend fun request(
                    path: String,
                    payload: JsonObject? = null,
                ): String {
                    if (requests >= 6) throw Failure(Failure.Kind.PROTOCOL)
                    last?.let { wait(Duration.between(clock.instant(), it.plusSeconds(2)).toMillis().coerceAtLeast(0)) }
                    val builder = Request.Builder().url(base.trimEnd('/') + path)
                    val url = builder.build().url
                    synchronized(cookies) {
                        cookies[owner]
                            ?.filter { it.expiresAt > clock.millis() && it.matches(url) }
                            ?.takeIf { it.isNotEmpty() }
                            ?.let {
                                builder.header(
                                    "Cookie",
                                    it.joinToString("; ") { c -> "${c.name}=${c.value}" },
                                )
                            }
                    }
                    if (payload != null) builder.post(payload.toString().toRequestBody("application/json; charset=UTF-8".toMediaType()))
                    requests++
                    last = clock.instant()
                    return client.newCall(builder.build()).consume { response ->
                        if (response.code == 401 || response.code == 403) throw Failure(Failure.Kind.AUTHENTICATION)
                        if (response.code != 200) throw Failure(Failure.Kind.TRANSPORT)
                        synchronized(cookies) {
                            val jar = cookies.getOrPut(owner) { mutableListOf() }
                            Cookie.parseAll(url, response.headers).forEach { cookie ->
                                jar.removeAll { it.name == cookie.name && it.domain == cookie.domain && it.path == cookie.path }
                                jar.add(cookie)
                            }
                        }
                        val source = response.body?.source() ?: throw Failure(Failure.Kind.TRANSPORT)
                        if (source.request(4L * 1024 * 1024 + 1)) throw Failure(Failure.Kind.PROTOCOL)
                        source.readUtf8()
                    }
                }
                val html = request("/")
                state.apply(bootstrap(html, "var uidl ="))
                val ui = bootstrap(html, "var config =")["v-uiId"].integer()
                if (state.stageOrNull() == null) {
                    repeat(4) { step ->
                        val numberNode = state.find("vaadin-text-field", "Number of application")
                        val passwordNode = state.find("vaadin-password-field", "Password")
                        val button = state.find("vaadin-button", "")
                        val rpc =
                            when (step) {
                                0 -> JsonArray(listOf(event(passwordNode, "value-changed")))
                                1 -> change(numberNode, number, "change")
                                2 -> change(passwordNode, password, "value-changed")
                                else -> JsonArray(listOf(event(button, "click")))
                            }
                        if (state.token.isEmpty()) throw Failure(Failure.Kind.PROTOCOL)
                        val payload =
                            buildJsonObject {
                                put("csrfToken", state.token)
                                put("syncId", state.sync)
                                put("clientId", state.client)
                                put("rpc", rpc)
                            }
                        val messages = Json.parseToJsonElement(request("/?v-r=uidl&v-uiId=$ui", payload).removePrefix("for(;;);")).jsonArray
                        if (messages.isEmpty()) throw Failure(Failure.Kind.PROTOCOL)
                        messages.forEach { state.apply(it.jsonObject) }
                    }
                }
                Observation(state.stageOrNull() ?: throw Failure(Failure.Kind.AUTHENTICATION), clock.instant())
            }
        } catch (error: Exception) {
            forgetSession(owner)
            if (error is kotlinx.coroutines.CancellationException || error is Failure) throw error
            throw Failure(Failure.Kind.PROTOCOL)
        }
    }

    override fun forgetSession(owner: Long) {
        cookies.remove(owner)
    }

    override fun close() {
        cookies.clear()
        client.connectionPool.evictAll()
        client.dispatcher.executorService.shutdown()
    }
}

internal fun bootstrap(
    html: String,
    marker: String,
): JsonObject {
    val offset = html.indexOf(marker)
    if (offset < 0) throw Failure(Failure.Kind.PROTOCOL)
    val text = html.substring(offset + marker.length).trimStart()
    var depth = 0
    var quoted = false
    var escaped = false
    text.forEachIndexed { index, char ->
        if (quoted) {
            if (escaped) {
                escaped = false
            } else if (char == '\\') {
                escaped = true
            } else if (char == '"') {
                quoted = false
            }
        } else {
            when (char) {
                '"' -> quoted = true
                '{' -> depth++
                '}' -> {
                    depth--
                    if (depth == 0) return Json.parseToJsonElement(text.substring(0, index + 1)).jsonObject
                }
            }
        }
    }
    throw Failure(Failure.Kind.PROTOCOL)
}

private fun JsonElement?.integer(): Long {
    val value = (this as? JsonPrimitive)?.longOrNull ?: throw Failure(Failure.Kind.PROTOCOL)
    if (value !in 0..9007199254740991L) throw Failure(Failure.Kind.PROTOCOL)
    return value
}

private fun event(
    node: Long,
    name: String,
) = buildJsonObject {
    put("type", "event")
    put("node", node)
    put("event", name)
    put(
        "data",
        buildJsonObject {
            if (name == "click") {
                listOf("shiftKey", "metaKey", "ctrlKey", "altKey").forEach { put("event.$it", false) }
                listOf("detail", "clientX", "clientY", "button", "screenX", "screenY").forEach { put("event.$it", 0) }
            }
        },
    )
}

private fun change(
    node: Long,
    value: String,
    name: String,
) = JsonArray(
    listOf(
        buildJsonObject {
            put("type", "mSync")
            put("node", node)
            put("feature", 1)
            put("property", "value")
            put("value", value)
        },
        event(node, name),
    ),
)

internal class RpcState {
    private val properties = mutableMapOf<Long, MutableMap<String, JsonElement>>()
    private val children = mutableMapOf<Long, List<Long>>()
    var sync = 0L
    var client = 0L
    var token = ""

    fun apply(message: JsonObject) {
        sync = message["syncId"].integer()
        client = message["clientId"].integer()
        message["Vaadin-Security-Key"]?.jsonPrimitive?.contentOrNull?.let { token = it }
        val changes = message["changes"] ?: return
        changes.jsonArray.forEach { element ->
            val change = element.jsonObject
            val node = change["node"].integer()
            val props = properties.getOrPut(node) { mutableMapOf() }
            val feature = change["feat"]?.jsonPrimitive?.intOrNull ?: 0
            when (change["type"]?.jsonPrimitive?.content) {
                "attach" -> Unit
                "put" -> if (feature in listOf(0, 1, 7)) props[change.getValue("key").jsonPrimitive.content] = change.getValue("value")
                "remove" -> if (feature in listOf(0, 1, 7)) props.remove(change.getValue("key").jsonPrimitive.content)
                "detach" -> {
                    properties.remove(node)
                    children.remove(node)
                }
                "clear", "empty" -> if (feature == 2) children.remove(node)
                "splice" ->
                    if (feature == 2) {
                        val old = children[node].orEmpty()
                        val index = change["index"].integer()
                        val remove = change["remove"]?.integer() ?: 0
                        if (index > old.size || remove > old.size - index) throw Failure(Failure.Kind.PROTOCOL)
                        children[node] =
                            old.take(index.toInt()) + (change["addNodes"]?.jsonArray?.map { it.integer() }.orEmpty()) +
                            old.drop((index + remove).toInt())
                    }
                else -> throw Failure(Failure.Kind.PROTOCOL)
            }
        }
    }

    fun find(
        tag: String,
        label: String,
    ): Long =
        properties
            .filter { (node, props) ->
                props["tag"] == JsonPrimitive(tag) &&
                    (label.isEmpty() || props["label"] == JsonPrimitive(label)) &&
                    (tag != "vaadin-button" || children[node].orEmpty().any { properties[it]?.get("text") == JsonPrimitive("Log in") })
            }.keys
            .singleOrNull() ?: throw Failure(Failure.Kind.PROTOCOL)

    fun stageOrNull(): String? {
        fun descendants(
            node: Long,
            seen: MutableSet<Long>,
        ): List<Long> = if (!seen.add(node)) emptyList() else listOf(node) + children[node].orEmpty().flatMap { descendants(it, seen) }
        children.keys.forEach { root ->
            val nodes = descendants(root, mutableSetOf())
            if (nodes.any {
                    properties[it]?.let { p ->
                        p["text"] == JsonPrimitive("Stage of your case") ||
                            p["nodeValue"] == JsonPrimitive("Stage of your case")
                    } ==
                        true
                }
            ) {
                val values =
                    nodes
                        .mapNotNull { properties[it] }
                        .filter { it["tag"] == JsonPrimitive("vaadin-text-field") }
                        .mapNotNull { (it["value"] as? JsonPrimitive)?.contentOrNull }
                        .filter { it.isNotBlank() }
                if (values.size == 1) return values.single()
            }
        }
        return null
    }
}
