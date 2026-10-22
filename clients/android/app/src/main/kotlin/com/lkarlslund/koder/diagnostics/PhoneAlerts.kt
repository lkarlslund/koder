package com.lkarlslund.koder.diagnostics

import android.annotation.SuppressLint
import android.app.AlarmManager
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build
import androidx.core.app.NotificationManagerCompat
import com.lkarlslund.koder.MainActivity
import com.lkarlslund.koder.R
import java.time.Instant
import java.util.UUID

/**
 * Alerts Koder schedules through the phone_alerts tool. They are armed with
 * Android's alarm manager, so they fire later even when Koder is closed or
 * offline.
 */
object PhoneAlerts {
	private const val PREFERENCES = "koder_alerts"
	private const val BOOK = "book"
	private const val CHANNEL_ID = "koder_alerts"
	private const val TAG = "alert"

	@Synchronized
	private fun <T> edit(context: Context, block: (AlertBook) -> T): T {
		val preferences = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
		val book = AlertBook(preferences.getString(BOOK, "[]").orEmpty())
		return block(book).also { preferences.edit().putString(BOOK, book.toJSON()).apply() }
	}

	fun schedule(context: Context, title: String, body: String, fireAtMillis: Long): PhoneAlert = edit(context) { book ->
		require(title.isNotBlank()) { "title is required" }
		require(fireAtMillis > System.currentTimeMillis()) { "The alert time is not in the future" }
		val alert = PhoneAlert("alert-${UUID.randomUUID()}", title.trim().take(500), body.trim().take(2000), fireAtMillis)
		val (status, detail) = arm(context, alert)
		val armed = alert.copy(status = status, detail = detail, updatedAtMillis = System.currentTimeMillis())
		book.add(armed)
		AppLog.i(TAG, "Alert ${alert.id} \"${alert.title}\" for ${Instant.ofEpochMilli(fireAtMillis)}: $status $detail".trim())
		armed
	}

	fun list(context: Context): List<PhoneAlert> = edit(context) { it.all() }

	fun cancel(context: Context, id: String): PhoneAlert = edit(context) { book ->
		val alert = book[id] ?: error("No alert $id")
		check(!alert.finished) { "Alert $id is already ${alert.status}" }
		val intent = alertIntent(context, id)
		context.getSystemService(AlarmManager::class.java).cancel(intent)
		intent.cancel()
		book.mark(id, PhoneAlert.CANCELLED)
		AppLog.i(TAG, "Alert $id cancelled by Koder")
		book[id]!!
	}

	/** Re-arms alerts after a reboot or app update, which clear Android alarms. */
	fun rearm(context: Context) = edit(context) { book ->
		book.armed().forEach { alert ->
			val (status, detail) = arm(context, alert)
			if (status != PhoneAlert.SCHEDULED || detail != alert.detail) book.mark(alert.id, status, detail)
		}
	}

	fun fire(context: Context, id: String) = edit(context) { book ->
		val alert = book[id] ?: return@edit
		if (alert.status != PhoneAlert.SCHEDULED) return@edit
		show(context, alert)
		val late = System.currentTimeMillis() - alert.fireAtMillis
		book.mark(id, PhoneAlert.FIRED, if (late > 60_000) "late by ${late / 60_000} min" else "")
		AppLog.i(TAG, "Alert $id \"${alert.title}\" fired")
	}

	@SuppressLint("MissingPermission")
	private fun arm(context: Context, alert: PhoneAlert): Pair<String, String> {
		if (!NotificationManagerCompat.from(context).areNotificationsEnabled()) {
			return PhoneAlert.FAILED to "notifications are turned off for Koder on the phone"
		}
		if (alert.fireAtMillis <= System.currentTimeMillis()) {
			show(context, alert)
			return PhoneAlert.FIRED to "late: the phone restarted after the fire time"
		}
		val alarms = context.getSystemService(AlarmManager::class.java)
		val intent = alertIntent(context, alert.id)
		return if (Build.VERSION.SDK_INT < 31 || alarms.canScheduleExactAlarms()) {
			alarms.setExactAndAllowWhileIdle(AlarmManager.RTC_WAKEUP, alert.fireAtMillis, intent)
			PhoneAlert.SCHEDULED to "exact"
		} else {
			alarms.setAndAllowWhileIdle(AlarmManager.RTC_WAKEUP, alert.fireAtMillis, intent)
			PhoneAlert.SCHEDULED to "inexact: allow Alarms & reminders for Koder in Android settings for on-time alerts"
		}
	}

	@SuppressLint("MissingPermission")
	private fun show(context: Context, alert: PhoneAlert) {
		val manager = context.getSystemService(NotificationManager::class.java)
		manager.createNotificationChannel(NotificationChannel(CHANNEL_ID, "Koder alerts", NotificationManager.IMPORTANCE_HIGH).apply {
			description = "Reminders and alerts Koder schedules for you"
		})
		val open = PendingIntent.getActivity(
			context, alert.id.hashCode(),
			Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP),
			PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
		)
		manager.notify(alert.id.hashCode(), Notification.Builder(context, CHANNEL_ID)
			.setSmallIcon(R.drawable.ic_koder)
			.setContentTitle(alert.title)
			.apply { if (alert.body.isNotBlank()) setContentText(alert.body).setStyle(Notification.BigTextStyle().bigText(alert.body)) }
			.setCategory(Notification.CATEGORY_REMINDER)
			.setWhen(alert.fireAtMillis)
			.setShowWhen(true)
			.setAutoCancel(true)
			.setContentIntent(open)
			.build())
	}

	private fun alertIntent(context: Context, id: String): PendingIntent = PendingIntent.getBroadcast(
		context, "${PhoneAlertReceiver.ACTION_ALERT}:$id".hashCode(),
		Intent(context, PhoneAlertReceiver::class.java).setAction(PhoneAlertReceiver.ACTION_ALERT).putExtra(PhoneAlertReceiver.EXTRA_ALERT_ID, id),
		PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
	)
}

/** Fires alert alarms, and re-arms them after a reboot or app update. */
class PhoneAlertReceiver : BroadcastReceiver() {
	override fun onReceive(context: Context, intent: Intent) {
		AppLog.init(context)
		when (intent.action) {
			ACTION_ALERT -> PhoneAlerts.fire(context, intent.getStringExtra(EXTRA_ALERT_ID).orEmpty())
			Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED -> {
				AppLog.i("alert", "Re-arming after ${intent.action?.substringAfterLast('.')}")
				PhoneAlerts.rearm(context)
			}
		}
	}

	companion object {
		const val ACTION_ALERT = "com.lkarlslund.koder.action.PHONE_ALERT"
		const val EXTRA_ALERT_ID = "com.lkarlslund.koder.extra.ALERT_ID"
	}
}
