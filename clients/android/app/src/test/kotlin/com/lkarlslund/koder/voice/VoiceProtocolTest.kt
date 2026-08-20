package com.lkarlslund.koder.voice

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test

class VoiceProtocolTest {
	@Test
	fun readinessWebsocketURLUsesDiagnosticEndpointWithoutLeakingServerPathDetails() {
		assertEquals("ws://phone.test:7979/voice/v1/readiness", VoiceProtocol.readinessWebsocketUrl("http://phone.test:7979"))
		assertEquals("wss://phone.test/koder/voice/v1/readiness", VoiceProtocol.readinessWebsocketUrl("https://phone.test/koder"))
	}
	@Test
	fun pingUsesTheVersionedVoiceProtocol() {
		val ping = JSONObject(VoiceProtocol.ping())
		assertEquals("ping", ping.getString("type"))
		assertEquals(VOICE_PROTOCOL, ping.getString("protocol"))
	}

	@Test
	fun helloCarriesResponsePacingWithoutAddingAChatMessage() {
		val hello = JSONObject(VoiceProtocol.hello(VoiceResponsePacing.CONCISE))
		assertEquals("hello", hello.getString("type"))
		assertEquals("concise", hello.getString("response_pacing"))
		assertFalse(hello.has("text"))
	}

	@Test
	fun transcriptSearchEncodesQueryAndDecodesJumpContext() {
		val request = JSONObject(VoiceProtocol.searchHistory("boots normally"))
		assertEquals("search_history", request.getString("type"))
		assertEquals("boots normally", request.getString("query"))
		val frame = VoiceProtocol.parse(
			"""{"type":"history_search","protocol":"voice.v1","search_results":[{"match":{"id":"m2","role":"assistant","text":"It boots normally."},"context":[{"id":"m1","role":"user","text":"Check it"},{"id":"m2","role":"assistant","text":"It boots normally."}]}]}""",
		)
		assertEquals("m2", frame.searchResults.single().match.id)
		assertEquals(listOf("m1", "m2"), frame.searchResults.single().context.map { it.id })
	}

	@Test
	fun messageCarriesDurableTranscriptIdentity() {
		val frame = VoiceProtocol.parse(
			"""{"type":"message","protocol":"voice.v1","message":{"spoken_text":"Done.","transcript_id":"assistant-42","parts":[]}}""",
		)
		assertEquals("assistant-42", frame.message?.transcriptId)
	}
	@Test
	fun decodesHistoryPageAndEncodesCursorRequest() {
		val payload = checkNotNull(javaClass.getResourceAsStream("/history.json"))
			.bufferedReader().use { it.readText() }
		val frame = VoiceProtocol.parse(payload)
		assertEquals("history", frame.type)
		assertEquals(listOf("message-1", "message-2"), frame.history.map { it.id })
		assertTrue(frame.historyHasMore)

		val request = JSONObject(VoiceProtocol.history("message-1"))
		assertEquals("history", request.getString("type"))
		assertEquals("message-1", request.getString("before_id"))
		assertEquals(5, request.getInt("limit"))
	}

	@Test
	fun deliberatePresentationTakesOverWithoutOpeningTranscript() {
		val payload = checkNotNull(javaClass.getResourceAsStream("/presentation.json"))
			.bufferedReader()
			.use { it.readText() }
		val frame = VoiceProtocol.parse(payload)
		val presentation = requireNotNull(frame.message).parts.last()
		assertTrue(presentation.isPresentation)
		assertEquals("Appointment", presentation.title)
		assertEquals(KODER_PRESENTATION_MIME, presentation.mimeType)
		assertEquals(7, requireNotNull(PresentationDocuments.parse(presentation.data)).blocks.size)
		assertEquals(
			ConversationSurface.PRESENTATION,
			conversationSurface(active = true, transcriptShown = false, presentationShown = true),
		)
		assertEquals(
			ConversationSurface.TRANSCRIPT,
			conversationSurface(active = true, transcriptShown = true, presentationShown = true),
		)
	}

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
	fun decodesOrganizationMetadataSortsPinnedAndEncodesPartialUpdates() {
		val home = VoiceProtocol.parseHome(
			"""{"protocol":"voice.v1","voice_sessions":[{"id":"newer","title":"Newer","updated_at":"2026-08-20T12:00:00Z","last_message":"Finished the task.","result_count":7,"busy":true,"status":"running_tools"},{"id":"pinned","title":"Pinned","updated_at":"2026-08-18T12:00:00Z","pinned":true,"favorite":true},{"id":"old","title":"Old","archived":true,"deleted":true}]}""",
		)
		assertEquals(listOf("pinned", "newer", "old"), home.voiceSessions.map { it.id })
		assertTrue(home.voiceSessions.first().favorite)
		assertTrue(home.voiceSessions.last().archived)
		assertTrue(home.voiceSessions.last().deleted)
		val newer = home.voiceSessions.first { it.id == "newer" }
		assertEquals("Finished the task.", newer.lastMessage)
		assertEquals(7, newer.resultCount)
		assertTrue(newer.busy)
		assertEquals("running_tools", newer.status)

		val update = JSONObject(VoiceProtocol.updateSessionRequest(pinned = false, archived = true))
		assertEquals(false, update.getBoolean("pinned"))
		assertEquals(true, update.getBoolean("archived"))
		assertFalse(update.has("title"))
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
		val expected = checkNotNull(javaClass.getResourceAsStream("/audio_start.json"))
			.bufferedReader().use { JSONObject(it.readText()) }
		val start = JSONObject(VoiceProtocol.audioStart("utterance-audio-1", format, setOf("en", "da")))
		assertEquals("audio_start", start.getString("type"))
		assertEquals(16_000, start.getJSONObject("audio_format").getInt("sample_rate"))
		assertEquals(expected.toString(), start.toString())
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
