package com.lkarlslund.koder.voice

import android.content.Context
import android.media.AudioAttributes
import android.os.Bundle
import android.speech.tts.TextToSpeech
import android.speech.tts.UtteranceProgressListener
import java.util.Locale
import java.util.UUID

class DeviceTts(
    context: Context,
    private val doneCallback: () -> Unit,
    private val errorCallback: (String) -> Unit,
) :
    AutoCloseable {
    private var ready = false
    private var pending: String? = null
    private lateinit var tts: TextToSpeech

    init {
        tts = TextToSpeech(context) { status ->
        ready = status == TextToSpeech.SUCCESS
        if (ready) {
            tts.language = Locale.getDefault()
            tts.setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                    .build(),
            )
            pending?.let {
                pending = null
                speak(it)
            }
        } else {
            errorCallback("Text-to-speech is unavailable")
        }
        }
        tts.setOnUtteranceProgressListener(object : UtteranceProgressListener() {
            override fun onStart(utteranceId: String?) = Unit
            override fun onDone(utteranceId: String?) = doneCallback()
            @Deprecated("Deprecated in Java")
            override fun onError(utteranceId: String?) = errorCallback("Text-to-speech playback failed")
        })
    }

    fun speak(text: String) {
        val normalized = text.trim()
        if (normalized.isEmpty()) {
            doneCallback()
            return
        }
        if (!ready) {
            pending = normalized
            return
        }
        tts.speak(normalized, TextToSpeech.QUEUE_FLUSH, Bundle(), UUID.randomUUID().toString())
    }

    fun stop() {
        pending = null
        tts.stop()
    }

    override fun close() {
        stop()
        tts.shutdown()
    }
}
