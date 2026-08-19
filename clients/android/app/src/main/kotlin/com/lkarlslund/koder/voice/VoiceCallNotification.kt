package com.lkarlslund.koder.voice

data class VoiceCallNotificationState(
	val detail: String = "Connecting…",
	val muted: Boolean = false,
	val paused: Boolean = false,
	val audioRoute: String = "Phone audio",
)

interface VoiceCallControlTarget {
	fun notificationState(): VoiceCallNotificationState
	fun toggleMuteFromNotification()
	fun togglePauseFromNotification()
	fun cycleAudioRouteFromNotification()
	fun endFromNotification()
}

object VoiceCallControlRegistry {
	@Volatile private var active: VoiceCallControlTarget? = null

	@Synchronized fun attach(target: VoiceCallControlTarget) {
		active = target
	}

	@Synchronized fun detach(target: VoiceCallControlTarget) {
		if (active === target) active = null
	}

	fun target(): VoiceCallControlTarget? = active
}
