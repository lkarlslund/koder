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
	/** Whether the server has acknowledged [status]. */
	val reported: Boolean = false,
	val updatedAtMillis: Long = 0,
) {
	val finished get() = status != SCHEDULED

	companion object {
		const val SCHEDULED = "scheduled"
		const val FIRED = "fired"
		const val CANCELLED = "cancelled"
		const val FAILED = "failed"
	}
}

/**
 * The phone's record of alerts from Koder: which are armed, and which status
 * changes the server has not yet heard about. Pure logic, stored as JSON.
 */
class AlertBook(json: String = "[]") {
	private val alerts = linkedMapOf<String, PhoneAlert>()

	init {
		runCatching { JSONArray(json) }.getOrNull()?.let { array ->
			for (index in 0 until array.length()) {
				val item = array.optJSONObject(index) ?: continue
				val alert = PhoneAlert(
					id = item.optString("id"), title = item.optString("title"), body = item.optString("body"),
					fireAtMillis = item.optLong("fire_at"), status = item.optString("status", PhoneAlert.SCHEDULED),
					detail = item.optString("detail"), reported = item.optBoolean("reported"),
					updatedAtMillis = item.optLong("updated_at"),
				)
				if (alert.id.isNotBlank()) alerts[alert.id] = alert
			}
		}
	}

	fun toJSON(): String = JSONArray().also { array ->
		alerts.values.forEach { alert ->
			array.put(JSONObject().put("id", alert.id).put("title", alert.title).put("body", alert.body)
				.put("fire_at", alert.fireAtMillis).put("status", alert.status).put("detail", alert.detail)
				.put("reported", alert.reported).put("updated_at", alert.updatedAtMillis))
		}
	}.toString()

	operator fun get(id: String): PhoneAlert? = alerts[id]

	/** Records alerts the server sent and returns those not seen before. */
	fun accept(incoming: List<PhoneAlert>): List<PhoneAlert> = incoming.filter { alert ->
		alert.id.isNotBlank() && alert.id !in alerts
	}.onEach { alerts[it.id] = it.copy(status = PhoneAlert.SCHEDULED, reported = false) }

	fun mark(id: String, status: String, detail: String = "", nowMillis: Long = System.currentTimeMillis()) {
		val alert = alerts[id] ?: return
		alerts[id] = alert.copy(status = status, detail = detail, reported = false, updatedAtMillis = nowMillis)
	}

	fun armed(): List<PhoneAlert> = alerts.values.filter { it.status == PhoneAlert.SCHEDULED }

	fun unreported(): List<PhoneAlert> = alerts.values.filter { !it.reported }

	/** Marks reports as delivered unless the status changed since they were sent. */
	fun reported(sent: List<PhoneAlert>, nowMillis: Long = System.currentTimeMillis()) {
		sent.forEach { report ->
			val current = alerts[report.id] ?: return@forEach
			if (current.status == report.status && current.detail == report.detail) alerts[report.id] = current.copy(reported = true)
		}
		alerts.values.removeAll { it.finished && it.reported && nowMillis - it.updatedAtMillis > KEEP_FINISHED_MILLIS }
	}

	private companion object {
		const val KEEP_FINISHED_MILLIS = 7L * 24 * 60 * 60 * 1000
	}
}
