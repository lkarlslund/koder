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

	@Test
	fun reconnectStatusTranslatesHandshakeFailures() {
		assertEquals("Authorization failed · check Settings", reconnectStatus("Expected HTTP 101 (HTTP 401)"))
		assertEquals("Voice endpoint unavailable · reconnecting", reconnectStatus("Expected HTTP 101 (HTTP 404)"))
		assertEquals("Conversation is busy · reconnecting", reconnectStatus("Expected HTTP 101 (HTTP 409)"))
		assertEquals("Reconnecting · network changed", reconnectStatus("network changed"))
	}
}
