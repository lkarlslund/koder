package com.lkarlslund.koder.voice

import android.content.Context
import android.media.AudioAttributes
import android.os.Build
import android.os.VibrationEffect
import android.os.VibrationAttributes
import android.os.Vibrator
import android.provider.Settings

enum class VoiceHapticCue { LISTENING, INTERRUPTION, CONFIRMATION_REQUIRED, SUCCESS, FAILURE }

data class VoiceHapticPattern(val timings: LongArray, val amplitudes: IntArray) {
	init {
		require(timings.isNotEmpty() && timings.size == amplitudes.size)
		require(timings.all { it >= 0 })
		require(amplitudes.all { it in 0..255 })
	}
}

fun voiceHapticPattern(cue: VoiceHapticCue): VoiceHapticPattern = when (cue) {
	VoiceHapticCue.LISTENING -> VoiceHapticPattern(longArrayOf(0, 18), intArrayOf(0, 55))
	VoiceHapticCue.INTERRUPTION -> VoiceHapticPattern(longArrayOf(0, 20, 32, 28), intArrayOf(0, 90, 0, 120))
	VoiceHapticCue.CONFIRMATION_REQUIRED -> VoiceHapticPattern(longArrayOf(0, 38, 70, 38), intArrayOf(0, 145, 0, 145))
	VoiceHapticCue.SUCCESS -> VoiceHapticPattern(longArrayOf(0, 20, 38, 34), intArrayOf(0, 65, 0, 125))
	VoiceHapticCue.FAILURE -> VoiceHapticPattern(longArrayOf(0, 62, 42, 62), intArrayOf(0, 165, 0, 165))
}

fun voiceHapticCueForTransition(previous: CallController.Stage, next: CallController.Stage): VoiceHapticCue? =
	if (previous == next) null else when (next) {
		CallController.Stage.LISTENING -> VoiceHapticCue.LISTENING
		CallController.Stage.ERROR -> VoiceHapticCue.FAILURE
		else -> null
	}

interface VoiceHaptics {
	fun play(cue: VoiceHapticCue)
}

class AndroidVoiceHaptics(context: Context) : VoiceHaptics {
	private val appContext = context.applicationContext
	private val vibrator = appContext.getSystemService(Vibrator::class.java)
	private val attributes = AudioAttributes.Builder()
		.setUsage(AudioAttributes.USAGE_ASSISTANCE_SONIFICATION)
		.setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
		.build()

	override fun play(cue: VoiceHapticCue) {
		if (vibrator?.hasVibrator() != true || !systemHapticsEnabled()) return
		val pattern = voiceHapticPattern(cue)
		val effect = VibrationEffect.createWaveform(pattern.timings, pattern.amplitudes, -1)
		if (Build.VERSION.SDK_INT >= 33) {
			vibrator.vibrate(effect, VibrationAttributes.createForUsage(VibrationAttributes.USAGE_TOUCH))
		} else {
			@Suppress("DEPRECATION")
			vibrator.vibrate(effect, attributes)
		}
	}

	private fun systemHapticsEnabled(): Boolean = runCatching {
		Settings.System.getInt(appContext.contentResolver, "haptic_feedback_enabled", 1) != 0
	}.getOrDefault(true)
}
