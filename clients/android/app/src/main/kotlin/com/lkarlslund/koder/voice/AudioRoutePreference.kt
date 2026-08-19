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
