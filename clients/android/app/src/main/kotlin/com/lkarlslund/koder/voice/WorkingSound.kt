package com.lkarlslund.koder.voice

import android.media.AudioManager
import android.media.ToneGenerator
import android.os.Handler
import android.os.Looper

interface WorkingSound : AutoCloseable {
	fun start()
	fun stop()
}

const val PROCESSING_SOUND_DELAY_MILLIS = 2_000L

enum class WorkingSoundAction { KEEP, START, START_DELAYED, STOP }

fun workingSoundAction(previous: CallController.Stage, next: CallController.Stage): WorkingSoundAction = when {
	next == CallController.Stage.PROCESSING && previous !in setOf(CallController.Stage.PROCESSING, CallController.Stage.WORKING) ->
		WorkingSoundAction.START_DELAYED
	next == CallController.Stage.WORKING && previous != CallController.Stage.WORKING -> WorkingSoundAction.START
	next in setOf(CallController.Stage.PROCESSING, CallController.Stage.WORKING) -> WorkingSoundAction.KEEP
	else -> WorkingSoundAction.STOP
}

class AndroidWorkingSound : WorkingSound {
	private val handler = Handler(Looper.getMainLooper())
	private var active = false
	private var tone: ToneGenerator? = null
	private val pulse = object : Runnable {
		override fun run() {
			if (!active) return
			if (tone == null) {
				tone = runCatching {
					ToneGenerator(AudioManager.STREAM_VOICE_CALL, VOLUME_PERCENT)
				}.getOrNull()
			}
			tone?.startTone(ToneGenerator.TONE_PROP_BEEP2, PULSE_MILLIS)
			handler.postDelayed(this, REPEAT_MILLIS)
		}
	}

	override fun start() {
		if (active) return
		active = true
		handler.post(pulse)
	}

	override fun stop() {
		active = false
		handler.removeCallbacks(pulse)
		tone?.stopTone()
	}

	override fun close() {
		stop()
		tone?.release()
		tone = null
	}

	private companion object {
		const val VOLUME_PERCENT = 65
		const val PULSE_MILLIS = 120
		const val REPEAT_MILLIS = 2_100L
	}
}
