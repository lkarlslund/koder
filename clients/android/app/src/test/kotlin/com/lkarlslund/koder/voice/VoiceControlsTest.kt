package com.lkarlslund.koder.voice

import org.junit.Assert.assertEquals
import org.junit.Test

class VoiceControlsTest {
	@Test
	fun muteControlDescribesTheAction() {
		assertEquals("Mute", muteControlLabel(false))
		assertEquals("Unmute", muteControlLabel(true))
	}

	@Test
	fun primaryControlDistinguishesFailureFromPause() {
		assertEquals("Retry", primaryVoiceControlLabel(CallController.Stage.ERROR, false))
		assertEquals("Resume", primaryVoiceControlLabel(CallController.Stage.DISCONNECTED, false))
		assertEquals("Pause", primaryVoiceControlLabel(CallController.Stage.LISTENING, true))
	}
}
