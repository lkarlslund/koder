package com.lkarlslund.koder.phone

import com.lkarlslund.koder.voice.VOICE_PROTOCOL
import org.json.JSONArray
import org.json.JSONObject
import java.util.Base64

data class PhoneToolRequest(val requestId: String, val action: String, val arguments: Map<String, String>)

object PhoneDeviceProtocol {
    fun hello(policies: Map<String, PhoneActionPolicy>) = JSONObject()
        .put("type", "device_hello")
        .put("protocol", VOICE_PROTOCOL)
		.put("capabilities", JSONArray(policies.keys.sorted()))
		.put("confirmation_policies", JSONObject().also { body ->
			policies.toSortedMap().forEach { (action, policy) -> body.put(action, policy.wireValue) }
		})
        .toString()

    fun parseRequest(text: String): PhoneToolRequest {
        val message = JSONObject(text)
        require(message.getString("type") == "device_tool_request") { "Expected device_tool_request" }
        require(message.getString("protocol") == VOICE_PROTOCOL) { "Unsupported phone tool protocol" }
        val requestId = message.getString("request_id").trim()
        val action = message.getString("action").trim()
        require(requestId.isNotEmpty() && action.isNotEmpty()) { "Phone request identity is missing" }
        val arguments = message.optJSONObject("arguments")?.let { value ->
            value.keys().asSequence().associateWith { value.opt(it)?.toString().orEmpty() }
        }.orEmpty()
        return PhoneToolRequest(requestId, action, arguments)
    }

    fun result(requestId: String, result: PhoneToolResult) = JSONObject()
        .put("type", "device_tool_result")
        .put("protocol", VOICE_PROTOCOL)
        .put("request_id", requestId)
		.put("result", JSONObject().put("text", result.text).also { body ->
			result.data?.let { body.put("data", it) }
			if (result.artifacts.isNotEmpty()) body.put("artifacts", JSONArray().also { artifacts ->
				result.artifacts.forEach { artifact -> artifacts.put(JSONObject()
					.put("id", artifact.id)
					.put("name", artifact.name)
					.put("mime_type", artifact.mimeType)
					.put("data", Base64.getEncoder().encodeToString(artifact.bytes)))
				}
			})
		})
        .toString()

    fun error(requestId: String, message: String) = JSONObject()
        .put("type", "device_tool_result")
        .put("protocol", VOICE_PROTOCOL)
        .put("request_id", requestId)
        .put("error", message.take(2048))
        .toString()
}
