package com.lkarlslund.koder.voice

import android.content.Context
import android.media.AudioManager
import android.media.ToneGenerator

interface InterruptionFeedback : AutoCloseable {
	fun acknowledge()
	override fun close() = Unit
}

class AndroidInterruptionFeedback(
	context: Context,
	private val haptics: VoiceHaptics = AndroidVoiceHaptics(context),
) : InterruptionFeedback {
	private val tone = ToneGenerator(AudioManager.STREAM_VOICE_CALL, 65)

	override fun acknowledge() {
		tone.startTone(ToneGenerator.TONE_PROP_ACK, ACK_TONE_MILLISECONDS)
		haptics.play(VoiceHapticCue.INTERRUPTION)
	}

	override fun close() = tone.release()

	private companion object {
		const val ACK_TONE_MILLISECONDS = 55
	}
}

fun recordingStatus(interruptedPlayback: Boolean): String =
	if (interruptedPlayback) "Interrupted · listening to you…" else "Listening to you…"
