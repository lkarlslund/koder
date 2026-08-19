package com.lkarlslund.koder.voice

fun muteControlLabel(muted: Boolean): String = if (muted) "Unmute" else "Mute"

enum class ConversationAvailability { CONNECTING, RETRYING, ONLINE, PAUSED, OFFLINE }

fun conversationAvailability(stage: CallController.Stage?, detail: String = ""): ConversationAvailability = when {
	stage == CallController.Stage.CONNECTING && detail.startsWith("Reconnecting", ignoreCase = true) ->
		ConversationAvailability.RETRYING
	stage == CallController.Stage.CONNECTING -> ConversationAvailability.CONNECTING
	stage == CallController.Stage.ERROR -> ConversationAvailability.OFFLINE
	stage == CallController.Stage.DISCONNECTED || stage == null -> ConversationAvailability.PAUSED
	else -> ConversationAvailability.ONLINE
}

fun conversationStatusText(stage: CallController.Stage?, detail: String): String = when (conversationAvailability(stage, detail)) {
	ConversationAvailability.CONNECTING -> detail.ifBlank { "Connecting…" }
	ConversationAvailability.RETRYING -> detail.ifBlank { "Retrying connection…" }
	ConversationAvailability.OFFLINE -> "Offline · ${detail.ifBlank { "connection unavailable" }}"
	ConversationAvailability.PAUSED -> detail.ifBlank { "Conversation paused" }
	ConversationAvailability.ONLINE -> detail
}

fun primaryVoiceControlLabel(stage: CallController.Stage?, currentlyActive: Boolean): String = when (stage) {
	CallController.Stage.ERROR -> "Retry"
	CallController.Stage.DISCONNECTED -> "Resume"
	null -> if (currentlyActive) "Pause" else "Resume"
	else -> "Pause"
}

fun reconnectStatus(reason: String): String = when {
	reason.contains("HTTP 401", ignoreCase = true) || reason.contains("unauthorized", ignoreCase = true) ->
		"Authorization failed · check Settings"
	reason.contains("HTTP 404", ignoreCase = true) -> "Voice endpoint unavailable · reconnecting"
	reason.contains("HTTP 409", ignoreCase = true) || reason.contains("busy", ignoreCase = true) ->
		"Conversation is busy · reconnecting"
	reason.contains("Expected HTTP 101", ignoreCase = true) -> "Voice connection unavailable · reconnecting"
	else -> "Reconnecting · " + reason.substringBefore(" (HTTP").take(72).ifBlank { "connection lost" }
}
