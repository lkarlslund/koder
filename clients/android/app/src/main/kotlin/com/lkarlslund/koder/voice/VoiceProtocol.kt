package com.lkarlslund.koder.voice

import org.json.JSONArray
import org.json.JSONObject
import java.net.URI
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.time.Instant

const val VOICE_PROTOCOL = "voice.v1"

data class VoiceSession(
    val id: String,
    val title: String,
    val lastMessage: String = "",
    val updatedAt: Instant? = null,
	val archived: Boolean = false,
	val pinned: Boolean = false,
	val favorite: Boolean = false,
	val deleted: Boolean = false,
	val resultCount: Long = 0,
	val busy: Boolean = false,
	val status: String = "",
)

data class VoicePart(
	val id: String = "",
    val mimeType: String,
	val data: Any? = null,
	val uri: String = "",
	val metadata: Map<String, String> = emptyMap(),
) {
	val text: String get() = data as? String ?: ""
	val url: String get() = uri
	val name: String get() = metadata["name"].orEmpty()
	val title: String get() = metadata["title"].orEmpty()
	val alt: String get() = metadata["alt"].orEmpty()
	val isPresentation: Boolean get() = metadata["presentation"] == "true"
}

enum class ConversationSurface { ACTIVE, PRESENTATION, TRANSCRIPT }

fun conversationSurface(active: Boolean, transcriptShown: Boolean, presentationShown: Boolean): ConversationSurface = when {
	!active || transcriptShown -> ConversationSurface.TRANSCRIPT
	presentationShown -> ConversationSurface.PRESENTATION
	else -> ConversationSurface.ACTIVE
}

data class VoiceDelegation(
    val sessionId: String,
    val sessionTitle: String,
    val chatId: String,
    val needsAttention: Boolean,
)

data class VoiceMessage(
    val spokenText: String,
	val transcriptId: String = "",
    val parts: List<VoicePart>,
    val delegation: VoiceDelegation?,
)

data class VoiceTranscriptEntry(
	val id: String,
	val role: String,
	val text: String,
	val createdAt: Instant? = null,
)

data class VoiceTranscriptSearchResult(
	val match: VoiceTranscriptEntry,
	val context: List<VoiceTranscriptEntry>,
)

data class VoiceCallState(
    val voiceSessionId: String,
    val activeSessionId: String,
    val sessions: List<VoiceSession>,
    val voiceSessions: List<VoiceSession> = emptyList(),
	val history: List<VoiceTranscriptEntry> = emptyList(),
	val historyHasMore: Boolean = false,
)

data class VoiceAudioFormat(
    val encoding: String,
    val sampleRate: Int,
    val channels: Int,
)

data class VoiceAudioConfig(
    val input: VoiceAudioFormat,
    val output: VoiceAudioFormat,
    val maxUtteranceSeconds: Int,
)

data class AppUpdate(
    val channel: String,
    val applicationId: String,
    val versionCode: Long,
    val versionName: String,
    val signingCertificateSHA256: String,
    val apkSHA256: String,
    val apkSize: Long,
    val downloadUri: String,
)

data class VoiceHome(
    val voiceSessions: List<VoiceSession>,
    val createdVoiceSession: VoiceSession? = null,
    val appUpdate: AppUpdate? = null,
)

data class ServerInfo(
    val serverTime: Instant,
    val version: String,
    val commit: String,
    val dirty: String,
    val buildTime: String,
    val startedAt: Instant,
    val uptimeSeconds: Long,
    val platform: String,
    val goVersion: String,
    val logicalCPUs: Int,
    val maxProcs: Int,
    val goroutines: Int,
    val heapAllocBytes: Long,
    val heapSysBytes: Long,
    val heapObjects: Long,
    val gcCycles: Long,
    val sessionCount: Int,
    val voiceSessionCount: Int,
    val voiceConnectionActive: Boolean,
    val voiceConnectionSince: Instant?,
    val tokenRequired: Boolean,
    val roundTripMillis: Long = 0,
)

enum class VoiceAudioFrameKind(val wireValue: Int) {
    INPUT_PCM(1),
    OUTPUT_PCM(2),
    ;

    companion object {
        fun fromWire(value: Int): VoiceAudioFrameKind = entries.firstOrNull { it.wireValue == value }
            ?: error("Unsupported voice audio frame kind $value")
    }
}

data class VoiceAudioFrame(
    val kind: VoiceAudioFrameKind,
    val flags: Int,
    val sequence: Long,
    val pcm: ByteArray,
)

data class VoiceServerFrame(
    val type: String,
    val protocol: String,
    val utteranceId: String = "",
    val state: String = "",
    val callState: VoiceCallState? = null,
	val workingOn: VoiceSession? = null,
	val audioConfig: VoiceAudioConfig? = null,
	val audioFormat: VoiceAudioFormat? = null,
	val transcript: String = "",
	val history: List<VoiceTranscriptEntry> = emptyList(),
	val historyHasMore: Boolean = false,
	val searchResults: List<VoiceTranscriptSearchResult> = emptyList(),
    val message: VoiceMessage? = null,
    val appUpdate: AppUpdate? = null,
    val error: String = "",
)

object VoiceProtocol {
	fun createSessionRequest(title: String): String = JSONObject()
		.put("title", title.trim())
		.toString()

	fun updateSessionRequest(
		title: String? = null,
		archived: Boolean? = null,
		pinned: Boolean? = null,
		favorite: Boolean? = null,
		deleted: Boolean? = null,
	): String = JSONObject().apply {
		title?.let { put("title", it.trim()) }
		archived?.let { put("archived", it) }
		pinned?.let { put("pinned", it) }
		favorite?.let { put("favorite", it) }
		deleted?.let { put("deleted", it) }
	}.toString()

	fun parseHome(payload: String): VoiceHome {
		val root = JSONObject(payload)
		val protocol = root.optString("protocol")
		require(protocol == VOICE_PROTOCOL) { "Unsupported voice protocol: $protocol" }
		return VoiceHome(
			voiceSessions = root.optJSONArray("voice_sessions").mapObjects { it.toVoiceSession() }
				.sortedWith(compareByDescending<VoiceSession> { it.pinned }.thenByDescending { it.updatedAt ?: Instant.MIN }),
			createdVoiceSession = root.optJSONObject("voice_session")?.toVoiceSession(),
			appUpdate = root.optJSONObject("app_update")?.toAppUpdate(),
		)
	}

    fun parseServerInfo(payload: String): ServerInfo {
        val root = JSONObject(payload)
        val protocol = root.optString("protocol")
        require(protocol == VOICE_PROTOCOL) { "Unsupported voice protocol: $protocol" }
        return ServerInfo(
            serverTime = Instant.parse(root.getString("server_time")),
            version = root.getString("version"),
            commit = root.getString("commit"),
            dirty = root.getString("dirty"),
            buildTime = root.getString("build_time"),
            startedAt = Instant.parse(root.getString("started_at")),
            uptimeSeconds = root.getLong("uptime_seconds"),
            platform = root.getString("platform"),
            goVersion = root.getString("go_version"),
            logicalCPUs = root.getInt("logical_cpus"),
            maxProcs = root.getInt("max_procs"),
            goroutines = root.getInt("goroutines"),
            heapAllocBytes = root.getLong("heap_alloc_bytes"),
            heapSysBytes = root.getLong("heap_sys_bytes"),
            heapObjects = root.getLong("heap_objects"),
            gcCycles = root.getLong("gc_cycles"),
            sessionCount = root.getInt("session_count"),
            voiceSessionCount = root.getInt("voice_session_count"),
            voiceConnectionActive = root.getBoolean("voice_connection_active"),
            voiceConnectionSince = root.optString("voice_connection_since")
                .takeIf(String::isNotBlank)?.let(Instant::parse),
            tokenRequired = root.getBoolean("token_required"),
        )
    }

    fun hello(responsePacing: VoiceResponsePacing = VoiceResponsePacing.NORMAL): String = JSONObject()
        .put("type", "hello")
        .put("protocol", VOICE_PROTOCOL)
		.put("response_pacing", responsePacing.wireValue)
        .toString()

	fun ping(): String = JSONObject()
		.put("type", "ping")
		.put("protocol", VOICE_PROTOCOL)
		.toString()

	fun history(beforeId: String, limit: Int = 5): String = JSONObject()
		.put("type", "history")
		.put("protocol", VOICE_PROTOCOL)
		.put("before_id", beforeId)
		.put("limit", limit)
		.toString()

	fun searchHistory(query: String, limit: Int = 20): String = JSONObject()
		.put("type", "search_history")
		.put("protocol", VOICE_PROTOCOL)
		.put("query", query.trim())
		.put("limit", limit)
		.toString()

    fun utterance(id: String, text: String, sessionId: String = ""): String = JSONObject()
        .put("type", "utterance")
        .put("protocol", VOICE_PROTOCOL)
        .put("utterance_id", id)
        .put("text", text)
        .apply { if (sessionId.isNotBlank()) put("session_id", sessionId) }
        .toString()

    fun audioStart(id: String, format: VoiceAudioFormat, languages: Collection<String> = emptyList()): String = JSONObject()
        .put("type", "audio_start")
        .put("protocol", VOICE_PROTOCOL)
        .put("utterance_id", id)
        .put("audio_format", format.toJSON())
		.apply { if (languages.isNotEmpty()) put("languages", JSONArray(languages.sorted())) }
        .toString()

    fun audioCommit(id: String, sessionId: String = ""): String = JSONObject()
        .put("type", "audio_commit")
        .put("protocol", VOICE_PROTOCOL)
        .put("utterance_id", id)
        .apply { if (sessionId.isNotBlank()) put("session_id", sessionId) }
        .toString()

    fun audioCancel(id: String): String = JSONObject()
        .put("type", "audio_cancel")
        .put("protocol", VOICE_PROTOCOL)
        .put("utterance_id", id)
        .toString()

    fun encodeAudio(frame: VoiceAudioFrame): ByteArray {
        require(frame.flags in 0..255) { "Audio flags must fit in one byte" }
        require(frame.sequence in 0..UINT32_MAX) { "Audio sequence must fit in uint32" }
        require(frame.pcm.isNotEmpty() && frame.pcm.size <= MAX_AUDIO_PAYLOAD && frame.pcm.size % 2 == 0) {
            "Audio payload must be non-empty, even-sized PCM16 up to $MAX_AUDIO_PAYLOAD bytes"
        }
        return ByteBuffer.allocate(AUDIO_HEADER_BYTES + frame.pcm.size)
            .order(ByteOrder.BIG_ENDIAN)
            .put(AUDIO_MAGIC)
            .put(frame.kind.wireValue.toByte())
            .put(frame.flags.toByte())
            .putShort(0)
            .putInt(frame.sequence.toInt())
            .put(frame.pcm)
            .array()
    }

    fun decodeAudio(payload: ByteArray): VoiceAudioFrame {
        require(payload.size >= AUDIO_HEADER_BYTES) { "Voice audio frame is shorter than its header" }
        val buffer = ByteBuffer.wrap(payload).order(ByteOrder.BIG_ENDIAN)
        val magic = ByteArray(AUDIO_MAGIC.size).also(buffer::get)
        require(magic.contentEquals(AUDIO_MAGIC)) { "Invalid voice audio frame magic" }
        val kind = VoiceAudioFrameKind.fromWire(buffer.get().toInt() and 0xff)
        val flags = buffer.get().toInt() and 0xff
        require(buffer.short.toInt() == 0) { "Voice audio reserved bytes must be zero" }
        val sequence = buffer.int.toLong() and UINT32_MAX
        val pcm = ByteArray(buffer.remaining()).also(buffer::get)
        require(pcm.isNotEmpty() && pcm.size <= MAX_AUDIO_PAYLOAD && pcm.size % 2 == 0) {
            "Invalid voice PCM16 payload size ${pcm.size}"
        }
        return VoiceAudioFrame(kind, flags, sequence, pcm)
    }

    fun selectSession(sessionId: String): String = JSONObject()
        .put("type", "select_session")
        .put("protocol", VOICE_PROTOCOL)
        .put("session_id", sessionId)
        .toString()

    fun selectVoiceSession(sessionId: String): String = JSONObject()
        .put("type", "select_voice_session")
        .put("protocol", VOICE_PROTOCOL)
        .put("voice_session_id", sessionId)
        .toString()

	fun createVoiceSession(title: String): String = JSONObject()
		.put("type", "create_voice_session")
		.put("protocol", VOICE_PROTOCOL)
		.put("title", title.trim())
		.toString()

    fun parse(payload: String): VoiceServerFrame {
        val root = JSONObject(payload)
        val protocol = root.optString("protocol")
        require(protocol == VOICE_PROTOCOL) { "Unsupported voice protocol: $protocol" }
        return VoiceServerFrame(
            type = root.getString("type"),
            protocol = protocol,
            utteranceId = root.optString("utterance_id"),
            state = root.optString("state"),
            callState = root.optJSONObject("call_state")?.toCallState(),
			workingOn = root.optJSONObject("working_on")?.toVoiceSession(),
			audioConfig = root.optJSONObject("audio_config")?.toAudioConfig(),
			audioFormat = root.optJSONObject("audio_format")?.toAudioFormat(),
			transcript = root.optString("transcript"),
			history = root.optJSONArray("history").mapObjects { it.toTranscriptEntry() },
			historyHasMore = root.optBoolean("has_more"),
			searchResults = root.optJSONArray("search_results").mapObjects { result ->
				VoiceTranscriptSearchResult(
					match = result.getJSONObject("match").toTranscriptEntry(),
					context = result.optJSONArray("context").mapObjects { it.toTranscriptEntry() },
				)
			},
            message = root.optJSONObject("message")?.toVoiceMessage(),
            appUpdate = root.optJSONObject("app_update")?.toAppUpdate(),
            error = root.optString("error"),
        )
    }

    fun websocketUrl(server: String): String {
        var value = server.trim().trimEnd('/')
        require(value.isNotBlank()) { "Server address is required" }
        if (!value.contains("://")) value = "http://$value"
        val uri = URI(value)
        val scheme = when (uri.scheme.lowercase()) {
            "http", "ws" -> "ws"
            "https", "wss" -> "wss"
            else -> error("Server must use http, https, ws, or wss")
        }
        val path = when {
            uri.path.isNullOrBlank() || uri.path == "/" -> "/voice/v1"
            uri.path.endsWith("/voice/v1") -> uri.path
            else -> uri.path.trimEnd('/') + "/voice/v1"
        }
        return URI(scheme, uri.userInfo, uri.host, uri.port, path, null, null).toString()
    }

    fun resourceUrl(server: String, resource: String): String {
        require(resource.isNotBlank()) { "Presentation URL is required" }
        val direct = URI(resource)
        if (direct.isAbsolute) {
            require(direct.scheme == "http" || direct.scheme == "https") {
                "Presentation URL must use http or https"
            }
            return direct.toString()
        }
        var value = server.trim()
        if (!value.contains("://")) value = "http://$value"
        val serverUri = URI(value)
        val scheme = when (serverUri.scheme.lowercase()) {
            "http", "ws" -> "http"
            "https", "wss" -> "https"
            else -> error("Server must use http, https, ws, or wss")
        }
        val origin = URI(scheme, null, serverUri.host, serverUri.port, "/", null, null)
        return origin.resolve(direct).toString()
    }

    fun isSameOrigin(server: String, resourceUrl: String): Boolean {
        val base = URI(resourceUrl(server, "/"))
        val resource = URI(resourceUrl)
        fun URI.effectivePort(): Int = if (port >= 0) port else if (scheme == "https") 443 else 80
        return base.scheme.equals(resource.scheme, ignoreCase = true) &&
            base.host.equals(resource.host, ignoreCase = true) &&
            base.effectivePort() == resource.effectivePort()
    }

    private fun JSONObject.toCallState(): VoiceCallState = VoiceCallState(
		voiceSessionId = optString("voice_session_id"),
        activeSessionId = optString("active_session_id"),
		sessions = optJSONArray("sessions").mapObjects { it.toVoiceSession() },
		voiceSessions = optJSONArray("voice_sessions").mapObjects { it.toVoiceSession() },
		history = optJSONArray("history").mapObjects { it.toTranscriptEntry() },
		historyHasMore = optBoolean("history_has_more"),
    )

	private fun JSONObject.toTranscriptEntry() = VoiceTranscriptEntry(
		id = getString("id"),
		role = getString("role"),
		text = getString("text"),
		createdAt = optString("created_at").takeIf(String::isNotBlank)?.let {
			runCatching { Instant.parse(it) }.getOrNull()
		},
	)

	private fun JSONObject.toVoiceSession(): VoiceSession = VoiceSession(
		id = getString("id"),
		title = getString("title"),
		lastMessage = optString("last_message"),
		updatedAt = optString("updated_at").takeIf(String::isNotBlank)?.let {
			runCatching { Instant.parse(it) }.getOrNull()
		},
		archived = optBoolean("archived"),
		pinned = optBoolean("pinned"),
		favorite = optBoolean("favorite"),
		deleted = optBoolean("deleted"),
		resultCount = optLong("result_count").coerceAtLeast(0),
		busy = optBoolean("busy"),
		status = optString("status"),
	)

	private fun VoiceAudioFormat.toJSON(): JSONObject = JSONObject()
		.put("encoding", encoding)
		.put("sample_rate", sampleRate)
		.put("channels", channels)

	private fun JSONObject.toAudioFormat(): VoiceAudioFormat = VoiceAudioFormat(
		encoding = getString("encoding"),
		sampleRate = getInt("sample_rate"),
		channels = getInt("channels"),
	)

	private fun JSONObject.toAudioConfig(): VoiceAudioConfig = VoiceAudioConfig(
		input = getJSONObject("input").toAudioFormat(),
		output = getJSONObject("output").toAudioFormat(),
		maxUtteranceSeconds = getInt("max_utterance_seconds"),
	)

    private fun JSONObject.toVoiceMessage(): VoiceMessage = VoiceMessage(
        spokenText = optString("spoken_text"),
		transcriptId = optString("transcript_id"),
        parts = optJSONArray("parts").mapObjects { item ->
            VoicePart(
				id = item.optString("id"),
                mimeType = item.getString("mime_type"),
				data = if (item.has("data")) item.get("data") else item.optString("text"),
				uri = item.optString("uri", item.optString("url")),
				metadata = mapOf(
					"name" to item.optString("name"),
					"alt" to item.optString("alt"),
				).filterValues { it.isNotBlank() } + item.optJSONObject("metadata")?.toStringMap().orEmpty(),
            )
        },
        delegation = optJSONObject("delegation")?.let { item ->
            VoiceDelegation(
                sessionId = item.optString("session_id"),
                sessionTitle = item.optString("session_title"),
                chatId = item.optString("chat_id"),
                needsAttention = item.optBoolean("needs_attention"),
            )
        },
    )

    private fun <T> JSONArray?.mapObjects(transform: (JSONObject) -> T): List<T> {
        if (this == null) return emptyList()
        return List(length()) { index -> transform(getJSONObject(index)) }
    }

	private fun JSONObject.toStringMap(): Map<String, String> = keys().asSequence()
		.associateWith { key -> optString(key) }

	private val AUDIO_MAGIC = byteArrayOf('K'.code.toByte(), 'V'.code.toByte(), 'A'.code.toByte(), '1'.code.toByte())
	private const val AUDIO_HEADER_BYTES = 12
	private const val MAX_AUDIO_PAYLOAD = 64 * 1024
	private const val UINT32_MAX = 0xffff_ffffL
}

private fun JSONObject.toAppUpdate() = AppUpdate(
    channel = getString("channel"),
    applicationId = getString("application_id"),
    versionCode = getLong("version_code"),
    versionName = getString("version_name"),
    signingCertificateSHA256 = getString("signing_certificate_sha256"),
    apkSHA256 = getString("apk_sha256"),
    apkSize = getLong("apk_size"),
    downloadUri = getString("download_uri"),
)
