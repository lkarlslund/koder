package com.lkarlslund.koder.phone

data class PermissionAvailability(val label: String, val remotelyAvailable: Boolean)

fun permissionAvailability(enabled: Boolean, granted: Boolean, requiresAndroidAccess: Boolean, connected: Boolean): PermissionAvailability = when {
	enabled && granted && connected -> PermissionAvailability("● Available to Koder", true)
	enabled && granted -> PermissionAvailability("● Ready to offer · start or resume a conversation", false)
	enabled -> PermissionAvailability("● Enabled · Android access missing", false)
	granted && requiresAndroidAccess -> PermissionAvailability("○ Off · Android access granted", false)
	requiresAndroidAccess -> PermissionAvailability("○ Off · Android access not granted", false)
	else -> PermissionAvailability("○ Off · no Android permission needed", false)
}
