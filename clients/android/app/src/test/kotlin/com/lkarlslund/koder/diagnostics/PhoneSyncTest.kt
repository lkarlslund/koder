package com.lkarlslund.koder.diagnostics

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class PhoneSyncTest {
	@Test
	fun requestCarriesLogsAndAlertReports() {
		val logs = listOf(AppLogEntry(7, Instant.parse("2026-10-21T10:00:00.250Z"), "W", "popup", "Speech audio arrived out of order"))
		val reports = listOf(PhoneAlert("alert-1", "Tea", "", 0, PhoneAlert.SCHEDULED, "exact"))
		val root = JSONObject(PhoneSyncProtocol.request("epoch-1", logs, reports))
		assertEquals("voice.v1", root.getString("protocol"))
		assertEquals("epoch-1", root.getString("log_epoch"))
		val line = root.getJSONArray("logs").getJSONObject(0)
		assertEquals(7, line.getLong("seq"))
		assertEquals("2026-10-21T10:00:00.250Z", line.getString("time"))
		assertEquals("popup", line.getString("tag"))
		val report = root.getJSONArray("alerts").getJSONObject(0)
		assertEquals("alert-1", report.getString("id"))
		assertEquals("scheduled", report.getString("status"))
		assertEquals("exact", report.getString("detail"))
	}

	@Test
	fun responseParsesAlertsAndSkipsBadOnes() {
		val response = PhoneSyncProtocol.response(
			"""{"protocol":"voice.v1","logs_enabled":true,"log_cursor":41,"alerts":[{"id":"a1","title":"Stretch","body":"Stand up","fire_at":"2026-10-21T15:00:00Z"},{"id":"a2","title":"Bad","fire_at":"tomorrow"}],"cancel_alerts":["a0"]}""",
		)
		assertTrue(response.logsEnabled)
		assertEquals(41, response.logCursor)
		assertEquals(listOf("a1"), response.alerts.map { it.id })
		assertEquals(Instant.parse("2026-10-21T15:00:00Z").toEpochMilli(), response.alerts[0].fireAtMillis)
		assertEquals(listOf("a0"), response.cancelAlerts)
	}

	private fun canonical(value: Any?): Any? = when (value) {
		is JSONObject -> value.keys().asSequence().sorted().associateWith { canonical(value.get(it)) }
		is JSONArray -> (0 until value.length()).map { canonical(value.get(it)) }
		is Number -> value.toLong()
		else -> value
	}

	private fun fixture(name: String) = checkNotNull(javaClass.getResourceAsStream("/$name")).bufferedReader().use { it.readText() }

	@Test
	fun requestMatchesSharedGoFixture() {
		val request = PhoneSyncProtocol.request(
			"epoch-fixture-1",
			listOf(
				AppLogEntry(1, Instant.parse("2026-10-21T10:00:00.250Z"), "W", "popup", "Speech audio arrived out of order"),
				AppLogEntry(2, Instant.parse("2026-10-21T10:00:01Z"), "E", "playback", "Playback thread stuck in AudioTrack.write for 9000 ms; stop is queued behind it"),
			),
			listOf(PhoneAlert("alert-fixture-1", "", "", 0, PhoneAlert.SCHEDULED, "exact")),
		)
		assertEquals(canonical(JSONObject(fixture("device_sync_request.json"))), canonical(JSONObject(request)))
	}

	@Test
	fun responseMatchesSharedGoFixture() {
		val response = PhoneSyncProtocol.response(fixture("device_sync_response.json"))
		assertEquals(2, response.logCursor)
		assertEquals("Stand up for a minute", response.alerts.single().body)
		assertEquals(listOf("alert-fixture-0"), response.cancelAlerts)
	}

	@Test(expected = IllegalArgumentException::class)
	fun responseRejectsOtherProtocols() {
		PhoneSyncProtocol.response("""{"protocol":"voice.v0"}""")
	}

	@Test
	fun logcatKeepsNewerLinesWithTheirOwnTime() {
		val lines = listOf(
			"--------- beginning of main",
			"1729504800.100  4242  4300 W AudioTrack: restoreTrack_l(): dead IAudioTrack, PCM, creating a new one",
			"1729504801.500  4242  4301 E AudioTrack: obtainBuffer timed out (is the CPU pegged?)",
			"1729504802.000  4242  4301 D OkHttp: --> POST",
		)
		val parsed = PhoneSyncProtocol.logcat(lines, afterMillis = 1729504800100)
		assertEquals(2, parsed.size)
		assertEquals(Triple(1729504801500, "E", "AudioTrack: obtainBuffer timed out (is the CPU pegged?)"), parsed[0])
		assertEquals("D", parsed[1].second)
	}
}

class AlertBookTest {
	private fun alert(id: String) = PhoneAlert(id, "Title $id", "", 1_000)

	@Test
	fun acceptsEachAlertOnceAndReportsStatusChanges() {
		val book = AlertBook()
		assertEquals(listOf("a1"), book.accept(listOf(alert("a1"))).map { it.id })
		assertEquals(emptyList<PhoneAlert>(), book.accept(listOf(alert("a1"))))
		book.mark("a1", PhoneAlert.SCHEDULED, "exact")
		val sent = book.unreported()
		assertEquals(listOf("exact"), sent.map { it.detail })
		book.mark("a1", PhoneAlert.FIRED)
		book.reported(sent)
		assertEquals("a change after sending must still be reported", listOf(PhoneAlert.FIRED), book.unreported().map { it.status })
		book.reported(book.unreported())
		assertTrue(book.unreported().isEmpty())
		assertTrue(book.armed().isEmpty())
	}

	@Test
	fun survivesStorageAndPrunesOldFinishedAlerts() {
		val book = AlertBook()
		book.accept(listOf(alert("a1"), alert("a2")))
		book.mark("a1", PhoneAlert.FIRED, nowMillis = 0)
		val stored = AlertBook(book.toJSON())
		assertEquals(listOf("a2"), stored.armed().map { it.id })
		stored.reported(stored.unreported(), nowMillis = 8L * 24 * 60 * 60 * 1000)
		assertEquals(null, stored["a1"])
		assertFalse(stored["a2"] == null)
	}

	@Test
	fun ignoresCorruptStorage() {
		assertTrue(AlertBook("not json").armed().isEmpty())
	}
}
