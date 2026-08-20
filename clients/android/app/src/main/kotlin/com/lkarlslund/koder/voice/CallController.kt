package com.lkarlslund.koder.voice

import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.app.Activity
import com.lkarlslund.koder.phone.AndroidPhoneToolProvider
import com.lkarlslund.koder.phone.PhoneDeviceConnection
import com.lkarlslund.koder.phone.PhoneIdentity
import com.lkarlslund.koder.phone.PhoneToolProvider
import java.nio.ByteBuffer
import java.nio.ByteOrder

class CallController(
    context: Context,
    private val listener: Listener,
    private val microphone: MicrophoneCapture = AndroidMicrophoneCapture(),
	detector: VoiceActivityDetector = SileroVad.fromAssets(context.applicationContext),
	audioPlayback: StreamingAudioPlayback? = null,
	private val workingSound: WorkingSound = AndroidWorkingSound(),
	private val haptics: VoiceHaptics = AndroidVoiceHaptics(context),
	private val interruptionFeedback: InterruptionFeedback = AndroidInterruptionFeedback(context, haptics),
	private val phoneTools: PhoneToolProvider = AndroidPhoneToolProvider(context as Activity),
	private val phoneIdentity: PhoneIdentity? = null,
	private val onBuiltInAudioRouteSelected: (BuiltInAudioRoute) -> Unit = {},
) : AutoCloseable, VoiceCallControlTarget {
	    enum class Stage { DISCONNECTED, CONNECTING, LISTENING, RECORDING, TRANSCRIBING, PROCESSING, WORKING, SPEAKING, MUTED, HELD, ERROR }

    data class Snapshot(
        val stage: Stage = Stage.DISCONNECTED,
        val detail: String = "Ready",
        val partialTranscript: String = "",
        val sessions: List<VoiceSession> = emptyList(),
        val voiceSessions: List<VoiceSession> = emptyList(),
        val activeSessionId: String = "",
        val voiceSessionId: String = "",
		val history: List<VoiceTranscriptEntry> = emptyList(),
		val historyHasMore: Boolean = false,
		val historyLoading: Boolean = false,
        val appUpdate: AppUpdate? = null,
		val microphoneMuted: Boolean = false,
		val audioEndpointName: String = "",
		val audioEndpoints: List<VoiceAudioEndpoint> = emptyList(),
    )

    interface Listener {
        fun onSnapshot(snapshot: Snapshot)
        fun onUserMessage(text: String)
        fun onAssistantMessage(message: VoiceMessage)
		fun onHistoryPage(entries: List<VoiceTranscriptEntry>) = Unit
		fun onHistorySearch(results: List<VoiceTranscriptSearchResult>, error: String?) = Unit
		fun onAudioLevel(level: Float, user: Boolean) = Unit
    }

    private val appContext = context.applicationContext
	private val phoneDevice = PhoneDeviceConnection(phoneTools, identity = phoneIdentity)
	private var connectedServer = ""
	private var connectedToken = ""
    private val main = Handler(Looper.getMainLooper())
	private val delayedProcessingSound = Runnable {
		if (snapshot.stage == Stage.PROCESSING) workingSound.start()
	}
	private val playback = audioPlayback ?: AndroidStreamingAudioPlayback { message ->
		onMain { update(Stage.ERROR, message) }
	}
	private val endpointSampleRate = detector.sampleRate
	private val endpointFrameSamples = detector.frameSamples
	private val endpoint = VadEndpointPipeline(detector)
	private val diagnostics = AudioDiagnosticsTracker()
	private val connection = VoiceConnection(object : VoiceConnection.Listener {
        override fun onConnected() = onMain { update(Stage.CONNECTING, "Loading conversation…") }
		override fun onCallIdentity(callId: String) {
			phoneDevice.connect(connectedServer, connectedToken, callId)
		}
        override fun onFrame(frame: VoiceServerFrame) = onMain { handleFrame(frame) }
        override fun onAudioFrame(frame: VoiceAudioFrame) = handleOutputAudio(frame)
		override fun onRoundTripMillis(milliseconds: Long) = diagnostics.recordRoundTrip(milliseconds)
        override fun onDisconnected(reason: String) = onMain {
            microphone.stop()
			acceptingOutput = false
			playback.stop()
			diagnostics.recordReconnect()
	            if (running) {
					if (pausedByUser || telecomHeld) update(Stage.HELD, "Conversation paused")
					else update(Stage.CONNECTING, reconnectStatus(reason))
				}
        }
	}, identity = phoneIdentity)
    private val telecom = TelecomVoiceCall(context, object : TelecomVoiceCall.Listener {
        override fun onCallReady() = onMain { telecomReady = true; maybeListen() }
        override fun onCallHeld(held: Boolean) = onMain {
			telecomHeld = held
            if (held) {
                microphone.stop()
				acceptingOutput = false
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

		override fun onAudioEndpoints(currentName: String, endpoints: List<VoiceAudioEndpoint>) = onMain {
			audioEndpoint = currentName.ifBlank { audioEndpoint }
			snapshot = snapshot.copy(audioEndpointName = currentName, audioEndpoints = endpoints)
			publish()
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
	private var outputUtteranceId = ""
	    @Volatile private var acceptingOutput = false
		private var speechLanguages: Set<String> = emptySet()
		@Volatile private var microphoneMuted = false
		@Volatile private var interruptedPlayback = false
		@Volatile private var pausedByUser = false
		@Volatile private var telecomHeld = false

    fun start(
		server: String,
		token: String,
		sessionId: String,
		chatId: String,
		languages: Set<String> = emptySet(),
		vadSensitivityPercent: Int = 50,
		vadSilenceMilliseconds: Int = 600,
		builtInAudioRoute: BuiltInAudioRoute = BuiltInAudioRoute.SPEAKER,
		responsePacing: VoiceResponsePacing = VoiceResponsePacing.NORMAL,
	) {
        if (running) end()
        running = true
		diagnostics.reset()
        serverReady = false
        telecomReady = false
        audioConfig = null
		connectedServer = server
		connectedToken = token
			speechLanguages = languages.toSet()
		microphoneMuted = false
		interruptedPlayback = false
		pausedByUser = false
		telecomHeld = false
		val startThreshold = vadSensitivityPercent.coerceIn(35, 75) / 100f
		endpoint.configure(EndpointConfig(
			sampleRate = endpointSampleRate,
			frameSamples = endpointFrameSamples,
			startThreshold = startThreshold,
			endThreshold = (startThreshold - 0.15f).coerceAtLeast(0.1f),
			endSilenceMilliseconds = vadSilenceMilliseconds.coerceIn(300, 1_200),
		))
		telecom.setBuiltInAudioRoute(builtInAudioRoute)
		VoiceCallControlRegistry.attach(this)
		snapshot = Snapshot(stage = Stage.CONNECTING, detail = "Connecting…", activeSessionId = sessionId, voiceSessionId = chatId)
        publish()
        appContext.startForegroundService(Intent(appContext, VoiceCallService::class.java))
        telecom.start()
        try {
			connection.connect(server, token, sessionId, chatId, responsePacing)
        } catch (error: Exception) {
            update(Stage.ERROR, error.message ?: "Connection failed")
        }
    }

	    fun submit(text: String) {
        val normalized = text.trim()
        if (!running || normalized.isEmpty()) return
        microphone.stop()
        listener.onUserMessage(normalized)
        update(Stage.PROCESSING, processingStatusText(), "")
        try {
            connection.sendUtterance(normalized)
        } catch (error: Exception) {
            update(Stage.ERROR, error.message ?: "Could not send request")
	    }
	}

	fun setMicrophoneMuted(muted: Boolean) {
		if (!running || muted == microphoneMuted) return
		microphoneMuted = muted
		if (muted) {
			cancelCapture()
			if (pausedByUser || telecomHeld) update(Stage.HELD, "Conversation paused")
			else update(Stage.MUTED, "Microphone muted")
		} else {
			maybeListen(force = true)
		}
	}

	fun loadOlderHistory() {
		if (!running || snapshot.historyLoading || !snapshot.historyHasMore) return
		val beforeId = snapshot.history.firstOrNull()?.id.orEmpty()
		if (beforeId.isBlank()) return
		snapshot = snapshot.copy(historyLoading = true)
		publish()
		try {
			connection.requestHistory(beforeId)
		} catch (error: Exception) {
			snapshot = snapshot.copy(historyLoading = false)
			publish()
		}
	}

	fun searchHistory(query: String) {
		if (!running || query.isBlank()) return
		try {
			connection.searchHistory(query)
		} catch (error: Exception) {
			listener.onHistorySearch(emptyList(), error.message ?: "Could not search transcript")
		}
	}

	fun audioDiagnostics(): AudioDiagnostics = diagnostics.snapshot(running, audioEndpoint, audioConfig)

	fun refreshAudioDiagnostics() = connection.sendPing()

	fun selectAudioEndpoint(endpointId: String) {
		if (!running || endpointId.isBlank()) return
		telecom.selectAudioEndpoint(endpointId)
	}

	fun setPaused(paused: Boolean) {
		if (!running || (paused && pausedByUser) || (!paused && !pausedByUser && !telecomHeld)) return
		pausedByUser = paused
		telecom.setHeld(paused)
		if (paused) {
			cancelCapture()
			acceptingOutput = false
			playback.stop()
			update(Stage.HELD, "Conversation paused", "")
		} else {
			update(Stage.CONNECTING, "Resuming conversation…", "")
			maybeListen(force = true)
		}
	}

	override fun notificationState(): VoiceCallNotificationState = VoiceCallNotificationState(
		detail = snapshot.detail,
		muted = microphoneMuted,
		paused = pausedByUser || telecomHeld || snapshot.stage == Stage.HELD,
		audioRoute = snapshot.audioEndpointName.ifBlank { audioEndpoint },
	)

	override fun toggleMuteFromNotification() = setMicrophoneMuted(!microphoneMuted)

	override fun togglePauseFromNotification() = setPaused(!(pausedByUser || telecomHeld))

	override fun cycleAudioRouteFromNotification() {
		val endpoints = snapshot.audioEndpoints
		if (endpoints.size < 2) return
		val currentIndex = endpoints.indexOfFirst(VoiceAudioEndpoint::current).takeIf { it >= 0 } ?: 0
		val next = endpoints[(currentIndex + 1) % endpoints.size]
		when (next.type) {
			VoiceAudioEndpointType.EARPIECE -> onBuiltInAudioRouteSelected(BuiltInAudioRoute.EARPIECE)
			VoiceAudioEndpointType.SPEAKER -> onBuiltInAudioRouteSelected(BuiltInAudioRoute.SPEAKER)
			else -> Unit
		}
		selectAudioEndpoint(next.id)
	}

	override fun endFromNotification() = end()

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
		interruptedPlayback = false
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
		stopWorkingSound()
		interruptedPlayback = false
		pausedByUser = false
		telecomHeld = false
        endpoint.reset()
        connection.close()
		phoneDevice.close()
        telecom.disconnect()
		VoiceCallControlRegistry.detach(this)
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
		interruptionFeedback.close()
        endpoint.close()
        connection.close()
		phoneDevice.close()
		phoneTools.close()
    }

    private fun handleFrame(frame: VoiceServerFrame) {
		frame.callState?.let { state ->
			val selectedSessionId = state.sessionId.ifBlank { state.activeSessionId }
			val selectedChatId = state.chatId.ifBlank { state.voiceSessionId }
			val sameVoiceSession = selectedChatId == snapshot.voiceSessionId
			val mergedHistory = if (sameVoiceSession) {
				mergeTranscriptHistory(snapshot.history, state.history)
			} else {
				state.history
			}
			val retainedOlderPage = sameVoiceSession && snapshot.history.firstOrNull()?.id != state.history.firstOrNull()?.id
            snapshot = snapshot.copy(
                sessions = state.sessions,
                voiceSessions = state.voiceSessions,
				activeSessionId = selectedSessionId,
				voiceSessionId = selectedChatId,
				history = mergedHistory,
				historyHasMore = if (retainedOlderPage) snapshot.historyHasMore else state.historyHasMore,
				historyLoading = false,
            )
			connection.resumeSelection(selectedSessionId, selectedChatId)
        }
        frame.audioConfig?.let { audioConfig = it }
        when (frame.type) {
            "ready" -> {
                snapshot = snapshot.copy(appUpdate = frame.appUpdate)
                serverReady = true
                maybeListen()
            }
			"state" -> if (!pausedByUser && !telecomHeld) when (frame.state) {
                "recording" -> update(Stage.RECORDING, recordingStatus(interruptedPlayback))
                "transcribing" -> update(Stage.TRANSCRIBING, "Recognizing speech…", "")
                "processing" -> update(Stage.PROCESSING, processingStatusText(), "")
				"working" -> update(
					Stage.WORKING,
					frame.workingOn?.title?.takeIf(String::isNotBlank)?.let { "Working in $it…" } ?: "Working…",
					"",
				)
                "speaking" -> update(Stage.SPEAKING, "Koder is speaking…", "")
            }
            "transcript" -> if (frame.transcript.isNotBlank()) listener.onUserMessage(frame.transcript)
			"history" -> {
				if (frame.error.isNotBlank()) {
					snapshot = snapshot.copy(historyLoading = false, historyHasMore = false)
					publish()
					return
				}
				val known = snapshot.history.asSequence().map(VoiceTranscriptEntry::id).toHashSet()
				val older = frame.history.filterNot { it.id in known }
				snapshot = snapshot.copy(
					history = mergeTranscriptHistory(older, snapshot.history),
					historyHasMore = frame.historyHasMore,
					historyLoading = false,
				)
				listener.onHistoryPage(older)
				publish()
			}
			"history_search" -> listener.onHistorySearch(frame.searchResults, frame.error.takeIf(String::isNotBlank))
            "message" -> frame.message?.let(listener::onAssistantMessage)
            "tts_start" -> {
				diagnostics.startOutput(frame.utteranceId)
				if (pausedByUser || telecomHeld) {
					acceptingOutput = false
					update(Stage.HELD, "Conversation paused", "")
					return
				}
                val format = frame.audioFormat ?: audioConfig?.output
                if (format == null) {
                    update(Stage.ERROR, "Server omitted the speech audio format")
                } else {
					if (outputUtteranceId != frame.utteranceId) outputSequence = 0
					outputUtteranceId = frame.utteranceId
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
				outputUtteranceId = ""
				outputSequence = 0
            }
            "error" -> {
                acceptingOutput = false
				outputUtteranceId = ""
				outputSequence = 0
                playback.stop()
				snapshot = snapshot.copy(historyLoading = false)
                update(Stage.ERROR, frame.error.ifBlank { "Voice request failed" })
            }
        }
        publish()
    }

    @Synchronized
	private fun handleOutputAudio(frame: VoiceAudioFrame) {
        if (!running || !acceptingOutput) return
		audioConfig?.output?.let { diagnostics.recordOutput(frame, it) }
        if (frame.kind != VoiceAudioFrameKind.OUTPUT_PCM || frame.sequence != outputSequence) {
            acceptingOutput = false
            playback.stop()
            onMain { update(Stage.ERROR, "Speech audio arrived out of order") }
            return
        }
		listener.onAudioLevel(pcmLevel(frame.pcm), false)
        outputSequence++
        playback.write(frame.pcm)
    }

	    private fun maybeListen(force: Boolean = false) {
	        if (!running || !serverReady || !telecomReady || audioConfig == null) return
			if (pausedByUser || telecomHeld) {
				update(Stage.HELD, "Conversation paused")
				return
			}
			if (microphoneMuted) {
				update(Stage.MUTED, "Microphone muted")
				return
			}
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
			val evaluated = endpoint.evaluate(samples)
			diagnostics.recordInput(samples, evaluated.vad, utteranceId.isNotBlank())
			listener.onAudioLevel(pcmLevel(samples), true)
			for (event in evaluated.events) {
                when (event) {
                    is UtteranceEvent.Started -> {
						val wasInterruption = acceptingOutput
						interruptedPlayback = wasInterruption
						if (wasInterruption) {
							acceptingOutput = false
							playback.stop()
							interruptionFeedback.acknowledge()
						}
						utteranceId = connection.startAudio(format, speechLanguages)
                        inputSequence = 0
                        event.frames.forEach(::sendPCM)
						onMain { update(Stage.RECORDING, recordingStatus(wasInterruption)) }
                    }
                    is UtteranceEvent.Continued -> sendPCM(event.frame)
                    is UtteranceEvent.Committed -> {
                        val completedId = utteranceId
                        utteranceId = ""
                        microphone.stop()
                        connection.commitAudio(completedId)
						interruptedPlayback = false
                        onMain { update(Stage.TRANSCRIBING, "Recognizing speech…", "") }
                    }
                }
            }
        } catch (error: Exception) {
            val failedId = utteranceId
            utteranceId = ""
			interruptedPlayback = false
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
        val previousStage = snapshot.stage
		when (workingSoundAction(previousStage, stage)) {
			WorkingSoundAction.KEEP -> Unit
			WorkingSoundAction.START -> {
				main.removeCallbacks(delayedProcessingSound)
				workingSound.start()
			}
			WorkingSoundAction.START_DELAYED -> {
				workingSound.stop()
				main.removeCallbacks(delayedProcessingSound)
				main.postDelayed(delayedProcessingSound, PROCESSING_SOUND_DELAY_MILLIS)
			}
			WorkingSoundAction.STOP -> stopWorkingSound()
        }
        snapshot = snapshot.copy(stage = stage, detail = detail, partialTranscript = partial, microphoneMuted = microphoneMuted)
        voiceHapticCueForTransition(previousStage, stage)?.let(haptics::play)
        publish()
    }

	private fun stopWorkingSound() {
		main.removeCallbacks(delayedProcessingSound)
		workingSound.stop()
	}

	private fun publish() {
		listener.onSnapshot(snapshot)
		if (running) VoiceCallService.refresh(notificationState())
	}
    private fun onMain(action: () -> Unit) {
        if (Looper.myLooper() == Looper.getMainLooper()) action() else main.post(action)
    }

}
