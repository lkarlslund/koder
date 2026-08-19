package com.lkarlslund.koder.voice

import androidx.test.ext.junit.runners.AndroidJUnit4
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
	import okio.ByteString
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.net.InetAddress
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

@RunWith(AndroidJUnit4::class)
class VoiceConnectionInstrumentedTest {
    private val server = MockWebServer()

    @After
    fun closeServer() {
        server.close()
    }

    @Test
    fun websocketHandshakeAndUtteranceMatchGoProtocol() {
        val clientReady = CountDownLatch(1)
        val serverReceivedUtterance = CountDownLatch(1)
		val serverReceivedHistory = CountDownLatch(1)
		val serverReceivedVoiceCreation = CountDownLatch(1)
		val serverReceivedAudio = CountDownLatch(1)
		val clientReceivedAudio = CountDownLatch(1)
        var receivedFrame: VoiceServerFrame? = null
        var utteranceText = ""
		var voiceTitle = ""
		var historyCursor = ""

        server.enqueue(
            MockResponse.Builder().webSocketUpgrade(object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    webSocket.send(
                        JSONObject()
                            .put("type", "ready")
                            .put("protocol", VOICE_PROTOCOL)
                            .put("state", "listening")
                            .put("call_state", JSONObject().put("sessions", org.json.JSONArray()))
                            .toString(),
                    )
                }

                override fun onMessage(webSocket: WebSocket, text: String) {
                    val message = JSONObject(text)
                    if (message.getString("type") == "utterance") {
                        utteranceText = message.getString("text")
                        serverReceivedUtterance.countDown()
                    }
					if (message.getString("type") == "history") {
						historyCursor = message.getString("before_id")
						serverReceivedHistory.countDown()
					}
					if (message.getString("type") == "create_voice_session") {
						voiceTitle = message.getString("title")
						serverReceivedVoiceCreation.countDown()
					}
                }

				override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
					val frame = VoiceProtocol.decodeAudio(bytes.toByteArray())
					if (frame.kind == VoiceAudioFrameKind.INPUT_PCM && frame.sequence == 0L) {
						serverReceivedAudio.countDown()
						webSocket.send(
							ByteString.of(
								*VoiceProtocol.encodeAudio(
									VoiceAudioFrame(VoiceAudioFrameKind.OUTPUT_PCM, 0, 0, byteArrayOf(3, 0, 4, 0)),
								),
							),
						)
					}
				}

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(code, reason)
                }
            }).build(),
        )
        server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)

        VoiceConnection(object : VoiceConnection.Listener {
            override fun onConnected() = Unit
            override fun onFrame(frame: VoiceServerFrame) {
                receivedFrame = frame
                clientReady.countDown()
            }
			override fun onAudioFrame(frame: VoiceAudioFrame) {
				if (frame.kind == VoiceAudioFrameKind.OUTPUT_PCM && frame.pcm.contentEquals(byteArrayOf(3, 0, 4, 0))) {
					clientReceivedAudio.countDown()
				}
			}
            override fun onDisconnected(reason: String) = Unit
        }).use { connection ->
            connection.connect(server.url("/").toString(), "test-token")
            assertTrue("client did not receive ready", clientReady.await(5, TimeUnit.SECONDS))
            connection.sendUtterance("check the calendar")
			assertTrue("server did not receive utterance", serverReceivedUtterance.await(5, TimeUnit.SECONDS))
			connection.requestHistory("message-5")
			assertTrue("server did not receive history cursor", serverReceivedHistory.await(5, TimeUnit.SECONDS))
			connection.createVoiceSession("Phone work")
			assertTrue("server did not receive voice-chat creation", serverReceivedVoiceCreation.await(5, TimeUnit.SECONDS))
			val format = VoiceAudioFormat("pcm_s16le", 16_000, 1)
			val utteranceId = connection.startAudio(format)
			connection.sendAudio(0, byteArrayOf(1, 0, 2, 0))
			connection.commitAudio(utteranceId)
			assertTrue("server did not receive binary PCM", serverReceivedAudio.await(5, TimeUnit.SECONDS))
			assertTrue("client did not receive binary PCM", clientReceivedAudio.await(5, TimeUnit.SECONDS))
        }

        val request = server.takeRequest(5, TimeUnit.SECONDS)
        assertEquals("Bearer test-token", request?.headers?.get("Authorization"))
		assertTrue(request?.target.orEmpty().contains("call_id="))
        assertEquals("ready", receivedFrame?.type)
        assertEquals("check the calendar", utteranceText)
		assertEquals("message-5", historyCursor)
		assertEquals("Phone work", voiceTitle)
    }
}
