package com.lkarlslund.koder.voice

import android.annotation.SuppressLint
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.MediaRecorder
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.concurrent.thread
import kotlin.math.max

interface MicrophoneCapture : AutoCloseable {
    interface Listener {
        fun onFrame(samples: ShortArray)
        fun onCaptureError(message: String)
    }

    fun start(format: VoiceAudioFormat, frameSamples: Int, listener: Listener)
    fun stop()
}

class AndroidMicrophoneCapture : MicrophoneCapture {
    private val running = AtomicBoolean(false)
    private var record: AudioRecord? = null
    private var captureThread: Thread? = null

    @SuppressLint("MissingPermission")
    @Synchronized
    override fun start(format: VoiceAudioFormat, frameSamples: Int, listener: MicrophoneCapture.Listener) {
        stop()
        require(format.encoding == "pcm_s16le" && format.channels == 1) {
            "Microphone requires mono pcm_s16le"
        }
        require(frameSamples > 0)
        val channel = AudioFormat.CHANNEL_IN_MONO
        val minimum = AudioRecord.getMinBufferSize(format.sampleRate, channel, AudioFormat.ENCODING_PCM_16BIT)
        check(minimum > 0) { "Android did not provide a microphone buffer size" }
        val audioRecord = AudioRecord.Builder()
            .setAudioSource(MediaRecorder.AudioSource.VOICE_COMMUNICATION)
            .setAudioFormat(
                AudioFormat.Builder()
                    .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                    .setSampleRate(format.sampleRate)
                    .setChannelMask(channel)
                    .build(),
            )
            .setBufferSizeInBytes(max(minimum * 2, frameSamples * 2 * 4))
            .build()
        check(audioRecord.state == AudioRecord.STATE_INITIALIZED) { "Microphone initialization failed" }
        audioRecord.startRecording()
        record = audioRecord
        running.set(true)
        captureThread = thread(name = "koder-voice-capture", isDaemon = true) {
            val frame = ShortArray(frameSamples)
            try {
                while (running.get()) {
                    var offset = 0
                    while (running.get() && offset < frame.size) {
                        val count = audioRecord.read(frame, offset, frame.size - offset, AudioRecord.READ_BLOCKING)
                        if (count < 0) error("Microphone read failed ($count)")
                        offset += count
                    }
                    if (running.get() && offset == frame.size) listener.onFrame(frame.copyOf())
                }
            } catch (error: Exception) {
                if (running.getAndSet(false)) {
                    listener.onCaptureError(error.message ?: "Microphone capture failed")
                }
            }
        }
    }

    @Synchronized
    override fun stop() {
        running.set(false)
        val current = record
        record = null
        try {
            current?.stop()
        } catch (_: IllegalStateException) {
            // The recorder can already be stopped during audio-route changes.
        }
        current?.release()
        captureThread = null
    }

    override fun close() = stop()
}
