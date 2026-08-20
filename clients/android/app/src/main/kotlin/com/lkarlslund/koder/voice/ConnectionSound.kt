package com.lkarlslund.koder.voice

import android.media.AudioManager
import android.media.ToneGenerator
import android.os.Handler
import android.os.Looper

interface ConnectionSound : AutoCloseable {
	fun disconnected()
	fun ready()
	fun stop()
}

enum class DisconnectCaptureAction { BUFFER_ACTIVE_SPEECH, STOP_LISTENING }

internal fun disconnectCaptureAction(outgoingSpeechActive: Boolean, paused: Boolean): DisconnectCaptureAction =
	if (outgoingSpeechActive && !paused) DisconnectCaptureAction.BUFFER_ACTIVE_SPEECH else DisconnectCaptureAction.STOP_LISTENING

class AndroidConnectionSound : ConnectionSound {
	private val handler = Handler(Looper.getMainLooper())
	private var offline = false
	private var tone: ToneGenerator? = null
	private val offlinePulse = object : Runnable {
		override fun run() {
			if (!offline) return
			ensureTone()?.startTone(ToneGenerator.TONE_PROP_NACK, OFFLINE_PULSE_MILLIS)
			handler.postDelayed(this, OFFLINE_REPEAT_MILLIS)
		}
	}

	override fun disconnected() {
		if (offline) return
		offline = true
		handler.post(offlinePulse)
	}

	override fun ready() {
		stop()
		ensureTone()?.startTone(ToneGenerator.TONE_PROP_ACK, READY_MILLIS)
	}

	override fun stop() {
		offline = false
		handler.removeCallbacks(offlinePulse)
		tone?.stopTone()
	}

	override fun close() {
		stop()
		tone?.release()
		tone = null
	}

	private fun ensureTone(): ToneGenerator? {
		if (tone == null) tone = runCatching { ToneGenerator(AudioManager.STREAM_VOICE_CALL, VOLUME_PERCENT) }.getOrNull()
		return tone
	}

	private companion object {
		const val VOLUME_PERCENT = 72
		const val READY_MILLIS = 170
		const val OFFLINE_PULSE_MILLIS = 240
		const val OFFLINE_REPEAT_MILLIS = 3_200L
	}
}
