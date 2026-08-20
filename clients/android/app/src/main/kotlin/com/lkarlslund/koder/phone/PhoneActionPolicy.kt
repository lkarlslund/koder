package com.lkarlslund.koder.phone

enum class PhoneActionPolicy(val wireValue: String, val label: String) {
	OFF("off", "Off"),
	ASK("ask", "Ask"),
	ON("on", "On");

	companion object {
		fun fromStorage(value: String?): PhoneActionPolicy? = entries.firstOrNull { it.wireValue == value }

		fun legacyDefault(action: String): PhoneActionPolicy = if (action in sensitiveActions) ASK else ON

		val sensitiveActions = setOf(
			"place_call", "send_sms", "compose_email", "create_contact", "create_calendar_event", "open_map",
			"set_alarm", "set_timer", "write_clipboard", "open_url", "media_control", "open_app", "share_text",
		)
	}
}

fun actionTitle(action: String): String = action.replace('_', ' ').replaceFirstChar { it.titlecase() }
