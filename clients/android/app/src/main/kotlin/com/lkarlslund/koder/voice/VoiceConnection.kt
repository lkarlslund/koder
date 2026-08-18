package com.lkarlslund.koder.voice

import okhttp3.Call
import okhttp3.Callback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import java.io.IOException
import java.util.UUID
import java.util.concurrent.TimeUnit

class VoiceConnection(
    private val listener: Listener,
    private val client: OkHttpClient = OkHttpClient.Builder()
        .pingInterval(20, TimeUnit.SECONDS)
        .retryOnConnectionFailure(true)
        .build(),
) : AutoCloseable {
    interface Listener {
        fun onConnected()
        fun onFrame(frame: VoiceServerFrame)
        fun onDisconnected(reason: String)
    }

    private var socket: WebSocket? = null
    private var token = ""
    private var server = ""

    fun connect(server: String, bearerToken: String) {
        close()
        token = bearerToken.trim()
        this.server = server.trim()
        val request = Request.Builder()
            .url(VoiceProtocol.websocketUrl(server))
            .apply { if (token.isNotBlank()) header("Authorization", "Bearer $token") }
            .build()
        socket = client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                webSocket.send(VoiceProtocol.hello())
                listener.onConnected()
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                try {
                    listener.onFrame(VoiceProtocol.parse(text))
                } catch (error: Exception) {
                    listener.onDisconnected("Invalid server response: ${error.message}")
                }
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                listener.onDisconnected("Unexpected binary frame (${bytes.size} bytes)")
            }

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                webSocket.close(code, reason)
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                if (socket === webSocket) listener.onDisconnected(reason.ifBlank { "Connection closed" })
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                if (socket === webSocket) {
                    val suffix = response?.let { " (HTTP ${it.code})" }.orEmpty()
                    listener.onDisconnected((t.message ?: "Connection failed") + suffix)
                }
            }
        })
    }

    fun sendUtterance(text: String, sessionId: String = ""): String {
        val id = UUID.randomUUID().toString()
        check(socket?.send(VoiceProtocol.utterance(id, text, sessionId)) == true) {
            "Voice connection is not open"
        }
        return id
    }

    fun selectSession(sessionId: String) {
        check(socket?.send(VoiceProtocol.selectSession(sessionId)) == true) {
            "Voice connection is not open"
        }
    }

    fun loadBytes(url: String, callback: (ByteArray?, String?) -> Unit) {
        val resolved = try {
            VoiceProtocol.resourceUrl(server, url)
        } catch (error: Exception) {
            callback(null, error.message ?: "Invalid presentation URL")
            return
        }
        val request = Request.Builder().url(resolved)
            .apply {
                if (token.isNotBlank() && VoiceProtocol.isSameOrigin(server, resolved)) {
                    header("Authorization", "Bearer $token")
                }
            }.build()
        client.newCall(request).enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                callback(null, e.message ?: "Image download failed")
            }

            override fun onResponse(call: Call, response: Response) {
                response.use {
                    if (!it.isSuccessful) {
                        callback(null, "Image download returned HTTP ${it.code}")
                    } else if (it.body.contentLength() > MAX_PART_BYTES) {
                        callback(null, "Presentation is too large to display")
                    } else {
                        callback(it.body.bytes(), null)
                    }
                }
            }
        })
    }

    override fun close() {
        socket?.close(1000, "call ended")
        socket = null
    }

    private companion object {
        const val MAX_PART_BYTES = 10L * 1024 * 1024
    }
}
