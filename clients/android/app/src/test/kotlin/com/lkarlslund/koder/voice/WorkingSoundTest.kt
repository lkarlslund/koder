package com.lkarlslund.koder.voice

import org.junit.Assert.assertEquals
import org.junit.Test

class WorkingSoundTest {
	@Test
	fun thinkingStartsDelayedAndToolWorkContinuesOrStartsSound() {
		assertEquals(2_000L, PROCESSING_SOUND_DELAY_MILLIS)
		assertEquals(
			WorkingSoundAction.START_DELAYED,
			workingSoundAction(CallController.Stage.TRANSCRIBING, CallController.Stage.PROCESSING),
		)
		assertEquals(
			WorkingSoundAction.KEEP,
			workingSoundAction(CallController.Stage.PROCESSING, CallController.Stage.PROCESSING),
		)
		assertEquals(
			WorkingSoundAction.START,
			workingSoundAction(CallController.Stage.PROCESSING, CallController.Stage.WORKING),
		)
		assertEquals(
			WorkingSoundAction.KEEP,
			workingSoundAction(CallController.Stage.WORKING, CallController.Stage.PROCESSING),
		)
	}

	@Test
	fun leavingThinkingOrToolWorkStopsSound() {
		assertEquals(
			WorkingSoundAction.STOP,
			workingSoundAction(CallController.Stage.PROCESSING, CallController.Stage.SPEAKING),
		)
		assertEquals(
			WorkingSoundAction.STOP,
			workingSoundAction(CallController.Stage.WORKING, CallController.Stage.ERROR),
		)
	}
}
