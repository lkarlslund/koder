package com.lkarlslund.koder.voice

import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioRouting
import android.media.AudioTrack
import com.lkarlslund.koder.diagnostics.AppLog
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicLong
import kotlin.math.max
import kotlin.math.min

interface StreamingAudioPlayback : AutoCloseable {
    fun start(format: VoiceAudioFormat)
    fun write(pcm: ByteArray)
	fun finish(onComplete: () -> Unit)
    fun stop()
}

class AndroidStreamingAudioPlayback(
	private val onPlaybackChunkQueued: (ByteArray) -> Unit = {},
    private val onError: (String) -> Unit,
) : StreamingAudioPlayback {
    private val executor = Executors.newSingleThreadExecutor { runnable ->
        Thread(runnable, "koder-voice-playback").apply { isDaemon = true }
    }
    private val generation = AtomicLong()
    @Volatile private var track: AudioTrack? = null
	@Volatile private var sampleRate = 0
	@Volatile private var bytesWritten = 0L
	/** When the playback thread entered a blocking write, or 0 when it is not in one. */
	@Volatile private var writeStartedNanos = 0L
	@Volatile private var droppedWithoutTrack = false

    @Synchronized
    override fun start(format: VoiceAudioFormat) {
		reportStuckWrite("start")
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
        if (audioTrack.state != AudioTrack.STATE_INITIALIZED) {
			AppLog.e(LOG_TAG, "AudioTrack did not initialize at ${format.sampleRate} Hz (state ${audioTrack.state})")
			audioTrack.release()
			error("Audio playback initialization failed")
		}
		audioTrack.addOnRoutingChangedListener(AudioRouting.OnRoutingChangedListener { router ->
			val device = router.routedDevice
			AppLog.i(LOG_TAG, "Output routed to ${device?.productName ?: "nothing"} (type ${device?.type ?: "-"})")
		}, null)
		AppLog.d(LOG_TAG, "Track ready: ${format.sampleRate} Hz, buffer ${audioTrack.bufferSizeInFrames} frames")
		droppedWithoutTrack = false
        track = audioTrack
		sampleRate = format.sampleRate
		bytesWritten = 0
        val currentGeneration = generation.incrementAndGet()
        executor.execute {
            if (generation.get() == currentGeneration && track === audioTrack) {
				try {
					audioTrack.play()
				} catch (error: IllegalStateException) {
					AppLog.e(LOG_TAG, "AudioTrack.play failed", error)
					throw error
				}
				if (audioTrack.playState != AudioTrack.PLAYSTATE_PLAYING) AppLog.w(LOG_TAG, "Track not playing after play(): state ${audioTrack.playState}")
			}
        }
    }

    override fun write(pcm: ByteArray) {
        if (pcm.isEmpty()) return
        val audioTrack = track ?: run {
			if (!droppedWithoutTrack) {
				droppedWithoutTrack = true
				AppLog.w(LOG_TAG, "Dropping ${pcm.size} bytes of speech: no playback track (not started, or already finished)")
			}
			return
		}
        val currentGeneration = generation.get()
        val copy = pcm.copyOf()
        executor.execute {
            if (generation.get() != currentGeneration || track !== audioTrack) return@execute
            var offset = 0
            while (offset < copy.size) {
				val startedAt = System.nanoTime()
				writeStartedNanos = startedAt
                val written = try {
					audioTrack.write(copy, offset, copy.size - offset, AudioTrack.WRITE_BLOCKING)
				} finally {
					writeStartedNanos = 0
				}
				val tookMillis = (System.nanoTime() - startedAt) / 1_000_000
				if (tookMillis >= SLOW_WRITE_MILLIS) {
					AppLog.w(LOG_TAG, "AudioTrack.write blocked ${tookMillis} ms (playState ${audioTrack.playState}, head ${audioTrack.playbackHeadPosition})")
				}
                if (written < 0) {
					AppLog.e(LOG_TAG, "AudioTrack.write returned $written (playState ${audioTrack.playState})")
                    onError("Audio playback failed ($written)")
                    return@execute
                }
                offset += written
				bytesWritten += written
            }
			if (generation.get() == currentGeneration && track === audioTrack) onPlaybackChunkQueued(copy)
        }
    }

	override fun finish(onComplete: () -> Unit) {
		val audioTrack = track
		val currentGeneration = generation.get()
		executor.execute {
			if (audioTrack != null && generation.get() == currentGeneration && track === audioTrack) {
				val target = bytesWritten / PCM16_MONO_FRAME_BYTES
				awaitPlaybackDrain(audioTrack, target, sampleRate) {
					generation.get() == currentGeneration && track === audioTrack
				}
				val played = audioTrack.playbackHeadPosition.toLong() and UINT32_MASK
				if (played < target) AppLog.w(LOG_TAG, "Playback drain ended at frame $played of $target (playState ${audioTrack.playState})")
				if (audioTrack.underrunCount > 0) AppLog.d(LOG_TAG, "Playback had ${audioTrack.underrunCount} underruns")
				if (generation.get() == currentGeneration && track === audioTrack) {
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
			}
			onComplete()
		}
	}

	private fun awaitPlaybackDrain(audioTrack: AudioTrack, targetFrames: Long, rate: Int, current: () -> Boolean) {
		if (targetFrames <= 0 || rate <= 0 || audioTrack.playState != AudioTrack.PLAYSTATE_PLAYING) return
		val startedAt = System.nanoTime()
		val initialHead = audioTrack.playbackHeadPosition.toLong() and UINT32_MASK
		var previousHead = initialHead
		var lastProgressAt = startedAt
		val remaining = (targetFrames - initialHead).coerceAtLeast(0)
		val timeoutNanos = ((remaining * NANOS_PER_SECOND) / rate + DRAIN_GRACE_NANOS).coerceAtMost(MAX_DRAIN_NANOS)
		while (audioTrack.playState == AudioTrack.PLAYSTATE_PLAYING) {
			if (!current()) return
			val played = audioTrack.playbackHeadPosition.toLong() and UINT32_MASK
			val now = System.nanoTime()
			if (played > previousHead) {
				previousHead = played
				lastProgressAt = now
			}
			if (played >= targetFrames || now - startedAt >= timeoutNanos || playbackDrainStalled(now, lastProgressAt)) return
			val remainingMillis = ((targetFrames - played).coerceAtLeast(1) * 1_000L / rate).coerceAtLeast(1)
			try {
				Thread.sleep(min(10L, remainingMillis))
			} catch (_: InterruptedException) {
				Thread.currentThread().interrupt()
				return
			}
		}
	}

    @Synchronized
    override fun stop() {
		reportStuckWrite("stop")
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

	/** Notes a blocking write that never returned: everything queued behind it, including this call, waits. */
	private fun reportStuckWrite(caller: String) {
		val since = writeStartedNanos
		if (since == 0L) return
		val stuckMillis = (System.nanoTime() - since) / 1_000_000
		if (stuckMillis >= SLOW_WRITE_MILLIS) {
			AppLog.e(LOG_TAG, "Playback thread stuck in AudioTrack.write for $stuckMillis ms; $caller is queued behind it")
		}
	}

	private companion object {
		const val PCM16_MONO_FRAME_BYTES = 2
		const val UINT32_MASK = 0xffff_ffffL
		const val NANOS_PER_SECOND = 1_000_000_000L
		const val DRAIN_GRACE_NANOS = 500_000_000L
		const val MAX_DRAIN_NANOS = 60_000_000_000L
		const val SLOW_WRITE_MILLIS = 2_000L
		const val LOG_TAG = "playback"
	}
}

internal fun playbackDrainStalled(nowNanos: Long, lastProgressNanos: Long): Boolean =
	nowNanos - lastProgressNanos >= PLAYBACK_DRAIN_STALL_NANOS

private const val PLAYBACK_DRAIN_STALL_NANOS = 1_500_000_000L
