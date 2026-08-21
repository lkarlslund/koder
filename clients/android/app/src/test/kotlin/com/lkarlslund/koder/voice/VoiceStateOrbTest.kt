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
		assertFalse(shouldAnimateVoiceOrb(VoiceOrbMode.PAUSED, systemAnimationsEnabled = true, shown = true))
		assertTrue(shouldAnimateVoiceOrb(VoiceOrbMode.AI_SPEAKING, systemAnimationsEnabled = true, shown = true))
	}
	@Test
	fun stagesMapToDistinctOrbBehaviors() {
		assertEquals(VoiceOrbMode.LISTENING, voiceOrbMode(CallController.Stage.LISTENING))
		assertEquals(VoiceOrbMode.CONNECTING, voiceOrbMode(CallController.Stage.CONNECTING))
		assertEquals(VoiceOrbMode.USER_SPEAKING, voiceOrbMode(CallController.Stage.RECORDING))
		assertEquals(VoiceOrbMode.PROCESSING, voiceOrbMode(CallController.Stage.TRANSCRIBING))
		assertEquals(VoiceOrbMode.PROCESSING, voiceOrbMode(CallController.Stage.PROCESSING))
		assertEquals(VoiceOrbMode.WORKING, voiceOrbMode(CallController.Stage.WORKING))
		assertEquals(VoiceOrbMode.AI_SPEAKING, voiceOrbMode(CallController.Stage.SPEAKING))
		assertEquals(VoiceOrbMode.PAUSED, voiceOrbMode(CallController.Stage.HELD))
		assertEquals(VoiceOrbMode.PAUSED, voiceOrbMode(CallController.Stage.ERROR))
	}

	@Test
	fun starsMoveForwardContinuouslyAndProcessingAddsWarpTrails() {
		val listeningStart = voiceStarMotion(VoiceOrbMode.LISTENING, initialRadius = 0.2f, speed = 1f, travel = 0f)
		val listeningLater = voiceStarMotion(VoiceOrbMode.LISTENING, initialRadius = 0.2f, speed = 1f, travel = voiceStarTravelRate(VoiceOrbMode.LISTENING))
		val processingLater = voiceStarMotion(VoiceOrbMode.PROCESSING, initialRadius = 0.2f, speed = 1f, travel = voiceStarTravelRate(VoiceOrbMode.PROCESSING))
		assertTrue(listeningLater.radius > listeningStart.radius)
		assertEquals(0f, listeningLater.trailFraction)
		assertTrue(voiceStarTravelRate(VoiceOrbMode.PROCESSING) > voiceStarTravelRate(VoiceOrbMode.LISTENING) * 5f)
		assertTrue(processingLater.radius > listeningLater.radius)
		assertTrue(processingLater.trailFraction > 0f)
	}

	@Test
	fun orbUsesLargerResponsiveSize() {
		assertEquals(300, voiceOrbSizeDp(fontScale = 1f))
		assertEquals(232, voiceOrbSizeDp(fontScale = 1.3f))
	}

	@Test
	fun pcmLevelsTrackInputAndOutputAmplitude() {
		assertEquals(0f, pcmLevel(shortArrayOf()), 0f)
		assertTrue(pcmLevel(shortArrayOf(16_000, -16_000)) > pcmLevel(shortArrayOf(1_000, -1_000)))
		val quiet = byteArrayOf(0x10, 0x00, 0x10, 0x00)
		val loud = byteArrayOf(0x00, 0x40, 0x00, 0x40)
		assertTrue(pcmLevel(loud) > pcmLevel(quiet))
	}

	@Test
	fun waveformUsesActualSignedInputAndOutputSamples() {
		val input = pcmWaveform(shortArrayOf(9_000, -9_000, 4_500, -4_500), bins = 4)
		assertEquals(listOf(1f, -1f, 0.5f, -0.5f), input.toList())
		val output = pcmWaveform(byteArrayOf(0x28, 0x23, 0xD8.toByte(), 0xDC.toByte()), bins = 2)
		assertEquals(1f, output[0], 0.001f)
		assertEquals(-1f, output[1], 0.001f)
	}

	@Test
	fun waveformGainLiftsQuietSpeechWithoutChangingStrongSpeech() {
		assertEquals(1f, voiceOrbWaveformGain(0f), 0f)
		assertEquals(1f, voiceOrbWaveformGain(0.9f), 0f)
		assertTrue(voiceOrbWaveformGain(0.08f) > 5f)
		assertEquals(10f, voiceOrbWaveformGain(0.01f), 0f)
	}

	@Test
	fun staleWaveformDecaysInsteadOfFreezing() {
		assertEquals(1f, voiceOrbWaveformDecay(90), 0f)
		assertTrue(voiceOrbWaveformDecay(300) < 0.3f)
		assertTrue(voiceOrbWaveformDecay(1_000) < 0.01f)
	}
}
