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
    fun decodesAndSelectsDurableVoiceChatsSeparatelyFromTargets() {
        val frame = VoiceProtocol.parse(
            """{"type":"ready","protocol":"voice.v1","call_state":{"voice_session_id":"voice-2","active_session_id":"work-1","sessions":[{"id":"work-1","title":"Laptop"}],"voice_sessions":[{"id":"voice-1","title":"Personal"},{"id":"voice-2","title":"Work"}]}}""",
        )
        assertEquals("voice-2", frame.callState?.voiceSessionId)
        assertEquals(listOf("voice-1", "voice-2"), frame.callState?.voiceSessions?.map { it.id })
        val selection = JSONObject(VoiceProtocol.selectVoiceSession("voice-1"))
        assertEquals("select_voice_session", selection.getString("type"))
        assertEquals("voice-1", selection.getString("voice_session_id"))
    }

	@Test
	fun audioFrameMatchesGoWireFixture() {
		val encoded = VoiceProtocol.encodeAudio(
			VoiceAudioFrame(
				kind = VoiceAudioFrameKind.INPUT_PCM,
				flags = 0,
				sequence = 0x01020304,
				pcm = byteArrayOf(0x34, 0x12, 0xcc.toByte(), 0xff.toByte()),
			),
		)
		assertEquals("4b56413101000000010203043412ccff", encoded.toHex())
		val decoded = VoiceProtocol.decodeAudio(encoded)
		assertEquals(VoiceAudioFrameKind.INPUT_PCM, decoded.kind)
		assertEquals(0x01020304, decoded.sequence)
		assertTrue(decoded.pcm.contentEquals(byteArrayOf(0x34, 0x12, 0xcc.toByte(), 0xff.toByte())))
	}

	@Test
	fun encodesAudioLifecycle() {
		val format = VoiceAudioFormat("pcm_s16le", 16_000, 1)
		val start = JSONObject(VoiceProtocol.audioStart("utterance-1", format))
		assertEquals("audio_start", start.getString("type"))
		assertEquals(16_000, start.getJSONObject("audio_format").getInt("sample_rate"))
		val commit = JSONObject(VoiceProtocol.audioCommit("utterance-1", "session-1"))
		assertEquals("audio_commit", commit.getString("type"))
		assertEquals("session-1", commit.getString("session_id"))
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

	private fun ByteArray.toHex(): String = joinToString("") { "%02x".format(it.toInt() and 0xff) }
}
