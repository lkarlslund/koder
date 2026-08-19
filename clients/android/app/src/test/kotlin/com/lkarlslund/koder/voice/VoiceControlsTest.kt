package com.lkarlslund.koder.voice

import org.junit.Assert.assertEquals
import org.junit.Test

class VoiceControlsTest {
	@Test
	fun muteControlDescribesTheAction() {
		assertEquals("Mute", muteControlLabel(false))
		assertEquals("Unmute", muteControlLabel(true))
	}
}
