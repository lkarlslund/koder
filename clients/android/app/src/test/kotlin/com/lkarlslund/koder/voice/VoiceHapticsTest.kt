package com.lkarlslund.koder.voice

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class VoiceHapticsTest {
	@Test
	fun cuesUseDistinctShortNonRepeatingPatterns() {
		val patterns = VoiceHapticCue.entries.map(::voiceHapticPattern)
		val signatures = patterns.map { pattern -> pattern.timings.joinToString() + ":" + pattern.amplitudes.joinToString() }
		assertEquals(patterns.size, signatures.distinct().size)
		patterns.forEach { pattern ->
			assertTrue(pattern.timings.sum() <= 250)
			assertTrue(pattern.amplitudes.first() == 0)
			assertTrue(pattern.amplitudes.any { it in 1..180 })
		}
	}

	@Test
	fun conversationalStateCuesOnlyFireOnMeaningfulTransitions() {
		assertEquals(VoiceHapticCue.LISTENING, voiceHapticCueForTransition(CallController.Stage.CONNECTING, CallController.Stage.LISTENING))
		assertEquals(VoiceHapticCue.FAILURE, voiceHapticCueForTransition(CallController.Stage.PROCESSING, CallController.Stage.ERROR))
		assertNull(voiceHapticCueForTransition(CallController.Stage.LISTENING, CallController.Stage.LISTENING))
		assertNull(voiceHapticCueForTransition(CallController.Stage.LISTENING, CallController.Stage.RECORDING))
	}
}
