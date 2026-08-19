package com.lkarlslund.koder.voice

import androidx.test.ext.junit.runners.AndroidJUnit4
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

@RunWith(AndroidJUnit4::class)
class VoiceSessionClientInstrumentedTest {
    @Test
    fun listsAndCreatesSessionsOverAuthenticatedHttp() {
        val server = MockWebServer()
        server.enqueue(MockResponse.Builder().body("""{"protocol":"voice.v1","voice_sessions":[{"id":"voice-1","title":"Personal"}]}""").build())
        server.enqueue(MockResponse.Builder().code(201).body("""{"protocol":"voice.v1","voice_session":{"id":"voice-2","title":"Work"},"voice_sessions":[]}""").build())
        server.start()
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
            }
        } finally {
            server.close()
        }
    }
}
