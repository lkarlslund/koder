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
	import java.util.concurrent.CopyOnWriteArrayList
	import java.util.concurrent.atomic.AtomicInteger

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
		val serverReceivedSearch = CountDownLatch(1)
		val clientReceivedSearch = CountDownLatch(1)
		val serverReceivedVoiceCreation = CountDownLatch(1)
		val serverReceivedAudio = CountDownLatch(1)
		val clientReceivedAudio = CountDownLatch(1)
		val clientReceivedPong = CountDownLatch(1)
		var readyFrame: VoiceServerFrame? = null
        var utteranceText = ""
		var voiceTitle = ""
		var historyCursor = ""
		var searchQuery = ""
		var responsePacing = ""
		var roundTripMillis = -1L

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
					if (message.getString("type") == "ping") {
						webSocket.send("""{"type":"pong","protocol":"voice.v1","server_time":"2026-08-20T00:00:00Z"}""")
					}
					if (message.getString("type") == "hello") responsePacing = message.getString("response_pacing")
                    if (message.getString("type") == "utterance") {
                        utteranceText = message.getString("text")
                        serverReceivedUtterance.countDown()
                    }
					if (message.getString("type") == "history") {
						historyCursor = message.getString("before_id")
						serverReceivedHistory.countDown()
					}
					if (message.getString("type") == "search_history") {
						searchQuery = message.getString("query")
						serverReceivedSearch.countDown()
						webSocket.send("""{"type":"history_search","protocol":"voice.v1","search_results":[{"match":{"id":"match-1","role":"assistant","text":"Found it"},"context":[{"id":"match-1","role":"assistant","text":"Found it"}]}]}""")
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
				if (frame.type == "ready") readyFrame = frame
                clientReady.countDown()
				if (frame.type == "history_search" && frame.searchResults.singleOrNull()?.match?.id == "match-1") clientReceivedSearch.countDown()
            }
			override fun onAudioFrame(frame: VoiceAudioFrame) {
				if (frame.kind == VoiceAudioFrameKind.OUTPUT_PCM && frame.pcm.contentEquals(byteArrayOf(3, 0, 4, 0))) {
					clientReceivedAudio.countDown()
				}
			}
			override fun onRoundTripMillis(milliseconds: Long) {
				roundTripMillis = milliseconds
				clientReceivedPong.countDown()
			}
            override fun onDisconnected(reason: String) = Unit
        }).use { connection ->
            connection.connect(server.url("/").toString(), "test-token", responsePacing = VoiceResponsePacing.DETAILED)
            assertTrue("client did not receive ready", clientReady.await(5, TimeUnit.SECONDS))
			connection.sendPing()
			assertTrue("client did not measure protocol round trip", clientReceivedPong.await(5, TimeUnit.SECONDS))
            connection.sendUtterance("check the calendar")
			assertTrue("server did not receive utterance", serverReceivedUtterance.await(5, TimeUnit.SECONDS))
			connection.requestHistory("message-5")
			assertTrue("server did not receive history cursor", serverReceivedHistory.await(5, TimeUnit.SECONDS))
			connection.searchHistory("boots")
			assertTrue("server did not receive search", serverReceivedSearch.await(5, TimeUnit.SECONDS))
			assertTrue("client did not receive search results", clientReceivedSearch.await(5, TimeUnit.SECONDS))
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
		assertEquals("ready", readyFrame?.type)
        assertEquals("check the calendar", utteranceText)
		assertEquals("message-5", historyCursor)
		assertEquals("boots", searchQuery)
		assertEquals("Phone work", voiceTitle)
		assertEquals("detailed", responsePacing)
		assertTrue(roundTripMillis >= 0)
    }

	@Test
	fun reconnectReplaysPendingTurnWithResumeCursor() {
		val firstReady = CountDownLatch(1)
		val firstAudio = CountDownLatch(1)
		val replayReceived = CountDownLatch(1)
		val completed = CountDownLatch(1)
		val utteranceIds = CopyOnWriteArrayList<String>()
		val deliveredAudio = CopyOnWriteArrayList<Long>()
		val assistantMessages = AtomicInteger()

		server.enqueue(
			MockResponse.Builder().webSocketUpgrade(object : WebSocketListener() {
				override fun onOpen(webSocket: WebSocket, response: Response) {
					webSocket.send("""{"type":"ready","protocol":"voice.v1","state":"listening"}""")
				}

				override fun onMessage(webSocket: WebSocket, text: String) {
					val message = JSONObject(text)
					if (message.getString("type") != "utterance") return
					val id = message.getString("utterance_id")
					utteranceIds += id
					webSocket.send("""{"type":"message","protocol":"voice.v1","utterance_id":"$id","message":{"spoken_text":"Still working."}}""")
					webSocket.send("""{"type":"tts_start","protocol":"voice.v1","utterance_id":"$id","audio_format":{"encoding":"pcm_s16le","sample_rate":24000,"channels":1}}""")
					webSocket.send(ByteString.of(*outputAudio(0)))
					check(firstAudio.await(2, TimeUnit.SECONDS))
					webSocket.cancel()
				}

				override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
					webSocket.close(code, reason)
				}
			}).build(),
		)
		server.enqueue(
			MockResponse.Builder().webSocketUpgrade(object : WebSocketListener() {
				override fun onOpen(webSocket: WebSocket, response: Response) {
					webSocket.send("""{"type":"ready","protocol":"voice.v1","state":"listening"}""")
				}

				override fun onMessage(webSocket: WebSocket, text: String) {
					val message = JSONObject(text)
					if (message.getString("type") != "utterance") return
					val id = message.getString("utterance_id")
					utteranceIds += id
					replayReceived.countDown()
					webSocket.send("""{"type":"tts_start","protocol":"voice.v1","utterance_id":"$id","audio_format":{"encoding":"pcm_s16le","sample_rate":24000,"channels":1}}""")
					webSocket.send(ByteString.of(*outputAudio(0))) // Defensive duplicate.
					webSocket.send(ByteString.of(*outputAudio(1)))
					webSocket.send("""{"type":"tts_end","protocol":"voice.v1","utterance_id":"$id"}""")
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
				if (frame.type == "ready") firstReady.countDown()
				if (frame.type == "message") assistantMessages.incrementAndGet()
				if (frame.type == "tts_end") completed.countDown()
			}
			override fun onAudioFrame(frame: VoiceAudioFrame) {
				deliveredAudio += frame.sequence
				if (frame.sequence == 0L) firstAudio.countDown()
			}
			override fun onDisconnected(reason: String) = Unit
		}).use { connection ->
			connection.connect(server.url("/").toString(), "")
			assertTrue("initial socket was not ready", firstReady.await(5, TimeUnit.SECONDS))
			connection.sendUtterance("keep this turn alive")
			assertTrue("pending turn was not replayed", replayReceived.await(8, TimeUnit.SECONDS))
			assertTrue("resumed turn did not complete", completed.await(5, TimeUnit.SECONDS))
			assertEquals(listOf(0L, 1L), deliveredAudio.toList())
			assertEquals(1, assistantMessages.get())
			assertEquals(2, utteranceIds.size)
			assertEquals(utteranceIds[0], utteranceIds[1])
		}

		server.takeRequest(5, TimeUnit.SECONDS)
		val resumed = server.takeRequest(5, TimeUnit.SECONDS)
		val target = resumed?.target.orEmpty()
		assertTrue(target.contains("resume_utterance_id="))
		assertTrue(target.contains("resume_message=true"))
		assertTrue(target.contains("resume_output_sequence=1"))
	}

	private fun outputAudio(sequence: Long): ByteArray = VoiceProtocol.encodeAudio(
		VoiceAudioFrame(VoiceAudioFrameKind.OUTPUT_PCM, 0, sequence, byteArrayOf((sequence + 1).toByte(), 0)),
	)
}
