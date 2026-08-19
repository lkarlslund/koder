package com.lkarlslund.koder.voice

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

fun conversationTimeLabel(
	createdAt: Instant?,
	now: Instant = Instant.now(),
	zone: ZoneId = ZoneId.systemDefault(),
): String {
	createdAt ?: return ""
	val created = createdAt.atZone(zone)
	val current = now.atZone(zone)
	return if (created.toLocalDate() == current.toLocalDate()) {
		created.format(DateTimeFormatter.ofPattern("HH:mm"))
	} else {
		created.format(DateTimeFormatter.ofPattern("MMM d, HH:mm"))
	}
}
