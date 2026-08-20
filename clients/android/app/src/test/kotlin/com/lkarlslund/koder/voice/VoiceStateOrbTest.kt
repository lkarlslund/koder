package com.lkarlslund.koder.voice

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class VoiceStateOrbTest {
	@Test
	fun everyOrbStateHasAConciseSpokenDescription() {
		assertEquals(VoiceOrbMode.entries.size, VoiceOrbMode.entries.map(::voiceOrbDescription).distinct().size)
		assertEquals("Koder is using tools", voiceOrbDescription(VoiceOrbMode.WORKING))
	}

	@Test
	fun systemReducedMotionStopsAnimationWithoutLosingState() {
		assertFalse(shouldAnimateVoiceOrb(VoiceOrbMode.PROCESSING, systemAnimationsEnabled = false, shown = true))
		assertFalse(shouldAnimateVoiceOrb(VoiceOrbMode.LISTENING, systemAnimationsEnabled = true, shown = false))
		assertFalse(shouldAnimateVoiceOrb(VoiceOrbMode.IDLE, systemAnimationsEnabled = true, shown = true))
		assertTrue(shouldAnimateVoiceOrb(VoiceOrbMode.AI_SPEAKING, systemAnimationsEnabled = true, shown = true))
	}
	@Test
	fun stagesMapToDistinctOrbBehaviors() {
		assertEquals(VoiceOrbMode.LISTENING, voiceOrbMode(CallController.Stage.LISTENING))
		assertEquals(VoiceOrbMode.USER_SPEAKING, voiceOrbMode(CallController.Stage.RECORDING))
		assertEquals(VoiceOrbMode.PROCESSING, voiceOrbMode(CallController.Stage.TRANSCRIBING))
		assertEquals(VoiceOrbMode.PROCESSING, voiceOrbMode(CallController.Stage.PROCESSING))
		assertEquals(VoiceOrbMode.WORKING, voiceOrbMode(CallController.Stage.WORKING))
		assertEquals(VoiceOrbMode.AI_SPEAKING, voiceOrbMode(CallController.Stage.SPEAKING))
	}

	@Test
	fun pcmLevelsTrackInputAndOutputAmplitude() {
		assertEquals(0f, pcmLevel(shortArrayOf()), 0f)
		assertTrue(pcmLevel(shortArrayOf(16_000, -16_000)) > pcmLevel(shortArrayOf(1_000, -1_000)))
		val quiet = byteArrayOf(0x10, 0x00, 0x10, 0x00)
		val loud = byteArrayOf(0x00, 0x40, 0x00, 0x40)
		assertTrue(pcmLevel(loud) > pcmLevel(quiet))
	}
}
