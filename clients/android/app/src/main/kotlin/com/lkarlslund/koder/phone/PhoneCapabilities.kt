package com.lkarlslund.koder.phone

import android.Manifest
import android.os.Build

data class PhoneCapability(
    val id: String,
    val title: String,
    val description: String,
    val actions: Set<String>,
    val permissions: Array<String> = emptyArray(),
    val notificationAccess: Boolean = false,
)

fun PhoneCapability.permissionsForCurrentDevice(): Array<String> = if (id == "photos") {
	if (Build.VERSION.SDK_INT >= 33) arrayOf(Manifest.permission.READ_MEDIA_IMAGES)
	else arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE)
} else permissions

object PhoneCapabilities {
    val all = listOf(
        PhoneCapability(
            "device", "Device information",
            "Battery, charging, storage, network, locale, installed apps, and app launching.",
            setOf("device_status", "list_apps", "open_app"),
        ),
        PhoneCapability(
            "location", "Location & maps",
            "Current location and opening addresses or places in your map app.",
            setOf("get_location", "open_map"),
            arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION),
        ),
        PhoneCapability(
            "contacts", "Contacts",
            "Search names, phone numbers, and email addresses; prepare new contacts.",
            setOf("search_contacts", "create_contact", "edit_contact"),
            arrayOf(Manifest.permission.READ_CONTACTS),
        ),
        PhoneCapability(
            "calendar", "Calendar",
            "Read upcoming appointments and prepare calendar entries for review.",
            setOf("upcoming_calendar", "create_calendar_event", "edit_calendar_event"),
            arrayOf(Manifest.permission.READ_CALENDAR),
        ),
        PhoneCapability(
            "calls", "Phone calls",
            "Place a real call after you confirm it on this phone.",
            setOf("place_call"),
            arrayOf(Manifest.permission.CALL_PHONE),
        ),
		PhoneCapability(
			"call_history", "Call history",
			"Search recent incoming, outgoing, rejected, and missed calls by contact, number, or time.",
			setOf("search_call_history"),
			arrayOf(Manifest.permission.READ_CALL_LOG),
		),
        PhoneCapability(
            "messages", "SMS messages",
            "Search stored text messages or send one after phone confirmation.",
            setOf("search_sms", "send_sms"),
            arrayOf(Manifest.permission.READ_SMS, Manifest.permission.SEND_SMS),
        ),
        PhoneCapability(
            "notifications", "Notifications & email previews",
            "Search notifications currently visible to Android, including mail and chat previews.",
            setOf("recent_notifications"),
            notificationAccess = true,
        ),
        PhoneCapability(
            "clipboard", "Clipboard",
            "Read or, after confirmation, replace the clipboard while Koder is open.",
            setOf("read_clipboard", "write_clipboard"),
        ),
        PhoneCapability(
			"photos", "Photos",
			"Let Koder inspect and edit the newest photo in your media library when you ask.",
			setOf("phone_photos_search", "phone_photos_thumbs", "phone_photo_view", "phone_photo_transfer"),
		),
		PhoneCapability(
            "assistant_actions", "Personal assistant actions",
            "Draft email, set alarms and timers, open safe links, control media, and share text.",
            setOf("compose_email", "set_alarm", "set_timer", "open_url", "media_control", "share_text"),
        ),
    )

    val byID = all.associateBy(PhoneCapability::id)
    val knownActions = all.flatMap { it.actions }.toSet()
}
