package com.lkarlslund.koder.voice

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test

class VoiceProtocolTest {
    @Test
    fun decodesSharedGoMessageFixture() {
        val payload = checkNotNull(javaClass.getResourceAsStream("/message.json"))
            .bufferedReader()
            .use { it.readText() }
        val frame = VoiceProtocol.parse(payload)

        assertEquals("message", frame.type)
        assertEquals("The laptop now boots normally.", frame.message?.spokenText)
        assertEquals("text/plain", frame.message?.parts?.single()?.mimeType)
        assertNotNull(frame.message?.delegation)
    }

    @Test
    fun encodesUtteranceCompatibleWithSharedFixture() {
        val payload = VoiceProtocol.utterance("utterance-fixture-1", "Check it", "session-fixture-1")
        val json = JSONObject(payload)

        assertEquals(VOICE_PROTOCOL, json.getString("protocol"))
        assertEquals("utterance", json.getString("type"))
        assertEquals("session-fixture-1", json.getString("session_id"))
    }

    @Test
    fun normalizesServerAddresses() {
        assertEquals("ws://10.0.2.2:7979/voice/v1", VoiceProtocol.websocketUrl("10.0.2.2:7979"))
        assertEquals("wss://koder.example/voice/v1", VoiceProtocol.websocketUrl("https://koder.example"))
        assertEquals("ws://localhost:7979/voice/v1", VoiceProtocol.websocketUrl("ws://localhost:7979/voice/v1"))
    }

    @Test
    fun resolvesPresentationUrlsWithoutLeakingOrigin() {
        assertEquals(
            "http://phone.local:7979/artifacts/image.png",
            VoiceProtocol.resourceUrl("ws://phone.local:7979/voice/v1", "/artifacts/image.png"),
        )
        assertTrue(
            VoiceProtocol.isSameOrigin(
                "ws://phone.local:7979/voice/v1",
                "http://phone.local:7979/artifacts/image.png",
            ),
        )
        assertFalse(
            VoiceProtocol.isSameOrigin(
                "ws://phone.local:7979/voice/v1",
                "https://example.org/image.png",
            ),
        )
    }
}
