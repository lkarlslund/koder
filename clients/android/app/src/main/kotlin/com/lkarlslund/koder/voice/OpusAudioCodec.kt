package com.lkarlslund.koder.voice

import java.nio.ByteBuffer
import java.nio.ByteOrder
import org.concentus.OpusApplication
import org.concentus.OpusDecoder
import org.concentus.OpusEncoder

internal const val OPUS_FRAME_MILLISECONDS = 20
internal const val INPUT_OPUS_BITRATE = 18_000
internal const val OUTPUT_OPUS_BITRATE = 32_000
private const val MAX_OPUS_PACKET_BYTES = 1_275

internal data class EncodedAudioPacket(val payload: ByteArray, val sourcePCMBytes: Int)

/** Stateful 20 ms packetizer used after VAD so capture remains local PCM. */
internal class OpusAudioEncoder(
	format: VoiceAudioFormat,
	bitrate: Int,
) {
	private val channels = format.channels
	private val frameSamplesPerChannel = format.sampleRate * OPUS_FRAME_MILLISECONDS / 1_000
	private val frame = ShortArray(frameSamplesPerChannel * channels)
	private var bufferedSamples = 0
	private val packet = ByteArray(MAX_OPUS_PACKET_BYTES)
	private val encoder = OpusEncoder(format.sampleRate, channels, OpusApplication.OPUS_APPLICATION_VOIP).apply {
		setBitrate(bitrate)
		setUseVBR(true)
		setUseConstrainedVBR(true)
	}

	init {
		require(format.encoding == VoiceProtocol.OPUS_ENCODING) { "Opus encoder requires an Opus transport format" }
		require(format.sampleRate in setOf(8_000, 12_000, 16_000, 24_000, 48_000)) { "Unsupported Opus sample rate ${format.sampleRate}" }
		require(channels in 1..2) { "Unsupported Opus channel count $channels" }
		require(bitrate > 0) { "Opus bitrate must be positive" }
	}

	fun append(samples: ShortArray): List<EncodedAudioPacket> {
		val packets = mutableListOf<EncodedAudioPacket>()
		var offset = 0
		while (offset < samples.size) {
			val count = minOf(samples.size - offset, frame.size - bufferedSamples)
			samples.copyInto(frame, bufferedSamples, offset, offset + count)
			bufferedSamples += count
			offset += count
			if (bufferedSamples == frame.size) packets += encodeFrame(frame.size)
		}
		return packets
	}

	fun finish(): EncodedAudioPacket? {
		if (bufferedSamples == 0) return null
		val sourceSamples = bufferedSamples
		frame.fill(0, bufferedSamples)
		return encodeFrame(sourceSamples)
	}

	private fun encodeFrame(sourceSamples: Int): EncodedAudioPacket {
		val size = encoder.encode(frame, 0, frameSamplesPerChannel, packet, 0, packet.size)
		bufferedSamples = 0
		return EncodedAudioPacket(packet.copyOf(size), sourceSamples * 2)
	}
}

/** Stateful raw-packet decoder. Playback and UI always consume decoded PCM. */
internal class OpusAudioDecoder(format: VoiceAudioFormat) {
	private val channels = format.channels
	private val decoder = OpusDecoder(format.sampleRate, channels)
	private val samples = ShortArray(format.sampleRate * 120 / 1_000 * channels)

	init {
		require(format.encoding == VoiceProtocol.OPUS_ENCODING) { "Opus decoder requires an Opus transport format" }
		require(format.sampleRate in setOf(8_000, 12_000, 16_000, 24_000, 48_000)) { "Unsupported Opus sample rate ${format.sampleRate}" }
		require(channels in 1..2) { "Unsupported Opus channel count $channels" }
	}

	fun decode(packet: ByteArray): ByteArray {
		require(packet.isNotEmpty() && packet.size <= MAX_OPUS_PACKET_BYTES) { "Invalid Opus packet size ${packet.size}" }
		val samplesPerChannel = decoder.decode(packet, 0, packet.size, samples, 0, samples.size / channels, false)
		return ByteBuffer.allocate(samplesPerChannel * channels * 2).order(ByteOrder.LITTLE_ENDIAN).apply {
			for (index in 0 until samplesPerChannel * channels) putShort(samples[index])
		}.array()
	}
}
