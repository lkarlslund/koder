package com.lkarlslund.koder.diagnostics

import android.Manifest
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import android.util.Log
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.lkarlslund.koder.SecureSettings
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.net.InetAddress
import java.time.Instant
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

@RunWith(AndroidJUnit4::class)
class PhoneSyncInstrumentedTest {
	private val context: Context = ApplicationProvider.getApplicationContext()

	@Test
	fun syncUploadsLogsArmsAlertAndFiresIt() {
		if (Build.VERSION.SDK_INT >= 33) {
			InstrumentationRegistry.getInstrumentation().uiAutomation.grantRuntimePermission(context.packageName, Manifest.permission.POST_NOTIFICATIONS)
		}
		context.getSharedPreferences("koder_diagnostics", Context.MODE_PRIVATE).edit().clear().commit()
		context.getSharedPreferences("koder_alerts", Context.MODE_PRIVATE).edit().clear().commit()
		val alertId = "alert-it-${System.nanoTime()}"
		val fireAt = Instant.now().plusSeconds(3600)
		val server = MockWebServer()
		server.enqueue(MockResponse.Builder().body(
			"""{"protocol":"voice.v1","logs_enabled":true,"log_cursor":0,"alerts":[{"id":"$alertId","title":"Stretch","body":"Stand up","fire_at":"$fireAt"}],"cancel_alerts":[]}""",
		).build())
		repeat(4) {
			server.enqueue(MockResponse.Builder().body("""{"protocol":"voice.v1","logs_enabled":true,"log_cursor":999999,"alerts":[],"cancel_alerts":[]}""").build())
		}
		server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		try {
			SecureSettings(context).save(server.url("/").toString(), "kdv1_test")
			AppLog.init(context)
			AppLog.w("popup", "Instrumented popup")
			Log.w("KoderSyncTest", "logcat marker")
			val sync = PhoneSync.get(context)
			val done = CountDownLatch(1)
			sync.syncInBackground { done.countDown() }
			assertTrue(done.await(30, TimeUnit.SECONDS))

			val first = JSONObject(server.takeRequest(5, TimeUnit.SECONDS)?.body?.utf8().orEmpty())
			assertEquals("logs wait until Koder says they are enabled", 0, first.getJSONArray("logs").length())
			val second = server.takeRequest(5, TimeUnit.SECONDS)
			assertEquals("/voice/v1/device/sync", second?.target)
			assertEquals("Bearer kdv1_test", second?.headers?.get("Authorization"))
			val body = JSONObject(second?.body?.utf8().orEmpty())
			val messages = (0 until body.getJSONArray("logs").length()).map { body.getJSONArray("logs").getJSONObject(it) }
				.map { it.getString("tag") + ": " + it.getString("message") }
			assertTrue(messages.joinToString("\n"), "popup: Instrumented popup" in messages)
			assertTrue(messages.joinToString("\n"), messages.any { it.startsWith("logcat: KoderSyncTest: logcat marker") })
			val report = body.getJSONArray("alerts").getJSONObject(0)
			assertEquals(alertId, report.getString("id"))
			assertEquals("scheduled", report.getString("status"))
			assertEquals("exact", report.getString("detail"))

			val alarm = Intent(context, PhoneSyncReceiver::class.java).setAction(PhoneSyncReceiver.ACTION_ALERT)
				.putExtra(PhoneSyncReceiver.EXTRA_ALERT_ID, alertId)
			assertNotNull("alert alarm is armed", PendingIntent.getBroadcast(
				context, "${PhoneSyncReceiver.ACTION_ALERT}:$alertId".hashCode(), alarm, PendingIntent.FLAG_NO_CREATE or PendingIntent.FLAG_IMMUTABLE,
			))

			context.sendBroadcast(alarm)
			val notifications = context.getSystemService(NotificationManager::class.java)
			val deadline = System.currentTimeMillis() + 10_000
			while (notifications.activeNotifications.none { it.id == alertId.hashCode() } && System.currentTimeMillis() < deadline) Thread.sleep(100)
			val shown = notifications.activeNotifications.firstOrNull { it.id == alertId.hashCode() }
			assertNotNull("alert notification is shown", shown)
			assertEquals("Stretch", shown?.notification?.extras?.getString("android.title"))
			notifications.cancel(alertId.hashCode())
		} finally {
			SecureSettings(context).save("", "")
			server.close()
		}
	}
}
