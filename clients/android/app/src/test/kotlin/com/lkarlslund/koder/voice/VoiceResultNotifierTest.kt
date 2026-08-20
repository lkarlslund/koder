package com.lkarlslund.koder.voice

import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class VoiceResultNotifierTest {
	@Test
	fun onlyBackgroundDelegatedResultsNotify() {
		assertTrue(shouldNotifyCompletedWork(appVisible = false, delegatedWorkPending = true, voiceSessionId = "voice-1"))
		assertFalse(shouldNotifyCompletedWork(appVisible = true, delegatedWorkPending = true, voiceSessionId = "voice-1"))
		assertFalse(shouldNotifyCompletedWork(appVisible = false, delegatedWorkPending = false, voiceSessionId = "voice-1"))
		assertFalse(shouldNotifyCompletedWork(appVisible = false, delegatedWorkPending = true, voiceSessionId = ""))
	}

	@Test
	fun resultNotificationsAreStablePerExactResult() {
		val first = VoiceResultNotifier.notificationId("voice-1", "message-1")
		assertTrue(first > 0)
		assertTrue(first == VoiceResultNotifier.notificationId("voice-1", "message-1"))
		assertNotEquals(first, VoiceResultNotifier.notificationId("voice-1", "message-2"))
	}
}
