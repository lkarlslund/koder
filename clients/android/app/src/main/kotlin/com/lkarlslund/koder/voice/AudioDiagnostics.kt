package com.lkarlslund.koder.voice

import kotlin.math.abs
import kotlin.math.log10
import kotlin.math.sqrt

internal fun pcmLevel(samples: ShortArray): Float {
	if (samples.isEmpty()) return 0f
	val meanSquare = samples.sumOf { sample ->
		val normalized = sample.toDouble() / Short.MAX_VALUE
		normalized * normalized
	} / samples.size
	return sqrt(meanSquare).toFloat().coerceIn(0f, 1f)
}

internal fun pcmLevel(bytes: ByteArray): Float {
	if (bytes.size < 2) return 0f
	var sum = 0.0
	var count = 0
	var index = 0
	while (index + 1 < bytes.size) {
		val sample = ((bytes[index].toInt() and 0xff) or (bytes[index + 1].toInt() shl 8)).toShort()
		val normalized = sample.toDouble() / Short.MAX_VALUE
		sum += normalized * normalized
		count++
		index += 2
	}
	return sqrt(sum / count.coerceAtLeast(1)).toFloat().coerceIn(0f, 1f)
}

internal fun pcmWaveform(samples: ShortArray, bins: Int = 65): FloatArray {
	require(bins > 0)
	if (samples.isEmpty()) return FloatArray(bins)
	return FloatArray(bins) { bin ->
		val start = bin * samples.size / bins
		val end = ((bin + 1) * samples.size / bins).coerceAtLeast(start + 1).coerceAtMost(samples.size)
		var peak: Short = 0
		for (index in start until end) {
			if (abs(samples[index].toInt()) > abs(peak.toInt())) peak = samples[index]
		}
		(peak.toFloat() / 9_000f).coerceIn(-1f, 1f)
	}
}

internal fun pcmWaveform(bytes: ByteArray, bins: Int = 65): FloatArray {
	val sampleCount = bytes.size / 2
	if (sampleCount == 0) return FloatArray(bins)
	val samples = ShortArray(sampleCount) { index ->
		val offset = index * 2
		((bytes[offset].toInt() and 0xff) or (bytes[offset + 1].toInt() shl 8)).toShort()
	}
	return pcmWaveform(samples, bins)
}

data class AudioDiagnostics(
	val active: Boolean = false,
	val microphoneLevelDbfs: Double = -96.0,
	val vadProbabilityPercent: Int = 0,
	val vadState: String = "idle",
	val inputRoute: String = "Unavailable",
	val outputRoute: String = "Unavailable",
	val inputFormat: VoiceAudioFormat? = null,
	val outputFormat: VoiceAudioFormat? = null,
	val capturedFrames: Long = 0,
	val receivedFrames: Long = 0,
	val droppedOutputFrames: Long = 0,
	val duplicateOutputFrames: Long = 0,
	val outputJitterMillis: Double = 0.0,
	val roundTripMillis: Long? = null,
	val reconnects: Long = 0,
)

internal class AudioDiagnosticsTracker(
	private val nanoTime: () -> Long = System::nanoTime,
) {
	private var microphoneLevelDbfs = -96.0
	private var vadProbabilityPercent = 0
	private var vadState = "idle"
	private var capturedFrames = 0L
	private var receivedFrames = 0L
	private var droppedOutputFrames = 0L
	private var duplicateOutputFrames = 0L
	private var outputJitterMillis = 0.0
	private var expectedOutputSequence = 0L
	private var outputUtteranceId = ""
	private var previousOutputAtNanos: Long? = null
	private var previousOutputDurationMillis = 0.0
	private var roundTripMillis: Long? = null
	private var reconnects = 0L

	@Synchronized
	fun reset() {
		microphoneLevelDbfs = -96.0
		vadProbabilityPercent = 0
		vadState = "idle"
		capturedFrames = 0
		receivedFrames = 0
		droppedOutputFrames = 0
		duplicateOutputFrames = 0
		outputJitterMillis = 0.0
		expectedOutputSequence = 0
		outputUtteranceId = ""
		previousOutputAtNanos = null
		previousOutputDurationMillis = 0.0
		roundTripMillis = null
		reconnects = 0
	}

	@Synchronized
	fun recordInput(samples: ShortArray, vad: VadResult, speechActive: Boolean) {
		val linearLevel = pcmLevel(samples).toDouble()
		val measured = if (linearLevel <= 0.0) -96.0 else (20.0 * log10(linearLevel)).coerceIn(-96.0, 0.0)
		microphoneLevelDbfs = if (capturedFrames == 0L) measured else microphoneLevelDbfs * 0.72 + measured * 0.28
		vadProbabilityPercent = (vad.speechProbability * 100).toInt().coerceIn(0, 100)
		vadState = when {
			speechActive -> "speech"
			vad.speechProbability >= 0.5f -> "triggering"
			else -> "silence"
		}
		capturedFrames++
	}

	@Synchronized
	fun startOutput(utteranceId: String) {
		if (utteranceId == outputUtteranceId) return
		outputUtteranceId = utteranceId
		expectedOutputSequence = 0
		previousOutputAtNanos = null
		previousOutputDurationMillis = 0.0
	}

	@Synchronized
	fun recordOutput(frame: VoiceAudioFrame, format: VoiceAudioFormat) {
		when {
			frame.sequence > expectedOutputSequence -> droppedOutputFrames += frame.sequence - expectedOutputSequence
			frame.sequence < expectedOutputSequence -> duplicateOutputFrames++
		}
		expectedOutputSequence = maxOf(expectedOutputSequence, frame.sequence + 1)
		receivedFrames++

		val now = nanoTime()
		previousOutputAtNanos?.let { previous ->
			val arrivalMillis = (now - previous).coerceAtLeast(0) / 1_000_000.0
			val variation = abs(arrivalMillis - previousOutputDurationMillis)
			outputJitterMillis += (variation - outputJitterMillis) / 16.0
		}
		previousOutputAtNanos = now
		val bytesPerFrame = format.channels.coerceAtLeast(1) * 2
		previousOutputDurationMillis = frame.pcm.size.toDouble() / bytesPerFrame / format.sampleRate.coerceAtLeast(1) * 1_000.0
	}

	@Synchronized fun recordRoundTrip(milliseconds: Long) {
		roundTripMillis = milliseconds.coerceAtLeast(0)
	}

	@Synchronized fun recordReconnect() {
		reconnects++
	}

	@Synchronized
	fun snapshot(active: Boolean, route: String, config: VoiceAudioConfig?): AudioDiagnostics = AudioDiagnostics(
		active = active,
		microphoneLevelDbfs = microphoneLevelDbfs,
		vadProbabilityPercent = vadProbabilityPercent,
		vadState = if (active) vadState else "idle",
		inputRoute = route.ifBlank { "Phone audio" },
		outputRoute = route.ifBlank { "Phone audio" },
		inputFormat = config?.input,
		outputFormat = config?.output,
		capturedFrames = capturedFrames,
		receivedFrames = receivedFrames,
		droppedOutputFrames = droppedOutputFrames,
		duplicateOutputFrames = duplicateOutputFrames,
		outputJitterMillis = outputJitterMillis,
		roundTripMillis = roundTripMillis,
		reconnects = reconnects,
	)
}
