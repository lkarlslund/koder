package com.lkarlslund.koder.voice

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Test

class VoiceCallNotificationTest {
	@Test
	fun notificationPrioritizesPauseAndMuteState() {
		assertEquals("Listening · Speaker", notificationDetail(VoiceCallNotificationState(detail = "Listening · Speaker")))
		assertEquals("Microphone muted · Listening · Speaker", notificationDetail(VoiceCallNotificationState(detail = "Listening · Speaker", muted = true)))
		assertEquals("Conversation paused", notificationDetail(VoiceCallNotificationState(detail = "Working…", muted = true, paused = true)))
	}

	@Test
	fun staleControllerCannotDetachItsReplacement() {
		val first = FakeTarget()
		val replacement = FakeTarget()
		VoiceCallControlRegistry.attach(first)
		VoiceCallControlRegistry.attach(replacement)
		VoiceCallControlRegistry.detach(first)
		assertSame(replacement, VoiceCallControlRegistry.target())
		VoiceCallControlRegistry.detach(replacement)
		assertNull(VoiceCallControlRegistry.target())
	}

	@Test
	fun notificationActionsDispatchToTheLiveCall() {
		val target = FakeTarget()
		performVoiceNotificationAction(VoiceCallService.ACTION_MUTE, target)
		performVoiceNotificationAction(VoiceCallService.ACTION_PAUSE, target)
		performVoiceNotificationAction(VoiceCallService.ACTION_ROUTE, target)
		performVoiceNotificationAction(VoiceCallService.ACTION_END, target)
		assertEquals(listOf("mute", "pause", "route", "end"), target.actions)
	}

	private class FakeTarget : VoiceCallControlTarget {
		val actions = mutableListOf<String>()
		override fun notificationState() = VoiceCallNotificationState()
		override fun toggleMuteFromNotification() { actions += "mute" }
		override fun togglePauseFromNotification() { actions += "pause" }
		override fun cycleAudioRouteFromNotification() { actions += "route" }
		override fun endFromNotification() { actions += "end" }
	}
}
