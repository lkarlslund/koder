package com.lkarlslund.koder.voice

import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class SileroVadInstrumentedTest {
    @Test
    fun evaluatesSilenceAndCanReset() {
        SileroVad.fromAssets(ApplicationProvider.getApplicationContext()).use { vad ->
            repeat(4) {
                val result = vad.evaluate(ShortArray(vad.frameSamples))
                assertTrue(result.speechProbability in 0.0f..1.0f)
            }
            vad.reset()
            val result = vad.evaluate(ShortArray(vad.frameSamples))
            assertTrue(result.speechProbability < 0.5f)
        }
    }
}
