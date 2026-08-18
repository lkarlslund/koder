package com.lkarlslund.koder.voice

import org.json.JSONArray
import org.json.JSONObject
import java.net.URI

const val VOICE_PROTOCOL = "voice.v1"

data class VoiceSession(
    val id: String,
    val title: String,
    val lastMessage: String = "",
)

data class VoicePart(
    val mimeType: String,
    val text: String = "",
    val url: String = "",
    val name: String = "",
    val alt: String = "",
)

data class VoiceDelegation(
    val sessionId: String,
    val sessionTitle: String,
    val chatId: String,
    val needsAttention: Boolean,
)

data class VoiceMessage(
    val spokenText: String,
    val parts: List<VoicePart>,
    val delegation: VoiceDelegation?,
)

data class VoiceCallState(
    val activeSessionId: String,
    val sessions: List<VoiceSession>,
)

data class VoiceServerFrame(
    val type: String,
    val protocol: String,
    val utteranceId: String = "",
    val state: String = "",
    val callState: VoiceCallState? = null,
    val message: VoiceMessage? = null,
    val error: String = "",
)

object VoiceProtocol {
    fun hello(): String = JSONObject()
        .put("type", "hello")
        .put("protocol", VOICE_PROTOCOL)
        .toString()

    fun utterance(id: String, text: String, sessionId: String = ""): String = JSONObject()
        .put("type", "utterance")
        .put("protocol", VOICE_PROTOCOL)
        .put("utterance_id", id)
        .put("text", text)
        .apply { if (sessionId.isNotBlank()) put("session_id", sessionId) }
        .toString()

    fun selectSession(sessionId: String): String = JSONObject()
        .put("type", "select_session")
        .put("protocol", VOICE_PROTOCOL)
        .put("session_id", sessionId)
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
            message = root.optJSONObject("message")?.toVoiceMessage(),
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
        activeSessionId = optString("active_session_id"),
        sessions = optJSONArray("sessions").mapObjects { item ->
            VoiceSession(
                id = item.getString("id"),
                title = item.getString("title"),
                lastMessage = item.optString("last_message"),
            )
        },
    )

    private fun JSONObject.toVoiceMessage(): VoiceMessage = VoiceMessage(
        spokenText = optString("spoken_text"),
        parts = optJSONArray("parts").mapObjects { item ->
            VoicePart(
                mimeType = item.getString("mime_type"),
                text = item.optString("text"),
                url = item.optString("url"),
                name = item.optString("name"),
                alt = item.optString("alt"),
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
}
