package com.lkarlslund.koder.voice

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class UtteranceSegmenterTest {
	@Test
	fun bargeInRequiresStrongerSustainedSpeechThanOrdinaryListening() {
		val listening = EndpointConfig(startThreshold = 0.5f, endThreshold = 0.35f, minimumSpeechMilliseconds = 160)
		val bargeIn = bargeInEndpointConfig(listening)

		assertEquals(0.7f, bargeIn.startThreshold)
		assertEquals(0.5f, bargeIn.endThreshold)
		assertEquals(320, bargeIn.minimumSpeechMilliseconds)
		assertEquals(listening.endSilenceMilliseconds, bargeIn.endSilenceMilliseconds)
	}

    private val config = EndpointConfig(
        sampleRate = 1_000,
        frameSamples = 100,
        preRollMilliseconds = 300,
        minimumSpeechMilliseconds = 200,
        endSilenceMilliseconds = 300,
        maximumUtteranceMilliseconds = 1_000,
    )

    @Test
    fun waitsForConsecutiveSpeechAndIncludesPreRoll() {
        val segmenter = UtteranceSegmenter(config)

        assertTrue(segmenter.accept(frame(1), probability(0.0f)).isEmpty())
        assertTrue(segmenter.accept(frame(2), probability(0.9f)).isEmpty())
        val events = segmenter.accept(frame(3), probability(0.9f))

        val started = events.single() as UtteranceEvent.Started
        assertEquals(3, started.frames.size)
        assertArrayEquals(frame(1), started.frames[0])
        assertArrayEquals(frame(2), started.frames[1])
        assertArrayEquals(frame(3), started.frames[2])
    }

    @Test
    fun lowProbabilityBeforeOnsetResetsConsecutiveSpeech() {
        val segmenter = UtteranceSegmenter(config)

        assertTrue(segmenter.accept(frame(1), probability(0.9f)).isEmpty())
        assertTrue(segmenter.accept(frame(2), probability(0.4f)).isEmpty())
        assertTrue(segmenter.accept(frame(3), probability(0.9f)).isEmpty())
        assertTrue(segmenter.accept(frame(4), probability(0.9f)).single() is UtteranceEvent.Started)
    }

    @Test
    fun commitsAfterTrailingSilence() {
        val segmenter = startedSegmenter()

        repeat(2) { index ->
            val events = segmenter.accept(frame(10 + index), probability(0.1f))
            assertEquals(1, events.size)
            assertTrue(events.single() is UtteranceEvent.Continued)
        }
        val events = segmenter.accept(frame(12), probability(0.1f))

        assertEquals(2, events.size)
        assertTrue(events[0] is UtteranceEvent.Continued)
        assertEquals(
            CommitReason.SILENCE,
            (events[1] as UtteranceEvent.Committed).reason,
        )
    }

	@Test
	fun aLongerEndPauseWaitsForMoreSilentFrames() {
		val segmenter = UtteranceSegmenter(config.copy(endSilenceMilliseconds = 500))
		segmenter.accept(frame(1), probability(0.9f))
		segmenter.accept(frame(2), probability(0.9f))

		repeat(4) { assertTrue(segmenter.accept(frame(10 + it), probability(0.1f)).none { event -> event is UtteranceEvent.Committed }) }
		assertTrue(segmenter.accept(frame(20), probability(0.1f)).any { it is UtteranceEvent.Committed })
	}

    @Test
    fun hysteresisBandDoesNotAdvanceSilence() {
        val segmenter = startedSegmenter()

        segmenter.accept(frame(10), probability(0.1f))
        repeat(4) { segmenter.accept(frame(20 + it), probability(0.4f)) }
        val secondLow = segmenter.accept(frame(30), probability(0.1f))
        val thirdLow = segmenter.accept(frame(31), probability(0.1f))

        assertEquals(1, secondLow.size)
        assertEquals(CommitReason.SILENCE, (thirdLow[1] as UtteranceEvent.Committed).reason)
    }

    @Test
    fun commitsAtMaximumDuration() {
        val segmenter = startedSegmenter()

        var committed: UtteranceEvent.Committed? = null
        for (index in 0 until 20) {
            val events = segmenter.accept(frame(10 + index), probability(0.9f))
            committed = events.filterIsInstance<UtteranceEvent.Committed>().singleOrNull()
            if (committed != null) break
        }

        assertEquals(CommitReason.MAXIMUM_DURATION, committed?.reason)
    }

    @Test
    fun explicitCommitOnlyAppliesToActiveUtterance() {
        val segmenter = startedSegmenter()

        assertEquals(
            CommitReason.EXPLICIT,
            (segmenter.commit().single() as UtteranceEvent.Committed).reason,
        )
        assertTrue(segmenter.commit().isEmpty())
    }

    @Test
    fun pipelineUsesDetectorAndResetsBothComponents() {
        val detector = FakeDetector(ArrayDeque(listOf(0.9f, 0.9f)))
        val segmenter = UtteranceSegmenter(config)
        val pipeline = VadEndpointPipeline(detector, segmenter)

        assertTrue(pipeline.accept(frame(1)).isEmpty())
        assertTrue(pipeline.accept(frame(2)).single() is UtteranceEvent.Started)
        pipeline.reset()

        assertEquals(1, detector.resetCount)
    }

	@Test
	fun pipelineCanApplyNewSensitivity() {
		val detector = FakeDetector(ArrayDeque(listOf(0.6f, 0.6f)))
		val pipeline = VadEndpointPipeline(detector, UtteranceSegmenter(config))
		pipeline.configure(config.copy(startThreshold = 0.7f, endThreshold = 0.5f))

		assertTrue(pipeline.accept(frame(1)).isEmpty())
		assertTrue(pipeline.accept(frame(2)).isEmpty())
		assertEquals(1, detector.resetCount)
	}

    private fun startedSegmenter(): UtteranceSegmenter = UtteranceSegmenter(config).also {
        it.accept(frame(1), probability(0.9f))
        it.accept(frame(2), probability(0.9f))
    }

    private fun frame(value: Int) = ShortArray(config.frameSamples) { value.toShort() }

    private fun probability(value: Float) = VadResult(value)

    private class FakeDetector(private val probabilities: ArrayDeque<Float>) :
        VoiceActivityDetector {
        override val sampleRate = 1_000
        override val frameSamples = 100
        var resetCount = 0

        override fun evaluate(samples: ShortArray) = VadResult(probabilities.removeFirst())

        override fun reset() {
            resetCount++
        }
    }
}
