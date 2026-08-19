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

/** Lets Android own call audio routing while applying Koder's preferred endpoint. */
class TelecomVoiceCall(context: Context, private val listener: Listener) : AutoCloseable {
	interface Listener {
		fun onCallReady()
		fun onCallHeld(held: Boolean)
		fun onAudioEndpoint(name: String)
		fun onAudioEndpoints(currentName: String, endpoints: List<VoiceAudioEndpoint>) = Unit
		fun onCallEnded()
		fun onTelecomUnavailable(message: String)
	}

	private val callsManager = CallsManager(context.applicationContext)
	private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
	private var callJob: Job? = null
	private var routeJob: Job? = null
	private var control: CallControlScope? = null
	private var ending = false
	private var builtInRoute = BuiltInAudioRoute.SPEAKER
	private var availableEndpoints = emptyList<CallEndpointCompat>()
	private var currentEndpoint: CallEndpointCompat? = null
	private var manualEndpointId: String? = null

	fun setBuiltInAudioRoute(route: BuiltInAudioRoute) {
		builtInRoute = route
		manualEndpointId = null
		applyPreferredRoute()
	}

	fun selectAudioEndpoint(endpointId: String) {
		manualEndpointId = endpointId.takeIf { id -> availableEndpoints.any { it.identifier.toString() == id } }
		applyPreferredRoute()
	}

	fun setHeld(held: Boolean) {
		val callControl = control ?: return
		scope.launch {
			if (held) callControl.setInactive() else callControl.setActive()
		}
	}

	fun start() {
		if (callJob != null) return
		ending = false
		manualEndpointId = null
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
					onDisconnect = { ending = true; listener.onCallEnded() },
					onSetActive = { listener.onCallHeld(false) },
					onSetInactive = { listener.onCallHeld(true) },
				) {
					control = this
					launch {
						currentCallEndpoint.collectLatest { endpoint ->
							currentEndpoint = endpoint
							listener.onAudioEndpoint(endpoint.description())
							publishEndpoints()
						}
					}
					launch {
						availableEndpoints.collectLatest { endpoints ->
							this@TelecomVoiceCall.availableEndpoints = endpoints
							if (manualEndpointId != null && endpoints.none { it.identifier.toString() == manualEndpointId }) {
								manualEndpointId = null
							}
							publishEndpoints()
							applyPreferredRoute()
						}
					}
					launch { setActive(); listener.onCallReady() }
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
				routeJob?.cancel()
				routeJob = null
				availableEndpoints = emptyList()
				currentEndpoint = null
				manualEndpointId = null
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
		routeJob?.cancel()
		scope.cancel()
	}

	private fun applyPreferredRoute() {
		val callControl = control ?: return
		val target = preferredEndpoint() ?: return
		if (target.identifier == currentEndpoint?.identifier) return
		routeJob?.cancel()
		routeJob = scope.launch {
			try {
				callControl.requestEndpointChange(target)
			} catch (error: CancellationException) {
				throw error
			} catch (_: Exception) {
				// An endpoint can vanish between the availability event and this
				// request. Clear a stale manual choice and retry the best route
					// from the latest endpoint snapshot once.
				if (manualEndpointId == target.identifier.toString()) manualEndpointId = null
				val fallback = preferredEndpoint()
				if (fallback != null && fallback.identifier != target.identifier && fallback.identifier != currentEndpoint?.identifier) {
					runCatching { callControl.requestEndpointChange(fallback) }
				}
			}
		}
	}

	private fun preferredEndpoint(): CallEndpointCompat? {
		val selected = preferredAudioEndpoint(
			builtInRoute,
			availableEndpoints.map { endpoint ->
				VoiceAudioEndpoint(
					endpoint.identifier.toString(),
					endpoint.description(),
					endpoint.endpointType(),
					endpoint.identifier == currentEndpoint?.identifier,
				)
			},
			manualEndpointId,
		) ?: return null
		return availableEndpoints.firstOrNull { it.identifier.toString() == selected.id }
	}

	private fun publishEndpoints() {
		val current = currentEndpoint
		listener.onAudioEndpoints(
			current?.description().orEmpty(),
			availableEndpoints.map { endpoint ->
				VoiceAudioEndpoint(
					endpoint.identifier.toString(),
					endpoint.description(),
					endpoint.endpointType(),
					endpoint.identifier == current?.identifier,
				)
			},
		)
	}

	private fun CallEndpointCompat.endpointType(): VoiceAudioEndpointType = when (type) {
		CallEndpointCompat.TYPE_BLUETOOTH -> VoiceAudioEndpointType.BLUETOOTH
		CallEndpointCompat.TYPE_WIRED_HEADSET -> VoiceAudioEndpointType.WIRED_HEADSET
		CallEndpointCompat.TYPE_EARPIECE -> VoiceAudioEndpointType.EARPIECE
		CallEndpointCompat.TYPE_SPEAKER -> VoiceAudioEndpointType.SPEAKER
		else -> VoiceAudioEndpointType.OTHER
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
