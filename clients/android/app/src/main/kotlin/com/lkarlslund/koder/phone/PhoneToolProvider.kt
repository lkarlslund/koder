package com.lkarlslund.koder.phone

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.app.AlertDialog
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ContentUris
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.database.Cursor
import android.location.Address
import android.location.Geocoder
import android.location.Location
import android.location.LocationManager
import android.media.AudioManager
import android.telephony.SmsManager
import android.net.ConnectivityManager
import android.net.Uri
import android.os.BatteryManager
import android.os.Environment
import android.os.StatFs
import android.provider.AlarmClock
import android.provider.CalendarContract
import android.provider.ContactsContract
import android.provider.CallLog
import android.provider.Telephony
import android.view.KeyEvent
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.lkarlslund.koder.SecureSettings
import com.lkarlslund.koder.voice.AndroidVoiceHaptics
import com.lkarlslund.koder.voice.VoiceHapticCue
import com.lkarlslund.koder.voice.VoiceHaptics
import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant
import java.time.temporal.ChronoUnit
import java.util.Locale
import java.util.TimeZone
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicReference

data class PhoneToolResult(val text: String, val data: Any? = null)

interface PhoneToolProvider : AutoCloseable {
    fun enabledActions(): Set<String>
	fun actionPolicies(): Map<String, PhoneActionPolicy> = enabledActions().associateWith { PhoneActionPolicy.ASK }
    fun execute(action: String, arguments: Map<String, String>, callback: (Result<PhoneToolResult>) -> Unit)
    override fun close() = Unit
}

class AndroidPhoneToolProvider(
    private val activity: Activity,
    private val settings: SecureSettings = SecureSettings(activity),
    private val executor: ExecutorService = Executors.newSingleThreadExecutor { runnable ->
        Thread(runnable, "koder-phone-tools").apply { isDaemon = true }
    },
    private val intentLauncher: ((Intent) -> Unit)? = null,
	private val haptics: VoiceHaptics = AndroidVoiceHaptics(activity),
) : PhoneToolProvider {
    override fun enabledActions(): Set<String> = actionPolicies().keys

	override fun actionPolicies(): Map<String, PhoneActionPolicy> {
		val values = settings.load()
		return values.phoneActionPolicies.filter { (action, policy) ->
			policy != PhoneActionPolicy.OFF && PhoneCapabilities.all.any { capability ->
				action in capability.actions && permissionAvailable(capability)
			}
		}
	}

    override fun execute(action: String, arguments: Map<String, String>, callback: (Result<PhoneToolResult>) -> Unit) {
        if (action !in enabledActions()) {
            callback(Result.failure(IllegalStateException("$action is not enabled on this phone")))
            return
        }
        val work = {
			executor.execute {
				val result = runCatching { perform(action, arguments) }
				if (result.isSuccess) settings.recordPhoneActionUse(action)
				callback(result)
			}
		}
        if (settings.load().phoneActionPolicies[action] == PhoneActionPolicy.ASK) {
            activity.runOnUiThread {
				haptics.play(VoiceHapticCue.CONFIRMATION_REQUIRED)
                AlertDialog.Builder(activity)
                    .setTitle("Allow Koder to ${humanAction(action)}?")
                    .setMessage(confirmMessage(action, arguments))
                    .setNegativeButton("Deny") { _, _ -> callback(Result.failure(SecurityException("Denied on phone"))) }
                    .setPositiveButton("Allow") { _, _ -> work() }
                    .setOnCancelListener { callback(Result.failure(SecurityException("Cancelled on phone"))) }
                    .show()
            }
        } else {
            work()
        }
    }

    override fun close() {
        executor.shutdownNow()
    }

    private fun permissionAvailable(capability: PhoneCapability): Boolean {
        if (capability.notificationAccess && activity.packageName !in NotificationManagerCompat.getEnabledListenerPackages(activity)) return false
        return if (capability.id == "location") {
            capability.permissions.any { ContextCompat.checkSelfPermission(activity, it) == PackageManager.PERMISSION_GRANTED }
        } else {
            capability.permissions.all { ContextCompat.checkSelfPermission(activity, it) == PackageManager.PERMISSION_GRANTED }
        }
    }

    private fun perform(action: String, args: Map<String, String>): PhoneToolResult = when (action) {
        "device_status" -> deviceStatus()
        "get_location" -> location()
        "search_contacts" -> contacts(args)
        "upcoming_calendar" -> calendar(args)
        "search_sms" -> sms(args)
		"search_call_history" -> callHistory(args)
        "recent_notifications" -> notifications(args)
        "place_call" -> placeCall(args)
        "send_sms" -> sendSMS(args)
        "compose_email" -> composeEmail(args)
        "create_contact" -> createContact(args)
        "edit_contact" -> editContact(args)
        "create_calendar_event" -> createCalendarEvent(args)
        "edit_calendar_event" -> editCalendarEvent(args)
        "open_map" -> openMap(args)
        "set_alarm" -> setAlarm(args)
        "set_timer" -> setTimer(args)
        "read_clipboard" -> readClipboard()
        "write_clipboard" -> writeClipboard(args)
        "open_url" -> openURL(args)
        "media_control" -> mediaControl(args)
        "list_apps" -> listApps(args)
        "open_app" -> openApp(args)
        "share_text" -> shareText(args)
        else -> error("Unknown phone action: $action")
    }

    private fun deviceStatus(): PhoneToolResult {
        val battery = activity.registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
        val level = battery?.getIntExtra(BatteryManager.EXTRA_LEVEL, -1) ?: -1
        val scale = battery?.getIntExtra(BatteryManager.EXTRA_SCALE, 100) ?: 100
        val charging = battery?.getIntExtra(BatteryManager.EXTRA_STATUS, -1) in setOf(BatteryManager.BATTERY_STATUS_CHARGING, BatteryManager.BATTERY_STATUS_FULL)
        val storage = StatFs(Environment.getDataDirectory().path)
        val network = activity.getSystemService(ConnectivityManager::class.java).activeNetwork != null
        val data = JSONObject()
            .put("battery_percent", if (level >= 0) level * 100 / scale.coerceAtLeast(1) else JSONObject.NULL)
            .put("charging", charging)
            .put("online", network)
            .put("storage_free_bytes", storage.availableBytes)
            .put("storage_total_bytes", storage.totalBytes)
            .put("locale", Locale.getDefault().toLanguageTag())
            .put("time_zone", TimeZone.getDefault().id)
        return PhoneToolResult("Phone status read", data)
    }

    @SuppressLint("MissingPermission")
    private fun location(): PhoneToolResult {
        val manager = activity.getSystemService(LocationManager::class.java)
        val best = manager.getProviders(true).mapNotNull { provider -> runCatching { manager.getLastKnownLocation(provider) }.getOrNull() }
            .maxWithOrNull(compareBy<Location> { it.time }.thenByDescending { -it.accuracy })
            ?: error("No recent phone location is available")
		return phoneLocationResult(best, reverseGeocode(best))
    }

	@Suppress("DEPRECATION")
	private fun reverseGeocode(location: Location): Address? {
		if (!Geocoder.isPresent()) return null
		return runCatching {
			Geocoder(activity, Locale.getDefault()).getFromLocation(location.latitude, location.longitude, 1)?.firstOrNull()
		}.getOrNull()
	}

    private fun contacts(args: Map<String, String>): PhoneToolResult {
        val query = args["query"].orEmpty().lowercase(Locale.getDefault())
        val limit = limit(args)
        val rows = JSONArray()
        activity.contentResolver.query(
            ContactsContract.CommonDataKinds.Phone.CONTENT_URI,
            arrayOf(
                ContactsContract.CommonDataKinds.Phone.CONTACT_ID,
                ContactsContract.CommonDataKinds.Phone.DISPLAY_NAME,
                ContactsContract.CommonDataKinds.Phone.NUMBER,
            ), null, null, ContactsContract.CommonDataKinds.Phone.DISPLAY_NAME + " ASC",
        ).useCursor { cursor ->
            while (cursor.moveToNext() && rows.length() < limit) {
                val name = cursor.string(1)
                val number = cursor.string(2)
                if (query.isNotBlank() && query !in name.lowercase() && query !in number.lowercase()) continue
                rows.put(JSONObject().put("id", cursor.string(0)).put("name", name).put("phone_number", number)
                    .put("email", contactEmail(cursor.string(0))))
            }
        }
        return PhoneToolResult("Found ${rows.length()} matching contacts", JSONObject().put("contacts", rows))
    }

    private fun calendar(args: Map<String, String>): PhoneToolResult {
        val start = parseTime(args["start_time"]) ?: Instant.now()
        val end = parseTime(args["end_time"]) ?: start.plus(14, ChronoUnit.DAYS)
        val query = args["query"].orEmpty().lowercase(Locale.getDefault())
        val uri = CalendarContract.Instances.CONTENT_URI.buildUpon().also {
            ContentUris.appendId(it, start.toEpochMilli()); ContentUris.appendId(it, end.toEpochMilli())
        }.build()
        val rows = JSONArray()
        activity.contentResolver.query(uri, arrayOf(
            CalendarContract.Instances.EVENT_ID, CalendarContract.Instances.TITLE, CalendarContract.Instances.BEGIN, CalendarContract.Instances.END,
            CalendarContract.Instances.EVENT_LOCATION, CalendarContract.Instances.ALL_DAY,
        ), null, null, CalendarContract.Instances.BEGIN + " ASC").useCursor { cursor ->
            while (cursor.moveToNext() && rows.length() < limit(args)) {
                val title = cursor.string(1)
                val place = cursor.string(4)
                if (query.isNotBlank() && query !in title.lowercase() && query !in place.lowercase()) continue
                rows.put(JSONObject().put("event_id", cursor.string(0)).put("title", title)
                    .put("start_time", Instant.ofEpochMilli(cursor.getLong(2)).toString())
                    .put("end_time", Instant.ofEpochMilli(cursor.getLong(3)).toString()).put("location", place).put("all_day", cursor.getInt(5) != 0))
            }
        }
        return PhoneToolResult("Found ${rows.length()} calendar events", JSONObject().put("events", rows))
    }

    private fun sms(args: Map<String, String>): PhoneToolResult {
        val query = args["query"].orEmpty().lowercase(Locale.getDefault())
        val address = args["phone_number"].orEmpty().lowercase(Locale.getDefault())
        val since = parseTime(args["since_time"])?.toEpochMilli() ?: 0L
        val rows = JSONArray()
        activity.contentResolver.query(Telephony.Sms.CONTENT_URI, arrayOf(
            Telephony.Sms.ADDRESS, Telephony.Sms.BODY, Telephony.Sms.DATE, Telephony.Sms.TYPE,
        ), null, null, Telephony.Sms.DEFAULT_SORT_ORDER).useCursor { cursor ->
            while (cursor.moveToNext() && rows.length() < limit(args)) {
                val number = cursor.string(0); val body = cursor.string(1); val date = cursor.getLong(2)
                if (date < since || (address.isNotBlank() && address !in number.lowercase()) || (query.isNotBlank() && query !in body.lowercase())) continue
                rows.put(JSONObject().put("phone_number", number).put("text", body).put("time", Instant.ofEpochMilli(date).toString())
                    .put("direction", if (cursor.getInt(3) == Telephony.Sms.MESSAGE_TYPE_INBOX) "received" else "sent"))
            }
        }
        return PhoneToolResult("Found ${rows.length()} SMS messages", JSONObject().put("messages", rows))
    }

	private fun callHistory(args: Map<String, String>): PhoneToolResult {
		val query = args["query"].orEmpty().lowercase(Locale.getDefault())
		val numberQuery = args["phone_number"].orEmpty().lowercase(Locale.getDefault())
		val since = parseTime(args["since_time"])?.toEpochMilli() ?: 0L
		val rows = JSONArray()
		activity.contentResolver.query(CallLog.Calls.CONTENT_URI, arrayOf(
			CallLog.Calls.NUMBER, CallLog.Calls.CACHED_NAME, CallLog.Calls.DATE, CallLog.Calls.DURATION, CallLog.Calls.TYPE,
		), null, null, CallLog.Calls.DEFAULT_SORT_ORDER).useCursor { cursor ->
			while (cursor.moveToNext() && rows.length() < limit(args)) {
				val number = cursor.string(0)
				val name = cursor.string(1)
				val date = cursor.getLong(2)
				if (date < since || (numberQuery.isNotBlank() && numberQuery !in number.lowercase()) ||
					(query.isNotBlank() && query !in name.lowercase() && query !in number.lowercase())) continue
				rows.put(JSONObject()
					.put("contact_name", name)
					.put("phone_number", number)
					.put("time", Instant.ofEpochMilli(date).toString())
					.put("duration_seconds", cursor.getLong(3))
					.put("direction", callDirection(cursor.getInt(4))))
			}
		}
		return PhoneToolResult("Found ${rows.length()} call history entries", JSONObject().put("calls", rows))
	}

    private fun notifications(args: Map<String, String>): PhoneToolResult {
        val query = args["query"].orEmpty().lowercase(Locale.getDefault())
        val app = args["app"].orEmpty().lowercase(Locale.getDefault())
        val rows = JSONArray()
        PhoneNotificationListener.snapshot().asSequence().filter {
            (query.isBlank() || query in it.title.lowercase() || query in it.text.lowercase()) &&
                (app.isBlank() || app in it.appName.lowercase() || app in it.packageName.lowercase())
        }.take(limit(args)).forEach {
            rows.put(JSONObject().put("app", it.appName).put("package_name", it.packageName).put("title", it.title)
                .put("text", it.text).put("posted_at", Instant.ofEpochMilli(it.postedAt).toString()))
        }
        return PhoneToolResult("Found ${rows.length()} current notifications", JSONObject().put("notifications", rows))
    }

    private fun placeCall(args: Map<String, String>): PhoneToolResult {
        val number = phoneNumber(args)
        launch(Intent(Intent.ACTION_CALL, Uri.parse("tel:${Uri.encode(number)}")))
        return PhoneToolResult("Call started to $number")
    }

    @Suppress("DEPRECATION")
    private fun sendSMS(args: Map<String, String>): PhoneToolResult {
        val number = phoneNumber(args); val message = required(args, "message")
        SmsManager.getDefault().sendTextMessage(number, null, message, null, null)
        return PhoneToolResult("SMS sent to $number")
    }

    private fun composeEmail(args: Map<String, String>): PhoneToolResult {
        val intent = Intent(Intent.ACTION_SENDTO, Uri.parse("mailto:${Uri.encode(args["to"].orEmpty())}"))
            .putExtra(Intent.EXTRA_SUBJECT, args["subject"].orEmpty()).putExtra(Intent.EXTRA_TEXT, args["body"].orEmpty())
        launch(intent); return PhoneToolResult("Email draft opened for review")
    }

    private fun createContact(args: Map<String, String>): PhoneToolResult {
        val intent = Intent(Intent.ACTION_INSERT, ContactsContract.Contacts.CONTENT_URI)
            .putExtra(ContactsContract.Intents.Insert.NAME, required(args, "name"))
            .putExtra(ContactsContract.Intents.Insert.PHONE, args["phone_number"].orEmpty())
            .putExtra(ContactsContract.Intents.Insert.EMAIL, args["email"].orEmpty())
        launch(intent); return PhoneToolResult("New contact screen opened for review")
    }

    private fun editContact(args: Map<String, String>): PhoneToolResult {
        val proposed = listOf("phone_number", "email", "address", "note").filter { !args[it].isNullOrBlank() }
        if (proposed.isEmpty()) error("At least one proposed phone_number, email, address, or note is required")
        val contact = resolveContact(args)
        val intent = Intent(Intent.ACTION_EDIT).setDataAndType(contact.uri, ContactsContract.Contacts.CONTENT_ITEM_TYPE)
            .putExtra(ContactsContract.Intents.Insert.PHONE, args["phone_number"].orEmpty())
            .putExtra(ContactsContract.Intents.Insert.EMAIL, args["email"].orEmpty())
            .putExtra(ContactsContract.Intents.Insert.POSTAL, args["address"].orEmpty())
            .putExtra(ContactsContract.Intents.Insert.NOTES, args["note"].orEmpty())
        launch(intent)
        return PhoneToolResult("Opened ${contact.name} for review with proposed ${proposed.joinToString()}")
    }

    private fun createCalendarEvent(args: Map<String, String>): PhoneToolResult {
        val start = parseTime(args["start_time"]) ?: error("start_time must be RFC 3339")
        val intent = Intent(Intent.ACTION_INSERT, CalendarContract.Events.CONTENT_URI)
            .putExtra(CalendarContract.Events.TITLE, required(args, "title"))
            .putExtra(CalendarContract.EXTRA_EVENT_BEGIN_TIME, start.toEpochMilli())
            .putExtra(CalendarContract.EXTRA_EVENT_END_TIME, (parseTime(args["end_time"]) ?: start.plus(1, ChronoUnit.HOURS)).toEpochMilli())
            .putExtra(CalendarContract.Events.EVENT_LOCATION, args["location"].orEmpty())
            .putExtra(CalendarContract.Events.DESCRIPTION, args["description"].orEmpty())
        launch(intent); return PhoneToolResult("Calendar entry opened for review")
    }

    private fun editCalendarEvent(args: Map<String, String>): PhoneToolResult {
        val operation = args["operation"].orEmpty().ifBlank { "edit" }
        if (operation !in setOf("edit", "cancel")) error("operation must be edit or cancel")
        val proposed = listOf("title", "start_time", "end_time", "location", "description").filter { !args[it].isNullOrBlank() }
        if (operation == "edit" && proposed.isEmpty()) error("At least one proposed title, start_time, end_time, location, or description is required")
        if (operation == "cancel" && proposed.isNotEmpty()) error("Cancellation cannot include proposed event changes")
        val proposedStart = args["start_time"]?.takeIf(String::isNotBlank)?.let { parseTime(it) ?: error("start_time must be RFC 3339") }
        val proposedEnd = args["end_time"]?.takeIf(String::isNotBlank)?.let { parseTime(it) ?: error("end_time must be RFC 3339") }
        val event = resolveCalendarEvent(args)
        val intent = Intent(Intent.ACTION_EDIT, ContentUris.withAppendedId(CalendarContract.Events.CONTENT_URI, event.id))
        args["title"]?.takeIf(String::isNotBlank)?.let { intent.putExtra(CalendarContract.Events.TITLE, it) }
        proposedStart?.let { intent.putExtra(CalendarContract.EXTRA_EVENT_BEGIN_TIME, it.toEpochMilli()) }
        proposedEnd?.let { intent.putExtra(CalendarContract.EXTRA_EVENT_END_TIME, it.toEpochMilli()) }
        args["location"]?.takeIf(String::isNotBlank)?.let { intent.putExtra(CalendarContract.Events.EVENT_LOCATION, it) }
        args["description"]?.takeIf(String::isNotBlank)?.let { intent.putExtra(CalendarContract.Events.DESCRIPTION, it) }
        launch(intent)
        return if (operation == "cancel") {
            PhoneToolResult("Opened ${event.title}; use the calendar's delete control to confirm cancellation")
        } else {
            PhoneToolResult("Opened ${event.title} for review with proposed ${proposed.joinToString()}")
        }
    }

    private fun openMap(args: Map<String, String>): PhoneToolResult {
        val target = args["query"]?.takeIf(String::isNotBlank)?.let { "geo:0,0?q=${Uri.encode(it)}" }
            ?: "geo:${required(args, "latitude")},${required(args, "longitude")}?q=${args["latitude"]},${args["longitude"]}"
        launch(Intent(Intent.ACTION_VIEW, Uri.parse(target))); return PhoneToolResult("Map opened")
    }

    private fun setAlarm(args: Map<String, String>): PhoneToolResult {
        launch(Intent(AlarmClock.ACTION_SET_ALARM).putExtra(AlarmClock.EXTRA_HOUR, required(args, "hour").toInt())
            .putExtra(AlarmClock.EXTRA_MINUTES, required(args, "minute").toInt()).putExtra(AlarmClock.EXTRA_MESSAGE, args["label"].orEmpty()))
        return PhoneToolResult("Alarm screen opened")
    }

    private fun setTimer(args: Map<String, String>): PhoneToolResult {
        launch(Intent(AlarmClock.ACTION_SET_TIMER).putExtra(AlarmClock.EXTRA_LENGTH, required(args, "duration_seconds").toInt())
            .putExtra(AlarmClock.EXTRA_MESSAGE, args["label"].orEmpty()))
        return PhoneToolResult("Timer screen opened")
    }

    private fun readClipboard(): PhoneToolResult {
        val text = activity.getSystemService(ClipboardManager::class.java).primaryClip?.getItemAt(0)?.coerceToText(activity)?.toString().orEmpty()
        return PhoneToolResult(if (text.isBlank()) "Clipboard is empty" else "Clipboard read", JSONObject().put("text", text))
    }

    private fun writeClipboard(args: Map<String, String>): PhoneToolResult {
        activity.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Koder", required(args, "text")))
        return PhoneToolResult("Clipboard updated")
    }

    private fun openURL(args: Map<String, String>): PhoneToolResult {
        val uri = Uri.parse(required(args, "url")); require(uri.scheme == "https") { "Only HTTPS links may be opened" }
        launch(Intent(Intent.ACTION_VIEW, uri)); return PhoneToolResult("Link opened")
    }

    private fun mediaControl(args: Map<String, String>): PhoneToolResult {
        val code = when (required(args, "media_action")) {
            "play" -> KeyEvent.KEYCODE_MEDIA_PLAY; "pause" -> KeyEvent.KEYCODE_MEDIA_PAUSE
            "toggle" -> KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE; "next" -> KeyEvent.KEYCODE_MEDIA_NEXT
            "previous" -> KeyEvent.KEYCODE_MEDIA_PREVIOUS; else -> error("Unknown media action")
        }
        val audio = activity.getSystemService(AudioManager::class.java)
        audio.dispatchMediaKeyEvent(KeyEvent(KeyEvent.ACTION_DOWN, code)); audio.dispatchMediaKeyEvent(KeyEvent(KeyEvent.ACTION_UP, code))
        return PhoneToolResult("Media control sent")
    }

    private fun listApps(args: Map<String, String>): PhoneToolResult {
        val query = args["query"].orEmpty().lowercase(Locale.getDefault())
        val intent = Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
        val rows = JSONArray()
        activity.packageManager.queryIntentActivities(intent, 0).asSequence().map {
            it.loadLabel(activity.packageManager).toString() to it.activityInfo.packageName
        }.distinct().filter { query.isBlank() || query in it.first.lowercase() || query in it.second.lowercase() }
            .sortedBy { it.first.lowercase() }.take(limit(args)).forEach { (label, packageName) ->
                rows.put(JSONObject().put("name", label).put("package_name", packageName))
            }
        return PhoneToolResult("Found ${rows.length()} launchable apps", JSONObject().put("apps", rows))
    }

    private fun openApp(args: Map<String, String>): PhoneToolResult {
        val packageName = required(args, "package_name")
        val intent = activity.packageManager.getLaunchIntentForPackage(packageName) ?: error("App is not installed or cannot be opened")
        launch(intent); return PhoneToolResult("App opened")
    }

    private fun shareText(args: Map<String, String>): PhoneToolResult {
        val intent = Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, required(args, "text"))
            .putExtra(Intent.EXTRA_TITLE, args["title"].orEmpty())
        launch(Intent.createChooser(intent, args["title"].orEmpty().ifBlank { "Share with" }))
        return PhoneToolResult("Android share sheet opened")
    }

    private fun launch(intent: Intent) {
        val done = CountDownLatch(1)
        val failure = AtomicReference<Throwable?>()
        activity.runOnUiThread {
            try {
                if (intent.resolveActivity(activity.packageManager) == null) error("No app can handle this action")
                intentLauncher?.invoke(intent) ?: activity.startActivity(intent)
            } catch (error: Throwable) {
                failure.set(error)
            } finally {
                done.countDown()
            }
        }
        done.await()
        failure.get()?.let { throw it }
    }

    private fun limit(args: Map<String, String>) = args["limit"]?.toIntOrNull()?.coerceIn(1, 50) ?: 10
    private fun required(args: Map<String, String>, key: String) = args[key]?.trim()?.takeIf(String::isNotBlank) ?: error("$key is required")
    private fun parseTime(value: String?) = value?.trim()?.takeIf(String::isNotBlank)?.let { runCatching { Instant.parse(it) }.getOrNull() }
    private fun Cursor?.useCursor(block: (Cursor) -> Unit) { this?.use(block) }
    private fun Cursor.string(index: Int) = getString(index).orEmpty()

    private fun contactEmail(contactId: String): String {
        var email = ""
        activity.contentResolver.query(
            ContactsContract.CommonDataKinds.Email.CONTENT_URI,
            arrayOf(ContactsContract.CommonDataKinds.Email.ADDRESS),
            ContactsContract.CommonDataKinds.Email.CONTACT_ID + " = ?", arrayOf(contactId), null,
        ).useCursor { cursor -> if (cursor.moveToFirst()) email = cursor.string(0) }
        return email
    }

    private fun phoneNumber(args: Map<String, String>): String {
        args["phone_number"]?.trim()?.takeIf(String::isNotBlank)?.let { return it }
        val name = required(args, "contact_name")
        var number = ""
        activity.contentResolver.query(
            ContactsContract.CommonDataKinds.Phone.CONTENT_URI,
            arrayOf(ContactsContract.CommonDataKinds.Phone.NUMBER),
            ContactsContract.CommonDataKinds.Phone.DISPLAY_NAME + " LIKE ?", arrayOf("%$name%"),
            ContactsContract.CommonDataKinds.Phone.DISPLAY_NAME + " ASC",
        ).useCursor { cursor -> if (cursor.moveToFirst()) number = cursor.string(0) }
        return number.ifBlank { error("No phone number found for $name") }
    }

    private data class ResolvedContact(val name: String, val uri: Uri)

    private data class ResolvedCalendarEvent(val id: Long, val title: String)

    private fun resolveCalendarEvent(args: Map<String, String>): ResolvedCalendarEvent {
        val requestedID = args["event_id"]?.trim().orEmpty()
        val query = args["query"]?.trim().orEmpty()
        if (requestedID.isBlank() && query.isBlank()) error("event_id or query is required")
        val selection: String
        val selectionArgs: Array<String>
        if (requestedID.isNotBlank()) {
            selection = CalendarContract.Events._ID + " = ?"
            selectionArgs = arrayOf(requestedID)
        } else {
            selection = CalendarContract.Events.TITLE + " LIKE ?"
            selectionArgs = arrayOf("%$query%")
        }
        val matches = mutableListOf<ResolvedCalendarEvent>()
        activity.contentResolver.query(
            CalendarContract.Events.CONTENT_URI,
            arrayOf(CalendarContract.Events._ID, CalendarContract.Events.TITLE),
            selection, selectionArgs, CalendarContract.Events.DTSTART + " ASC",
        ).useCursor { cursor ->
            while (cursor.moveToNext() && matches.size < 2) {
                matches += ResolvedCalendarEvent(cursor.getLong(0), cursor.string(1).ifBlank { query.ifBlank { "calendar event" } })
            }
        }
        if (matches.isEmpty()) error("No matching calendar event was found")
        if (matches.size > 1) error("More than one calendar event matched; use upcoming_calendar and pass event_id")
        return matches.single()
    }

    private fun resolveContact(args: Map<String, String>): ResolvedContact {
        val requestedID = args["contact_id"]?.trim().orEmpty()
        val requestedName = (args["contact_name"] ?: args["query"])?.trim().orEmpty()
        if (requestedID.isBlank() && requestedName.isBlank()) error("contact_id or contact_name is required")
        val selection: String
        val selectionArgs: Array<String>
        if (requestedID.isNotBlank()) {
            selection = ContactsContract.Contacts._ID + " = ?"
            selectionArgs = arrayOf(requestedID)
        } else {
            selection = ContactsContract.Contacts.DISPLAY_NAME_PRIMARY + " LIKE ?"
            selectionArgs = arrayOf("%$requestedName%")
        }
        val matches = mutableListOf<ResolvedContact>()
        activity.contentResolver.query(
            ContactsContract.Contacts.CONTENT_URI,
            arrayOf(ContactsContract.Contacts._ID, ContactsContract.Contacts.LOOKUP_KEY, ContactsContract.Contacts.DISPLAY_NAME_PRIMARY),
            selection, selectionArgs, ContactsContract.Contacts.DISPLAY_NAME_PRIMARY + " ASC",
        ).useCursor { cursor ->
            while (cursor.moveToNext() && matches.size < 2) {
                val id = cursor.getLong(0)
                val uri = ContactsContract.Contacts.getLookupUri(id, cursor.string(1)) ?: continue
                matches += ResolvedContact(cursor.string(2).ifBlank { requestedName.ifBlank { "contact" } }, uri)
            }
        }
        if (matches.isEmpty()) error("No matching contact was found")
        if (matches.size > 1) error("More than one contact matched; use search_contacts and pass contact_id")
        return matches.single()
    }

    private fun humanAction(action: String) = action.replace('_', ' ')
    private fun confirmMessage(action: String, args: Map<String, String>): String = when (action) {
        "place_call" -> "Call ${args["phone_number"] ?: args["contact_name"].orEmpty()} now?"
        "send_sms" -> "Send this SMS to ${args["phone_number"] ?: args["contact_name"].orEmpty()}?\n\n${args["message"].orEmpty()}"
        else -> "Koder requested this action during the active voice conversation."
    }

}

internal fun phoneLocationResult(location: Location, address: Address?): PhoneToolResult {
	val locality = address?.locality.orEmpty().ifBlank { address?.subAdminArea.orEmpty() }
	val region = address?.adminArea.orEmpty()
	val country = address?.countryName.orEmpty()
	val placeName = listOf(locality, region, country).filter(String::isNotBlank).distinct().joinToString(", ")
	val data = JSONObject()
		.put("latitude", location.latitude)
		.put("longitude", location.longitude)
		.put("accuracy_meters", location.accuracy)
		.put("captured_at", Instant.ofEpochMilli(location.time).toString())
		.put("age_seconds", ((System.currentTimeMillis() - location.time).coerceAtLeast(0) / 1000))
	if (placeName.isNotBlank()) data.put("place_name", placeName)
	if (locality.isNotBlank()) data.put("locality", locality)
	if (region.isNotBlank()) data.put("admin_area", region)
	if (country.isNotBlank()) data.put("country", country)
	address?.getAddressLine(0)?.takeIf(String::isNotBlank)?.let { data.put("formatted_address", it) }
	val summary = if (placeName.isBlank()) "Current location coordinates are available" else "Current location resolved to $placeName"
	return PhoneToolResult("$summary with ${location.accuracy.toInt()} meter accuracy", data)
}

internal fun callDirection(type: Int): String = when (type) {
	CallLog.Calls.INCOMING_TYPE -> "incoming"
	CallLog.Calls.OUTGOING_TYPE -> "outgoing"
	CallLog.Calls.MISSED_TYPE -> "missed"
	CallLog.Calls.REJECTED_TYPE -> "rejected"
	CallLog.Calls.BLOCKED_TYPE -> "blocked"
	CallLog.Calls.VOICEMAIL_TYPE -> "voicemail"
	else -> "unknown"
}
