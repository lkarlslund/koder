package com.lkarlslund.koder.voice

import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper

class CallController(context: Context, private val listener: Listener) : AutoCloseable {
    enum class Stage { DISCONNECTED, CONNECTING, LISTENING, PROCESSING, SPEAKING, HELD, ERROR }

    data class Snapshot(
        val stage: Stage = Stage.DISCONNECTED,
        val detail: String = "Ready",
        val partialTranscript: String = "",
        val sessions: List<VoiceSession> = emptyList(),
        val activeSessionId: String = "",
    )

    interface Listener {
        fun onSnapshot(snapshot: Snapshot)
        fun onUserMessage(text: String)
        fun onAssistantMessage(message: VoiceMessage)
    }

    private val appContext = context.applicationContext
    private val main = Handler(Looper.getMainLooper())
    private val connection = VoiceConnection(object : VoiceConnection.Listener {
        override fun onConnected() = onMain { update(Stage.CONNECTING, "Loading sessions…") }
        override fun onFrame(frame: VoiceServerFrame) = onMain { handleFrame(frame) }
        override fun onDisconnected(reason: String) = onMain {
            if (running) update(Stage.ERROR, reason)
        }
    })
    private lateinit var speech: DeviceSpeech
    private val speechListener = object : DeviceSpeech.Listener {
        override fun onPartial(text: String) = onMain {
            snapshot = snapshot.copy(partialTranscript = text)
            publish()
        }

        override fun onFinal(text: String) = onMain { submit(text) }
        override fun onSpeechError(message: String, recoverable: Boolean) = onMain {
            if (!running || snapshot.stage == Stage.PROCESSING || snapshot.stage == Stage.SPEAKING) return@onMain
            if (recoverable) {
                update(Stage.LISTENING, message, "")
                main.postDelayed({ if (running && snapshot.stage == Stage.LISTENING) speech.start() }, 350)
            } else {
                update(Stage.ERROR, message)
            }
        }
    }
    private val tts = DeviceTts(
        context,
        doneCallback = { onMain { resumeListening() } },
        errorCallback = { message -> onMain { update(Stage.ERROR, message) } },
    )
    private val telecom = TelecomVoiceCall(context, object : TelecomVoiceCall.Listener {
        override fun onCallReady() = onMain { telecomReady = true; maybeListen() }
        override fun onCallHeld(held: Boolean) = onMain {
            if (held) {
                speech.stop()
                tts.stop()
                update(Stage.HELD, "Call on hold")
            } else {
                telecomReady = true
                maybeListen()
            }
        }

        override fun onAudioEndpoint(name: String) = onMain {
            audioEndpoint = name
            if (snapshot.stage == Stage.LISTENING) update(Stage.LISTENING, "Listening · $name")
        }

        override fun onCallEnded() = onMain { if (running) end() }
        override fun onTelecomUnavailable(message: String) = onMain {
            audioEndpoint = "default audio"
            update(Stage.CONNECTING, "$message; using default audio")
        }
    })

    private var snapshot = Snapshot()
    private var running = false
    private var serverReady = false
    private var telecomReady = false
    private var audioEndpoint = "call audio"

    init {
        speech = DeviceSpeech(context, speechListener)
    }

    fun start(server: String, token: String) {
        if (running) end()
        running = true
        serverReady = false
        telecomReady = false
        snapshot = Snapshot(stage = Stage.CONNECTING, detail = "Connecting…")
        publish()
        appContext.startForegroundService(Intent(appContext, VoiceCallService::class.java))
        telecom.start()
        try {
            connection.connect(server, token)
        } catch (error: Exception) {
            update(Stage.ERROR, error.message ?: "Connection failed")
        }
    }

    fun submit(text: String) {
        val normalized = text.trim()
        if (!running || normalized.isEmpty()) return
        speech.stop()
        listener.onUserMessage(normalized)
        update(Stage.PROCESSING, "Koder is working…", "")
        try {
            connection.sendUtterance(normalized, snapshot.activeSessionId)
        } catch (error: Exception) {
            update(Stage.ERROR, error.message ?: "Could not send request")
        }
    }

    fun selectSession(sessionId: String) {
        if (!running) return
        try {
            connection.selectSession(sessionId)
        } catch (error: Exception) {
            update(Stage.ERROR, error.message ?: "Could not select chat")
        }
    }

    fun loadBytes(url: String, callback: (ByteArray?, String?) -> Unit) =
        connection.loadBytes(url, callback)

    fun end() {
        if (!running) return
        running = false
        speech.stop()
        tts.stop()
        connection.close()
        telecom.disconnect()
        appContext.stopService(Intent(appContext, VoiceCallService::class.java))
        snapshot = Snapshot(stage = Stage.DISCONNECTED, detail = "Call ended")
        publish()
    }

    override fun close() {
        end()
        telecom.close()
        speech.close()
        tts.close()
        connection.close()
    }

    private fun handleFrame(frame: VoiceServerFrame) {
        frame.callState?.let { state ->
            snapshot = snapshot.copy(sessions = state.sessions, activeSessionId = state.activeSessionId)
        }
        when (frame.type) {
            "ready" -> {
                serverReady = true
                maybeListen()
            }
            "state" -> if (frame.state == "processing") update(Stage.PROCESSING, "Koder is working…", "")
            "message" -> frame.message?.let { message ->
                listener.onAssistantMessage(message)
                if (message.spokenText.isBlank()) {
                    resumeListening()
                } else {
                    update(Stage.SPEAKING, "Koder is speaking…", "")
                    tts.speak(message.spokenText)
                }
            }
            "error" -> update(Stage.ERROR, frame.error.ifBlank { "Voice request failed" })
        }
        publish()
    }

    private fun maybeListen() {
        if (!running || !serverReady || !telecomReady || snapshot.stage == Stage.PROCESSING || snapshot.stage == Stage.SPEAKING) return
        resumeListening()
    }

    private fun resumeListening() {
        if (!running || !serverReady || !telecomReady) return
        update(Stage.LISTENING, "Listening · $audioEndpoint", "")
        speech.start()
    }

    private fun update(stage: Stage, detail: String, partial: String = snapshot.partialTranscript) {
        snapshot = snapshot.copy(stage = stage, detail = detail, partialTranscript = partial)
        publish()
    }

    private fun publish() = listener.onSnapshot(snapshot)
    private fun onMain(action: () -> Unit) {
        if (Looper.myLooper() == Looper.getMainLooper()) action() else main.post(action)
    }
}
