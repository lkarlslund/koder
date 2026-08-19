package com.lkarlslund.koder.voice

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ConversationHistoryTest {
	@Test
	fun tracksWhetherNewMessagesShouldFollowTheBottom() {
		assertTrue(isNearConversationBottom(contentHeight = 1_000, scrollY = 500, viewportHeight = 480, threshold = 48))
		assertFalse(isNearConversationBottom(contentHeight = 1_000, scrollY = 300, viewportHeight = 480, threshold = 48))
	}

	@Test
	fun prependsOlderHistoryWithoutDuplicatingTheCursorEdge() {
		val older = listOf(entry("one"), entry("two"))
		val current = listOf(entry("two"), entry("three"))
		assertEquals(listOf("one", "two", "three"), mergeTranscriptHistory(older, current).map { it.id })
	}

	private fun entry(id: String) = VoiceTranscriptEntry(id, "user", id)
}
