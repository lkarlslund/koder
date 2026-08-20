package com.lkarlslund.koder.voice

import com.lkarlslund.koder.phone.PhoneIdentity
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
	private val identity: PhoneIdentity? = null,
) : AutoCloseable {
	private sealed class PendingTurn(open val id: String) {
		var transcriptReceived = false
		var messageReceived = false
		var nextOutputSequence = 0L
	}

	private class PendingText(
		override val id: String,
		val text: String,
		val sessionId: String,
	) : PendingTurn(id)

	private class PendingAudio(
		override val id: String,
		val format: VoiceAudioFormat,
		val languages: List<String>,
		val maxBufferedPCMBytes: Int,
	) : PendingTurn(id) {
		val frames = mutableListOf<ByteString>()
		var bufferedPCMBytes = 0
		var committed = false
		var sessionId = ""
	}

    interface Listener {
        fun onConnected()
		fun onCallIdentity(callId: String) = Unit
        fun onFrame(frame: VoiceServerFrame)
		fun onAudioFrame(frame: VoiceAudioFrame) = Unit
		fun onRoundTripMillis(milliseconds: Long) = Unit
        fun onDisconnected(reason: String)
    }

    private var socket: WebSocket? = null
    private var token = ""
    private var server = ""
	private var voiceSessionId = ""
	private var sessionId = ""
	private var chatId = ""
	private var callId = ""
	private var responsePacing = VoiceResponsePacing.NORMAL
	private var desired = false
	private var generation = 0L
	private var reconnectAttempt = 0
	private var socketAttempt = 0L
	private var replayedSocketAttempt = -1L
	private var pendingTurn: PendingTurn? = null
	private var activeOutputUtteranceId = ""
	private var pendingPingAtNanos: Long? = null
	private var reconnect: ScheduledFuture<*>? = null
	private val scheduler = Executors.newSingleThreadScheduledExecutor { runnable ->
		Thread(runnable, "koder-voice-reconnect").apply { isDaemon = true }
	}

	@Synchronized
	fun connect(server: String, bearerToken: String, sessionId: String = "", chatId: String = "", responsePacing: VoiceResponsePacing = VoiceResponsePacing.NORMAL) {
		stopSocket("new call")
        token = bearerToken.trim()
        this.server = server.trim()
		this.sessionId = sessionId.trim()
		this.chatId = chatId.trim()
		this.voiceSessionId = ""
		this.responsePacing = responsePacing
		callId = UUID.randomUUID().toString()
		pendingTurn = null
		activeOutputUtteranceId = ""
		desired = true
		reconnectAttempt = 0
		generation++
		openSocket(generation)
	}

	@Synchronized
	private fun openSocket(expectedGeneration: Long) {
		if (!desired || expectedGeneration != generation) return
		val expectedSocketAttempt = ++socketAttempt
		val websocketURL = VoiceProtocol.websocketUrl(server)
		val requestURL = when {
			websocketURL.startsWith("ws://") -> "http://" + websocketURL.removePrefix("ws://")
			websocketURL.startsWith("wss://") -> "https://" + websocketURL.removePrefix("wss://")
			else -> websocketURL
		}
		val url = requestURL.toHttpUrl().newBuilder()
			.addQueryParameter("call_id", callId)
			.apply {
				if (sessionId.isNotBlank()) addQueryParameter("session_id", sessionId)
				if (chatId.isNotBlank()) addQueryParameter("chat_id", chatId)
				if (sessionId.isBlank() && voiceSessionId.isNotBlank()) addQueryParameter("voice_session_id", voiceSessionId)
			}
			.apply {
				pendingTurn?.let { turn ->
					addQueryParameter("resume_utterance_id", turn.id)
					if (turn.transcriptReceived) addQueryParameter("resume_transcript", "true")
					if (turn.messageReceived) addQueryParameter("resume_message", "true")
					if (turn.nextOutputSequence > 0) addQueryParameter("resume_output_sequence", turn.nextOutputSequence.toString())
				}
			}
			.build()
		val request = Request.Builder().also { builder -> identity?.applyTo(builder) }
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
				webSocket.send(VoiceProtocol.hello(responsePacing))
                listener.onCallIdentity(callId)
                listener.onConnected()
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
				try {
					val frame = VoiceProtocol.parse(text)
					val roundTrip = synchronized(this@VoiceConnection) {
						observe(frame)
						if (frame.type == "ready") replayPending(webSocket, expectedSocketAttempt)
						if (frame.type == "pong") pendingPingAtNanos?.let { started ->
							pendingPingAtNanos = null
							((System.nanoTime() - started).coerceAtLeast(0) / 1_000_000)
						} else null
					}
					listener.onFrame(frame)
					roundTrip?.let(listener::onRoundTripMillis)
                } catch (error: Exception) {
                    listener.onDisconnected("Invalid server response: ${error.message}")
                }
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
				try {
					val frame = VoiceProtocol.decodeAudio(bytes.toByteArray())
					val deliver = synchronized(this@VoiceConnection) { observe(frame) }
					if (deliver) listener.onAudioFrame(frame)
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
		pendingPingAtNanos = null
		listener.onDisconnected(reason)
		if (!desired) return
		val delay = (500L shl reconnectAttempt.coerceAtMost(4)).coerceAtMost(8_000L)
		reconnectAttempt++
		reconnect?.cancel(false)
		reconnect = scheduler.schedule({ openSocket(expectedGeneration) }, delay, TimeUnit.MILLISECONDS)
	}

	@Synchronized
	fun sendPing() {
		if (pendingPingAtNanos != null) return
		val current = socket ?: return
		pendingPingAtNanos = System.nanoTime()
		if (!current.send(VoiceProtocol.ping())) pendingPingAtNanos = null
	}

    fun sendUtterance(text: String, sessionId: String = ""): String {
        val id = UUID.randomUUID().toString()
		synchronized(this) {
			check(desired) { "Voice conversation is not active" }
			pendingTurn = PendingText(id, text, sessionId)
			socket?.send(VoiceProtocol.utterance(id, text, sessionId))
		}
        return id
    }

	fun startAudio(
		format: VoiceAudioFormat,
		languages: Collection<String> = emptyList(),
		maxUtteranceSeconds: Int = MAX_BUFFERED_UTTERANCE_SECONDS,
	): String {
		val id = UUID.randomUUID().toString()
		synchronized(this) {
			check(desired) { "Voice conversation is not active" }
			val pending = PendingAudio(
				id, format, languages.map(String::lowercase).distinct().sorted(),
				maxBufferedAudioBytes(format, maxUtteranceSeconds),
			)
			pendingTurn = pending
			socket?.send(VoiceProtocol.audioStart(id, format, pending.languages))
		}
		return id
	}

	fun sendAudio(sequence: Long, pcm: ByteArray) {
		val payload = VoiceProtocol.encodeAudio(
			VoiceAudioFrame(VoiceAudioFrameKind.INPUT_PCM, 0, sequence, pcm),
		)
		val encoded = ByteString.of(*payload)
		synchronized(this) {
			val pending = pendingTurn as? PendingAudio ?: error("No audio utterance is active")
			check(!pending.committed) { "Audio utterance is already committed" }
			check(sequence == pending.frames.size.toLong()) { "Audio sequence $sequence is out of order" }
			check(pending.bufferedPCMBytes + pcm.size <= pending.maxBufferedPCMBytes) {
				"Buffered speech exceeds the negotiated utterance duration"
			}
			pending.frames += encoded
			pending.bufferedPCMBytes += pcm.size
			socket?.send(encoded)
		}
	}

	fun commitAudio(utteranceId: String, sessionId: String = "") {
		synchronized(this) {
			val pending = pendingTurn as? PendingAudio
			check(pending?.id == utteranceId) { "Audio utterance is not active" }
			pending.committed = true
			pending.sessionId = sessionId
			socket?.send(VoiceProtocol.audioCommit(utteranceId, sessionId))
		}
	}

	fun cancelAudio(utteranceId: String) {
		if (utteranceId.isBlank()) return
		synchronized(this) {
			if (pendingTurn?.id == utteranceId) pendingTurn = null
			socket?.send(VoiceProtocol.audioCancel(utteranceId))
		}
	}

	private fun observe(frame: VoiceServerFrame) {
		if (frame.type == "tts_start") activeOutputUtteranceId = frame.utteranceId
		val pending = pendingTurn ?: return
		if (frame.utteranceId != pending.id) return
		when (frame.type) {
			"transcript" -> pending.transcriptReceived = true
			"message" -> {
				pending.messageReceived = true
				if (frame.message?.spokenText.isNullOrBlank()) pendingTurn = null
			}
			"tts_end", "error" -> {
				pendingTurn = null
				activeOutputUtteranceId = ""
			}
		}
	}

	private fun observe(frame: VoiceAudioFrame): Boolean {
		val pending = pendingTurn ?: return true
		if (activeOutputUtteranceId != pending.id || frame.kind != VoiceAudioFrameKind.OUTPUT_PCM) return true
		if (frame.sequence < pending.nextOutputSequence) return false
		if (frame.sequence == pending.nextOutputSequence) pending.nextOutputSequence++
		return true
	}

	private fun replayPending(webSocket: WebSocket, expectedSocketAttempt: Long) {
		if (replayedSocketAttempt == expectedSocketAttempt || socket !== webSocket) return
		val pending = pendingTurn ?: return
		replayedSocketAttempt = expectedSocketAttempt
		when (pending) {
			is PendingText -> webSocket.send(VoiceProtocol.utterance(pending.id, pending.text, pending.sessionId))
			is PendingAudio -> {
				webSocket.send(VoiceProtocol.audioStart(pending.id, pending.format, pending.languages))
				pending.frames.forEach(webSocket::send)
				if (pending.committed) webSocket.send(VoiceProtocol.audioCommit(pending.id, pending.sessionId))
			}
		}
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

	fun requestHistory(beforeId: String, limit: Int = 5) {
		require(beforeId.isNotBlank()) { "History cursor is required" }
		check(socket?.send(VoiceProtocol.history(beforeId, limit)) == true) {
			"Voice connection is not open"
		}
	}

	fun searchHistory(query: String, limit: Int = 20) {
		require(query.isNotBlank()) { "Transcript search query is required" }
		check(socket?.send(VoiceProtocol.searchHistory(query, limit)) == true) {
			"Voice connection is not open"
		}
	}

	@Synchronized
	fun resumeVoiceSession(voiceSessionId: String) {
		if (voiceSessionId.isNotBlank()) this.voiceSessionId = voiceSessionId.trim()
	}

	fun resumeSelection(sessionId: String, chatId: String) {
		if (sessionId.isNotBlank()) this.sessionId = sessionId.trim()
		if (chatId.isNotBlank()) this.chatId = chatId.trim()
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
		pendingTurn = null
		activeOutputUtteranceId = ""
		pendingPingAtNanos = null
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

internal const val MAX_BUFFERED_UTTERANCE_SECONDS = 120

internal fun maxBufferedAudioBytes(format: VoiceAudioFormat, seconds: Int = MAX_BUFFERED_UTTERANCE_SECONDS): Int {
	require(seconds > 0) { "Buffered audio duration must be positive" }
	val bytes = format.sampleRate.toLong() * format.channels.toLong() * 2L * seconds
	require(bytes in 1..Int.MAX_VALUE) { "Audio format is not bufferable" }
	return bytes.toInt()
}
