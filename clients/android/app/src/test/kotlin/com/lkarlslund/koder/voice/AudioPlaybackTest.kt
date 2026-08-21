package com.lkarlslund.koder.voice

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AudioPlaybackTest {
	@Test
	fun stalledPlaybackDrainIsReleasedAfterGracePeriod() {
		assertFalse(playbackDrainStalled(nowNanos = 1_499_999_999L, lastProgressNanos = 0L))
		assertTrue(playbackDrainStalled(nowNanos = 1_500_000_000L, lastProgressNanos = 0L))
	}
}
