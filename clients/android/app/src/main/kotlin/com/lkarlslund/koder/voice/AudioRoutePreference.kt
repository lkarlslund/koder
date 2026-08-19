package com.lkarlslund.koder.voice

enum class BuiltInAudioRoute(val storageValue: String) {
	EARPIECE("earpiece"),
	SPEAKER("speaker"),
	;

	companion object {
		fun fromStorage(value: String?): BuiltInAudioRoute = entries.firstOrNull {
			it.storageValue == value
		} ?: SPEAKER
	}
}

enum class VoiceAudioEndpointType { BLUETOOTH, WIRED_HEADSET, EARPIECE, SPEAKER, OTHER }

fun audioRouteChipText(endpointName: String): String {
	val normalized = endpointName.trim()
	val label = when {
		normalized.isBlank() -> "Audio"
		normalized.startsWith("Bluetooth:", ignoreCase = true) -> normalized.substringAfter(':').trim().ifBlank { "Bluetooth" }
		normalized.startsWith("Headset:", ignoreCase = true) -> normalized.substringAfter(':').trim().ifBlank { "Headset" }
		normalized.equals("Phone earpiece", ignoreCase = true) -> "Earpiece"
		normalized.equals("phone audio", ignoreCase = true) -> "Phone audio"
		else -> normalized
	}
	return "$label  ▾"
}

data class VoiceAudioEndpoint(
	val id: String,
	val name: String,
	val type: VoiceAudioEndpointType,
	val current: Boolean,
)

fun preferredBuiltInAudioRoute(
	preference: BuiltInAudioRoute,
	earpieceAvailable: Boolean,
	speakerAvailable: Boolean,
): BuiltInAudioRoute? = when {
	preference == BuiltInAudioRoute.EARPIECE && earpieceAvailable -> BuiltInAudioRoute.EARPIECE
	preference == BuiltInAudioRoute.SPEAKER && speakerAvailable -> BuiltInAudioRoute.SPEAKER
	speakerAvailable -> BuiltInAudioRoute.SPEAKER
	earpieceAvailable -> BuiltInAudioRoute.EARPIECE
	else -> null
}

fun automaticAudioEndpointType(
	builtInRoute: BuiltInAudioRoute,
	available: Set<VoiceAudioEndpointType>,
): VoiceAudioEndpointType? {
	if (VoiceAudioEndpointType.BLUETOOTH in available) return VoiceAudioEndpointType.BLUETOOTH
	if (VoiceAudioEndpointType.WIRED_HEADSET in available) return VoiceAudioEndpointType.WIRED_HEADSET
	return when (preferredBuiltInAudioRoute(
		builtInRoute,
		VoiceAudioEndpointType.EARPIECE in available,
		VoiceAudioEndpointType.SPEAKER in available,
	)) {
		BuiltInAudioRoute.EARPIECE -> VoiceAudioEndpointType.EARPIECE
		BuiltInAudioRoute.SPEAKER -> VoiceAudioEndpointType.SPEAKER
		null -> null
	}
}

fun preferredAudioEndpoint(
	builtInRoute: BuiltInAudioRoute,
	available: List<VoiceAudioEndpoint>,
	manualEndpointId: String?,
): VoiceAudioEndpoint? {
	manualEndpointId?.let { id -> available.firstOrNull { it.id == id } }?.let { return it }
	val targetType = automaticAudioEndpointType(builtInRoute, available.mapTo(linkedSetOf()) { it.type }) ?: return null
	return available.firstOrNull { it.type == targetType }
}
