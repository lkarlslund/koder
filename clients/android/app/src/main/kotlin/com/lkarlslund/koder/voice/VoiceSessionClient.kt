package com.lkarlslund.koder.voice

import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.IOException

class VoiceSessionClient(
    private val client: OkHttpClient = OkHttpClient(),
) : AutoCloseable {
    fun list(server: String, token: String, callback: (Result<VoiceHome>) -> Unit) {
        request(server, token, "/voice/v1/sessions", null, VoiceProtocol::parseHome, callback)
    }

    fun create(server: String, token: String, title: String, callback: (Result<VoiceHome>) -> Unit) {
        request(
            server,
            token,
            "/voice/v1/sessions",
            VoiceProtocol.createSessionRequest(title),
            VoiceProtocol::parseHome,
            callback,
        )
    }

	fun rename(server: String, token: String, sessionId: String, title: String, callback: (Result<VoiceHome>) -> Unit) {
		request(server, token, "/voice/v1/sessions/$sessionId", VoiceProtocol.createSessionRequest(title), VoiceProtocol::parseHome, callback, "PATCH")
	}

    fun serverInfo(server: String, token: String, callback: (Result<ServerInfo>) -> Unit) {
        val startedAt = System.nanoTime()
	        request(server, token, "/voice/v1/server-info", null, VoiceProtocol::parseServerInfo, callback = { result ->
            val elapsedMillis = ((System.nanoTime() - startedAt) / 1_000_000).coerceAtLeast(0)
            callback(result.map { it.copy(roundTripMillis = elapsedMillis) })
	        })
    }

    private fun <T> request(
        server: String,
        token: String,
        path: String,
        body: String?,
        parse: (String) -> T,
        callback: (Result<T>) -> Unit,
		method: String = if (body == null) "GET" else "POST",
    ) {
        val request = try {
            Request.Builder()
                .url(VoiceProtocol.resourceUrl(server, path))
				.apply {
                    if (token.isNotBlank()) header("Authorization", "Bearer ${token.trim()}")
					when (method) {
						"POST" -> post(requireNotNull(body).toRequestBody(JSON))
						"PATCH" -> patch(requireNotNull(body).toRequestBody(JSON))
					}
                }
                .build()
        } catch (error: Exception) {
            callback(Result.failure(error))
            return
        }
        client.newCall(request).enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                callback(Result.failure(e))
            }

            override fun onResponse(call: Call, response: Response) {
                response.use {
                    val payload = it.body.string()
                    if (!it.isSuccessful) {
                        val detail = payload.trim().ifBlank { "HTTP ${it.code}" }
                        callback(Result.failure(IOException("Koder returned $detail")))
                        return
                    }
                    callback(runCatching { parse(payload) })
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
