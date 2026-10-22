package com.lkarlslund.koder.diagnostics

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class AppLogTest {
	@Test
	fun logcatKeepsNewerLinesWithTheirOwnTime() {
		val lines = listOf(
			"--------- beginning of main",
			"1729504800.100  4242  4300 W AudioTrack: restoreTrack_l(): dead IAudioTrack, PCM, creating a new one",
			"1729504801.500  4242  4301 E AudioTrack: obtainBuffer timed out (is the CPU pegged?)",
			"1729504802.000  4242  4301 D OkHttp: --> POST",
		)
		val parsed = AppLog.parseLogcat(lines, afterMillis = 1729504800100)
		assertEquals(2, parsed.size)
		assertEquals(Triple(1729504801500, "E", "AudioTrack: obtainBuffer timed out (is the CPU pegged?)"), parsed[0])
		assertEquals("D", parsed[1].second)
	}

	@Test
	fun filterMatchesTimeLevelTagAndText() {
		val entry = AppLogEntry(1, Instant.ofEpochMilli(10_000), "W", "audio", "Playback stalled")
		assertTrue(AppLogFilter().matches(entry))
		assertTrue(AppLogFilter(sinceMillis = 10_000, minLevel = "W", tag = "audio", query = "STALLED").matches(entry))
		assertFalse(AppLogFilter(sinceMillis = 10_001).matches(entry))
		assertFalse(AppLogFilter(minLevel = "E").matches(entry))
		assertFalse(AppLogFilter(tag = "aud").matches(entry))
		assertTrue("query also searches the tag", AppLogFilter(query = "aud").matches(entry))
		assertFalse(AppLogFilter(query = "crash").matches(entry))
	}
}

class AlertBookTest {
	private fun alert(id: String, fireAt: Long = 1_000) = PhoneAlert(id, "Title $id", "", fireAt)

	@Test
	fun tracksStatusAndListsByFireTime() {
		val book = AlertBook()
		book.add(alert("a2", fireAt = 2_000))
		book.add(alert("a1", fireAt = 1_000))
		book.mark("a1", PhoneAlert.FIRED, nowMillis = 5_000)
		assertEquals(listOf("a2"), book.armed().map { it.id })
		assertEquals(listOf("a1", "a2"), book.all(nowMillis = 5_000).map { it.id })
	}

	@Test
	fun survivesStorageAndPrunesOldFinishedAlerts() {
		val book = AlertBook()
		book.add(alert("a1"))
		book.add(alert("a2"))
		book.mark("a1", PhoneAlert.FIRED, nowMillis = 0)
		val stored = AlertBook(book.toJSON())
		assertEquals(listOf("a2"), stored.armed().map { it.id })
		assertEquals(listOf("a2"), stored.all(nowMillis = 8L * 24 * 60 * 60 * 1000).map { it.id })
	}

	@Test
	fun ignoresCorruptStorage() {
		assertTrue(AlertBook("not json").armed().isEmpty())
	}
}
