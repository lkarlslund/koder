package com.lkarlslund.koder.phone

import android.net.Uri
import com.lkarlslund.koder.voice.VoiceProtocol
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.json.JSONObject
import java.io.IOException

data class PhoneBinding(val server: String, val token: String, val deviceId: String)

class PhoneBindingClient(
	private val identity: PhoneIdentity,
	private val client: OkHttpClient = OkHttpClient(),
) : AutoCloseable {
	fun bind(uri: Uri, callback: (Result<PhoneBinding>) -> Unit) {
		val parsed = runCatching {
			require(uri.scheme == "koder" && uri.host == "bind") { "This is not a Koder binding link" }
			val server = uri.getQueryParameter("server").orEmpty().trim()
			val code = uri.getQueryParameter("code").orEmpty().trim()
			require(server.isNotBlank() && code.isNotBlank()) { "The Koder binding link is incomplete" }
			val body = JSONObject().put("code", code).put("device", identity.toJSON()).toString()
			val request = identity.applyTo(Request.Builder())
				.url(VoiceProtocol.resourceUrl(server, "/voice/v1/bind"))
				.post(body.toRequestBody(JSON))
				.build()
			server to request
		}.getOrElse {
			callback(Result.failure(it))
			return
		}
		client.newCall(parsed.second).enqueue(object : Callback {
			override fun onFailure(call: Call, e: IOException) = callback(Result.failure(e))

			override fun onResponse(call: Call, response: Response) {
				response.use {
					val payload = it.body.string()
					if (!it.isSuccessful) {
						callback(Result.failure(IOException(payload.trim().ifBlank { "Koder returned HTTP ${it.code}" })))
						return
					}
					callback(runCatching {
						val root = JSONObject(payload)
						require(root.getString("protocol") == "voice.v1") { "Unsupported Koder binding response" }
						val binding = root.getJSONObject("binding")
						PhoneBinding(
							server = parsed.first,
							token = binding.getString("token"),
							deviceId = binding.getJSONObject("device").getString("id"),
						)
					})
				}
			}
		})
	}

	override fun close() {
		client.dispatcher.cancelAll()
		client.connectionPool.evictAll()
	}

	private companion object {
		val JSON = "application/json; charset=utf-8".toMediaType()
	}
}
