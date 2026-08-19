package com.lkarlslund.koder.phone

import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.util.concurrent.TimeUnit

class PhoneDeviceConnection(
    private val provider: PhoneToolProvider,
    private val client: OkHttpClient = OkHttpClient.Builder().pingInterval(20, TimeUnit.SECONDS).build(),
	private val identity: PhoneIdentity? = null,
) : AutoCloseable {
    private var socket: WebSocket? = null
    private var generation = 0L

    @Synchronized
    fun connect(server: String, token: String, callId: String) {
        closeSocket()
        generation++
        val currentGeneration = generation
        val base = server.trim().let { if ("://" in it) it else "http://$it" }.toHttpUrl()
        val scheme = if (base.scheme == "https") "https" else "http"
        val url = base.newBuilder().scheme(scheme).encodedPath("/voice/v1/device")
            .addQueryParameter("call_id", callId).build()
		val request = Request.Builder().also { builder -> identity?.applyTo(builder) }.url(url)
            .apply { if (token.isNotBlank()) header("Authorization", "Bearer ${token.trim()}") }.build()
        socket = client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                if (!owns(webSocket, currentGeneration)) return webSocket.close(1000, "superseded").let { }
                webSocket.send(PhoneDeviceProtocol.hello(provider.enabledActions()))
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                if (!owns(webSocket, currentGeneration)) return
                val message = runCatching { PhoneDeviceProtocol.parseRequest(text) }.getOrElse {
                    sendError(webSocket, "", "Invalid Koder phone request: ${it.message}"); return
                }
                provider.execute(message.action, message.arguments) { result ->
                    if (!owns(webSocket, currentGeneration)) return@execute
                    result.fold(
                        onSuccess = { responseResult ->
                            webSocket.send(PhoneDeviceProtocol.result(message.requestId, responseResult))
                        },
                        onFailure = { sendError(webSocket, message.requestId, it.message ?: "Phone action failed") },
                    )
                }
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) = clear(webSocket, currentGeneration)
            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) = clear(webSocket, currentGeneration)
        })
    }

    private fun sendError(socket: WebSocket, requestId: String, message: String) {
        socket.send(PhoneDeviceProtocol.error(requestId, message))
    }

    @Synchronized private fun owns(candidate: WebSocket, expectedGeneration: Long) = socket === candidate && generation == expectedGeneration
    @Synchronized private fun clear(candidate: WebSocket, expectedGeneration: Long) { if (owns(candidate, expectedGeneration)) socket = null }

    @Synchronized
    private fun closeSocket() {
        socket?.close(1000, "voice conversation ended")
        socket = null
    }

    @Synchronized
    override fun close() {
        generation++
        closeSocket()
    }

}
