package com.lkarlslund.koder.phone

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Test

class PhoneDeviceProtocolTest {
    @Test
    fun decodesSharedGoRequestFixture() {
        val payload = checkNotNull(javaClass.getResourceAsStream("/device_tool_request.json")).bufferedReader().use { it.readText() }
        val request = PhoneDeviceProtocol.parseRequest(payload)
        assertEquals("phone-request-fixture-1", request.requestId)
        assertEquals("search_contacts", request.action)
        assertEquals("Steen", request.arguments["query"])
        assertEquals("5", request.arguments["limit"])
    }

    @Test
    fun resultMatchesSharedFixtureShape() {
        val expected = checkNotNull(javaClass.getResourceAsStream("/device_tool_result.json")).bufferedReader().use { JSONObject(it.readText()) }
        val actual = JSONObject(PhoneDeviceProtocol.result("phone-request-fixture-1", PhoneToolResult("Found Steen", JSONObject().put("count", 1))))
        assertEquals(expected.toString(), actual.toString())
    }

    @Test
	fun helloSortsEnabledActionsAndPublishesConfirmationPolicies() {
		val hello = JSONObject(PhoneDeviceProtocol.hello(mapOf("search_contacts" to PhoneActionPolicy.ASK, "device_status" to PhoneActionPolicy.ON)))
        assertEquals("device_hello", hello.getString("type"))
        assertEquals("voice.v1", hello.getString("protocol"))
		assertEquals("device_status", hello.getJSONArray("capabilities").getString(0))
		assertEquals("on", hello.getJSONObject("confirmation_policies").getString("device_status"))
		assertEquals("ask", hello.getJSONObject("confirmation_policies").getString("search_contacts"))
	}
}
