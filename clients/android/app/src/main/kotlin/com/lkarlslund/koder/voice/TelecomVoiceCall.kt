package com.lkarlslund.koder.voice

import android.content.Context
import android.net.Uri
import android.telecom.DisconnectCause
import androidx.core.telecom.CallAttributesCompat
import androidx.core.telecom.CallControlScope
import androidx.core.telecom.CallEndpointCompat
import androidx.core.telecom.CallsManager
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch

/** Lets Android own call audio routing, including Bluetooth, wired headsets, and the earpiece. */
class TelecomVoiceCall(context: Context, private val listener: Listener) : AutoCloseable {
    interface Listener {
        fun onCallReady()
        fun onCallHeld(held: Boolean)
        fun onAudioEndpoint(name: String)
        fun onCallEnded()
        fun onTelecomUnavailable(message: String)
    }

    private val callsManager = CallsManager(context.applicationContext)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var callJob: Job? = null
    private var control: CallControlScope? = null
    private var ending = false

    fun start() {
        if (callJob != null) return
        ending = false
        try {
            callsManager.registerAppWithTelecom(CallsManager.CAPABILITY_BASELINE)
        } catch (error: Exception) {
            listener.onTelecomUnavailable(error.message ?: "Android audio routing is unavailable")
            listener.onCallReady()
            return
        }

        callJob = scope.launch {
            try {
                callsManager.addCall(
                    callAttributes = CallAttributesCompat(
                        displayName = "Koder",
                        address = Uri.parse("sip:koder-voice"),
                        direction = CallAttributesCompat.DIRECTION_OUTGOING,
                        callType = CallAttributesCompat.CALL_TYPE_AUDIO_CALL,
                        callCapabilities = CallAttributesCompat.SUPPORTS_SET_INACTIVE,
                    ),
                    onAnswer = { listener.onCallHeld(false) },
                    onDisconnect = {
                        ending = true
                        listener.onCallEnded()
                    },
                    onSetActive = { listener.onCallHeld(false) },
                    onSetInactive = { listener.onCallHeld(true) },
                ) {
                    control = this
                    launch {
                        currentCallEndpoint.collectLatest { endpoint ->
                            listener.onAudioEndpoint(endpoint.description())
                        }
                    }
                    launch {
                        setActive()
                        listener.onCallReady()
                    }
                    launch { awaitCancellation() }
                }
            } catch (_: CancellationException) {
                // Normal local hang-up.
            } catch (error: Exception) {
                if (!ending) {
                    listener.onTelecomUnavailable(error.message ?: "Android audio routing failed")
                    listener.onCallReady()
                }
            } finally {
                control = null
                callJob = null
            }
        }
    }

    fun disconnect() {
        if (ending) return
        ending = true
        val activeControl = control
        if (activeControl == null) {
            callJob?.cancel()
            callJob = null
            listener.onCallEnded()
            return
        }
        scope.launch {
            activeControl.disconnect(DisconnectCause(DisconnectCause.LOCAL))
            callJob?.cancel()
            listener.onCallEnded()
        }
    }

    override fun close() {
        ending = true
        callJob?.cancel()
        callJob = null
        scope.cancel()
    }

    private fun CallEndpointCompat.description(): String = when (type) {
        CallEndpointCompat.TYPE_BLUETOOTH -> "Bluetooth: $name"
        CallEndpointCompat.TYPE_WIRED_HEADSET -> "Headset: $name"
        CallEndpointCompat.TYPE_SPEAKER -> "Speaker"
        CallEndpointCompat.TYPE_EARPIECE -> "Phone earpiece"
        CallEndpointCompat.TYPE_STREAMING -> "Stream: $name"
        else -> name.toString()
    }
}
