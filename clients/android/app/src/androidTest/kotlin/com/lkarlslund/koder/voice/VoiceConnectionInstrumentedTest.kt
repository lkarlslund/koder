package com.lkarlslund.koder.voice

import androidx.test.ext.junit.runners.AndroidJUnit4
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
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
        var receivedFrame: VoiceServerFrame? = null
        var utteranceText = ""

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
                }

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(code, reason)
                }
            }).build(),
        )
        server.start()

        VoiceConnection(object : VoiceConnection.Listener {
            override fun onConnected() = Unit
            override fun onFrame(frame: VoiceServerFrame) {
                receivedFrame = frame
                clientReady.countDown()
            }
            override fun onDisconnected(reason: String) = Unit
        }).use { connection ->
            connection.connect(server.url("/").toString(), "test-token")
            assertTrue("client did not receive ready", clientReady.await(5, TimeUnit.SECONDS))
            connection.sendUtterance("check the calendar")
            assertTrue("server did not receive utterance", serverReceivedUtterance.await(5, TimeUnit.SECONDS))
        }

        val request = server.takeRequest(5, TimeUnit.SECONDS)
        assertEquals("Bearer test-token", request?.headers?.get("Authorization"))
        assertEquals("ready", receivedFrame?.type)
        assertEquals("check the calendar", utteranceText)
    }
}
