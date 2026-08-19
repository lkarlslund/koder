package com.lkarlslund.koder.voice

fun muteControlLabel(muted: Boolean): String = if (muted) "Unmute" else "Mute"

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
