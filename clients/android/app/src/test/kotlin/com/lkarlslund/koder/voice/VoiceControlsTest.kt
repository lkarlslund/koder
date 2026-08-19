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
		assertEquals("Voice connection unavailable · reconnecting", reconnectStatus("Expected HTTP 101 response but was '500'"))
		assertEquals("Reconnecting · network changed", reconnectStatus("network changed"))
	}

	@Test
	fun conversationAvailabilityDistinguishesStartupRetryAndOffline() {
		assertEquals(ConversationAvailability.CONNECTING, conversationAvailability(CallController.Stage.CONNECTING, "Connecting…"))
		assertEquals(ConversationAvailability.RETRYING, conversationAvailability(CallController.Stage.CONNECTING, "Reconnecting · network changed"))
		assertEquals(ConversationAvailability.ONLINE, conversationAvailability(CallController.Stage.LISTENING, "Listening"))
		assertEquals(ConversationAvailability.PAUSED, conversationAvailability(CallController.Stage.DISCONNECTED, "Conversation paused"))
		assertEquals(ConversationAvailability.OFFLINE, conversationAvailability(CallController.Stage.ERROR, "Connection failed"))
		assertEquals("Offline · Connection failed", conversationStatusText(CallController.Stage.ERROR, "Connection failed"))
	}

	@Test
	fun bargeInHasDistinctSpokenPlaybackFeedback() {
		assertEquals("Listening to you…", recordingStatus(false))
		assertEquals("Interrupted · listening to you…", recordingStatus(true))
	}
}
