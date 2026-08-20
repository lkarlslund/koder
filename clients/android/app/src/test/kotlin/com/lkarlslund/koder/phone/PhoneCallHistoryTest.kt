package com.lkarlslund.koder.phone

import android.Manifest
import android.provider.CallLog
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class PhoneCallHistoryTest {
	@Test
	fun callHistoryCapabilityIsSeparatelyPermissionGated() {
		val capability = checkNotNull(PhoneCapabilities.byID["call_history"])
		assertEquals(setOf("search_call_history"), capability.actions)
		assertTrue(Manifest.permission.READ_CALL_LOG in capability.permissions)
		assertEquals(PhoneActionPolicy.ON, PhoneActionPolicy.legacyDefault("search_call_history"))
	}

	@Test
	fun mapsAndroidCallTypesToStableWireNames() {
		assertEquals("incoming", callDirection(CallLog.Calls.INCOMING_TYPE))
		assertEquals("outgoing", callDirection(CallLog.Calls.OUTGOING_TYPE))
		assertEquals("missed", callDirection(CallLog.Calls.MISSED_TYPE))
		assertEquals("rejected", callDirection(CallLog.Calls.REJECTED_TYPE))
		assertEquals("blocked", callDirection(CallLog.Calls.BLOCKED_TYPE))
		assertEquals("voicemail", callDirection(CallLog.Calls.VOICEMAIL_TYPE))
		assertEquals("unknown", callDirection(Int.MAX_VALUE))
	}
}
