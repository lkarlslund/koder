package com.lkarlslund.koder.voice

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class AudioRoutePreferenceTest {
	@Test
	fun speakerIsTheDefaultBuiltInRoute() {
		assertEquals(BuiltInAudioRoute.SPEAKER, BuiltInAudioRoute.fromStorage(null))
		assertEquals(BuiltInAudioRoute.SPEAKER, BuiltInAudioRoute.fromStorage("invalid"))
	}

	@Test
	fun rememberedChoiceWinsAndFallsBackToAvailableRoute() {
		assertEquals(BuiltInAudioRoute.EARPIECE, preferredBuiltInAudioRoute(BuiltInAudioRoute.EARPIECE, true, true))
		assertEquals(BuiltInAudioRoute.SPEAKER, preferredBuiltInAudioRoute(BuiltInAudioRoute.SPEAKER, true, true))
		assertEquals(BuiltInAudioRoute.SPEAKER, preferredBuiltInAudioRoute(BuiltInAudioRoute.EARPIECE, false, true))
		assertEquals(BuiltInAudioRoute.EARPIECE, preferredBuiltInAudioRoute(BuiltInAudioRoute.SPEAKER, true, false))
		assertNull(preferredBuiltInAudioRoute(BuiltInAudioRoute.SPEAKER, false, false))
	}

	@Test
	fun automaticRoutingAlwaysPrefersExternalAudio() {
		val all = VoiceAudioEndpointType.entries.toSet()
		assertEquals(VoiceAudioEndpointType.BLUETOOTH, automaticAudioEndpointType(BuiltInAudioRoute.EARPIECE, all))
		assertEquals(
			VoiceAudioEndpointType.WIRED_HEADSET,
			automaticAudioEndpointType(BuiltInAudioRoute.SPEAKER, all - VoiceAudioEndpointType.BLUETOOTH),
		)
		assertEquals(
			VoiceAudioEndpointType.SPEAKER,
			automaticAudioEndpointType(BuiltInAudioRoute.SPEAKER, setOf(VoiceAudioEndpointType.EARPIECE, VoiceAudioEndpointType.SPEAKER)),
		)
	}
}
