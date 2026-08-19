package com.lkarlslund.koder.voice

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.drawable.Icon
import android.os.Build
import android.os.IBinder
import com.lkarlslund.koder.MainActivity
import com.lkarlslund.koder.R

class VoiceCallService : Service() {
	override fun onCreate() {
		super.onCreate()
		activeService = this
		getSystemService(NotificationManager::class.java).createNotificationChannel(
			NotificationChannel(CHANNEL_ID, "Koder voice conversation", NotificationManager.IMPORTANCE_LOW),
		)
		show(VoiceCallControlRegistry.target()?.notificationState() ?: VoiceCallNotificationState())
	}

	override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
		val target = VoiceCallControlRegistry.target()
		performVoiceNotificationAction(intent?.action, target)
		if (intent?.action == ACTION_END && target == null) stopSelf()
		if (intent?.action != ACTION_END) show(target?.notificationState() ?: VoiceCallNotificationState())
		return START_NOT_STICKY
	}

	override fun onDestroy() {
		if (activeService === this) activeService = null
		super.onDestroy()
	}

	override fun onBind(intent: Intent?): IBinder? = null

	private fun show(state: VoiceCallNotificationState) {
		val open = PendingIntent.getActivity(
			this,
			0,
			Intent(this, MainActivity::class.java),
			PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
		)
		val notification = Notification.Builder(this, CHANNEL_ID)
			.setContentTitle("Koder voice conversation")
			.setContentText(notificationDetail(state))
			.setSubText(state.audioRoute)
			.setSmallIcon(R.drawable.ic_koder)
			.setCategory(Notification.CATEGORY_CALL)
			.setVisibility(Notification.VISIBILITY_PUBLIC)
			.setOngoing(true)
			.setOnlyAlertOnce(true)
			.setContentIntent(open)
			.addAction(notificationAction(if (state.muted) R.drawable.ic_voice_mic_off else R.drawable.ic_voice_mic, if (state.muted) "Unmute" else "Mute", ACTION_MUTE, 1))
			.addAction(notificationAction(if (state.paused) R.drawable.ic_voice_play else R.drawable.ic_voice_pause, if (state.paused) "Resume" else "Pause", ACTION_PAUSE, 2))
			.addAction(notificationAction(R.drawable.ic_voice_end, "End", ACTION_END, 4))
			.addAction(notificationAction(R.drawable.ic_voice_audio, "Audio", ACTION_ROUTE, 3))
			.build()
		if (Build.VERSION.SDK_INT >= 34) {
			startForeground(
				NOTIFICATION_ID,
				notification,
				ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE or ServiceInfo.FOREGROUND_SERVICE_TYPE_PHONE_CALL,
			)
		} else {
			startForeground(NOTIFICATION_ID, notification)
		}
	}

	private fun action(action: String, requestCode: Int): PendingIntent = PendingIntent.getService(
		this,
		requestCode,
		Intent(this, VoiceCallService::class.java).setAction(action),
		PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
	)

	private fun notificationAction(icon: Int, title: String, action: String, requestCode: Int): Notification.Action =
		Notification.Action.Builder(Icon.createWithResource(this, icon), title, action(action, requestCode)).build()

	companion object {
		private const val CHANNEL_ID = "koder_voice_call"
		const val NOTIFICATION_ID = 7979
		internal const val ACTION_MUTE = "com.lkarlslund.koder.voice.MUTE"
		internal const val ACTION_PAUSE = "com.lkarlslund.koder.voice.PAUSE"
		internal const val ACTION_ROUTE = "com.lkarlslund.koder.voice.ROUTE"
		internal const val ACTION_END = "com.lkarlslund.koder.voice.END"
		@Volatile private var activeService: VoiceCallService? = null

		fun refresh(state: VoiceCallNotificationState) {
			activeService?.show(state)
		}
	}
}

internal fun notificationDetail(state: VoiceCallNotificationState): String = when {
	state.paused -> "Conversation paused"
	state.muted -> "Microphone muted · ${state.detail}"
	else -> state.detail.ifBlank { "Voice conversation active" }
}

internal fun performVoiceNotificationAction(action: String?, target: VoiceCallControlTarget?) {
	when (action) {
		VoiceCallService.ACTION_MUTE -> target?.toggleMuteFromNotification()
		VoiceCallService.ACTION_PAUSE -> target?.togglePauseFromNotification()
		VoiceCallService.ACTION_ROUTE -> target?.cycleAudioRouteFromNotification()
		VoiceCallService.ACTION_END -> target?.endFromNotification()
	}
}
