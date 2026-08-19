package com.lkarlslund.koder

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.widget.EditText
import android.widget.ProgressBar
import android.widget.TextView
import androidx.test.core.app.ActivityScenario
import androidx.test.espresso.Espresso.onView
import androidx.test.espresso.action.ViewActions.click
import androidx.test.espresso.action.ViewActions.scrollTo
import androidx.test.espresso.action.ViewActions.swipeDown
import androidx.test.espresso.action.ViewActions.replaceText
import androidx.test.espresso.assertion.ViewAssertions.matches
import androidx.test.espresso.matcher.ViewMatchers.isDisplayed
import androidx.test.espresso.matcher.ViewMatchers.withContentDescription
import androidx.test.espresso.matcher.ViewMatchers.withText
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.lkarlslund.koder.update.AndroidAppUpdater
import com.lkarlslund.koder.voice.BuiltInAudioRoute
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.Dispatcher
import mockwebserver3.RecordedRequest
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.net.InetAddress
import java.security.MessageDigest
import java.util.concurrent.CountDownLatch

@RunWith(AndroidJUnit4::class)
class MainActivityInstrumentedTest {
    private val context: Context get() = InstrumentationRegistry.getInstrumentation().targetContext

    @After
    fun clearSettings() {
        context.getSharedPreferences("koder_voice", Context.MODE_PRIVATE).edit().clear().commit()
    }

    @Test
	fun builtInAudioRouteDefaultsToSpeakerAndRemembersEarpiece() {
		clearSettings()
		val secureSettings = SecureSettings(context)
		assertEquals(BuiltInAudioRoute.SPEAKER, secureSettings.load().builtInAudioRoute)
		secureSettings.saveBuiltInAudioRoute(BuiltInAudioRoute.EARPIECE)
		assertEquals(BuiltInAudioRoute.EARPIECE, secureSettings.load().builtInAudioRoute)
	}

    @Test
	fun setupDoesNotKeepTheScreenAwake() {
		clearSettings()
		ActivityScenario.launch(MainActivity::class.java).use { scenario ->
			scenario.onActivity { activity ->
				assertEquals(
					0,
					activity.window.attributes.flags and WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON,
				)
			}
		}
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
	fun bindingLinkRegistersAndConnectsWithoutManualCredentialEntry() {
		clearSettings()
		val server = MockWebServer()
		server.enqueue(MockResponse.Builder().code(201).body(
			"""{"protocol":"voice.v1","binding":{"device":{"id":"device-1","name":"Test phone","registered_at":"2026-08-19T20:00:00Z"},"token":"kdv1_private"}}""",
		).build())
		server.enqueue(MockResponse.Builder().body(
			"""{"protocol":"voice.v1","voice_sessions":[{"id":"voice-1","title":"Bound conversation"}]}""",
		).build())
		server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		try {
			val bindingURI = Uri.parse("koder://bind?server=${Uri.encode(server.url("/").toString())}&code=kdb1_invitation")
			val launch = Intent(context, MainActivity::class.java).setAction(Intent.ACTION_VIEW).setData(bindingURI)
			ActivityScenario.launch<MainActivity>(launch).use { scenario ->
				val labels = waitForText(scenario, "Bound conversation")
				assertTrue(labels.contains("Conversations"))
				val saved = SecureSettings(context).load()
				assertEquals(server.url("/").toString(), saved.server)
				assertEquals("kdv1_private", saved.token)
			}
			val bindRequest = server.takeRequest()
			assertEquals("/voice/v1/bind", bindRequest.target)
			assertTrue(bindRequest.headers["X-Koder-Device-ID"].orEmpty().isNotBlank())
			val homeRequest = server.takeRequest()
			assertEquals("/voice/v1/sessions", homeRequest.target)
			assertEquals("Bearer kdv1_private", homeRequest.headers["Authorization"])
		} finally {
			server.close()
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
        server.enqueue(
            MockResponse.Builder()
                .code(200)
                .body("""{"protocol":"voice.v1","server_time":"2026-08-19T12:00:05Z","version":"0.1.0","commit":"abc123","dirty":"false","build_time":"2026-08-19T11:00:00Z","started_at":"2026-08-19T12:00:00Z","uptime_seconds":5,"platform":"linux/amd64","go_version":"go1.26.6","logical_cpus":16,"max_procs":12,"goroutines":42,"heap_alloc_bytes":1048576,"heap_sys_bytes":4194304,"heap_objects":1234,"gc_cycles":9,"session_count":7,"voice_session_count":3,"voice_connection_active":false,"token_required":true}""")
                .build(),
        )
        server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
        try {
            SecureSettings(context).save(server.url("/").toString(), "")
            ActivityScenario.launch(MainActivity::class.java).use { scenario ->
                val labels = waitForText(scenario, "Personal")
                assertTrue(labels.contains("Conversations"))
                assertTrue(labels.contains("Koder Voice"))
                assertTrue(labels.any { it.startsWith("Personal") })
                assertTrue(labels.any { it.contains("New conversation") })
                assertTrue(labels.contains("⋮"))
                assertFalse(labels.contains("Send"))
                assertFalse(labels.any { it.contains("Message Koder") })
                scenario.onActivity { activity ->
                    activity.showUpdateStatus(AndroidAppUpdater.Status.Downloading("next-dev", 42, 100))
                    val progress = activity.findViewById<View>(android.R.id.content)
                        .findByDescription("Update download progress") as ProgressBar
                    assertEquals(View.VISIBLE, progress.visibility)
                    assertEquals(42, progress.progress)
                    assertTrue(
                        activity.findViewById<View>(android.R.id.content).allText()
                            .any { it.equals("Downloading next-dev · 42%", ignoreCase = true) },
                    )
                }
                onView(withContentDescription("More options")).perform(click())
                onView(withText("Server info")).check(matches(isDisplayed()))
                onView(withText("Settings")).check(matches(isDisplayed()))
                onView(withText("About")).check(matches(isDisplayed()))
                onView(withText("Server info")).perform(click())
                waitForDisplayedText("Runtime")
                onView(withText("linux/amd64")).check(matches(isDisplayed()))
                onView(withText("7")).check(matches(isDisplayed()))
                onView(withText("Close")).perform(click())
                onView(withContentDescription("More options")).perform(click())
                onView(withText("Settings")).perform(click())
                val settingsLabels = waitForText(scenario, "Phone tools")
                assertTrue(settingsLabels.contains("Device information"))
				assertTrue(settingsLabels.contains("Speech recognition"))
				assertTrue(settingsLabels.contains("Automatic (all languages)"))
				assertTrue(settingsLabels.contains("Danish (da)"))
				assertTrue(settingsLabels.contains("German (de)"))
				assertTrue(settingsLabels.contains("Response pacing"))
				assertTrue(settingsLabels.any { it.contains("Detailed") })
				onView(withContentDescription("Recognize Danish")).perform(scrollTo(), click())
				assertEquals(setOf("da"), SecureSettings(context).load().speechLanguages)
				scenario.onActivity { activity ->
					val root = activity.findViewById<View>(android.R.id.content)
					val scroll = root.firstScrollView()
					val target = root.findByDescription("Use detailed response pacing")
					val bounds = android.graphics.Rect().also(target::getDrawingRect)
					scroll.offsetDescendantRectToMyCoords(target, bounds)
					scroll.scrollTo(0, bounds.top.coerceAtLeast(0))
					target.performClick()
				}
				assertEquals(com.lkarlslund.koder.voice.VoiceResponsePacing.DETAILED, SecureSettings(context).load().responsePacing)
				var scrollBefore = 0
				var scrollAfter = 0
				scenario.onActivity { activity ->
					val root = activity.findViewById<View>(android.R.id.content)
					val scroll = root.firstScrollView()
					val target = root.findByDescription("Allow Device information")
					val bounds = android.graphics.Rect().also(target::getDrawingRect)
					scroll.offsetDescendantRectToMyCoords(target, bounds)
					scroll.scrollTo(0, bounds.top.coerceAtLeast(0))
					scrollBefore = scroll.scrollY
					target.performClick()
					scrollAfter = scroll.scrollY
				}
				assertTrue(scrollBefore > 0)
				assertTrue("tool toggle reset scroll from $scrollBefore to $scrollAfter", kotlin.math.abs(scrollAfter - scrollBefore) <= 2)
				assertTrue("device" in SecureSettings(context).load().enabledPhoneCapabilities)
                assertTrue(settingsLabels.contains("Contacts"))
                assertTrue(settingsLabels.contains("Notifications & email previews"))
            }
        } finally {
            server.close()
        }
    }

	@Test
	fun newConversationOpensWhileServerCreatesIt() {
		val allowCreateResponse = CountDownLatch(1)
		val server = MockWebServer()
		server.dispatcher = object : Dispatcher() {
			override fun dispatch(request: RecordedRequest): MockResponse = when {
				request.method == "POST" && request.target == "/voice/v1/sessions" -> {
					allowCreateResponse.await()
					MockResponse.Builder().code(201).body(
						"""{"protocol":"voice.v1","voice_sessions":[{"id":"voice-new","title":"Trip planning"}],"voice_session":{"id":"voice-new","title":"Trip planning"}}""",
					).build()
				}
				else -> MockResponse.Builder().body(
					"""{"protocol":"voice.v1","voice_sessions":[{"id":"voice-1","title":"Personal"}]}""",
				).build()
			}
		}
		server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		try {
			SecureSettings(context).save(server.url("/").toString(), "")
			ActivityScenario.launch(MainActivity::class.java).use { scenario ->
				waitForText(scenario, "Personal")
				onView(withContentDescription("Create a new voice conversation")).perform(click())
				onView(withContentDescription("Conversation name")).perform(replaceText("Trip planning"))
				onView(withText("Create")).perform(click())
				val labels = waitForText(scenario, "Creating conversation…")
				assertTrue(labels.contains("Trip planning"))
				onView(withContentDescription("Conversation connecting")).check(matches(isDisplayed()))
			}
		} finally {
			allowCreateResponse.countDown()
			server.close()
		}
	}

    @Test
    fun pullingConversationListRefreshesAndShowsNewestFirst() {
        val server = MockWebServer()
        val packageInfo = context.packageManager.getPackageInfo(
            context.packageName,
            PackageManager.PackageInfoFlags.of(PackageManager.GET_SIGNING_CERTIFICATES.toLong()),
        )
        val signer = MessageDigest.getInstance("SHA-256")
            .digest(requireNotNull(packageInfo.signingInfo).apkContentsSigners.single().toByteArray())
            .joinToString("") { "%02x".format(it) }
        val update = """"app_update":{"channel":"local","application_id":"${context.packageName}","version_code":${packageInfo.longVersionCode + 1},"version_name":"next-dev","signing_certificate_sha256":"$signer","apk_sha256":"${"a".repeat(64)}","apk_size":1,"download_uri":"/voice/v1/android/koder.apk"}"""
        server.enqueue(
            MockResponse.Builder().body(
                """{"protocol":"voice.v1","voice_sessions":[{"id":"voice-1","title":"Older","updated_at":"2026-08-18T12:00:00Z"}]}""",
            ).build(),
        )
        server.enqueue(
            MockResponse.Builder().body(
                """{"protocol":"voice.v1","voice_sessions":[{"id":"voice-1","title":"Older","updated_at":"2026-08-18T12:00:00Z"},{"id":"voice-2","title":"Newest","updated_at":"2026-08-19T12:00:00Z"}],$update}""",
            ).build(),
        )
        server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
        try {
            SecureSettings(context).save(server.url("/").toString(), "")
            ActivityScenario.launch(MainActivity::class.java).use { scenario ->
                waitForText(scenario, "Older")
                onView(withContentDescription("Conversation list")).perform(swipeDown())
                val labels = waitForText(scenario, "Newest")
                assertTrue(labels.indexOf("Newest") < labels.indexOf("Older"))
                assertEquals(2, labels.count { it.startsWith("Last used") })
                assertTrue(waitForText(scenario, "Update Koder").any { it.contains("next-dev") })
            }
        } finally {
            server.close()
        }
    }

	@Test
	fun transcriptSearchJumpsToServerSideMatch() {
		val instrumentation = InstrumentationRegistry.getInstrumentation()
		val permissions = buildList {
			add(Manifest.permission.RECORD_AUDIO)
			if (Build.VERSION.SDK_INT >= 31) add(Manifest.permission.BLUETOOTH_CONNECT)
			if (Build.VERSION.SDK_INT >= 33) add(Manifest.permission.POST_NOTIFICATIONS)
		}
		permissions.forEach { instrumentation.uiAutomation.grantRuntimePermission(context.packageName, it) }
		val server = MockWebServer()
		var voiceSocket: WebSocket? = null
		server.enqueue(MockResponse.Builder().body(
			"""{"protocol":"voice.v1","voice_sessions":[{"id":"voice-search","title":"Searchable"}]}""",
		).build())
		server.enqueue(MockResponse.Builder().webSocketUpgrade(object : WebSocketListener() {
			override fun onOpen(webSocket: WebSocket, response: Response) {
				voiceSocket = webSocket
				webSocket.send(
					"""{"type":"ready","protocol":"voice.v1","audio_config":{"input":{"encoding":"pcm_s16le","sample_rate":16000,"channels":1},"output":{"encoding":"pcm_s16le","sample_rate":24000,"channels":1},"max_utterance_seconds":120},"call_state":{"voice_session_id":"voice-search","active_session_id":"","sessions":[],"voice_sessions":[{"id":"voice-search","title":"Searchable"}],"history":[{"id":"latest","role":"assistant","text":"Newest answer"}]}}""",
				)
			}

			override fun onMessage(webSocket: WebSocket, text: String) {
				if (org.json.JSONObject(text).optString("type") == "search_history") {
					webSocket.send(
						"""{"type":"history_search","protocol":"voice.v1","search_results":[{"match":{"id":"match","role":"assistant","text":"BIOS gate found"},"context":[{"id":"before","role":"user","text":"What blocked it?"},{"id":"match","role":"assistant","text":"BIOS gate found"}]}]}""",
					)
				}
			}
		}).build())
		server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		try {
			SecureSettings(context).save(server.url("/").toString(), "")
			ActivityScenario.launch(MainActivity::class.java).use { scenario ->
				waitForText(scenario, "Searchable")
				onView(withContentDescription("Open voice conversation Searchable")).perform(click())
				onView(withContentDescription("Search transcript")).perform(click())
				onView(withContentDescription("Transcript search query")).perform(replaceText("BIOS"))
				onView(withText("Search")).perform(click())
				waitForDisplayedText("Transcript matches")
				onView(withText("Koder · BIOS gate found")).perform(click())
				val labels = waitForText(scenario, "BIOS gate found")
				assertTrue(labels.contains("What blocked it?"))
				assertTrue(labels.contains("Back to latest"))
				onView(withText("Back to latest")).perform(click())
				assertTrue(waitForText(scenario, "Newest answer").contains("Newest answer"))
			}
		} finally {
			voiceSocket?.close(1000, "test complete")
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

    private fun waitForDisplayedText(wanted: String) {
        repeat(50) {
            val displayed = runCatching {
                onView(withText(wanted)).check(matches(isDisplayed()))
            }.isSuccess
            if (displayed) return
            Thread.sleep(100)
        }
        error("Timed out waiting for displayed text $wanted")
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

	private fun View.firstScrollView(): android.widget.ScrollView {
		if (this is android.widget.ScrollView) return this
		if (this is ViewGroup) {
			repeat(childCount) { index -> runCatching { return getChildAt(index).firstScrollView() } }
		}
		error("No ScrollView found")
	}
}
