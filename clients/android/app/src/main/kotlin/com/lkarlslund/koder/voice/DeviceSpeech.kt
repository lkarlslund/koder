package com.lkarlslund.koder.voice

import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.speech.RecognitionListener
import android.speech.RecognizerIntent
import android.speech.SpeechRecognizer

class DeviceSpeech(context: Context, private val listener: Listener) : AutoCloseable {
    interface Listener {
        fun onPartial(text: String)
        fun onFinal(text: String)
        fun onSpeechError(message: String, recoverable: Boolean)
    }

    private val recognizer = SpeechRecognizer.createSpeechRecognizer(context)
    private var listening = false

    private val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
        putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
        putExtra(RecognizerIntent.EXTRA_PARTIAL_RESULTS, true)
        putExtra(RecognizerIntent.EXTRA_MAX_RESULTS, 1)
        putExtra(RecognizerIntent.EXTRA_PREFER_OFFLINE, true)
    }

    init {
        recognizer.setRecognitionListener(object : RecognitionListener {
            override fun onReadyForSpeech(params: Bundle?) = Unit
            override fun onBeginningOfSpeech() = Unit
            override fun onRmsChanged(rmsdB: Float) = Unit
            override fun onBufferReceived(buffer: ByteArray?) = Unit
            override fun onEndOfSpeech() { listening = false }
            override fun onEvent(eventType: Int, params: Bundle?) = Unit

            override fun onPartialResults(results: Bundle?) {
                bestResult(results)?.let(listener::onPartial)
            }

            override fun onResults(results: Bundle?) {
                listening = false
                val text = bestResult(results).orEmpty().trim()
                if (text.isNotEmpty()) listener.onFinal(text) else listener.onSpeechError("No speech recognized", true)
            }

            override fun onError(error: Int) {
                listening = false
                val recoverable = error == SpeechRecognizer.ERROR_NO_MATCH ||
                    error == SpeechRecognizer.ERROR_SPEECH_TIMEOUT ||
                    error == SpeechRecognizer.ERROR_CLIENT
                listener.onSpeechError(errorMessage(error), recoverable)
            }
        })
    }

    fun start() {
        if (listening) return
        listening = true
        recognizer.startListening(intent)
    }

    fun stop() {
        if (!listening) return
        listening = false
        recognizer.cancel()
    }

    override fun close() {
        stop()
        recognizer.destroy()
    }

    private fun bestResult(results: Bundle?): String? =
        results?.getStringArrayList(SpeechRecognizer.RESULTS_RECOGNITION)?.firstOrNull()

    private fun errorMessage(error: Int): String = when (error) {
        SpeechRecognizer.ERROR_AUDIO -> "Speech recognizer audio error"
        SpeechRecognizer.ERROR_INSUFFICIENT_PERMISSIONS -> "Microphone permission is required"
        SpeechRecognizer.ERROR_NETWORK, SpeechRecognizer.ERROR_NETWORK_TIMEOUT -> "Speech recognition network error"
        SpeechRecognizer.ERROR_NO_MATCH -> "No speech recognized"
        SpeechRecognizer.ERROR_RECOGNIZER_BUSY -> "Speech recognizer is busy"
        SpeechRecognizer.ERROR_SERVER -> "Speech recognition service error"
        SpeechRecognizer.ERROR_SPEECH_TIMEOUT -> "Listening"
        else -> "Speech recognition error $error"
    }
}
