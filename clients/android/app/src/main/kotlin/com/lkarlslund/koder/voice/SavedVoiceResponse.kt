package com.lkarlslund.koder.voice

enum class SavedVoiceResponseKind(val wireValue: String, val label: String) {
	BOOKMARK("bookmark", "Bookmarked"),
	FOLLOW_UP("follow_up", "Follow up");

	companion object {
		fun fromWire(value: String): SavedVoiceResponseKind? = entries.firstOrNull { it.wireValue == value }
	}
}

data class SavedVoiceResponse(
	val sessionId: String,
	val messageId: String,
	val text: String,
	val kind: SavedVoiceResponseKind,
	val savedAtMillis: Long = System.currentTimeMillis(),
)
