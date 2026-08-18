package com.lkarlslund.koder.voice

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/** Opt-in smoke test for a developer-owned Koder instance; skipped in normal test runs. */
@RunWith(AndroidJUnit4::class)
class VoiceLiveInstrumentedTest {
    @Test
    fun delegatesToOneExistingChat() {
        val arguments = InstrumentationRegistry.getArguments()
        val server = arguments.getString("voiceLiveServer").orEmpty()
        assumeTrue("set voiceLiveServer to enable the live test", server.isNotBlank())

        val ready = CountDownLatch(1)
        val voiceChatSelected = CountDownLatch(1)
        val replied = CountDownLatch(1)
        var sessions = emptyList<VoiceSession>()
        var voiceSessions = emptyList<VoiceSession>()
        var response: VoiceServerFrame? = null
        var disconnectReason = ""
        var terminalError = ""
        val connection = VoiceConnection(object : VoiceConnection.Listener {
            override fun onConnected() = Unit
            override fun onFrame(frame: VoiceServerFrame) {
                if (frame.type == "ready" && frame.callState != null) {
                    sessions = frame.callState.sessions
                    voiceSessions = frame.callState.voiceSessions
                    ready.countDown()
                }
                if (frame.type == "message") {
                    if (frame.message?.spokenText?.startsWith("Using voice chat ") == true) {
                        voiceChatSelected.countDown()
                    } else {
                        response = frame
                        replied.countDown()
                    }
                }
                if (frame.type == "error") {
                    terminalError = frame.error
                    replied.countDown()
                }
            }

            override fun onDisconnected(reason: String) {
                disconnectReason = reason
                ready.countDown()
                replied.countDown()
            }
        })

        try {
            connection.connect(server, arguments.getString("voiceLiveToken").orEmpty())
            assertTrueWithReason("live server did not become ready", ready.await(15, TimeUnit.SECONDS), disconnectReason)
            assertFalse("live server exposed no regular sessions", sessions.isEmpty())
            assertFalse("live server exposed no durable voice chats", voiceSessions.isEmpty())
            println("Koder live voice sessions: " + sessions.joinToString { "${it.title} (${it.id})" })
            connection.selectVoiceSession(voiceSessions.first().id)
            assertTrueWithReason(
                "live server did not switch its durable voice chat",
                voiceChatSelected.await(30, TimeUnit.SECONDS),
                disconnectReason,
            )

            val prompt = arguments.getString("voiceLiveUtterance").orEmpty()
            if (prompt.isBlank()) return
            val requestedId = arguments.getString("voiceLiveSessionId").orEmpty()
            val requestedTitle = arguments.getString("voiceLiveSessionTitle").orEmpty()
            val target = sessions.firstOrNull { it.id == requestedId }
                ?: sessions.firstOrNull { requestedTitle.isNotBlank() && it.title.contains(requestedTitle, ignoreCase = true) }
                ?: sessions.first()
            connection.sendUtterance(prompt, target.id)
            assertTrueWithReason("delegated chat did not reply", replied.await(5, TimeUnit.MINUTES), disconnectReason)

            val message = response?.message
            assertNotNull(
                "live response did not contain a message" +
                    if (terminalError.isBlank()) "" else ": $terminalError",
                message,
            )
            assertEquals(target.id, message?.delegation?.sessionId)
            assertFalse("live response did not contain speech", message?.spokenText.isNullOrBlank())
            println("Koder live voice response from ${target.title}: ${message?.spokenText}")
        } finally {
            connection.close()
        }
    }

    private fun assertTrueWithReason(message: String, condition: Boolean, reason: String) {
        if (!condition) throw AssertionError("$message${if (reason.isBlank()) "" else ": $reason"}")
    }
}
