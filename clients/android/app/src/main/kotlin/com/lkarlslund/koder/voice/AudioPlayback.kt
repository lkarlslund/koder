package com.lkarlslund.koder.voice

import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioTrack
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicLong
import kotlin.math.max

interface StreamingAudioPlayback : AutoCloseable {
    fun start(format: VoiceAudioFormat)
    fun write(pcm: ByteArray)
	fun finish(onComplete: () -> Unit)
    fun stop()
}

class AndroidStreamingAudioPlayback(
    private val onError: (String) -> Unit,
) : StreamingAudioPlayback {
    private val executor = Executors.newSingleThreadExecutor { runnable ->
        Thread(runnable, "koder-voice-playback").apply { isDaemon = true }
    }
    private val generation = AtomicLong()
    @Volatile private var track: AudioTrack? = null

    @Synchronized
    override fun start(format: VoiceAudioFormat) {
        stop()
        require(format.encoding == "pcm_s16le" && format.channels == 1) {
            "Playback requires mono pcm_s16le"
        }
        val channel = AudioFormat.CHANNEL_OUT_MONO
        val minimum = AudioTrack.getMinBufferSize(format.sampleRate, channel, AudioFormat.ENCODING_PCM_16BIT)
        check(minimum > 0) { "Android did not provide an audio playback buffer size" }
        val audioTrack = AudioTrack.Builder()
            .setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                    .build(),
            )
            .setAudioFormat(
                AudioFormat.Builder()
                    .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                    .setSampleRate(format.sampleRate)
                    .setChannelMask(channel)
                    .build(),
            )
            .setTransferMode(AudioTrack.MODE_STREAM)
            .setBufferSizeInBytes(max(minimum * 2, format.sampleRate / 2))
            .build()
        check(audioTrack.state == AudioTrack.STATE_INITIALIZED) { "Audio playback initialization failed" }
        track = audioTrack
        val currentGeneration = generation.incrementAndGet()
        executor.execute {
            if (generation.get() == currentGeneration && track === audioTrack) audioTrack.play()
        }
    }

    override fun write(pcm: ByteArray) {
        if (pcm.isEmpty()) return
        val audioTrack = track ?: return
        val currentGeneration = generation.get()
        val copy = pcm.copyOf()
        executor.execute {
            if (generation.get() != currentGeneration || track !== audioTrack) return@execute
            var offset = 0
            while (offset < copy.size) {
                val written = audioTrack.write(copy, offset, copy.size - offset, AudioTrack.WRITE_BLOCKING)
                if (written < 0) {
                    onError("Audio playback failed ($written)")
                    return@execute
                }
                offset += written
            }
        }
    }

	override fun finish(onComplete: () -> Unit) {
		val audioTrack = track
		val currentGeneration = generation.get()
		executor.execute {
			if (audioTrack != null && generation.get() == currentGeneration && track === audioTrack) {
				try {
					audioTrack.stop()
				} catch (_: IllegalStateException) {
					// The route may have stopped playback after the final queued write.
				} finally {
					synchronized(this) {
						if (generation.get() == currentGeneration && track === audioTrack) track = null
					}
					audioTrack.release()
				}
			}
			onComplete()
		}
	}

    @Synchronized
    override fun stop() {
        generation.incrementAndGet()
        val current = track
        track = null
        executor.execute {
            try {
                current?.pause()
                current?.flush()
                current?.stop()
            } catch (_: IllegalStateException) {
                // Route changes can stop the track first.
            } finally {
                current?.release()
            }
        }
    }

    override fun close() = stop()
}
