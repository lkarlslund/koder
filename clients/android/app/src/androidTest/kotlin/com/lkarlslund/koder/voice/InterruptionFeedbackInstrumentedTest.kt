package com.lkarlslund.koder.voice

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class InterruptionFeedbackInstrumentedTest {
	@Test
	fun audibleAndHapticAcknowledgementIsAvailableOnDevice() {
		val cues = mutableListOf<VoiceHapticCue>()
		AndroidInterruptionFeedback(InstrumentationRegistry.getInstrumentation().targetContext, object : VoiceHaptics {
			override fun play(cue: VoiceHapticCue) { cues += cue }
		}).use {
			it.acknowledge()
		}
		org.junit.Assert.assertEquals(listOf(VoiceHapticCue.INTERRUPTION), cues)
	}

	@Test
	fun everySubtleCueCanBeSubmittedToTheDeviceVibrator() {
		val haptics = AndroidVoiceHaptics(InstrumentationRegistry.getInstrumentation().targetContext)
		VoiceHapticCue.entries.forEach(haptics::play)
	}
}
