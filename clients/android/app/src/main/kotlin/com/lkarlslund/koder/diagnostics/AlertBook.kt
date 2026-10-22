package com.lkarlslund.koder.diagnostics

import org.json.JSONArray
import org.json.JSONObject

data class PhoneAlert(
	val id: String,
	val title: String,
	val body: String,
	val fireAtMillis: Long,
	val status: String = SCHEDULED,
	val detail: String = "",
	val updatedAtMillis: Long = 0,
) {
	val finished get() = status != SCHEDULED

	fun toJSON(): JSONObject = JSONObject().put("id", id).put("title", title).put("body", body)
		.put("fire_at", fireAtMillis).put("status", status).put("detail", detail).put("updated_at", updatedAtMillis)

	companion object {
		const val SCHEDULED = "scheduled"
		const val FIRED = "fired"
		const val CANCELLED = "cancelled"
		const val FAILED = "failed"
	}
}

/** The alerts Koder scheduled on this phone, stored as JSON. Pure logic. */
class AlertBook(json: String = "[]") {
	private val alerts = linkedMapOf<String, PhoneAlert>()

	init {
		runCatching { JSONArray(json) }.getOrNull()?.let { array ->
			for (index in 0 until array.length()) {
				val item = array.optJSONObject(index) ?: continue
				val alert = PhoneAlert(
					id = item.optString("id"), title = item.optString("title"), body = item.optString("body"),
					fireAtMillis = item.optLong("fire_at"), status = item.optString("status", PhoneAlert.SCHEDULED),
					detail = item.optString("detail"), updatedAtMillis = item.optLong("updated_at"),
				)
				if (alert.id.isNotBlank()) alerts[alert.id] = alert
			}
		}
	}

	fun toJSON(): String = JSONArray().also { array -> alerts.values.forEach { array.put(it.toJSON()) } }.toString()

	operator fun get(id: String): PhoneAlert? = alerts[id]

	fun add(alert: PhoneAlert) {
		alerts[alert.id] = alert
	}

	fun mark(id: String, status: String, detail: String = "", nowMillis: Long = System.currentTimeMillis()) {
		val alert = alerts[id] ?: return
		alerts[id] = alert.copy(status = status, detail = detail, updatedAtMillis = nowMillis)
	}

	fun armed(): List<PhoneAlert> = alerts.values.filter { it.status == PhoneAlert.SCHEDULED }

	/** Every alert by fire time, after dropping those finished over a week ago. */
	fun all(nowMillis: Long = System.currentTimeMillis()): List<PhoneAlert> {
		alerts.values.removeAll { it.finished && nowMillis - it.updatedAtMillis > KEEP_FINISHED_MILLIS }
		return alerts.values.sortedBy { it.fireAtMillis }
	}

	private companion object {
		const val KEEP_FINISHED_MILLIS = 7L * 24 * 60 * 60 * 1000
	}
}
