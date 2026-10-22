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
import android.os.Handler
import android.os.Looper
import android.os.Process
import android.os.SystemClock
import androidx.core.app.NotificationManagerCompat
import com.lkarlslund.koder.MainActivity
import com.lkarlslund.koder.R
import com.lkarlslund.koder.SecureSettings
import com.lkarlslund.koder.phone.PhoneIdentity
import com.lkarlslund.koder.voice.VOICE_PROTOCOL
import com.lkarlslund.koder.voice.VoiceProtocol
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/** Wire format of `POST /voice/v1/device/sync`. Pure, for JVM tests. */
object PhoneSyncProtocol {
	const val MAX_LOGS = 2000

	data class Response(val logsEnabled: Boolean, val logCursor: Long, val alerts: List<PhoneAlert>, val cancelAlerts: List<String>)

	fun request(epoch: String, logs: List<AppLogEntry>, reports: List<PhoneAlert>, logsAllowed: Boolean, alertsAllowed: Boolean): String = JSONObject()
		.put("protocol", VOICE_PROTOCOL)
		.put("logs_allowed", logsAllowed)
		.put("alerts_allowed", alertsAllowed)
		.put("log_epoch", epoch)
		.put("logs", JSONArray().also { array -> logs.forEach { array.put(it.toJSON()) } })
		.put("alerts", JSONArray().also { array ->
			reports.forEach { array.put(JSONObject().put("id", it.id).put("status", it.status).put("detail", it.detail)) }
		})
		.toString()

	fun response(text: String): Response {
		val root = JSONObject(text)
		require(root.optString("protocol") == VOICE_PROTOCOL) { "Unsupported phone sync response" }
		val alerts = root.optJSONArray("alerts") ?: JSONArray()
		val cancels = root.optJSONArray("cancel_alerts") ?: JSONArray()
		return Response(
			logsEnabled = root.optBoolean("logs_enabled"),
			logCursor = root.optLong("log_cursor"),
			alerts = (0 until alerts.length()).mapNotNull { index ->
				val item = alerts.optJSONObject(index) ?: return@mapNotNull null
				val fireAt = runCatching { Instant.parse(item.getString("fire_at")).toEpochMilli() }.getOrNull() ?: return@mapNotNull null
				PhoneAlert(item.optString("id"), item.optString("title"), item.optString("body"), fireAt)
			},
			cancelAlerts = (0 until cancels.length()).map { cancels.optString(it) }.filter(String::isNotBlank),
		)
	}

	private val logcatLine = Regex("""^\s*(\d+)\.(\d{3})\s+\d+\s+\d+\s+([VDIWEF])\s+(.*?)\s*: (.*)$""")

	/** Parses `logcat -v epoch` lines newer than [afterMillis]. */
	fun logcat(lines: List<String>, afterMillis: Long): List<Triple<Long, String, String>> = lines.mapNotNull { line ->
		val match = logcatLine.matchEntire(line) ?: return@mapNotNull null
		val (seconds, millis, level, tag, message) = match.destructured
		val time = seconds.toLong() * 1000 + millis.toLong()
		if (time <= afterMillis) return@mapNotNull null
		val appLevel = when (level) { "V", "D" -> "D"; "I" -> "I"; "W" -> "W"; else -> "E" }
		Triple(time, appLevel, "$tag: $message")
	}
}

/**
 * Keeps the server up to date with this phone: uploads [AppLog] while the user
 * shares logs in the app's settings and Koder has the `phone_logs` tool on, and
 * arms the alerts Koder queued while the user accepts alerts. Syncs
 * shortly after a warning, every minute while the app is visible, and about
 * every 15 minutes in the background.
 */
class PhoneSync private constructor(context: Context) {
	private val app = context.applicationContext
	private val preferences = app.getSharedPreferences("koder_diagnostics", Context.MODE_PRIVATE)
	private val executor = Executors.newSingleThreadExecutor { runnable -> Thread(runnable, "koder-phone-sync").apply { isDaemon = true } }
	private val main = Handler(Looper.getMainLooper())
	private val client = OkHttpClient.Builder().callTimeout(15, TimeUnit.SECONDS).build()
	@Volatile private var visible = false
	@Volatile private var pending = false
	private val syncRunnable = Runnable { pending = false; executor.execute { sync() } }
	private val foregroundTick = object : Runnable {
		override fun run() {
			if (!visible) return
			requestSoon(0)
			main.postDelayed(this, FOREGROUND_INTERVAL_MILLIS)
		}
	}

	init {
		AppLog.setListener { entry ->
			if ((entry.level == "W" || entry.level == "E") && entry.tag != TAG && entry.tag != AppLog.LOGCAT_TAG) requestSoon(WARNING_DELAY_MILLIS)
		}
	}

	/** Syncs after [delayMillis], unless a sync is already waiting. */
	fun requestSoon(delayMillis: Long = WARNING_DELAY_MILLIS) {
		if (pending) return
		pending = true
		main.postDelayed(syncRunnable, delayMillis)
	}

	fun setVisible(value: Boolean) {
		visible = value
		main.removeCallbacks(foregroundTick)
		if (value) main.post(foregroundTick)
	}

	/** Arms the background sync; Android drops it on reboot, so boot re-arms it. */
	fun armBackground() {
		val alarms = app.getSystemService(AlarmManager::class.java)
		alarms.setInexactRepeating(
			AlarmManager.ELAPSED_REALTIME, SystemClock.elapsedRealtime() + AlarmManager.INTERVAL_FIFTEEN_MINUTES,
			AlarmManager.INTERVAL_FIFTEEN_MINUTES, receiverIntent(app, PhoneSyncReceiver.ACTION_SYNC, 0),
		)
	}

	fun syncInBackground(done: () -> Unit) = executor.execute {
		try { sync() } finally { done() }
	}

	private fun sync() {
		val settings = runCatching { SecureSettings(app).load() }.getOrNull() ?: return
		if (settings.server.isBlank() || settings.token.isBlank()) return
		captureLogcat()
		if (!settings.acceptAlerts) PhoneAlerts.withdrawAll(app)
		val identity = PhoneIdentity.from(app)
		var more = true
		var rounds = 0
		while (more && rounds++ < MAX_ROUNDS) {
			val epoch = AppLog.epoch
			val logsEnabled = settings.shareLogs && preferences.getBoolean(LOGS_ENABLED, false)
			val cursor = if (preferences.getString(CURSOR_EPOCH, "") == epoch) preferences.getLong(CURSOR, 0) else 0
			val logs = if (logsEnabled) AppLog.entriesAfter(cursor, PhoneSyncProtocol.MAX_LOGS) else emptyList()
			val reports = PhoneAlerts.unreported(app)
			val response = try {
				post(settings.server, settings.token, identity, PhoneSyncProtocol.request(epoch, logs, reports, settings.shareLogs, settings.acceptAlerts))
			} catch (error: Exception) {
				if (preferences.getBoolean(LAST_OK, true)) AppLog.w(TAG, "Sync with ${settings.server} failed", error)
				preferences.edit().putBoolean(LAST_OK, false).apply()
				return
			}
			if (!preferences.getBoolean(LAST_OK, true)) AppLog.i(TAG, "Sync works again")
			preferences.edit().putBoolean(LAST_OK, true).putBoolean(LOGS_ENABLED, response.logsEnabled)
				.putLong(CURSOR, response.logCursor).putString(CURSOR_EPOCH, epoch).apply()
			if (response.logsEnabled != logsEnabled) AppLog.i(TAG, if (response.logsEnabled) "Koder enabled log upload" else "Koder disabled log upload")
			PhoneAlerts.reported(app, reports)
			val armed = PhoneAlerts.apply(app, response.alerts, response.cancelAlerts)
			more = response.logsEnabled && (!logsEnabled || logs.size == PhoneSyncProtocol.MAX_LOGS || response.logCursor < cursor) || armed
		}
	}

	private fun post(server: String, token: String, identity: PhoneIdentity, body: String): PhoneSyncProtocol.Response {
		val request = identity.applyTo(Request.Builder())
			.url(VoiceProtocol.resourceUrl(server, "/voice/v1/device/sync"))
			.header("Authorization", "Bearer ${token.trim()}")
			.post(body.toRequestBody(JSON))
			.build()
		client.newCall(request).execute().use { response ->
			val text = response.body.string()
			check(response.isSuccessful) { "HTTP ${response.code}: ${text.trim().take(200)}" }
			return PhoneSyncProtocol.response(text)
		}
	}

	/** Copies this process's newer logcat lines (Android's own audio and system messages) into [AppLog]. */
	private fun captureLogcat() {
		val after = preferences.getLong(LOGCAT_AFTER, 0)
		val lines = runCatching {
			val process = ProcessBuilder(listOf("logcat", "-v", "epoch", "-t", LOGCAT_LINES.toString(), "--pid=${Process.myPid()}") + AppLog.logcatFilter)
				.redirectErrorStream(true).start()
			val output = process.inputStream.bufferedReader().readLines()
			process.waitFor(5, TimeUnit.SECONDS)
			output
		}.getOrDefault(emptyList())
		val fresh = PhoneSyncProtocol.logcat(lines, after)
		fresh.forEach { (time, level, message) -> AppLog.log(level, AppLog.LOGCAT_TAG, message, Instant.ofEpochMilli(time)) }
		fresh.lastOrNull()?.let { preferences.edit().putLong(LOGCAT_AFTER, it.first).apply() }
	}

	companion object {
		const val TAG = "sync"
		private const val FOREGROUND_INTERVAL_MILLIS = 60_000L
		private const val WARNING_DELAY_MILLIS = 5_000L
		private const val MAX_ROUNDS = 5
		private const val LOGCAT_LINES = 2000
		private const val LOGS_ENABLED = "logs_enabled"
		private const val CURSOR = "log_cursor"
		private const val CURSOR_EPOCH = "log_cursor_epoch"
		private const val LAST_OK = "sync_last_ok"
		private const val LOGCAT_AFTER = "logcat_after_millis"
		private val JSON = "application/json; charset=utf-8".toMediaType()

		@Volatile private var instance: PhoneSync? = null

		fun get(context: Context): PhoneSync {
			AppLog.init(context)
			return instance ?: synchronized(this) { instance ?: PhoneSync(context).also { instance = it } }
		}
	}
}

/** Arms, fires and cancels Koder's alerts with Android's alarm manager. */
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

	fun unreported(context: Context): List<PhoneAlert> = edit(context) { it.unreported() }

	fun reported(context: Context, sent: List<PhoneAlert>) = edit(context) { it.reported(sent) }

	/** Arms new alerts and cancels withdrawn ones. Returns whether anything changed. */
	fun apply(context: Context, incoming: List<PhoneAlert>, cancel: List<String>): Boolean = edit(context) { book ->
		val fresh = book.accept(incoming)
		fresh.forEach { alert ->
			val (status, detail) = arm(context, alert)
			book.mark(alert.id, status, detail)
			AppLog.i(TAG, "Alert ${alert.id} \"${alert.title}\" for ${Instant.ofEpochMilli(alert.fireAtMillis)}: $status $detail".trim())
		}
		cancel.forEach { id ->
			if (book[id]?.status != PhoneAlert.SCHEDULED) return@forEach
			context.getSystemService(AlarmManager::class.java).cancel(receiverIntent(context, PhoneSyncReceiver.ACTION_ALERT, id))
			book.mark(id, PhoneAlert.CANCELLED)
			AppLog.i(TAG, "Alert $id cancelled by Koder")
		}
		fresh.isNotEmpty() || cancel.isNotEmpty()
	}

	/** Cancels every armed alert once the user stops accepting alerts. */
	fun withdrawAll(context: Context) = edit(context) { book ->
		book.armed().forEach { alert ->
			context.getSystemService(AlarmManager::class.java).cancel(receiverIntent(context, PhoneSyncReceiver.ACTION_ALERT, alert.id))
			book.mark(alert.id, PhoneAlert.CANCELLED, "alerts were turned off on the phone")
			AppLog.i(TAG, "Alert ${alert.id} cancelled because alerts were turned off")
		}
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
			return PhoneAlert.FIRED to "late: the phone synced after the fire time"
		}
		val alarms = context.getSystemService(AlarmManager::class.java)
		val intent = receiverIntent(context, PhoneSyncReceiver.ACTION_ALERT, alert.id)
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
}

private fun receiverIntent(context: Context, action: String, id: Any): PendingIntent = PendingIntent.getBroadcast(
	context, "$action:$id".hashCode(),
	Intent(context, PhoneSyncReceiver::class.java).setAction(action).putExtra(PhoneSyncReceiver.EXTRA_ALERT_ID, id.toString()),
	PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
)

/** Background sync ticks, alert alarms, and re-arming after boot or update. */
class PhoneSyncReceiver : BroadcastReceiver() {
	override fun onReceive(context: Context, intent: Intent) {
		val sync = PhoneSync.get(context)
		when (intent.action) {
			ACTION_ALERT -> {
				PhoneAlerts.fire(context, intent.getStringExtra(EXTRA_ALERT_ID).orEmpty())
				val result = goAsync()
				sync.syncInBackground { result.finish() }
			}
			ACTION_SYNC -> {
				val result = goAsync()
				sync.syncInBackground { result.finish() }
			}
			Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED -> {
				AppLog.i(PhoneSync.TAG, "Re-arming after ${intent.action?.substringAfterLast('.')}")
				PhoneAlerts.rearm(context)
				sync.armBackground()
			}
		}
	}

	companion object {
		const val ACTION_SYNC = "com.lkarlslund.koder.action.PHONE_SYNC"
		const val ACTION_ALERT = "com.lkarlslund.koder.action.PHONE_ALERT"
		const val EXTRA_ALERT_ID = "com.lkarlslund.koder.extra.ALERT_ID"
	}
}
