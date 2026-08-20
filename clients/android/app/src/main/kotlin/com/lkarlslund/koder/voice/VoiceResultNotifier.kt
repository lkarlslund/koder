package com.lkarlslund.koder.voice

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import com.lkarlslund.koder.MainActivity
import com.lkarlslund.koder.R

object VoiceResultNotifier {
	const val EXTRA_VOICE_SESSION_ID = "com.lkarlslund.koder.extra.VOICE_SESSION_ID"
	const val EXTRA_TRANSCRIPT_ID = "com.lkarlslund.koder.extra.TRANSCRIPT_ID"
	private const val CHANNEL_ID = "koder_voice_results"
	private const val NOTIFICATION_BASE = 879_000

	fun show(context: Context, voiceSessionId: String, sessionTitle: String, transcriptId: String, spokenText: String) {
		if (voiceSessionId.isBlank()) return
		val manager = context.getSystemService(NotificationManager::class.java)
		manager.createNotificationChannel(
			NotificationChannel(CHANNEL_ID, "Completed Koder work", NotificationManager.IMPORTANCE_DEFAULT).apply {
				description = "Results from voice conversations that finish while Koder is in the background"
			},
		)
		val open = PendingIntent.getActivity(
			context,
			notificationId(voiceSessionId, transcriptId),
			Intent(context, MainActivity::class.java)
				.putExtra(EXTRA_VOICE_SESSION_ID, voiceSessionId)
				.putExtra(EXTRA_TRANSCRIPT_ID, transcriptId)
				.addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP),
			PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
		)
		val title = sessionTitle.trim().ifBlank { "Koder" }
		val summary = spokenText.replace(Regex("\\s+"), " ").trim().take(180).ifBlank { "The result is ready." }
		manager.notify(
			notificationId(voiceSessionId, transcriptId),
			Notification.Builder(context, CHANNEL_ID)
				.setSmallIcon(R.drawable.ic_koder)
				.setContentTitle("$title is ready")
				.setContentText(summary)
				.setStyle(Notification.BigTextStyle().bigText(summary))
				.setCategory(Notification.CATEGORY_MESSAGE)
				.setAutoCancel(true)
				.setContentIntent(open)
				.build(),
		)
	}

	fun cancel(context: Context, voiceSessionId: String, transcriptId: String) {
		context.getSystemService(NotificationManager::class.java).cancel(notificationId(voiceSessionId, transcriptId))
	}

	internal fun notificationId(voiceSessionId: String, transcriptId: String): Int =
		NOTIFICATION_BASE + (31 * voiceSessionId.hashCode() + transcriptId.hashCode()).and(0x00ff_ffff)
}

internal fun shouldNotifyCompletedWork(appVisible: Boolean, delegatedWorkPending: Boolean, voiceSessionId: String): Boolean =
	!appVisible && delegatedWorkPending && voiceSessionId.isNotBlank()
