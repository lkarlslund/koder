package com.lkarlslund.koder.phone

import android.net.Uri
import androidx.test.ext.junit.runners.AndroidJUnit4
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.net.InetAddress
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

@RunWith(AndroidJUnit4::class)
class PhoneBindingInstrumentedTest {
	@Test
	fun bindingLinkRegistersHandsetAndReturnsPrivateToken() {
		val server = MockWebServer()
		server.enqueue(MockResponse.Builder().code(201).body(
			"""{"protocol":"voice.v1","binding":{"device":{"id":"device-1","name":"Test phone","registered_at":"2026-08-19T20:00:00Z"},"token":"kdv1_private"}}""",
		).build())
		server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		try {
			val identity = PhoneIdentity(
				installationId = "installation-1",
				name = "Test phone",
				manufacturer = "Koder",
				model = "Emulator",
				androidVersion = "16",
				appVersion = "0.1.0-test",
				appId = "com.lkarlslund.koder.dev",
			)
			val finished = CountDownLatch(1)
			var result: Result<PhoneBinding>? = null
			PhoneBindingClient(identity).use { client ->
				val uri = Uri.parse("koder://bind?server=${Uri.encode(server.url("/").toString())}&code=kdb1_invitation")
				client.bind(uri) {
					result = it
					finished.countDown()
				}
				assertTrue(finished.await(5, TimeUnit.SECONDS))
			}
			val binding = result?.getOrThrow()
			assertEquals("kdv1_private", binding?.token)
			assertEquals("device-1", binding?.deviceId)
			val request = server.takeRequest(5, TimeUnit.SECONDS)
			assertEquals("/voice/v1/bind", request?.target)
			assertEquals("installation-1", request?.headers?.get("X-Koder-Device-ID"))
			val body = JSONObject(request?.body?.utf8().orEmpty())
			assertEquals("kdb1_invitation", body.getString("code"))
			assertEquals("Test phone", body.getJSONObject("device").getString("name"))
		} finally {
			server.close()
		}
	}
}
