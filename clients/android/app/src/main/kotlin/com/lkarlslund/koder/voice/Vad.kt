package com.lkarlslund.koder.voice

/** Result from evaluating one fixed-size PCM frame. */
data class VadResult(val speechProbability: Float) {
    init {
        require(speechProbability.isFinite() && speechProbability in 0.0f..1.0f) {
            "speech probability must be finite and between 0 and 1"
        }
    }
}

/** Evaluates mono PCM16 frames in chronological order. */
interface VoiceActivityDetector : AutoCloseable {
    val sampleRate: Int
    val frameSamples: Int

    fun evaluate(samples: ShortArray): VadResult

    fun reset()

    override fun close() = Unit
}
