package com.lkarlslund.koder.voice

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class VoiceStateOrbTest {
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
