package com.lkarlslund.koder.voice

import androidx.test.ext.junit.runners.AndroidJUnit4
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.net.InetAddress
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

@RunWith(AndroidJUnit4::class)
class VoiceSessionClientInstrumentedTest {
    @Test
    fun uploadsPhotoAsAuthenticatedMultipartAttachment() {
        val server = MockWebServer()
        server.enqueue(
            MockResponse.Builder()
                .code(201)
                .body(
                    """{"id":"draft-1","name":"camera.jpg","mime":"image/jpeg","path":"/drafts/draft-1.jpg","size":9,"source":"phone_image"}""",
                )
                .build(),
        )
        server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
        try {
            VoiceSessionClient().use { client ->
                val uploaded = CountDownLatch(1)
                var uploadResult: Result<VoiceAttachmentDraft>? = null
                client.uploadImage(
                    server.url("/").toString(),
                    "secret",
                    "jpeg-data".toByteArray(),
                    "camera.jpg",
                    "image/jpeg",
                ) {
                    uploadResult = it
                    uploaded.countDown()
                }

                assertTrue(uploaded.await(5, TimeUnit.SECONDS))
                assertEquals("draft-1", uploadResult?.getOrThrow()?.id)
                val request = server.takeRequest(5, TimeUnit.SECONDS)
                assertEquals("POST", request?.method)
                assertEquals("/voice/v1/attachments/images", request?.target)
                assertEquals("Bearer secret", request?.headers?.get("Authorization"))
                assertTrue(request?.headers?.get("Content-Type").orEmpty().startsWith("multipart/form-data; boundary="))
                val body = request?.body?.utf8().orEmpty()
                assertTrue(body.contains("name=\"image\""))
                assertTrue(body.contains("filename=\"camera.jpg\""))
                assertTrue(body.contains("jpeg-data"))
            }
        } finally {
            server.close()
        }
    }

    @Test
    fun listsAndCreatesSessionsOverAuthenticatedHttp() {
        val server = MockWebServer()
        server.enqueue(MockResponse.Builder().body("""{"protocol":"voice.v1","voice_sessions":[{"id":"voice-1","title":"Personal"}]}""").build())
        server.enqueue(MockResponse.Builder().code(201).body("""{"protocol":"voice.v1","voice_session":{"id":"voice-2","title":"Work"},"voice_sessions":[]}""").build())
		server.enqueue(MockResponse.Builder().body("""{"protocol":"voice.v1","voice_session":{"id":"voice-1","title":"Family"},"voice_sessions":[]}""").build())
		server.enqueue(MockResponse.Builder().body("""{"protocol":"voice.v1","session":{"id":"voice-1","title":"Family","favorite":true},"sessions":[]}""").build())
		server.enqueue(MockResponse.Builder().body("""{"protocol":"voice.v1","chats":[{"id":"chat-1","session_id":"session-1","title":"Archived chat","role":"voice","archived":true}]}""").build())
		server.enqueue(MockResponse.Builder().body("""{"protocol":"voice.v1","chats":[]}""").build())
		server.enqueue(MockResponse.Builder().body("""{"protocol":"voice.v1","voice_sessions":[]}""").build())
        server.enqueue(MockResponse.Builder().body("""{"protocol":"voice.v1","server_time":"2026-08-19T12:00:05Z","version":"0.1.0","commit":"abc123","dirty":"false","build_time":"2026-08-19T11:00:00Z","started_at":"2026-08-19T12:00:00Z","uptime_seconds":5,"platform":"linux/amd64","go_version":"go1.26.6","logical_cpus":16,"max_procs":12,"goroutines":42,"heap_alloc_bytes":1048576,"heap_sys_bytes":4194304,"heap_objects":1234,"gc_cycles":9,"session_count":7,"voice_session_count":3,"voice_connection_active":false,"token_required":true}""").build())
        server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
        try {
            VoiceSessionClient().use { client ->
                val listed = CountDownLatch(1)
                var listResult: Result<VoiceHome>? = null
                client.list(server.url("/").toString(), "secret") {
                    listResult = it
                    listed.countDown()
                }
                assertTrue(listed.await(5, TimeUnit.SECONDS))
                assertEquals("voice-1", listResult?.getOrThrow()?.voiceSessions?.single()?.id)
                val listRequest = server.takeRequest(5, TimeUnit.SECONDS)
                assertEquals("GET", listRequest?.method)
                assertEquals("/voice/v1/sessions", listRequest?.target)
                assertEquals("Bearer secret", listRequest?.headers?.get("Authorization"))

                val created = CountDownLatch(1)
                var createResult: Result<VoiceHome>? = null
                client.create(server.url("/").toString(), "secret", "Work") {
                    createResult = it
                    created.countDown()
                }
                assertTrue(created.await(5, TimeUnit.SECONDS))
                assertEquals("voice-2", createResult?.getOrThrow()?.createdVoiceSession?.id)
                val createRequest = server.takeRequest(5, TimeUnit.SECONDS)
                assertEquals("POST", createRequest?.method)
                assertTrue(createRequest?.body?.utf8().orEmpty().contains("\"title\":\"Work\""))

				val renamed = CountDownLatch(1)
				client.rename(server.url("/").toString(), "secret", "voice-1", "Family") { renamed.countDown() }
				assertTrue(renamed.await(5, TimeUnit.SECONDS))
				val renameRequest = server.takeRequest(5, TimeUnit.SECONDS)
				assertEquals("PATCH", renameRequest?.method)
				assertEquals("/voice/v1/sessions/voice-1", renameRequest?.target)
				assertTrue(renameRequest?.body?.utf8().orEmpty().contains("\"title\":\"Family\""))

				val starred = CountDownLatch(1)
				client.update(server.url("/").toString(), "secret", "voice-1", favorite = true) { starred.countDown() }
				assertTrue(starred.await(5, TimeUnit.SECONDS))
				val starRequest = server.takeRequest(5, TimeUnit.SECONDS)
				assertEquals("PATCH", starRequest?.method)
				assertEquals("/voice/v1/sessions/voice-1", starRequest?.target)
				assertTrue(starRequest?.body?.utf8().orEmpty().contains("\"favorite\":true"))

				val chatUpdated = CountDownLatch(1)
				client.updateChat(server.url("/").toString(), "secret", "session-1", "chat-1", title = "Archived chat", archived = true) { chatUpdated.countDown() }
				assertTrue(chatUpdated.await(5, TimeUnit.SECONDS))
				val chatUpdateRequest = server.takeRequest(5, TimeUnit.SECONDS)
				assertEquals("PATCH", chatUpdateRequest?.method)
				assertEquals("/voice/v1/sessions/session-1/chats/chat-1", chatUpdateRequest?.target)
				assertTrue(chatUpdateRequest?.body?.utf8().orEmpty().contains("\"archived\":true"))

				val chatDeleted = CountDownLatch(1)
				client.deleteChat(server.url("/").toString(), "secret", "session-1", "chat-1") { chatDeleted.countDown() }
				assertTrue(chatDeleted.await(5, TimeUnit.SECONDS))
				val chatDeleteRequest = server.takeRequest(5, TimeUnit.SECONDS)
				assertEquals("DELETE", chatDeleteRequest?.method)
				assertEquals("/voice/v1/sessions/session-1/chats/chat-1", chatDeleteRequest?.target)

				val deleted = CountDownLatch(1)
				client.delete(server.url("/").toString(), "secret", "voice-1") { deleted.countDown() }
				assertTrue(deleted.await(5, TimeUnit.SECONDS))
				val deleteRequest = server.takeRequest(5, TimeUnit.SECONDS)
				assertEquals("DELETE", deleteRequest?.method)
				assertEquals("/voice/v1/sessions/voice-1", deleteRequest?.target)

                val receivedInfo = CountDownLatch(1)
                var infoResult: Result<ServerInfo>? = null
                client.serverInfo(server.url("/").toString(), "secret") {
                    infoResult = it
                    receivedInfo.countDown()
                }
                assertTrue(receivedInfo.await(5, TimeUnit.SECONDS))
                assertEquals("abc123", infoResult?.getOrThrow()?.commit)
                assertTrue((infoResult?.getOrThrow()?.roundTripMillis ?: -1) >= 0)
                val infoRequest = server.takeRequest(5, TimeUnit.SECONDS)
                assertEquals("GET", infoRequest?.method)
                assertEquals("/voice/v1/server-info", infoRequest?.target)
                assertEquals("Bearer secret", infoRequest?.headers?.get("Authorization"))
            }
        } finally {
            server.close()
        }
    }
}
