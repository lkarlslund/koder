package com.lkarlslund.koder.voice

import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import java.nio.ByteBuffer
import java.nio.ByteOrder

class CallController(
    context: Context,
    private val listener: Listener,
    private val microphone: MicrophoneCapture = AndroidMicrophoneCapture(),
	detector: VoiceActivityDetector = SileroVad.fromAssets(context.applicationContext),
	audioPlayback: StreamingAudioPlayback? = null,
	private val workingSound: WorkingSound = AndroidWorkingSound(),
) : AutoCloseable {
    enum class Stage { DISCONNECTED, CONNECTING, LISTENING, RECORDING, TRANSCRIBING, PROCESSING, WORKING, SPEAKING, HELD, ERROR }

    data class Snapshot(
        val stage: Stage = Stage.DISCONNECTED,
        val detail: String = "Ready",
        val partialTranscript: String = "",
        val sessions: List<VoiceSession> = emptyList(),
        val voiceSessions: List<VoiceSession> = emptyList(),
        val activeSessionId: String = "",
        val voiceSessionId: String = "",
		val history: List<VoiceTranscriptEntry> = emptyList(),
        val appUpdate: AppUpdate? = null,
    )

    interface Listener {
        fun onSnapshot(snapshot: Snapshot)
        fun onUserMessage(text: String)
        fun onAssistantMessage(message: VoiceMessage)
    }

    private val appContext = context.applicationContext
    private val main = Handler(Looper.getMainLooper())
	private val playback = audioPlayback ?: AndroidStreamingAudioPlayback { message ->
		onMain { update(Stage.ERROR, message) }
	}
	private val endpointSampleRate = detector.sampleRate
	private val endpointFrameSamples = detector.frameSamples
    private val endpoint = VadEndpointPipeline(detector)
    private val connection = VoiceConnection(object : VoiceConnection.Listener {
        override fun onConnected() = onMain { update(Stage.CONNECTING, "Loading conversation…") }
        override fun onFrame(frame: VoiceServerFrame) = onMain { handleFrame(frame) }
        override fun onAudioFrame(frame: VoiceAudioFrame) = handleOutputAudio(frame)
        override fun onDisconnected(reason: String) = onMain {
            microphone.stop()
            if (running) update(Stage.CONNECTING, "Reconnecting · $reason")
        }
    })
    private val telecom = TelecomVoiceCall(context, object : TelecomVoiceCall.Listener {
        override fun onCallReady() = onMain { telecomReady = true; maybeListen() }
        override fun onCallHeld(held: Boolean) = onMain {
            if (held) {
                microphone.stop()
                playback.stop()
                update(Stage.HELD, "Conversation on hold")
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
            audioEndpoint = "phone audio"
            telecomReady = true
            update(Stage.CONNECTING, "$message; using phone audio")
            maybeListen()
        }
    })

	@Volatile private var snapshot = Snapshot()
    @Volatile private var running = false
    private var serverReady = false
    private var telecomReady = false
    private var audioEndpoint = "phone audio"
    private var audioConfig: VoiceAudioConfig? = null
    private var utteranceId = ""
    private var inputSequence = 0L
    private var outputSequence = 0L
    @Volatile private var acceptingOutput = false

    fun start(server: String, token: String, voiceSessionId: String = "") {
        if (running) end()
        running = true
        serverReady = false
        telecomReady = false
        audioConfig = null
        snapshot = Snapshot(stage = Stage.CONNECTING, detail = "Connecting…", voiceSessionId = voiceSessionId)
        publish()
        appContext.startForegroundService(Intent(appContext, VoiceCallService::class.java))
        telecom.start()
        try {
            connection.connect(server, token, voiceSessionId)
        } catch (error: Exception) {
            update(Stage.ERROR, error.message ?: "Connection failed")
        }
    }

    fun submit(text: String) {
        val normalized = text.trim()
        if (!running || normalized.isEmpty()) return
        microphone.stop()
        listener.onUserMessage(normalized)
        update(Stage.PROCESSING, "Understanding…", "")
        try {
            connection.sendUtterance(normalized)
        } catch (error: Exception) {
            update(Stage.ERROR, error.message ?: "Could not send request")
        }
    }

    fun selectVoiceSession(sessionId: String) {
        if (!running || sessionId.isBlank() || sessionId == snapshot.voiceSessionId) return
        cancelCapture()
        try {
            connection.selectVoiceSession(sessionId)
            update(Stage.PROCESSING, "Switching conversation…", "")
        } catch (error: Exception) {
            update(Stage.ERROR, error.message ?: "Could not switch conversation")
        }
    }

	fun createVoiceSession(title: String) {
		if (!running) return
		cancelCapture()
		try {
			connection.createVoiceSession(title)
			update(Stage.PROCESSING, "Creating conversation…", "")
		} catch (error: Exception) {
			update(Stage.ERROR, error.message ?: "Could not create conversation")
		}
	}

    fun loadBytes(url: String, callback: (ByteArray?, String?) -> Unit) = connection.loadBytes(url, callback)

    private fun cancelCapture() {
        microphone.stop()
        endpoint.reset()
        val pending = utteranceId
        utteranceId = ""
        if (pending.isNotBlank()) connection.cancelAudio(pending)
    }

    fun end() {
        if (!running) return
        running = false
        serverReady = false
        microphone.stop()
        playback.stop()
		workingSound.stop()
        endpoint.reset()
        connection.close()
        telecom.disconnect()
        appContext.stopService(Intent(appContext, VoiceCallService::class.java))
        snapshot = Snapshot(stage = Stage.DISCONNECTED, detail = "Conversation paused")
        publish()
    }

    override fun close() {
        end()
        telecom.close()
        microphone.close()
        playback.close()
		workingSound.close()
        endpoint.close()
        connection.close()
    }

    private fun handleFrame(frame: VoiceServerFrame) {
        frame.callState?.let { state ->
            snapshot = snapshot.copy(
                sessions = state.sessions,
                voiceSessions = state.voiceSessions,
                activeSessionId = state.activeSessionId,
                voiceSessionId = state.voiceSessionId,
				history = state.history,
            )
			connection.resumeVoiceSession(state.voiceSessionId)
        }
        frame.audioConfig?.let { audioConfig = it }
        when (frame.type) {
            "ready" -> {
                snapshot = snapshot.copy(appUpdate = frame.appUpdate)
                serverReady = true
                maybeListen()
            }
            "state" -> when (frame.state) {
                "recording" -> update(Stage.RECORDING, "Listening to you…")
                "transcribing" -> update(Stage.TRANSCRIBING, "Recognizing speech…", "")
                "processing" -> update(Stage.PROCESSING, "Choosing a chat…", "")
				"working" -> update(
					Stage.WORKING,
					frame.workingOn?.title?.takeIf(String::isNotBlank)?.let { "Working in $it…" } ?: "Working…",
					"",
				)
                "speaking" -> update(Stage.SPEAKING, "Koder is speaking…", "")
            }
            "transcript" -> if (frame.transcript.isNotBlank()) listener.onUserMessage(frame.transcript)
            "message" -> frame.message?.let(listener::onAssistantMessage)
            "tts_start" -> {
                val format = frame.audioFormat ?: audioConfig?.output
                if (format == null) {
                    update(Stage.ERROR, "Server omitted the speech audio format")
                } else {
                    outputSequence = 0
                    acceptingOutput = true
                    playback.start(format)
                    update(Stage.SPEAKING, "Koder is speaking…", "")
					audioConfig?.input?.let { startMicrophone(it, showListeningState = false) }
                }
            }
            "tts_end" -> {
				if (acceptingOutput) {
					acceptingOutput = false
					playback.finish { onMain { maybeListen(force = true) } }
				}
            }
            "error" -> {
                acceptingOutput = false
                playback.stop()
                update(Stage.ERROR, frame.error.ifBlank { "Voice request failed" })
            }
        }
        publish()
    }

    @Synchronized
    private fun handleOutputAudio(frame: VoiceAudioFrame) {
        if (!running || !acceptingOutput) return
        if (frame.kind != VoiceAudioFrameKind.OUTPUT_PCM || frame.sequence != outputSequence) {
            acceptingOutput = false
            playback.stop()
            onMain { update(Stage.ERROR, "Speech audio arrived out of order") }
            return
        }
        outputSequence++
        playback.write(frame.pcm)
    }

    private fun maybeListen(force: Boolean = false) {
        if (!running || !serverReady || !telecomReady || audioConfig == null) return
        if (!force && snapshot.stage in setOf(Stage.RECORDING, Stage.TRANSCRIBING, Stage.PROCESSING, Stage.WORKING, Stage.SPEAKING, Stage.HELD)) return
        resumeListening()
    }

    private fun resumeListening() {
        val format = audioConfig?.input ?: return
		startMicrophone(format, showListeningState = true)
	}

	private fun startMicrophone(format: VoiceAudioFormat, showListeningState: Boolean) {
        if (format.sampleRate != endpointSampleRate || format.channels != 1 || format.encoding != "pcm_s16le") {
            update(Stage.ERROR, "Server microphone format is not supported by on-device VAD")
            return
        }
        endpoint.reset()
        utteranceId = ""
        inputSequence = 0
		if (showListeningState) update(Stage.LISTENING, "Listening · $audioEndpoint", "")
        try {
            microphone.start(format, endpointFrameSamples, object : MicrophoneCapture.Listener {
                override fun onFrame(samples: ShortArray) = processMicrophoneFrame(format, samples)
                override fun onCaptureError(message: String) = onMain { update(Stage.ERROR, message) }
            })
        } catch (error: Exception) {
            update(Stage.ERROR, error.message ?: "Could not start microphone")
        }
    }

    @Synchronized
    private fun processMicrophoneFrame(format: VoiceAudioFormat, samples: ShortArray) {
        if (!running) return
        try {
            for (event in endpoint.accept(samples)) {
                when (event) {
                    is UtteranceEvent.Started -> {
						if (acceptingOutput) {
							acceptingOutput = false
							playback.stop()
						}
                        utteranceId = connection.startAudio(format)
                        inputSequence = 0
                        event.frames.forEach(::sendPCM)
                        onMain { update(Stage.RECORDING, "Listening to you…") }
                    }
                    is UtteranceEvent.Continued -> sendPCM(event.frame)
                    is UtteranceEvent.Committed -> {
                        val completedId = utteranceId
                        utteranceId = ""
                        microphone.stop()
                        connection.commitAudio(completedId)
                        onMain { update(Stage.TRANSCRIBING, "Recognizing speech…", "") }
                    }
                }
            }
        } catch (error: Exception) {
            val failedId = utteranceId
            utteranceId = ""
            microphone.stop()
            connection.cancelAudio(failedId)
            onMain { update(Stage.ERROR, error.message ?: "Voice capture failed") }
        }
    }

    private fun sendPCM(samples: ShortArray) {
        val pcm = ByteBuffer.allocate(samples.size * 2).order(ByteOrder.LITTLE_ENDIAN)
        samples.forEach(pcm::putShort)
        connection.sendAudio(inputSequence++, pcm.array())
    }

    private fun update(stage: Stage, detail: String, partial: String = snapshot.partialTranscript) {
        if (stage == Stage.WORKING) {
            workingSound.start()
        } else {
            workingSound.stop()
        }
        snapshot = snapshot.copy(stage = stage, detail = detail, partialTranscript = partial)
        publish()
    }

    private fun publish() = listener.onSnapshot(snapshot)
    private fun onMain(action: () -> Unit) {
        if (Looper.myLooper() == Looper.getMainLooper()) action() else main.post(action)
    }
}
