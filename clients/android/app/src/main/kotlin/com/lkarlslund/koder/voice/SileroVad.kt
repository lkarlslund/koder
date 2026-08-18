package com.lkarlslund.koder.voice

import android.content.Context
import ai.onnxruntime.OnnxTensor
import ai.onnxruntime.OrtEnvironment
import ai.onnxruntime.OrtSession
import java.nio.FloatBuffer
import java.nio.LongBuffer

/**
 * Stateful Silero VAD inference for mono 16 kHz PCM16 audio.
 *
 * Calls must be serialized in audio order. [reset] starts a new independent
 * audio stream by clearing the recurrent state and input context.
 */
class SileroVad private constructor(
    private val environment: OrtEnvironment,
    private val session: OrtSession,
) : VoiceActivityDetector {
    override val sampleRate: Int = SAMPLE_RATE
    override val frameSamples: Int = FRAME_SAMPLES

    private val context = FloatArray(CONTEXT_SAMPLES)
    private val state = FloatArray(STATE_SIZE)
    private var closed = false

    override fun evaluate(samples: ShortArray): VadResult {
        check(!closed) { "Silero VAD is closed" }
        require(samples.size == frameSamples) {
            "Silero VAD requires $frameSamples samples, got ${samples.size}"
        }

        val input = FloatArray(INPUT_SAMPLES)
        context.copyInto(input)
        for (index in samples.indices) {
            input[CONTEXT_SAMPLES + index] = samples[index] / PCM_SCALE
        }

        OnnxTensor.createTensor(
            environment,
            FloatBuffer.wrap(input),
            longArrayOf(1, INPUT_SAMPLES.toLong()),
        ).use { inputTensor ->
            OnnxTensor.createTensor(
                environment,
                FloatBuffer.wrap(state),
                longArrayOf(2, 1, STATE_WIDTH.toLong()),
            ).use { stateTensor ->
                OnnxTensor.createTensor(
                    environment,
                    LongBuffer.wrap(longArrayOf(sampleRate.toLong())),
                    longArrayOf(1),
                ).use { sampleRateTensor ->
                    session.run(
                        mapOf(
                            "input" to inputTensor,
                            "state" to stateTensor,
                            "sr" to sampleRateTensor,
                        ),
                    ).use { result ->
                        val probabilityTensor = result.get("output").orElseThrow {
                            IllegalStateException("Silero output tensor is missing")
                        } as OnnxTensor
                        val nextStateTensor = result.get("stateN").orElseThrow {
                            IllegalStateException("Silero state output tensor is missing")
                        } as OnnxTensor

                        val probability = probabilityTensor.floatBuffer.get(0)
                        val nextState = nextStateTensor.floatBuffer
                        check(nextState.remaining() == state.size) {
                            "Silero returned ${nextState.remaining()} state values, want ${state.size}"
                        }
                        nextState.get(state)
                        input.copyInto(
                            destination = context,
                            startIndex = input.size - context.size,
                        )
                        return VadResult(probability.coerceIn(0.0f, 1.0f))
                    }
                }
            }
        }
    }

    override fun reset() {
        check(!closed) { "Silero VAD is closed" }
        context.fill(0.0f)
        state.fill(0.0f)
    }

    override fun close() {
        if (closed) return
        closed = true
        session.close()
    }

    companion object {
        const val SAMPLE_RATE = 16_000
        const val FRAME_SAMPLES = 512
        const val FRAME_MILLISECONDS = 32
        private const val CONTEXT_SAMPLES = 64
        private const val INPUT_SAMPLES = FRAME_SAMPLES + CONTEXT_SAMPLES
        private const val STATE_WIDTH = 128
        private const val STATE_SIZE = 2 * STATE_WIDTH
        private const val PCM_SCALE = 32_768.0f
        private const val MODEL_ASSET = "silero_vad.onnx"

        fun fromAssets(context: Context): SileroVad {
            val model = context.assets.open(MODEL_ASSET).use { it.readBytes() }
            val environment = OrtEnvironment.getEnvironment()
            val options = OrtSession.SessionOptions().apply {
                setInterOpNumThreads(1)
                setIntraOpNumThreads(1)
                setOptimizationLevel(OrtSession.SessionOptions.OptLevel.ALL_OPT)
            }
            return try {
                SileroVad(environment, environment.createSession(model, options))
            } finally {
                options.close()
            }
        }
    }
}
