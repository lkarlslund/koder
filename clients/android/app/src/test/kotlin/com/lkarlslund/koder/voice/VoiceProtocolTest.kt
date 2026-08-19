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
			"""{"type":"ready","protocol":"voice.v1","call_state":{"voice_session_id":"voice-2","active_session_id":"work-1","sessions":[{"id":"work-1","title":"Laptop"}],"voice_sessions":[{"id":"voice-1","title":"Personal"},{"id":"voice-2","title":"Work"}],"history":[{"id":"message-1","role":"user","text":"What happened?","created_at":"2026-08-19T12:00:00Z"}]}}""",
        )
        assertEquals("voice-2", frame.callState?.voiceSessionId)
        assertEquals(listOf("voice-1", "voice-2"), frame.callState?.voiceSessions?.map { it.id })
		assertEquals("What happened?", frame.callState?.history?.single()?.text)
        val selection = JSONObject(VoiceProtocol.selectVoiceSession("voice-1"))
        assertEquals("select_voice_session", selection.getString("type"))
        assertEquals("voice-1", selection.getString("voice_session_id"))
		val creation = JSONObject(VoiceProtocol.createVoiceSession("Phone work"))
		assertEquals("create_voice_session", creation.getString("type"))
		assertEquals("Phone work", creation.getString("title"))
    }

    @Test
    fun decodesSignedAppUpdate() {
        val frame = VoiceProtocol.parse(
            """{"type":"ready","protocol":"voice.v1","app_update":{"channel":"local","application_id":"com.lkarlslund.koder.dev","version_code":42,"version_name":"0.1.0-local.test","signing_certificate_sha256":"${"a".repeat(64)}","apk_sha256":"${"b".repeat(64)}","apk_size":1234,"download_uri":"/voice/v1/android/koder.apk"}}""",
        )
        assertEquals("com.lkarlslund.koder.dev", frame.appUpdate?.applicationId)
        assertEquals(42L, frame.appUpdate?.versionCode)
        assertEquals("/voice/v1/android/koder.apk", frame.appUpdate?.downloadUri)
    }

    @Test
    fun decodesVoiceHomeAndCreatesSessionRequest() {
        val home = VoiceProtocol.parseHome(
            """{"protocol":"voice.v1","voice_session":{"id":"voice-2","title":"New work"},"voice_sessions":[{"id":"voice-1","title":"Personal","last_message":"See you tomorrow","updated_at":"2026-08-18T12:00:00Z"},{"id":"voice-2","title":"New work","updated_at":"2026-08-19T12:00:00Z"}]}""",
        )
        assertEquals(listOf("voice-2", "voice-1"), home.voiceSessions.map { it.id })
        assertEquals("See you tomorrow", home.voiceSessions.last().lastMessage)
        assertEquals("2026-08-19T12:00:00Z", home.voiceSessions.first().updatedAt.toString())
        assertEquals("voice-2", home.createdVoiceSession?.id)

        val request = JSONObject(VoiceProtocol.createSessionRequest("  Phone work  "))
        assertEquals("Phone work", request.getString("title"))
    }

    @Test
    fun decodesServerDiagnostics() {
        val info = VoiceProtocol.parseServerInfo(
            """{"protocol":"voice.v1","server_time":"2026-08-19T12:00:05Z","version":"0.1.0","commit":"abc123","dirty":"false","build_time":"2026-08-19T11:00:00Z","started_at":"2026-08-19T12:00:00Z","uptime_seconds":5,"platform":"linux/amd64","go_version":"go1.26.6","logical_cpus":16,"max_procs":12,"goroutines":42,"heap_alloc_bytes":1048576,"heap_sys_bytes":4194304,"heap_objects":1234,"gc_cycles":9,"session_count":7,"voice_session_count":3,"voice_connection_active":true,"voice_connection_since":"2026-08-19T12:00:01Z","token_required":true}""",
        )
        assertEquals("abc123", info.commit)
        assertEquals("linux/amd64", info.platform)
        assertEquals(42, info.goroutines)
        assertEquals(7, info.sessionCount)
        assertEquals(3, info.voiceSessionCount)
        assertTrue(info.voiceConnectionActive)
        assertTrue(info.tokenRequired)
        assertEquals("2026-08-19T12:00:01Z", info.voiceConnectionSince.toString())
    }

	@Test
	fun decodesWorkingTargetForLocalWaitingCue() {
		val payload = checkNotNull(javaClass.getResourceAsStream("/working.json"))
			.bufferedReader()
			.use { it.readText() }
		val frame = VoiceProtocol.parse(payload)
		assertEquals("working", frame.state)
		assertEquals("session-fixture-1", frame.workingOn?.id)
		assertEquals("Laptop repair", frame.workingOn?.title)
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
        assertEquals(
            "http://10.0.2.2:7979/voice/v1/sessions",
            VoiceProtocol.resourceUrl("10.0.2.2:7979", "/voice/v1/sessions"),
        )
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
