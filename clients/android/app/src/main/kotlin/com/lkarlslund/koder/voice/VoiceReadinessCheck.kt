package com.lkarlslund.koder.voice

import android.content.Context
import com.lkarlslund.koder.phone.PhoneIdentity
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.TimeUnit
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okhttp3.HttpUrl.Companion.toHttpUrl

/** A non-persistent microphone -> VAD -> STT -> TTS/playback diagnostic. */
class VoiceReadinessCheck(
	context: Context,
	private val identity: PhoneIdentity,
	private val listener: Listener,
	private val microphone: MicrophoneCapture = AndroidMicrophoneCapture(),
	private val detectorFactory: () -> VoiceActivityDetector = { SileroVad.fromAssets(context.applicationContext) },
	audioPlayback: StreamingAudioPlayback? = null,
	private val client: OkHttpClient = OkHttpClient(),
) : AutoCloseable {
	enum class Step { SERVER, MICROPHONE, VAD, STT, PLAYBACK }

	interface Listener {
		fun onProgress(step: Step, detail: String)
		fun onComplete(transcript: String)
		fun onFailure(message: String)
	}

	private val scheduler = Executors.newSingleThreadScheduledExecutor { runnable ->
		Thread(runnable, "koder-readiness-timeout").apply { isDaemon = true }
	}
	private val playback = audioPlayback ?: AndroidStreamingAudioPlayback(onError = ::fail)
	private var socket: WebSocket? = null
	private var endpoint: VadEndpointPipeline? = null
	private var utteranceId = ""
	private var sequence = 0L
	private var transcript = ""
	private var serverComplete = false
	private var playbackComplete = false
	private var timeout: ScheduledFuture<*>? = null
	private var finished = false
	private var frameSamples = SileroVad.FRAME_SAMPLES
	private var vadDetected = false
	private var audioConfig: VoiceAudioConfig? = null
	private var inputOpusEncoder: OpusAudioEncoder? = null
	private var outputOpusDecoder: OpusAudioDecoder? = null
	private var captureStarted = false

	@Synchronized
	fun start(
		server: String,
		token: String,
		languages: Set<String>,
		sensitivity: Int,
		silenceMilliseconds: Int,
		inputCompression: AudioCompression = AudioCompression.PCM,
		outputCompression: AudioCompression = AudioCompression.OPUS_BALANCED,
	) {
		stopResources()
		finished = false
		serverComplete = false
		playbackComplete = false
		transcript = ""
		vadDetected = false
		audioConfig = null
		inputOpusEncoder = null
		outputOpusDecoder = null
		captureStarted = false
		val detector = detectorFactory()
		frameSamples = detector.frameSamples
		endpoint = VadEndpointPipeline(detector).apply {
			val threshold = sensitivity.coerceIn(35, 75) / 100f
			configure(EndpointConfig(
				sampleRate = detector.sampleRate,
				frameSamples = detector.frameSamples,
				startThreshold = threshold,
				endThreshold = (threshold - 0.15f).coerceAtLeast(0.1f),
				endSilenceMilliseconds = silenceMilliseconds.coerceIn(300, 1_200),
			))
		}
		val ws = VoiceProtocol.readinessWebsocketUrl(server)
		val requestURL = when {
			ws.startsWith("ws://") -> "http://" + ws.removePrefix("ws://")
			ws.startsWith("wss://") -> "https://" + ws.removePrefix("wss://")
			else -> ws
		}.toHttpUrl()
		val request = Request.Builder().also(identity::applyTo).url(requestURL)
			.apply { if (token.isNotBlank()) header("Authorization", "Bearer ${token.trim()}") }
			.build()
		socket = client.newWebSocket(request, socketListener(languages, inputCompression, outputCompression))
		timeout = scheduler.schedule({ fail("Readiness check timed out. Try speaking closer to the microphone.") }, 45, TimeUnit.SECONDS)
	}

	private fun socketListener(
		languages: Set<String>,
		inputCompression: AudioCompression,
		outputCompression: AudioCompression,
	) = object : WebSocketListener() {
		override fun onOpen(webSocket: WebSocket, response: Response) {
			webSocket.send(VoiceProtocol.hello(inputCompression = inputCompression, outputCompression = outputCompression))
			listener.onProgress(Step.SERVER, "Connected and authorized")
		}

		override fun onMessage(webSocket: WebSocket, text: String) {
			runCatching { handle(VoiceProtocol.parse(text), languages) }
				.onFailure { fail("Invalid readiness response: ${it.message}") }
		}

		override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
			runCatching { VoiceProtocol.decodeAudio(bytes.toByteArray()) }.onSuccess { frame ->
				val config = audioConfig ?: return@onSuccess
				val pcm = when (frame.kind) {
					VoiceAudioFrameKind.OUTPUT_PCM -> frame.payload
					VoiceAudioFrameKind.OUTPUT_OPUS -> outputOpusDecoder?.decode(frame.payload)
					else -> null
				}
				if (pcm != null) playback.write(pcm)
			}.onFailure { fail("Invalid readiness audio: ${it.message}") }
		}

		override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
			fail((t.message ?: "Could not connect") + response?.let { " (HTTP ${it.code})" }.orEmpty())
		}

		override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
			webSocket.close(code, reason)
		}
	}

	@Synchronized
	private fun handle(frame: VoiceServerFrame, languages: Set<String>) {
		if (finished) return
		when (frame.type) {
			"readiness_ready" -> {
				val config = frame.audioConfig ?: return fail("Server omitted microphone format")
				audioConfig = config
				if (config.transportSelected() && !captureStarted) {
					captureStarted = true
					beginCapture(config.input, languages)
				}
			}
			"transcript" -> {
				transcript = frame.transcript
				listener.onProgress(Step.STT, "Heard: ${frame.transcript}")
			}
			"tts_start" -> {
				outputOpusDecoder = when (audioConfig?.selectedOutputTransport()?.encoding) {
					VoiceProtocol.OPUS_ENCODING -> OpusAudioDecoder(audioConfig!!.selectedOutputTransport())
					else -> null
				}
				playback.start(frame.audioFormat ?: return fail("Server omitted playback format"))
				listener.onProgress(Step.PLAYBACK, "Playing the server response")
			}
			"tts_end" -> playback.finish {
				synchronized(this@VoiceReadinessCheck) {
					playbackComplete = true
					maybeComplete()
				}
			}
			"readiness_complete" -> {
				serverComplete = true
				if (transcript.isBlank()) transcript = frame.transcript
				maybeComplete()
			}
			"error" -> fail(frame.error.ifBlank { "Readiness check failed" })
		}
	}

	private fun beginCapture(format: VoiceAudioFormat, languages: Set<String>) {
		val pipeline = endpoint ?: return fail("Voice activity detector is unavailable")
		listener.onProgress(Step.MICROPHONE, "Microphone is active; say a short sentence")
		microphone.start(format, frameSamples, object : MicrophoneCapture.Listener {
			override fun onFrame(samples: ShortArray) {
				val evaluation = runCatching { pipeline.evaluate(samples) }.getOrElse { return fail("Voice detection failed: ${it.message}") }
				if (!vadDetected && evaluation.vad.speechProbability >= 0.5f) {
					vadDetected = true
					listener.onProgress(Step.VAD, "Speech detected")
				}
				evaluation.events.forEach { event ->
					when (event) {
						is UtteranceEvent.Started -> {
							utteranceId = UUID.randomUUID().toString()
							sequence = 0
							val transport = audioConfig?.selectedInputTransport() ?: format
							inputOpusEncoder = if (transport.encoding == VoiceProtocol.OPUS_ENCODING) {
								OpusAudioEncoder(transport, transport.bitrate.takeIf { it > 0 } ?: INPUT_OPUS_BITRATE)
							} else null
							socket?.send(VoiceProtocol.audioStart(utteranceId, transport, languages))
							event.frames.forEach(::sendAudio)
						}
						is UtteranceEvent.Continued -> sendAudio(event.frame)
						is UtteranceEvent.Committed -> {
							inputOpusEncoder?.finish()?.let(::sendOpus)
							inputOpusEncoder = null
							microphone.stop()
							socket?.send(VoiceProtocol.audioCommit(utteranceId))
							listener.onProgress(Step.STT, "Transcribing with Koder")
						}
					}
				}
			}

			override fun onCaptureError(message: String) = fail(message)
		})
	}

	private fun sendAudio(samples: ShortArray) {
		inputOpusEncoder?.let { encoder ->
			encoder.append(samples).forEach(::sendOpus)
			return
		}
		val pcm = ByteBuffer.allocate(samples.size * 2).order(ByteOrder.LITTLE_ENDIAN).apply {
			samples.forEach(::putShort)
		}.array()
		val payload = VoiceProtocol.encodeAudio(VoiceAudioFrame(VoiceAudioFrameKind.INPUT_PCM, 0, sequence++, pcm))
		socket?.send(ByteString.of(*payload))
	}

	private fun sendOpus(packet: EncodedAudioPacket) {
		val payload = VoiceProtocol.encodeAudio(
			VoiceAudioFrame(VoiceAudioFrameKind.INPUT_OPUS, 0, sequence++, packet.payload),
		)
		socket?.send(ByteString.of(*payload))
	}

	@Synchronized
	private fun maybeComplete() {
		if (!serverComplete || !playbackComplete || finished) return
		finished = true
		timeout?.cancel(false)
		microphone.stop()
		endpoint?.close()
		endpoint = null
		socket?.close(1000, "readiness complete")
		listener.onComplete(transcript)
	}

	@Synchronized
	private fun fail(message: String) {
		if (finished) return
		finished = true
		stopResources()
		listener.onFailure(message)
	}

	@Synchronized
	private fun stopResources() {
		timeout?.cancel(false)
		timeout = null
		microphone.stop()
		playback.stop()
		inputOpusEncoder = null
		outputOpusDecoder = null
		endpoint?.close()
		endpoint = null
		socket?.cancel()
		socket = null
	}

	override fun close() {
		finished = true
		stopResources()
		scheduler.shutdownNow()
	}
}
