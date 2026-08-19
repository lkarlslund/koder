package com.lkarlslund.koder.voice

import okhttp3.Call
import okhttp3.Callback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import java.io.IOException
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.TimeUnit
import okhttp3.HttpUrl.Companion.toHttpUrl

class VoiceConnection(
    private val listener: Listener,
    private val client: OkHttpClient = OkHttpClient.Builder()
        .pingInterval(20, TimeUnit.SECONDS)
        .retryOnConnectionFailure(true)
        .build(),
) : AutoCloseable {
    interface Listener {
        fun onConnected()
		fun onCallIdentity(callId: String) = Unit
        fun onFrame(frame: VoiceServerFrame)
		fun onAudioFrame(frame: VoiceAudioFrame) = Unit
        fun onDisconnected(reason: String)
    }

    private var socket: WebSocket? = null
    private var token = ""
    private var server = ""
	private var voiceSessionId = ""
	private var callId = ""
	private var desired = false
	private var generation = 0L
	private var reconnectAttempt = 0
	private var reconnect: ScheduledFuture<*>? = null
	private val scheduler = Executors.newSingleThreadScheduledExecutor { runnable ->
		Thread(runnable, "koder-voice-reconnect").apply { isDaemon = true }
	}

	@Synchronized
	fun connect(server: String, bearerToken: String, voiceSessionId: String = "") {
		stopSocket("new call")
        token = bearerToken.trim()
        this.server = server.trim()
        this.voiceSessionId = voiceSessionId.trim()
		callId = UUID.randomUUID().toString()
		desired = true
		reconnectAttempt = 0
		generation++
		openSocket(generation)
	}

	@Synchronized
	private fun openSocket(expectedGeneration: Long) {
		if (!desired || expectedGeneration != generation) return
		val websocketURL = VoiceProtocol.websocketUrl(server)
		val requestURL = when {
			websocketURL.startsWith("ws://") -> "http://" + websocketURL.removePrefix("ws://")
			websocketURL.startsWith("wss://") -> "https://" + websocketURL.removePrefix("wss://")
			else -> websocketURL
		}
		val url = requestURL.toHttpUrl().newBuilder()
			.addQueryParameter("call_id", callId)
			.apply { if (voiceSessionId.isNotBlank()) addQueryParameter("voice_session_id", voiceSessionId) }
			.build()
		val request = Request.Builder()
			.url(url)
            .apply { if (token.isNotBlank()) header("Authorization", "Bearer $token") }
            .build()
		val newSocket = client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
				synchronized(this@VoiceConnection) {
					if (!desired || generation != expectedGeneration || socket !== webSocket) {
						webSocket.close(1000, "superseded")
						return
					}
					reconnectAttempt = 0
				}
                webSocket.send(VoiceProtocol.hello())
                listener.onCallIdentity(callId)
                listener.onConnected()
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                try {
                    listener.onFrame(VoiceProtocol.parse(text))
                } catch (error: Exception) {
                    listener.onDisconnected("Invalid server response: ${error.message}")
                }
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
				try {
					listener.onAudioFrame(VoiceProtocol.decodeAudio(bytes.toByteArray()))
				} catch (error: Exception) {
					listener.onDisconnected("Invalid audio frame: ${error.message}")
				}
            }

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                webSocket.close(code, reason)
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
				handleDisconnect(webSocket, expectedGeneration, reason.ifBlank { "Connection closed" })
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
				val suffix = response?.let { " (HTTP ${it.code})" }.orEmpty()
				handleDisconnect(webSocket, expectedGeneration, (t.message ?: "Connection failed") + suffix)
            }
        })
		socket = newSocket
    }

	@Synchronized
	private fun handleDisconnect(webSocket: WebSocket, expectedGeneration: Long, reason: String) {
		if (socket !== webSocket || generation != expectedGeneration) return
		socket = null
		listener.onDisconnected(reason)
		if (!desired) return
		val delay = (500L shl reconnectAttempt.coerceAtMost(4)).coerceAtMost(8_000L)
		reconnectAttempt++
		reconnect?.cancel(false)
		reconnect = scheduler.schedule({ openSocket(expectedGeneration) }, delay, TimeUnit.MILLISECONDS)
	}

    fun sendUtterance(text: String, sessionId: String = ""): String {
        val id = UUID.randomUUID().toString()
        check(socket?.send(VoiceProtocol.utterance(id, text, sessionId)) == true) {
            "Voice connection is not open"
        }
        return id
    }

	fun startAudio(format: VoiceAudioFormat, languages: Collection<String> = emptyList()): String {
		val id = UUID.randomUUID().toString()
		check(socket?.send(VoiceProtocol.audioStart(id, format, languages)) == true) {
			"Voice connection is not open"
		}
		return id
	}

	fun sendAudio(sequence: Long, pcm: ByteArray) {
		val payload = VoiceProtocol.encodeAudio(
			VoiceAudioFrame(VoiceAudioFrameKind.INPUT_PCM, 0, sequence, pcm),
		)
		check(socket?.send(ByteString.of(*payload)) == true) { "Voice connection is not open" }
	}

	fun commitAudio(utteranceId: String, sessionId: String = "") {
		check(socket?.send(VoiceProtocol.audioCommit(utteranceId, sessionId)) == true) {
			"Voice connection is not open"
		}
	}

	fun cancelAudio(utteranceId: String) {
		if (utteranceId.isNotBlank()) socket?.send(VoiceProtocol.audioCancel(utteranceId))
	}

    fun selectSession(sessionId: String) {
        check(socket?.send(VoiceProtocol.selectSession(sessionId)) == true) {
            "Voice connection is not open"
        }
    }

    fun selectVoiceSession(sessionId: String) {
        require(sessionId.isNotBlank()) { "Voice chat id is required" }
        check(socket?.send(VoiceProtocol.selectVoiceSession(sessionId)) == true) {
            "Voice connection is not open"
        }
    }

	fun createVoiceSession(title: String) {
		check(socket?.send(VoiceProtocol.createVoiceSession(title)) == true) {
			"Voice connection is not open"
		}
	}

	@Synchronized
	fun resumeVoiceSession(voiceSessionId: String) {
		if (voiceSessionId.isNotBlank()) this.voiceSessionId = voiceSessionId.trim()
	}

    fun loadBytes(url: String, callback: (ByteArray?, String?) -> Unit) {
        val resolved = try {
            VoiceProtocol.resourceUrl(server, url)
        } catch (error: Exception) {
            callback(null, error.message ?: "Invalid presentation URL")
            return
        }
        val request = Request.Builder().url(resolved)
            .apply {
                if (token.isNotBlank() && VoiceProtocol.isSameOrigin(server, resolved)) {
                    header("Authorization", "Bearer $token")
                }
            }.build()
        client.newCall(request).enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                callback(null, e.message ?: "Image download failed")
            }

            override fun onResponse(call: Call, response: Response) {
                response.use {
                    if (!it.isSuccessful) {
                        callback(null, "Image download returned HTTP ${it.code}")
                    } else if (it.body.contentLength() > MAX_PART_BYTES) {
                        callback(null, "Presentation is too large to display")
                    } else {
                        callback(it.body.bytes(), null)
                    }
                }
            }
        })
    }

	@Synchronized
	override fun close() {
		desired = false
		generation++
		reconnect?.cancel(false)
		reconnect = null
		stopSocket("call ended")
	}

	@Synchronized
	private fun stopSocket(reason: String) {
		socket?.close(1000, reason)
		socket = null
    }

    private companion object {
        const val MAX_PART_BYTES = 10L * 1024 * 1024
    }
}
