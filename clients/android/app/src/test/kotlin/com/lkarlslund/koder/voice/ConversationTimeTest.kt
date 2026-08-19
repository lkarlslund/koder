package com.lkarlslund.koder.voice

import java.time.Instant
import java.time.ZoneId
import org.junit.Assert.assertEquals
import org.junit.Test

class ConversationTimeTest {
	private val utc = ZoneId.of("UTC")

	@Test
	fun sameDayUsesCompactTime() {
		assertEquals(
			"09:07",
			conversationTimeLabel(Instant.parse("2026-08-19T09:07:00Z"), Instant.parse("2026-08-19T12:00:00Z"), utc),
		)
	}

	@Test
	fun olderMessageIncludesDate() {
		assertEquals(
			"Aug 18, 23:40",
			conversationTimeLabel(Instant.parse("2026-08-18T23:40:00Z"), Instant.parse("2026-08-19T12:00:00Z"), utc),
		)
	}

	@Test
	fun missingTimestampStaysUnlabelled() {
		assertEquals("", conversationTimeLabel(null, zone = utc))
	}
}
