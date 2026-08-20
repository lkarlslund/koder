package com.lkarlslund.koder.voice

import java.util.Base64
import kotlin.math.PI
import kotlin.math.sin
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class OpusAudioCodecTest {
	@Test
	fun packetizesArbitraryVadFramesAndRoundTrips() {
		val input = VoiceAudioFormat(VoiceProtocol.OPUS_ENCODING, 16_000, 1)
		val encoder = OpusAudioEncoder(input, INPUT_OPUS_BITRATE)
		val first = sine(512, 16_000)
		val second = sine(128, 16_000, 512)
		val packets = encoder.append(first) + encoder.append(second)

		assertEquals(2, packets.size)
		assertEquals(listOf(640, 640), packets.map(EncodedAudioPacket::sourcePCMBytes))
		assertTrue(packets.sumOf { it.payload.size } < packets.sumOf { it.sourcePCMBytes } / 3)
		val decoder = OpusAudioDecoder(input)
		packets.forEach { packet ->
			val pcm = decoder.decode(packet.payload)
			assertEquals(640, pcm.size)
			assertTrue(pcmLevel(pcm) > 0.02f)
		}
	}

	@Test
	fun decoderAcceptsGoGopusPacket() {
		val packet = Base64.getDecoder().decode(
			"SIK1A2yemawAAARf+DAEigUbAV9h6UiZqPeol1Tp+pMLRk8JOV6m6v1d/9/DJ3pJHoNAJt1q7uIy4j8ufJp0pvhrU3K8",
		)
		val pcm = OpusAudioDecoder(VoiceAudioFormat(VoiceProtocol.OPUS_ENCODING, 16_000, 1)).decode(packet)
		assertEquals(640, pcm.size)
		assertTrue(pcmLevel(pcm) > 0.02f)
	}

	@Test
	fun padsOnlyTheFinalPartialPacket() {
		val encoder = OpusAudioEncoder(VoiceAudioFormat(VoiceProtocol.OPUS_ENCODING, 16_000, 1), INPUT_OPUS_BITRATE)
		assertTrue(encoder.append(sine(100, 16_000)).isEmpty())
		val final = checkNotNull(encoder.finish())
		assertEquals(200, final.sourcePCMBytes)
		assertTrue(encoder.finish() == null)
	}

	private fun sine(count: Int, sampleRate: Int, offset: Int = 0): ShortArray = ShortArray(count) { index ->
		(sin(2.0 * PI * 440.0 * (index + offset) / sampleRate) * 8_000).toInt().toShort()
	}
}
