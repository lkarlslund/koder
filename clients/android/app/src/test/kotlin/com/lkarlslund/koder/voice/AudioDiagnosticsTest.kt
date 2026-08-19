package com.lkarlslund.koder.voice

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class AudioDiagnosticsTest {
	@Test
	fun measuresMicrophoneVadRoutesAndFormats() {
		val tracker = AudioDiagnosticsTracker()
		val config = VoiceAudioConfig(
			input = VoiceAudioFormat("pcm_s16le", 16_000, 1),
			output = VoiceAudioFormat("pcm_s16le", 24_000, 1),
			maxUtteranceSeconds = 60,
		)

		tracker.recordInput(ShortArray(512) { Short.MAX_VALUE }, VadResult(0.83f), speechActive = true)
		val snapshot = tracker.snapshot(active = true, route = "Pixel Buds", config = config)

		assertTrue(snapshot.microphoneLevelDbfs > -0.01)
		assertEquals(83, snapshot.vadProbabilityPercent)
		assertEquals("speech", snapshot.vadState)
		assertEquals("Pixel Buds", snapshot.inputRoute)
		assertEquals("Pixel Buds", snapshot.outputRoute)
		assertEquals(16_000, snapshot.inputFormat?.sampleRate)
		assertEquals(24_000, snapshot.outputFormat?.sampleRate)
		assertEquals(1L, snapshot.capturedFrames)
	}

	@Test
	fun tracksJitterLossDuplicatesRoundTripAndReconnects() {
		var now = 0L
		val tracker = AudioDiagnosticsTracker { now }
		val format = VoiceAudioFormat("pcm_s16le", 1_000, 1)
		fun output(sequence: Long) = VoiceAudioFrame(
			VoiceAudioFrameKind.OUTPUT_PCM,
			0,
			sequence,
			ByteArray(20), // 10 ms at 1 kHz mono PCM16.
		)

		tracker.startOutput("answer-1")
		tracker.recordOutput(output(0), format)
		now = 14_000_000
		tracker.recordOutput(output(2), format)
		now = 24_000_000
		tracker.recordOutput(output(2), format)
		tracker.recordRoundTrip(37)
		tracker.recordReconnect()
		val snapshot = tracker.snapshot(active = true, route = "Speaker", config = null)

		assertEquals(3L, snapshot.receivedFrames)
		assertEquals(1L, snapshot.droppedOutputFrames)
		assertEquals(1L, snapshot.duplicateOutputFrames)
		assertTrue(snapshot.outputJitterMillis > 0.0)
		assertEquals(37L, snapshot.roundTripMillis)
		assertEquals(1L, snapshot.reconnects)
	}
}
