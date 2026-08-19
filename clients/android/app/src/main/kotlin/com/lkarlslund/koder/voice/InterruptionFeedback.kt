package com.lkarlslund.koder.voice

import android.content.Context
import android.media.AudioManager
import android.media.ToneGenerator
import android.os.VibrationEffect
import android.os.Vibrator

interface InterruptionFeedback : AutoCloseable {
	fun acknowledge()
	override fun close() = Unit
}

class AndroidInterruptionFeedback(context: Context) : InterruptionFeedback {
	private val vibrator = context.applicationContext.getSystemService(Vibrator::class.java)
	private val tone = ToneGenerator(AudioManager.STREAM_VOICE_CALL, 65)

	override fun acknowledge() {
		tone.startTone(ToneGenerator.TONE_PROP_ACK, ACK_TONE_MILLISECONDS)
		if (vibrator?.hasVibrator() == true) {
			vibrator.vibrate(VibrationEffect.createOneShot(ACK_HAPTIC_MILLISECONDS, VibrationEffect.DEFAULT_AMPLITUDE))
		}
	}

	override fun close() = tone.release()

	private companion object {
		const val ACK_TONE_MILLISECONDS = 55
		const val ACK_HAPTIC_MILLISECONDS = 45L
	}
}

fun recordingStatus(interruptedPlayback: Boolean): String =
	if (interruptedPlayback) "Interrupted · listening to you…" else "Listening to you…"
