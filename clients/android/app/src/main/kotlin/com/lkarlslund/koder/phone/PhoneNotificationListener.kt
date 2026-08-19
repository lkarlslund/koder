package com.lkarlslund.koder.phone

import android.app.Notification
import android.service.notification.NotificationListenerService
import android.service.notification.StatusBarNotification

data class PhoneNotification(
    val packageName: String,
    val appName: String,
    val title: String,
    val text: String,
    val postedAt: Long,
)

class PhoneNotificationListener : NotificationListenerService() {
    override fun onListenerConnected() {
        instance = this
    }

    override fun onListenerDisconnected() {
        if (instance === this) instance = null
    }

    override fun onDestroy() {
        if (instance === this) instance = null
        super.onDestroy()
    }

    companion object {
        @Volatile private var instance: PhoneNotificationListener? = null

        fun snapshot(): List<PhoneNotification> {
            val service = instance ?: return emptyList()
            return runCatching {
                service.activeNotifications.orEmpty().mapNotNull(service::describe)
                    .sortedByDescending(PhoneNotification::postedAt)
            }.getOrDefault(emptyList())
        }
    }

    private fun describe(notification: StatusBarNotification): PhoneNotification? {
        val extras = notification.notification.extras
        val title = extras.getCharSequence(Notification.EXTRA_TITLE)?.toString().orEmpty().trim()
        val text = sequenceOf(
            extras.getCharSequence(Notification.EXTRA_TEXT),
            extras.getCharSequence(Notification.EXTRA_BIG_TEXT),
            extras.getCharSequence(Notification.EXTRA_SUB_TEXT),
        ).mapNotNull { it?.toString()?.trim()?.takeIf(String::isNotBlank) }.distinct().joinToString(" · ")
        if (title.isBlank() && text.isBlank()) return null
        val appName = runCatching {
            val info = packageManager.getApplicationInfo(notification.packageName, 0)
            packageManager.getApplicationLabel(info).toString()
        }.getOrDefault(notification.packageName)
        return PhoneNotification(notification.packageName, appName, title, text, notification.postTime)
    }
}
