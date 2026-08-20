package com.lkarlslund.koder.voice

import org.junit.Assert.assertEquals
import org.junit.Test

class ConnectionSoundTest {
	@Test
	fun onlyAnActiveUnpausedUtteranceKeepsCaptureAliveAcrossDisconnect() {
		assertEquals(DisconnectCaptureAction.BUFFER_ACTIVE_SPEECH, disconnectCaptureAction(outgoingSpeechActive = true, paused = false))
		assertEquals(DisconnectCaptureAction.STOP_LISTENING, disconnectCaptureAction(outgoingSpeechActive = false, paused = false))
		assertEquals(DisconnectCaptureAction.STOP_LISTENING, disconnectCaptureAction(outgoingSpeechActive = true, paused = true))
	}
}
