package com.lkarlslund.koder.voice

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class InterruptionFeedbackInstrumentedTest {
	@Test
	fun audibleAndHapticAcknowledgementIsAvailableOnDevice() {
		AndroidInterruptionFeedback(InstrumentationRegistry.getInstrumentation().targetContext).use {
			it.acknowledge()
		}
	}
}
