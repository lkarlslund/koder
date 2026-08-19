package com.lkarlslund.koder.voice

fun muteControlLabel(muted: Boolean): String = if (muted) "Unmute" else "Mute"

fun primaryVoiceControlLabel(stage: CallController.Stage?, currentlyActive: Boolean): String = when (stage) {
	CallController.Stage.ERROR -> "Retry"
	CallController.Stage.DISCONNECTED -> "Resume"
	null -> if (currentlyActive) "Pause" else "Resume"
	else -> "Pause"
}
