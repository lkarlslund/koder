package com.lkarlslund.koder.voice

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class AudioRoutePreferenceTest {
	@Test
	fun routeChipAlwaysNamesTheCurrentEndpoint() {
		assertEquals("Audio  ▾", audioRouteChipText(""))
		assertEquals("Speaker  ▾", audioRouteChipText("Speaker"))
		assertEquals("Earpiece  ▾", audioRouteChipText("Phone earpiece"))
		assertEquals("Pixel Buds  ▾", audioRouteChipText("Bluetooth: Pixel Buds"))
		assertEquals("USB headset  ▾", audioRouteChipText("Headset: USB headset"))
	}

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

	@Test
	fun routeChangesFallBackAndAdoptExternalAudio() {
		val earpiece = endpoint("ear", VoiceAudioEndpointType.EARPIECE)
		val speaker = endpoint("speaker", VoiceAudioEndpointType.SPEAKER)
		val wired = endpoint("wired", VoiceAudioEndpointType.WIRED_HEADSET)
		val bluetooth = endpoint("buds", VoiceAudioEndpointType.BLUETOOTH)

		assertEquals("speaker", preferredAudioEndpoint(BuiltInAudioRoute.SPEAKER, listOf(earpiece, speaker), null)?.id)
		assertEquals("wired", preferredAudioEndpoint(BuiltInAudioRoute.SPEAKER, listOf(earpiece, speaker, wired), null)?.id)
		assertEquals("buds", preferredAudioEndpoint(BuiltInAudioRoute.SPEAKER, listOf(earpiece, speaker, wired, bluetooth), null)?.id)
		assertEquals("wired", preferredAudioEndpoint(BuiltInAudioRoute.SPEAKER, listOf(earpiece, speaker, wired), "buds")?.id)
		assertEquals("speaker", preferredAudioEndpoint(BuiltInAudioRoute.SPEAKER, listOf(earpiece, speaker), "buds")?.id)
	}

	@Test
	fun availableManualRouteSurvivesOtherEndpointChanges() {
		val speaker = endpoint("speaker", VoiceAudioEndpointType.SPEAKER)
		val bluetooth = endpoint("buds", VoiceAudioEndpointType.BLUETOOTH)
		assertEquals("speaker", preferredAudioEndpoint(BuiltInAudioRoute.SPEAKER, listOf(speaker, bluetooth), "speaker")?.id)
	}

	private fun endpoint(id: String, type: VoiceAudioEndpointType) = VoiceAudioEndpoint(id, id, type, false)
}
