package com.lkarlslund.koder.voice

import java.util.ArrayDeque
import kotlin.math.ceil

data class EndpointConfig(
    val sampleRate: Int = SileroVad.SAMPLE_RATE,
    val frameSamples: Int = SileroVad.FRAME_SAMPLES,
    val startThreshold: Float = 0.5f,
    val endThreshold: Float = 0.35f,
    val preRollMilliseconds: Int = 400,
    val minimumSpeechMilliseconds: Int = 160,
    val endSilenceMilliseconds: Int = 600,
    val maximumUtteranceMilliseconds: Int = 30_000,
) {
    init {
        require(sampleRate > 0)
        require(frameSamples > 0)
        require(startThreshold in 0.0f..1.0f)
        require(endThreshold in 0.0f..startThreshold)
        require(preRollMilliseconds >= 0)
        require(minimumSpeechMilliseconds > 0)
        require(endSilenceMilliseconds > 0)
        require(maximumUtteranceMilliseconds >= minimumSpeechMilliseconds)
    }

    internal val preRollFrames = framesFor(preRollMilliseconds)
    internal val minimumSpeechFrames = framesFor(minimumSpeechMilliseconds)
    internal val endSilenceFrames = framesFor(endSilenceMilliseconds)
    internal val maximumUtteranceFrames = framesFor(maximumUtteranceMilliseconds)

    private fun framesFor(milliseconds: Int): Int = ceil(
        milliseconds.toDouble() * sampleRate / (1_000.0 * frameSamples),
    ).toInt()
}

/** Requires stronger, sustained near-end speech while far-end audio is playing. */
fun bargeInEndpointConfig(listening: EndpointConfig): EndpointConfig = listening.copy(
	startThreshold = (listening.startThreshold + 0.20f).coerceAtMost(0.90f),
	endThreshold = (listening.endThreshold + 0.15f).coerceAtMost(listening.startThreshold),
	minimumSpeechMilliseconds = maxOf(listening.minimumSpeechMilliseconds, 320),
)

enum class CommitReason {
    SILENCE,
    MAXIMUM_DURATION,
    EXPLICIT,
}

sealed interface UtteranceEvent {
    data class Started(val frames: List<ShortArray>) : UtteranceEvent

    data class Continued(val frame: ShortArray) : UtteranceEvent

    data class Committed(val reason: CommitReason) : UtteranceEvent
}

/** Converts PCM frames and VAD probabilities into streamable utterance events. */
class UtteranceSegmenter(private val config: EndpointConfig = EndpointConfig()) {
    private val preRoll = ArrayDeque<ShortArray>(config.preRollFrames)
    private var consecutiveSpeechFrames = 0
    private var consecutiveSilenceFrames = 0
    private var utteranceFrames = 0
    private var active = false

    fun accept(samples: ShortArray, result: VadResult): List<UtteranceEvent> {
        require(samples.size == config.frameSamples) {
            "endpointing requires ${config.frameSamples} samples, got ${samples.size}"
        }
        val frame = samples.copyOf()

        if (!active) {
            appendPreRoll(frame)
            consecutiveSpeechFrames = if (result.speechProbability >= config.startThreshold) {
                consecutiveSpeechFrames + 1
            } else {
                0
            }
            if (consecutiveSpeechFrames < config.minimumSpeechFrames) return emptyList()

            active = true
            consecutiveSilenceFrames = 0
            utteranceFrames = preRoll.size
            val initialFrames = preRoll.map { it.copyOf() }
            preRoll.clear()
            return listOf(UtteranceEvent.Started(initialFrames))
        }

        utteranceFrames++
        when {
            result.speechProbability >= config.startThreshold -> consecutiveSilenceFrames = 0
            result.speechProbability <= config.endThreshold -> consecutiveSilenceFrames++
        }

        val events = mutableListOf<UtteranceEvent>(UtteranceEvent.Continued(frame))
        when {
            consecutiveSilenceFrames >= config.endSilenceFrames -> {
                events += UtteranceEvent.Committed(CommitReason.SILENCE)
                resetState()
            }
            utteranceFrames >= config.maximumUtteranceFrames -> {
                events += UtteranceEvent.Committed(CommitReason.MAXIMUM_DURATION)
                resetState()
            }
        }
        return events
    }

    fun commit(): List<UtteranceEvent> {
        if (!active) {
            resetState()
            return emptyList()
        }
        resetState()
        return listOf(UtteranceEvent.Committed(CommitReason.EXPLICIT))
    }

    fun reset() = resetState()

    private fun appendPreRoll(frame: ShortArray) {
        if (config.preRollFrames == 0) return
        while (preRoll.size >= config.preRollFrames) preRoll.removeFirst()
        preRoll.addLast(frame)
    }

    private fun resetState() {
        preRoll.clear()
        consecutiveSpeechFrames = 0
        consecutiveSilenceFrames = 0
        utteranceFrames = 0
        active = false
    }
}

/** Runs model inference and endpointing as one serial audio-stage operation. */
class VadEndpointPipeline(
    private val detector: VoiceActivityDetector,
	segmenter: UtteranceSegmenter = UtteranceSegmenter(
        EndpointConfig(
            sampleRate = detector.sampleRate,
            frameSamples = detector.frameSamples,
        ),
    ),
) : AutoCloseable {
	data class Evaluation(val vad: VadResult, val events: List<UtteranceEvent>)

	private var segmenter = segmenter

	fun configure(config: EndpointConfig) {
		require(config.sampleRate == detector.sampleRate && config.frameSamples == detector.frameSamples)
		detector.reset()
		segmenter = UtteranceSegmenter(config)
	}

	fun evaluate(samples: ShortArray): Evaluation {
		val vad = detector.evaluate(samples)
		return Evaluation(vad, segmenter.accept(samples, vad))
	}

    fun accept(samples: ShortArray): List<UtteranceEvent> = evaluate(samples).events

    fun commit(): List<UtteranceEvent> = segmenter.commit()

    fun reset() {
        detector.reset()
        segmenter.reset()
    }

    override fun close() = detector.close()
}
