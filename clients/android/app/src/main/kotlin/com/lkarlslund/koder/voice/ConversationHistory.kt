package com.lkarlslund.koder.voice

fun isNearConversationBottom(contentHeight: Int, scrollY: Int, viewportHeight: Int, threshold: Int): Boolean =
	contentHeight-scrollY-viewportHeight <= threshold

fun latestConversationLabel(unreadMessages: Int): String = when (unreadMessages) {
	0 -> "Latest"
	1 -> "Latest · 1 new"
	else -> "Latest · $unreadMessages new"
}

fun mergeTranscriptHistory(
	older: List<VoiceTranscriptEntry>,
	current: List<VoiceTranscriptEntry>,
): List<VoiceTranscriptEntry> = (older + current).distinctBy(VoiceTranscriptEntry::id)
