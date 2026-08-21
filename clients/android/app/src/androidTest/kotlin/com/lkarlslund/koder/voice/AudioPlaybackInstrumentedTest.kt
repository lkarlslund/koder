package com.lkarlslund.koder.voice

import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

@RunWith(AndroidJUnit4::class)
class AudioPlaybackInstrumentedTest {
	@Test
	fun completionWaitsForLocallyBufferedSpeechToPlay() {
		val sampleRate = 24_000
		val durationMilliseconds = 400L
		val pcm = ByteArray((sampleRate * durationMilliseconds / 1_000L * 2L).toInt())
		val completed = CountDownLatch(1)
		val visualized = CountDownLatch(1)
		val startedAt = System.nanoTime()

		AndroidStreamingAudioPlayback(onPlaybackChunkQueued = { chunk ->
			if (chunk.size == pcm.size) visualized.countDown()
		}) { throw AssertionError(it) }.use { playback ->
			playback.start(VoiceAudioFormat("pcm_s16le", sampleRate, 1))
			playback.write(pcm)
			playback.finish(completed::countDown)
			assertTrue("playback waveform was not driven by the AudioTrack feed", visualized.await(3, TimeUnit.SECONDS))
			assertTrue("playback did not complete", completed.await(3, TimeUnit.SECONDS))
		}

		val elapsedMilliseconds = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - startedAt)
		assertTrue(
			"completion fired after ${elapsedMilliseconds}ms for ${durationMilliseconds}ms of queued speech",
			elapsedMilliseconds >= durationMilliseconds / 2,
		)
	}
}
