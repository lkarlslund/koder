package com.lkarlslund.koder.voice

fun isNearConversationBottom(contentHeight: Int, scrollY: Int, viewportHeight: Int, threshold: Int): Boolean =
	contentHeight-scrollY-viewportHeight <= threshold

fun mergeTranscriptHistory(
	older: List<VoiceTranscriptEntry>,
	current: List<VoiceTranscriptEntry>,
): List<VoiceTranscriptEntry> = (older + current).distinctBy(VoiceTranscriptEntry::id)
