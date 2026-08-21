package com.lkarlslund.koder.voice

/** User-facing transport profiles shared by microphone upload and speech download. */
enum class AudioCompression(
	val storageValue: String,
	val title: String,
	val description: String,
	val encoding: String,
	val bitrate: Int,
) {
	PCM(
		"pcm",
		"PCM (uncompressed)",
		"Maximum fidelity and bandwidth; useful for diagnosing recognition.",
		VoiceProtocol.PCM16_ENCODING,
		0,
	),
	OPUS_LOW(
		"opus_low",
		"Opus · low compression",
		"64 kbit/s · highest compressed fidelity.",
		VoiceProtocol.OPUS_ENCODING,
		64_000,
	),
	OPUS_BALANCED(
		"opus_balanced",
		"Opus · balanced",
		"40 kbit/s · good fidelity with much lower bandwidth.",
		VoiceProtocol.OPUS_ENCODING,
		40_000,
	),
	OPUS_HIGH(
		"opus_high",
		"Opus · high compression",
		"24 kbit/s · suited to slower mobile links.",
		VoiceProtocol.OPUS_ENCODING,
		24_000,
	),
	OPUS_HIGHEST(
		"opus_highest",
		"Opus · highest compression",
		"16 kbit/s · minimum bandwidth; speech detail may be reduced.",
		VoiceProtocol.OPUS_ENCODING,
		16_000,
	),
	;

	fun preference() = VoiceAudioTransportPreference(encoding, bitrate)

	companion object {
		fun fromStorage(value: String?, fallback: AudioCompression): AudioCompression =
			entries.firstOrNull { it.storageValue == value } ?: fallback
	}
}
