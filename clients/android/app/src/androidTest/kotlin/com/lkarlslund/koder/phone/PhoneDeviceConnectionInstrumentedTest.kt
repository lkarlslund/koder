package com.lkarlslund.koder.phone

import android.location.Address
import android.location.Location
import androidx.test.ext.junit.runners.AndroidJUnit4
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.net.InetAddress
import java.util.Locale
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

@RunWith(AndroidJUnit4::class)
class PhoneDeviceConnectionInstrumentedTest {
	@Test
	fun locationResultIncludesHumanPlaceNameForLocalContextQuestions() {
		val capturedAt = System.currentTimeMillis() - 2_000
		val location = Location("test").apply {
			latitude = 56.1629
			longitude = 10.2039
			accuracy = 12.4f
			time = capturedAt
		}
		val address = Address(Locale.ENGLISH).apply {
			locality = "Aarhus"
			adminArea = "Central Denmark Region"
			countryName = "Denmark"
			setAddressLine(0, "Aarhus, Denmark")
		}

		val result = phoneLocationResult(location, address)
		val data = result.data as JSONObject

		assertTrue(result.text.startsWith("Current location resolved to Aarhus, Central Denmark Region, Denmark"))
		assertEquals("Aarhus, Central Denmark Region, Denmark", data.getString("place_name"))
		assertEquals("Aarhus", data.getString("locality"))
		assertEquals("Central Denmark Region", data.getString("admin_area"))
		assertEquals("Denmark", data.getString("country"))
		assertEquals("Aarhus, Denmark", data.getString("formatted_address"))
		assertEquals(56.1629, data.getDouble("latitude"), 0.0001)
		assertEquals(10.2039, data.getDouble("longitude"), 0.0001)
	}

    @Test
    fun advertisesEnabledCapabilitiesAndReturnsToolResult() {
        val server = MockWebServer()
        val helloReceived = CountDownLatch(1)
        val resultReceived = CountDownLatch(1)
        var hello: JSONObject? = null
        var response: JSONObject? = null
        server.enqueue(MockResponse.Builder().webSocketUpgrade(object : WebSocketListener() {
            override fun onMessage(webSocket: WebSocket, text: String) {
                val frame = JSONObject(text)
                when (frame.getString("type")) {
                    "device_hello" -> {
                        hello = frame
                        helloReceived.countDown()
                        webSocket.send(
                            JSONObject().put("type", "device_tool_request").put("protocol", "voice.v1")
                                .put("request_id", "request-1").put("action", "search_contacts")
                                .put("arguments", JSONObject().put("query", "Steen")).toString(),
                        )
                    }
					"device_tool_result" -> {
						response = frame
						webSocket.close(1000, "test complete")
						resultReceived.countDown()
					}
                }
            }
        }).build())
        server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
        try {
            val provider = object : PhoneToolProvider {
                override fun enabledActions() = setOf("search_contacts", "device_status")
                override fun execute(action: String, arguments: Map<String, String>, callback: (Result<PhoneToolResult>) -> Unit) {
                    assertEquals("search_contacts", action)
                    assertEquals("Steen", arguments["query"])
                    callback(Result.success(PhoneToolResult("Found Steen", JSONObject().put("count", 1))))
                }
            }
            PhoneDeviceConnection(provider).use { connection ->
                connection.connect(server.url("/").toString(), "secret", "call-1")
                assertTrue(helloReceived.await(5, TimeUnit.SECONDS))
                assertTrue(resultReceived.await(5, TimeUnit.SECONDS))
            }
            val request = server.takeRequest(5, TimeUnit.SECONDS)
            assertEquals("/voice/v1/device?call_id=call-1", request?.target)
            assertEquals("Bearer secret", request?.headers?.get("Authorization"))
            assertEquals(listOf("device_status", "search_contacts"), hello?.getJSONArray("capabilities")?.let { array ->
                (0 until array.length()).map(array::getString)
            })
			assertEquals("ask", hello?.getJSONObject("confirmation_policies")?.getString("device_status"))
			assertEquals("ask", hello?.getJSONObject("confirmation_policies")?.getString("search_contacts"))
            assertEquals("Found Steen", response?.getJSONObject("result")?.getString("text"))
            assertEquals(1, response?.getJSONObject("result")?.getJSONObject("data")?.getInt("count"))
        } finally {
            server.close()
        }
    }
}
