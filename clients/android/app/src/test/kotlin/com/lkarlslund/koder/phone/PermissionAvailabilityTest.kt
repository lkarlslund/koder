package com.lkarlslund.koder.phone

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PermissionAvailabilityTest {
	@Test
	fun grantedToolIsOnlyAvailableWhileDeviceChannelIsConnected() {
		val ready = permissionAvailability(enabled = true, granted = true, requiresAndroidAccess = false, connected = false)
		assertEquals("● Ready to offer · start or resume a conversation", ready.label)
		assertFalse(ready.remotelyAvailable)

		val available = permissionAvailability(enabled = true, granted = true, requiresAndroidAccess = false, connected = true)
		assertEquals("● Available to Koder", available.label)
		assertTrue(available.remotelyAvailable)
	}

	@Test
	fun missingAndroidAccessStillWinsOverConnection() {
		val state = permissionAvailability(enabled = true, granted = false, requiresAndroidAccess = true, connected = true)
		assertEquals("● Enabled · Android access missing", state.label)
		assertFalse(state.remotelyAvailable)
	}
}
