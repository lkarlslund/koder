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
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.time.Instant

@RunWith(AndroidJUnit4::class)
class PhoneAlertsInstrumentedTest {
	private val context: Context = ApplicationProvider.getApplicationContext()

	private fun alarm(id: String) = Intent(context, PhoneAlertReceiver::class.java).setAction(PhoneAlertReceiver.ACTION_ALERT)
		.putExtra(PhoneAlertReceiver.EXTRA_ALERT_ID, id)

	private fun armed(id: String) = PendingIntent.getBroadcast(
		context, "${PhoneAlertReceiver.ACTION_ALERT}:$id".hashCode(), alarm(id), PendingIntent.FLAG_NO_CREATE or PendingIntent.FLAG_IMMUTABLE,
	)

	@Test
	fun scheduledAlertIsArmedFiresAndCanBeCancelled() {
		if (Build.VERSION.SDK_INT >= 33) {
			InstrumentationRegistry.getInstrumentation().uiAutomation.grantRuntimePermission(context.packageName, Manifest.permission.POST_NOTIFICATIONS)
		}
		context.getSharedPreferences("koder_alerts", Context.MODE_PRIVATE).edit().clear().commit()
		AppLog.init(context)
		val fireAt = Instant.now().plusSeconds(3600).toEpochMilli()

		val alert = PhoneAlerts.schedule(context, "Stretch", "Stand up", fireAt)
		assertEquals(PhoneAlert.SCHEDULED, alert.status)
		assertEquals("exact", alert.detail)
		assertNotNull("alert alarm is armed", armed(alert.id))
		assertEquals(listOf(alert.id), PhoneAlerts.list(context).map { it.id })

		context.sendBroadcast(alarm(alert.id))
		val notifications = context.getSystemService(NotificationManager::class.java)
		val deadline = System.currentTimeMillis() + 10_000
		while (notifications.activeNotifications.none { it.id == alert.id.hashCode() } && System.currentTimeMillis() < deadline) Thread.sleep(100)
		val shown = notifications.activeNotifications.firstOrNull { it.id == alert.id.hashCode() }
		assertNotNull("alert notification is shown", shown)
		assertEquals("Stretch", shown?.notification?.extras?.getString("android.title"))
		notifications.cancel(alert.id.hashCode())
		assertEquals(PhoneAlert.FIRED, PhoneAlerts.list(context).single().status)

		val later = PhoneAlerts.schedule(context, "Tea", "", fireAt)
		assertEquals(PhoneAlert.CANCELLED, PhoneAlerts.cancel(context, later.id).status)
		assertNull("cancelled alarm is withdrawn", armed(later.id))
	}

	@Test
	fun appLogQueryIncludesOwnEventsAndLogcat() {
		AppLog.init(context)
		val marker = "marker-${System.nanoTime()}"
		AppLog.w("popup", "Instrumented popup $marker")
		Log.w("KoderLogTest", "logcat $marker")
		val messages = AppLog.query(AppLogFilter(query = marker), 50).map { "${it.tag}: ${it.message}" }
		assertTrue(messages.joinToString("\n"), "popup: Instrumented popup $marker" in messages)
		assertTrue(messages.joinToString("\n"), messages.any { it.startsWith("logcat: KoderLogTest: logcat $marker") })
	}
}
