package com.lkarlslund.koder

import android.content.Context
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import android.widget.TextView
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import org.junit.After
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class MainActivityInstrumentedTest {
    private val context: Context get() = InstrumentationRegistry.getInstrumentation().targetContext

    @After
    fun clearSettings() {
        context.getSharedPreferences("koder_voice", Context.MODE_PRIVATE).edit().clear().commit()
    }

    @Test
    fun firstRunExplainsBothConnectionFields() {
        clearSettings()
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            scenario.onActivity { activity ->
                val labels = activity.findViewById<View>(android.R.id.content).allText()
                assertTrue(labels.contains("Welcome to Koder Voice"))
                assertTrue(labels.contains("Server address"))
                assertTrue(labels.contains("Access token"))
                assertTrue(labels.any { it.contains("stored encrypted") })
                assertTrue(labels.contains("Connect"))
                assertFalse(labels.contains("Send"))
            }
        }
    }

    @Test
    fun savedAccessTokenIsMaskedOnSetupScreen() {
        SecureSettings(context).save("", "visible-secret")
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            scenario.onActivity { activity ->
                val tokenField = activity.findViewById<View>(android.R.id.content)
                    .findByDescription("Access token") as EditText
                assertTrue(tokenField.inputType and android.text.InputType.TYPE_TEXT_VARIATION_PASSWORD != 0)
                assertNotEquals("visible-secret", tokenField.transformationMethod.getTransformation(tokenField.text, tokenField).toString())
            }
        }
    }

    @Test
    fun connectedHomeListsVoiceChatsWithoutComposer() {
        val server = MockWebServer()
        server.enqueue(
            MockResponse.Builder()
                .code(200)
                .body("""{"protocol":"voice.v1","voice_sessions":[{"id":"voice-1","title":"Personal","last_message":"Calendar updated"}]}""")
                .build(),
        )
        server.start()
        try {
            SecureSettings(context).save(server.url("/").toString(), "")
            ActivityScenario.launch(MainActivity::class.java).use { scenario ->
                val labels = waitForText(scenario, "Personal")
                assertTrue(labels.contains("Conversations"))
                assertTrue(labels.any { it.startsWith("Personal") })
                assertTrue(labels.contains("New conversation"))
                assertTrue(labels.contains("Settings"))
                assertFalse(labels.contains("Send"))
                assertFalse(labels.any { it.contains("Message Koder") })
            }
        } finally {
            server.close()
        }
    }

    private fun waitForText(scenario: ActivityScenario<MainActivity>, wanted: String): List<String> {
        var lastLabels = emptyList<String>()
        repeat(50) {
            var labels = emptyList<String>()
            scenario.onActivity { activity -> labels = activity.findViewById<View>(android.R.id.content).allText() }
            if (labels.any { it.contains(wanted) }) return labels
            lastLabels = labels
            Thread.sleep(100)
        }
        error("Timed out waiting for $wanted; visible text was $lastLabels")
    }

    private fun View.allText(): List<String> = buildList {
        if (this@allText is TextView) {
            add(this@allText.text.toString())
            add(this@allText.hint?.toString().orEmpty())
        }
        if (this@allText is ViewGroup) {
            repeat(this@allText.childCount) { index -> addAll(this@allText.getChildAt(index).allText()) }
        }
    }

    private fun View.findByDescription(description: String): View {
        if (contentDescription?.toString() == description) return this
        if (this is ViewGroup) {
            repeat(childCount) { index ->
                runCatching { return getChildAt(index).findByDescription(description) }
            }
        }
        error("No view with content description $description")
    }
}
