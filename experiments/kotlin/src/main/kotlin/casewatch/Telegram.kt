package casewatch

import kotlinx.coroutines.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.*
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import java.time.*
import java.time.format.DateTimeFormatter

class Telegram(
    private val token: String,
    private val cases: CaseService,
    private val clock: Clock = Clock.systemUTC(),
    private val endpoint: String = "https://api.telegram.org",
) : AutoCloseable {
    private val client =
        OkHttpClient
            .Builder()
            .retryOnConnectionFailure(false)
            .addNetworkInterceptor { chain ->
                val response = chain.proceed(chain.request())
                // OkHttp otherwise follows 503 Retry-After: 0 even with connection retries disabled.
                if (response.code == 503) response.newBuilder().removeHeader("Retry-After").build() else response
            }.callTimeout(Duration.ofSeconds(65))
            .readTimeout(Duration.ofSeconds(65))
            .build()
    private val replies = Mutex()
    private val polling =
        java.util.concurrent.atomic
            .AtomicBoolean()
    private val states = mutableMapOf<Long, State>()
    private val messages = Json.parseToJsonElement(requireNotNull(javaClass.getResource("/messages.json")).readText()).jsonObject

    private class State {
        val lock = Mutex()
        var number = ""
        var deadline: Instant? = null
        var removal: Instant? = null
        var enrollment: Job? = null
    }

    private val commands =
        listOf(
            "start" to "Добавить дело для отслеживания",
            "status" to "Проверить статус дела",
            "remove" to "Удалить дело и связанные с ним данные",
            "cancel" to "Отменить текущее действие",
            "help" to "Показать эту справку",
        )

    private suspend fun api(
        method: String,
        body: JsonObject = buildJsonObject {},
    ): JsonElement {
        val request =
            Request
                .Builder()
                .url("${endpoint.trimEnd('/')}/bot$token/$method")
                .post(body.toString().toRequestBody("application/json".toMediaType()))
                .build()
        return client.newCall(request).consume { response ->
            if (response.code == 401) throw IllegalStateException("telegram_invalid_token")
            val result =
                try {
                    Json.parseToJsonElement(response.body?.string().orEmpty()).jsonObject
                } catch (
                    error: Exception,
                ) {
                    throw Failure(Failure.Kind.TRANSPORT)
                }
            if (result["error_code"]?.jsonPrimitive?.intOrNull == 401) throw IllegalStateException("telegram_invalid_token")
            if (result["ok"] !=
                JsonPrimitive(true)
            ) {
                throw Failure(
                    Failure.Kind.TRANSPORT,
                    result["parameters"]
                        ?.jsonObject
                        ?.get("retry_after")
                        ?.jsonPrimitive
                        ?.longOrNull,
                )
            }
            result["result"] ?: JsonNull
        }
    }

    private suspend fun reply(
        id: Long,
        text: String,
    ) {
        replies.withLock {
            try {
                withTimeout(15_000) {
                    api(
                        "sendMessage",
                        buildJsonObject {
                            put("chat_id", id)
                            put("text", text)
                        },
                    )
                }
            } catch (
                error: Failure,
            ) {
                System.err.println("telegram reply failure")
            } catch (
                error: TimeoutCancellationException,
            ) {
                System.err.println("telegram reply failure")
            }
        }
    }

    private suspend fun say(
        id: Long,
        key: String,
    ) = reply(id, messages.getValue(key).jsonPrimitive.content)

    private suspend fun erase(
        id: Long,
        message: Long,
    ) {
        try {
            withTimeout(15_000) {
                api(
                    "deleteMessage",
                    buildJsonObject {
                        put("chat_id", id)
                        put("message_id", message)
                    },
                )
            }
        } catch (
            error: Failure,
        ) {
            System.err.println("telegram deletion failure")
        } catch (
            error: TimeoutCancellationException,
        ) {
            System.err.println("telegram deletion failure")
        }
    }

    suspend fun initialize() =
        withTimeout(15_000) {
            api("getMe")
            try {
                val desired =
                    JsonArray(
                        commands.map { (name, description) ->
                            buildJsonObject {
                                put("command", name)
                                put("description", description)
                            }
                        },
                    )
                if (api("getMyCommands") != desired) api("setMyCommands", buildJsonObject { put("commands", desired) })
            } catch (error: Failure) {
                System.err.println("telegram command synchronization failure")
            }
        }

    suspend fun run(): Unit =
        coroutineScope {
            check(polling.compareAndSet(false, true)) { "telegram polling already started" }
            val sweep =
                launch {
                    while (isActive) {
                        delay(60_000)
                        states.values.toList().forEach { state ->
                            state.lock.withLock {
                                val now = clock.instant()
                                if (state.deadline?.let { now >= it } == true) {
                                    state.deadline = null
                                    state.number = ""
                                }
                                if (state.removal?.let { now >= it } == true) state.removal = null
                            }
                        }
                    }
                }
            try {
                val updatesChannel = kotlinx.coroutines.channels.Channel<JsonObject>(1024)
                val producer =
                    launch {
                        var offset = 1L
                        var backoff = 0L
                        try {
                            while (isActive) {
                                if (backoff > 0) delay(backoff)
                                val updates =
                                    try {
                                        withTimeout(60_000) {
                                            api(
                                                "getUpdates",
                                                buildJsonObject {
                                                    put("offset", offset)
                                                    put("timeout", 59)
                                                    put("allowed_updates", JsonArray(listOf(JsonPrimitive("message"))))
                                                },
                                            ).jsonArray
                                        }
                                    } catch (error: Failure) {
                                        System.err.println("telegram polling failure")
                                        backoff = error.retryAfter?.takeIf { it > 0 }?.times(1000)
                                            ?: if (backoff == 0L) 100 else (backoff * 2).coerceAtMost(5000)
                                        continue
                                    } catch (error: TimeoutCancellationException) {
                                        currentCoroutineContext().ensureActive()
                                        backoff = if (backoff == 0L) 100 else (backoff * 2).coerceAtMost(5000)
                                        continue
                                    }
                                backoff = 0
                                for (update in updates) {
                                    val value = update.jsonObject
                                    val updateId = value["update_id"]?.jsonPrimitive?.longOrNull
                                    if (updateId == null || updateId == 0L) {
                                        System.err.println("telegram polling failure")
                                        backoff = if (backoff == 0L) 100 else (backoff * 2).coerceAtMost(5000)
                                        continue
                                    }
                                    offset = if (updateId == Long.MAX_VALUE) updateId else updateId + 1
                                    updatesChannel.send(value)
                                }
                            }
                        } finally {
                            updatesChannel.close()
                        }
                    }
                try {
                    for (update in updatesChannel) update["message"]?.jsonObject?.let { handle(it, this) }
                } finally {
                    producer.cancel()
                }
            } finally {
                sweep.cancel()
                states.values.forEach {
                    it.enrollment?.cancel()
                    it.number = ""
                    it.deadline = null
                    it.removal = null
                }
            }
        }

    internal suspend fun handle(
        message: JsonObject,
        scope: CoroutineScope,
    ) {
        val sender =
            message["from"]
                ?.jsonObject
                ?.get("id")
                ?.jsonPrimitive
                ?.longOrNull ?: return
        val chat = message["chat"]?.jsonObject ?: return
        val id = chat["id"]?.jsonPrimitive?.longOrNull ?: return
        if (message["from"]?.jsonObject?.get("is_bot") == JsonPrimitive(true)) return
        if (id <= 0 || sender != id || chat["type"] != JsonPrimitive("private")) return
        val state = states.getOrPut(id) { State() }
        state.lock.withLock {
            val now = clock.instant()
            val expired = state.deadline?.let { now >= it } == true
            val expiredRemoval = state.removal?.let { now >= it } == true
            if (expired) {
                state.deadline = null
                state.number = ""
            }
            if (expiredRemoval) state.removal = null
            val text = message["text"]?.jsonPrimitive?.content.orEmpty()
            val messageId = message["message_id"]?.jsonPrimitive?.longOrNull ?: return
            val fields = text.trim().split(Regex("\\s+")).filter { it.isNotEmpty() }
            val command = fields.firstOrNull()?.takeIf { it.startsWith('/') }
            if (command != null && fields.size > 1) {
                state.number = ""
                state.deadline = null
                erase(id, messageId)
                say(id, "invalid_command")
                return
            }
            when (command) {
                "/cancel" -> {
                    state.enrollment?.cancel()
                    state.number = ""
                    state.deadline = null
                    state.removal = null
                    say(id, "canceled")
                }
                "/help" ->
                    reply(
                        id,
                        "CaseWatch помогает следить за ходом рассмотрения вашего дела.\n\nДоступные команды:\n" +
                            commands.joinToString("\n") { "/${it.first} — ${it.second}" },
                    )
                "/start" -> {
                    if (state.enrollment != null) {
                        state.enrollment?.cancel()
                        say(id, "cancel_previous")
                        return
                    }
                    state.number = ""
                    state.deadline = null
                    state.removal = null
                    val exists =
                        try {
                            cases.hasEnrollment(id)
                        } catch (error: Failure) {
                            say(id, "storage_failure")
                            return
                        }
                    if (exists) {
                        say(id, "already_enrolled")
                    } else {
                        state.deadline = now.plusSeconds(600)
                        say(id, "start")
                    }
                }
                "/status" -> {
                    try {
                        reply(id, formatStatus(cases.status(id)))
                    } catch (error: Failure) {
                        if (error.kind ==
                            Failure.Kind.NOT_FOUND
                        ) {
                            say(id, "not_enrolled")
                        } else {
                            reply(id, formatStatus(CheckResult(Status(null), error)))
                        }
                    }
                }
                "/remove" -> {
                    state.enrollment?.cancel()
                    state.number = ""
                    state.deadline = null
                    val exists =
                        try {
                            cases.hasEnrollment(id)
                        } catch (error: Failure) {
                            say(id, "storage_failure")
                            return
                        }
                    if (!exists) {
                        say(id, "not_enrolled")
                    } else {
                        state.removal = now.plusSeconds(600)
                        say(id, "confirm_remove")
                    }
                }
                null -> {
                    if (expiredRemoval) {
                        say(id, "expired_removal")
                        return
                    }
                    if (expired) {
                        say(id, "expired_enrollment")
                        return
                    }
                    if (state.removal != null) {
                        when (text.trim().lowercase()) {
                            "нет" -> {
                                state.removal = null
                                say(id, "remove_canceled")
                            }
                            "да" -> {
                                state.removal = null
                                try {
                                    cases.remove(id)
                                    say(id, "removed")
                                } catch (
                                    error: Failure,
                                ) {
                                    say(id, "remove_failed")
                                }
                            }
                            else -> say(id, "answer_yes_no")
                        }
                        return
                    }
                    if (state.deadline == null) {
                        say(id, "idle")
                        return
                    }
                    erase(id, messageId)
                    if (state.number.isEmpty()) {
                        if (text.isBlank() ||
                            text.toByteArray().size > 256 ||
                            '\r' in text ||
                            '\n' in text
                        ) {
                            say(id, "invalid_number")
                            return
                        }
                        state.number = text
                        state.deadline = now.plusSeconds(600)
                        say(id, "ask_password")
                    } else {
                        if (text.isEmpty() || text.toByteArray().size > 4096) {
                            say(id, "invalid_password")
                            return
                        }
                        val number = state.number
                        state.number = ""
                        state.deadline = null
                        say(id, "checking")
                        state.enrollment =
                            scope
                                .launch(start = CoroutineStart.LAZY) {
                                    var success = false
                                    try {
                                        cases.enroll(id, number, text)
                                        success = true
                                    } catch (
                                        error: Failure,
                                    ) {
                                    } catch (
                                        error: CancellationException,
                                    ) {
                                    } finally {
                                        withContext(NonCancellable) {
                                            state.lock.withLock {
                                                state.enrollment = null
                                                say(id, if (success) "enrolled" else "enroll_failed")
                                            }
                                        }
                                    }
                                }.also { it.start() }
                    }
                }
                else -> {
                    if (state.deadline != null) erase(id, messageId)
                    say(id, "unknown_command")
                }
            }
        }
    }

    override fun close() {
        client.connectionPool.evictAll()
        client.dispatcher.executorService.shutdown()
    }
}

internal fun formatStatus(result: CheckResult): String {
    val parts = mutableListOf<String>()
    if (result.failure != null) parts.add("Не удалось обновить статус дела.")
    val observation = result.status.observation
    if (observation == null) {
        parts.add("Пока нет данных об успешных проверках вашего дела.")
    } else {
        val stage = observation.stage.ifEmpty { "Не указан" }
        val time = DateTimeFormatter.ofPattern("dd.MM.yyyy, HH:mm").withZone(ZoneId.of("Europe/Warsaw")).format(observation.checkedAt)
        parts.add(
            when {
                result.failure != null -> "Показываю данные последней успешной проверки:\n\nСтатус дела: $stage\nПроверено: $time"
                result.status.fresh -> "Статус дела: $stage\nПоследняя проверка: $time\n\nДанные успешно обновлены."
                else -> "Статус дела: $stage\nПоследняя успешная проверка: $time"
            },
        )
    }
    if (result.status.inProgress) parts.add("Проверка статуса ещё выполняется.")
    return parts.joinToString("\n\n")
}
