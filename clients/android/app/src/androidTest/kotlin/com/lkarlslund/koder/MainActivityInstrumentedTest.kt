package com.lkarlslund.koder

import android.Manifest
import android.app.Notification
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.widget.EditText
import android.widget.ImageButton
import android.widget.ProgressBar
import android.widget.TextView
import androidx.test.core.app.ActivityScenario
import androidx.lifecycle.Lifecycle
import androidx.test.espresso.Espresso.onView
import androidx.test.espresso.action.ViewActions.click
import androidx.test.espresso.action.ViewActions.longClick
import androidx.test.espresso.action.ViewActions.scrollTo
import androidx.test.espresso.action.ViewActions.swipeDown
import androidx.test.espresso.action.ViewActions.replaceText
import androidx.test.espresso.assertion.ViewAssertions.matches
import androidx.test.espresso.matcher.RootMatchers.isDialog
import androidx.test.espresso.matcher.ViewMatchers.isDisplayed
import androidx.test.espresso.matcher.ViewMatchers.withContentDescription
import androidx.test.espresso.matcher.ViewMatchers.withId
import androidx.test.espresso.matcher.ViewMatchers.withText
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.lkarlslund.koder.update.AndroidAppUpdater
import com.lkarlslund.koder.phone.AndroidPhoneToolProvider
import com.lkarlslund.koder.phone.PhoneActionPolicy
import com.lkarlslund.koder.voice.BuiltInAudioRoute
import com.lkarlslund.koder.voice.SavedVoiceResponse
import com.lkarlslund.koder.voice.SavedVoiceResponseKind
import com.lkarlslund.koder.voice.VoiceCallService
import com.lkarlslund.koder.voice.VoiceResultNotifier
import com.lkarlslund.koder.voice.VoiceHapticCue
import com.lkarlslund.koder.voice.VoiceHaptics
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.Dispatcher
import mockwebserver3.RecordedRequest
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.net.InetAddress
import java.security.MessageDigest
import java.util.Base64
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

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
	fun readinessSuccessIsScopedToTheTestedServer() {
		val secureSettings = SecureSettings(context)
		assertFalse(secureSettings.readinessComplete("http://first:7979"))
		secureSettings.markReadinessComplete("http://first:7979/")
		assertTrue(secureSettings.readinessComplete("http://first:7979/"))
		assertFalse(secureSettings.readinessComplete("http://second:7979/"))
	}

	@Test
	fun homeActionsHaveTalkBackNamesAndMinimumTouchTargets() {
		val server = MockWebServer()
		server.enqueue(MockResponse.Builder().body(
			"""{"protocol":"voice.v1","voice_sessions":[{"id":"voice-1","title":"Accessible conversation"}]}""",
		).build())
		server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		try {
			SecureSettings(context).save(server.url("/").toString(), "")
			ActivityScenario.launch(MainActivity::class.java).use { scenario ->
				waitForText(scenario, "Accessible conversation")
				scenario.onActivity { activity ->
					val root = activity.findViewById<View>(android.R.id.content)
					assertTrue(root.allViews().any { it.accessibilityPaneTitle?.toString() == "Voice conversations" })
					val minimum = (48 * activity.resources.displayMetrics.density).toInt()
					root.allViews().filter { it.visibility == View.VISIBLE && it.isClickable }.forEach { view ->
						val spokenName = view.contentDescription?.toString().orEmpty().ifBlank { (view as? TextView)?.text?.toString().orEmpty() }
						assertTrue("Clickable ${view.javaClass.simpleName} has no accessible name", spokenName.isNotBlank())
						assertTrue("$spokenName touch target is ${view.width}x${view.height}", view.width >= minimum && view.height >= minimum)
					}
				}
			}
		} finally {
			server.close()
		}
	}

	@Test
	fun savedResponsesPersistBySessionMessageAndKind() {
		val first = SecureSettings(context)
		assertTrue(first.toggleSavedVoiceResponse(SavedVoiceResponse("voice-1", "message-1", "Remember this", SavedVoiceResponseKind.BOOKMARK)))
		assertTrue(first.toggleSavedVoiceResponse(SavedVoiceResponse("voice-1", "message-1", "Remember this", SavedVoiceResponseKind.FOLLOW_UP)))
		val restored = SecureSettings(context).savedVoiceResponses("voice-1")
		assertEquals(setOf(SavedVoiceResponseKind.BOOKMARK, SavedVoiceResponseKind.FOLLOW_UP), restored.map { it.kind }.toSet())
		assertTrue(first.toggleSavedVoiceResponse(restored.first()).not())
		assertEquals(1, SecureSettings(context).savedVoiceResponses("voice-1").size)
	}

	@Test
	fun unreadResultCursorsBaselineExistingSessionsAndTrackNewResults() {
		val secureSettings = SecureSettings(context)
		assertEquals(0L, secureSettings.unreadVoiceResults(mapOf("voice-1" to 3))["voice-1"])
		assertEquals(2L, secureSettings.unreadVoiceResults(mapOf("voice-1" to 5))["voice-1"])
		secureSettings.markVoiceSessionRead("voice-1", 5)
		assertEquals(0L, SecureSettings(context).unreadVoiceResults(mapOf("voice-1" to 5))["voice-1"])
		assertEquals(1L, secureSettings.unreadVoiceResults(mapOf("voice-1" to 5, "voice-2" to 1))["voice-2"])
	}

	@Test
	fun permissionHealthIsSeparateAndShowsEffectiveAccessAndLastUse() {
		val server = MockWebServer()
		server.enqueue(MockResponse.Builder().body(
			"""{"protocol":"voice.v1","voice_sessions":[{"id":"voice-1","title":"Personal"}]}""",
		).build())
		server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		try {
			val secureSettings = SecureSettings(context)
			secureSettings.save(server.url("/").toString(), "")
			secureSettings.savePhoneCapabilities(setOf("device"))
			secureSettings.recordPhoneActionUse("device_status", 1_700_000_000_000)
			assertEquals(1_700_000_000_000, secureSettings.phoneActionUses()["device_status"])
			ActivityScenario.launch(MainActivity::class.java).use { scenario ->
				waitForText(scenario, "Personal")
				onView(withContentDescription("More options")).perform(click())
				onView(withText("Permission health")).perform(click())
				onView(withContentDescription("Permission health for Device information: ● Ready to offer · start or resume a conversation")).check(matches(isDisplayed()))
				val labels = waitForText(scenario, "Remote access: Device status (On)")
				assertTrue(labels.any { it.startsWith("Last used ") })
				assertTrue(labels.contains("Android access: none required"))
				onView(withContentDescription("Manage phone tool permissions")).check(matches(isDisplayed()))
			}
		} finally {
			server.close()
		}
	}

	@Test
	fun successfulPhoneToolUseIsRecordedForPermissionHealth() {
		val secureSettings = SecureSettings(context)
		secureSettings.savePhoneCapabilities(setOf("device"))
		val completed = CountDownLatch(1)
		var provider: AndroidPhoneToolProvider? = null
		ActivityScenario.launch(MainActivity::class.java).use { scenario ->
			scenario.onActivity { activity ->
				provider = AndroidPhoneToolProvider(activity, secureSettings)
				provider?.execute("device_status", emptyMap()) { result ->
					assertTrue(result.isSuccess)
					completed.countDown()
				}
			}
			assertTrue(completed.await(5, TimeUnit.SECONDS))
			assertTrue((secureSettings.phoneActionUses()["device_status"] ?: 0) > 0)
			provider?.close()
		}
	}

	@Test
	fun askPolicyConfirmsOnPhoneWhileOnPolicyRunsDirectly() {
		val secureSettings = SecureSettings(context)
		secureSettings.savePhoneActionPolicy("device_status", PhoneActionPolicy.ASK)
		var provider: AndroidPhoneToolProvider? = null
		val hapticCues = mutableListOf<VoiceHapticCue>()
		ActivityScenario.launch(MainActivity::class.java).use { scenario ->
			val asked = CountDownLatch(1)
			scenario.onActivity { activity ->
				provider = AndroidPhoneToolProvider(activity, secureSettings, haptics = object : VoiceHaptics {
					override fun play(cue: VoiceHapticCue) { hapticCues += cue }
				})
				assertEquals(PhoneActionPolicy.ASK, provider?.actionPolicies()?.get("device_status"))
				provider?.execute("device_status", emptyMap()) { result ->
					assertTrue(result.isSuccess)
					asked.countDown()
				}
			}
			assertEquals(1L, asked.count)
			assertEquals(listOf(VoiceHapticCue.CONFIRMATION_REQUIRED), hapticCues)
			onView(withText("Allow")).inRoot(isDialog()).perform(click())
			assertTrue(asked.await(5, TimeUnit.SECONDS))

			secureSettings.savePhoneActionPolicy("device_status", PhoneActionPolicy.ON)
			val trusted = CountDownLatch(1)
			scenario.onActivity {
				assertEquals(PhoneActionPolicy.ON, provider?.actionPolicies()?.get("device_status"))
				provider?.execute("device_status", emptyMap()) { result ->
					assertTrue(result.isSuccess)
					trusted.countDown()
				}
			}
			assertTrue(trusted.await(5, TimeUnit.SECONDS))
			provider?.close()
		}
	}

	@Test
	fun callHistoryToolQueriesTheAndroidProviderWhenPermissionIsGranted() {
		val instrumentation = InstrumentationRegistry.getInstrumentation()
		instrumentation.uiAutomation.grantRuntimePermission(context.packageName, Manifest.permission.READ_CALL_LOG)
		val secureSettings = SecureSettings(context)
		secureSettings.savePhoneActionPolicy("search_call_history", PhoneActionPolicy.ON)
		val completed = CountDownLatch(1)
		var resultData: org.json.JSONObject? = null
		var provider: AndroidPhoneToolProvider? = null
		ActivityScenario.launch(MainActivity::class.java).use { scenario ->
			scenario.onActivity { activity ->
				provider = AndroidPhoneToolProvider(activity, secureSettings)
				assertTrue("search_call_history" in provider?.enabledActions().orEmpty())
				provider?.execute("search_call_history", mapOf("limit" to "5")) { result ->
					assertTrue(result.isSuccess)
					resultData = result.getOrThrow().data as? org.json.JSONObject
					completed.countDown()
				}
			}
			assertTrue(completed.await(5, TimeUnit.SECONDS))
			assertTrue(resultData?.has("calls") == true)
			provider?.close()
		}
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
				val readiness = waitForText(scenario, "Voice adjustments")
				assertTrue(readiness.contains("Step 1 of 4"))
				assertTrue(readiness.contains("1. Recognition languages"))
				scenario.onActivity { activity -> activity.findViewById<View>(android.R.id.content).findByDescription("Skip voice adjustments for now").performClick() }
				val labels = waitForText(scenario, "Bound conversation")
					assertTrue(labels.contains("Koder Voice"))
					assertTrue(labels.contains("Active 1"))
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
                .body("""{"protocol":"voice.v1","sessions":[{"id":"session-1","title":"Personal","kind":"regular","chat_count":2,"voice_chat_count":1,"last_message":"Calendar updated"}],"voice_sessions":[]}""")
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
				assertTrue(labels.contains("Active 1"))
                assertTrue(labels.contains("Koder Voice"))
                assertTrue(labels.any { it.startsWith("Personal") })
				assertTrue(labels.any { it.contains("New temporary conversation") })
				assertTrue(labels.contains("Session · 2 chats · 1 voice"))
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
				assertTrue(settingsLabels.contains("Voice adjustments & readiness"))
				onView(withContentDescription("Open voice adjustments and readiness check")).perform(scrollTo(), click())
				val adjustmentLabels = waitForText(scenario, "1. Recognition languages")
				assertTrue(adjustmentLabels.contains("Voice adjustments"))
				assertTrue(adjustmentLabels.contains("Automatic (all languages)"))
				assertTrue(adjustmentLabels.contains("Danish (da)"))
				assertTrue(adjustmentLabels.contains("German (de)"))
				onView(withContentDescription("Recognize Danish")).perform(scrollTo(), click())
				assertEquals(setOf("da"), SecureSettings(context).load().speechLanguages)
				scenario.onActivity { activity -> activity.findViewById<View>(android.R.id.content).findByDescription("Continue voice adjustments").performClick() }
				val pacingLabels = waitForText(scenario, "2. Spoken answer length")
				assertTrue(pacingLabels.any { it.contains("Detailed") })
				scenario.onActivity { activity ->
					val root = activity.findViewById<View>(android.R.id.content)
					val scroll = root.firstScrollView()
					val target = root.findByDescription("Use detailed spoken answer length. A fuller spoken explanation when useful")
					val bounds = android.graphics.Rect().also(target::getDrawingRect)
					scroll.offsetDescendantRectToMyCoords(target, bounds)
					scroll.scrollTo(0, bounds.top.coerceAtLeast(0))
					target.performClick()
				}
				assertEquals(com.lkarlslund.koder.voice.VoiceResponsePacing.DETAILED, SecureSettings(context).load().responsePacing)
				scenario.onActivity { activity -> activity.findViewById<View>(android.R.id.content).findByDescription("Continue voice adjustments").performClick() }
				waitForText(scenario, "3. Voice detection")
				scenario.onActivity { activity -> activity.findViewById<View>(android.R.id.content).findByDescription("Continue voice adjustments").performClick() }
				waitForText(scenario, "4. Live voice check")
				scenario.onActivity { activity -> activity.findViewById<View>(android.R.id.content).findByDescription("Exit voice adjustments to settings").performClick() }
				waitForText(scenario, "Phone tools")
				var scrollBefore = 0
				var scrollAfter = 0
				scenario.onActivity { activity ->
					val root = activity.findViewById<View>(android.R.id.content)
					val scroll = root.firstScrollView()
					val target = root.findByDescription("On Device status")
					val bounds = android.graphics.Rect().also(target::getDrawingRect)
					scroll.offsetDescendantRectToMyCoords(target, bounds)
					scroll.scrollTo(0, bounds.top.coerceAtLeast(0))
					scrollBefore = scroll.scrollY
					target.performClick()
					scrollAfter = scroll.scrollY
				}
				assertTrue(scrollBefore > 0)
				assertTrue("tool toggle reset scroll from $scrollBefore to $scrollAfter", kotlin.math.abs(scrollAfter - scrollBefore) <= 2)
				scenario.onActivity { activity ->
					activity.findViewById<View>(android.R.id.content).findByDescription("Ask Device status").performClick()
				}
				assertEquals(PhoneActionPolicy.ASK, SecureSettings(context).load().phoneActionPolicies["device_status"])
				assertEquals(PhoneActionPolicy.OFF, SecureSettings(context).load().phoneActionPolicies["list_apps"])
				assertEquals(PhoneActionPolicy.OFF, SecureSettings(context).load().phoneActionPolicies["open_app"])
				assertTrue("device" in SecureSettings(context).load().enabledPhoneCapabilities)
                assertTrue(settingsLabels.contains("Contacts"))
				assertTrue(settingsLabels.contains("Call history"))
                assertTrue(settingsLabels.contains("Notifications & email previews"))
            }
        } finally {
            server.close()
        }
    }

	@Test
	fun newTemporaryConversationShowsProgressThenOpens() {
		val allowCreateResponse = CountDownLatch(1)
		val server = MockWebServer()
		server.dispatcher = object : Dispatcher() {
			override fun dispatch(request: RecordedRequest): MockResponse = when {
				request.method == "POST" && request.target == "/voice/v1/sessions/temporary" -> {
					allowCreateResponse.await()
					MockResponse.Builder().code(201).body(
						"""{"protocol":"voice.v1","session":{"id":"session-new","title":"Trip planning","kind":"quick","chat_count":1,"voice_chat_count":1},"chat":{"id":"voice-new","session_id":"session-new","title":"Trip planning","role":"voice"},"chats":[{"id":"voice-new","session_id":"session-new","title":"Trip planning","role":"voice"}],"voice_sessions":[]}""",
					).build()
				}
				else -> MockResponse.Builder().body(
					"""{"protocol":"voice.v1","sessions":[{"id":"session-1","title":"Personal","kind":"regular","chat_count":1,"voice_chat_count":1}],"voice_sessions":[]}""",
				).build()
			}
		}
		server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		try {
			SecureSettings(context).save(server.url("/").toString(), "")
			ActivityScenario.launch(MainActivity::class.java).use { scenario ->
				waitForText(scenario, "Personal")
				onView(withContentDescription("Create a new voice conversation")).perform(click())
				onView(withContentDescription("Temporary conversation name")).perform(replaceText("Trip planning"))
				onView(withText("Create")).perform(click())
				waitForDisplayedText("Creating temporary conversation…")
				allowCreateResponse.countDown()
				val labels = waitForText(scenario, "Trip planning")
				assertTrue(labels.contains("Trip planning"))
				onView(withContentDescription("Show transcript")).perform(click())
				onView(withContentDescription("Send message")).check(matches(isDisplayed()))
				scenario.onActivity { activity ->
					val button = activity.findViewById<View>(android.R.id.content).findByDescription("Send message") as ImageButton
					val expectedSize = (48 * activity.resources.displayMetrics.density).toInt()
					assertEquals(expectedSize, button.width)
					assertEquals(expectedSize, button.height)
					assertEquals(null, button.background)
					val composer = button.parent as ViewGroup
					assertEquals(composer.width - composer.paddingRight, button.right)
				}
				onView(withContentDescription("Hide transcript")).perform(click())
				scenario.onActivity { activity ->
					val orb = activity.findViewById<View>(android.R.id.content).findByDescription("Koder is thinking")
					assertTrue(orb is com.lkarlslund.koder.voice.VoiceStateOrbView && orb.visibility == View.VISIBLE)
				}
				val orbStates = listOf(
					com.lkarlslund.koder.voice.CallController.Stage.LISTENING to "Koder is listening",
					com.lkarlslund.koder.voice.CallController.Stage.RECORDING to "You are speaking",
					com.lkarlslund.koder.voice.CallController.Stage.PROCESSING to "Koder is thinking",
					com.lkarlslund.koder.voice.CallController.Stage.WORKING to "Koder is using tools",
					com.lkarlslund.koder.voice.CallController.Stage.SPEAKING to "Koder is speaking",
				)
				orbStates.forEach { (stage, description) ->
					scenario.onActivity { activity ->
						activity.onSnapshot(com.lkarlslund.koder.voice.CallController.Snapshot(stage = stage, detail = if (stage == com.lkarlslund.koder.voice.CallController.Stage.WORKING) "Working in Laptop repair…" else description))
					}
					scenario.onActivity { activity ->
						val orb = activity.findViewById<View>(android.R.id.content).findByDescription(description)
						assertTrue(orb is com.lkarlslund.koder.voice.VoiceStateOrbView && orb.visibility == View.VISIBLE)
					}
					if (stage == com.lkarlslund.koder.voice.CallController.Stage.WORKING) {
						scenario.onActivity { activity ->
							val detail = activity.findViewById<View>(android.R.id.content).findByDescription("Working in Laptop repair…")
							assertTrue(detail is TextView && detail.text == "Working in Laptop repair…" && detail.visibility == View.VISIBLE)
						}
					}
				}
			}
		} finally {
			allowCreateResponse.countDown()
			server.close()
		}
	}

	@Test
	fun sessionBrowserShowsEveryChatButOnlyVoiceChatsAreSelectable() {
		val server = MockWebServer()
		server.dispatcher = object : Dispatcher() {
			override fun dispatch(request: RecordedRequest): MockResponse = when (request.target) {
				"/voice/v1/sessions" -> MockResponse.Builder().body(
					"""{"protocol":"voice.v1","sessions":[{"id":"session-1","title":"Laptop repair","kind":"regular","chat_count":3,"voice_chat_count":1}],"voice_sessions":[]}""",
				).build()
				"/voice/v1/sessions/session-1/chats" -> MockResponse.Builder().body(
					"""{"protocol":"voice.v1","chats":[{"id":"work-1","session_id":"session-1","title":"BIOS investigation","role":"execution","status_text":"Checking firmware"},{"id":"plan-1","session_id":"session-1","title":"Repair plan","role":"planning"},{"id":"voice-1","session_id":"session-1","title":"Laptop conversation","role":"voice"}],"voice_sessions":[]}""",
				).build()
				else -> MockResponse.Builder().code(404).build()
			}
		}
		server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		try {
			SecureSettings(context).save(server.url("/").toString(), "")
			ActivityScenario.launch(MainActivity::class.java).use { scenario ->
				val homeLabels = waitForText(scenario, "Laptop repair")
				assertTrue(homeLabels.none { it.startsWith("Deleted ") })
				onView(withContentDescription("Session options for Laptop repair")).perform(click())
				onView(withText("Rename")).perform(click())
				onView(withText("Rename session")).inRoot(isDialog()).check(matches(isDisplayed()))
				onView(withText("Cancel")).inRoot(isDialog()).perform(click())
				onView(withContentDescription("Open Laptop repair. 3 chats · 1 voice")).perform(click())
				val labels = waitForText(scenario, "Laptop conversation")
				assertTrue(labels.contains("BIOS investigation"))
				assertTrue(labels.any { it.contains("Checking firmware") })
				onView(withContentDescription("BIOS investigation, execution chat, visible but not selectable")).check(matches(isDisplayed()))
				onView(withContentDescription("Open voice conversation Laptop conversation")).check(matches(isDisplayed()))
				onView(withContentDescription("Chat options for BIOS investigation")).perform(click())
				onView(withText("Archive")).check(matches(isDisplayed()))
				onView(withText("Rename")).perform(click())
				onView(withText("Rename chat")).inRoot(isDialog()).check(matches(isDisplayed()))
				onView(withText("Cancel")).inRoot(isDialog()).perform(click())
			}
		} finally {
			server.close()
		}
	}

	@Test
	fun sessionCardsShowCountsAndTenRows() {
		val sessions = (1..10).joinToString(",") { index ->
			buildString {
				append("""{"id":"session-$index","title":"Session $index","kind":"regular","chat_count":$index,"voice_chat_count":${if (index % 2 == 0) 1 else 0},"updated_at":"2026-08-${index.toString().padStart(2, '0')}T12:00:00Z"""")
				append("}")
			}
		}
		val server = MockWebServer()
		server.enqueue(MockResponse.Builder().body("""{"protocol":"voice.v1","sessions":[$sessions],"voice_sessions":[]}""").build())
		server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		try {
			SecureSettings(context).save(server.url("/").toString(), "")
			ActivityScenario.launch(MainActivity::class.java).use { scenario ->
				val labels = waitForText(scenario, "Session 10")
				assertTrue(labels.any { it.startsWith("Session · 10 chats · 1 voice · ") })
				onView(withContentDescription("Open Session 10. 10 chats · 1 voice")).check(matches(isDisplayed()))
			}
		} finally {
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
				assertEquals(2, labels.count { it.contains("ago") })
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
			"""{"protocol":"voice.v1","sessions":[{"id":"session-search","title":"Search project","kind":"regular","chat_count":1,"voice_chat_count":1}],"voice_sessions":[]}""",
		).build())
		server.enqueue(MockResponse.Builder().body(
			"""{"protocol":"voice.v1","chats":[{"id":"voice-search","session_id":"session-search","title":"Searchable","role":"voice"}],"voice_sessions":[]}""",
		).build())
		server.enqueue(MockResponse.Builder().webSocketUpgrade(object : WebSocketListener() {
			override fun onOpen(webSocket: WebSocket, response: Response) {
				voiceSocket = webSocket
				webSocket.send(
					"""{"type":"ready","protocol":"voice.v1","audio_config":{"input":{"encoding":"pcm_s16le","sample_rate":16000,"channels":1},"output":{"encoding":"pcm_s16le","sample_rate":24000,"channels":1},"max_utterance_seconds":120},"call_state":{"session_id":"session-search","chat_id":"voice-search","sessions":[{"id":"session-search","title":"Search project"}],"chats":[{"id":"voice-search","session_id":"session-search","title":"Searchable","role":"voice"}],"history":[{"id":"latest","role":"assistant","text":"Newest answer"}]}}""",
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
				waitForText(scenario, "Search project")
				onView(withContentDescription("Open Search project. 1 chat · 1 voice")).perform(click())
				waitForText(scenario, "Searchable")
				onView(withContentDescription("Open voice conversation Searchable")).perform(click())
				waitForText(scenario, "Speaker  ▾")
				val notificationManager = context.getSystemService(NotificationManager::class.java)
				scenario.onActivity { activity ->
					activity.findViewById<View>(android.R.id.content).findByDescription("Search transcript").performClick()
				}
				onView(withContentDescription("Transcript search query")).perform(replaceText("BIOS"))
				onView(withText("Search")).perform(click())
				waitForDisplayedText("Transcript matches")
				onView(withText("Koder · BIOS gate found")).perform(click())
				val labels = waitForText(scenario, "BIOS gate found")
				assertTrue(labels.contains("What blocked it?"))
				assertTrue(labels.contains("Back to latest"))
				onView(withText("Back to latest")).perform(click())
				assertTrue(waitForText(scenario, "Newest answer").contains("Newest answer"))
				onView(withContentDescription("Koder message: Newest answer. Long press for actions")).perform(longClick())
				onView(withText("Bookmark")).perform(click())
				waitForDisplayedText("★ Bookmarked")
				Thread.sleep(350) // Let the native action dialog's dim layer disappear.
				onView(withContentDescription("Koder message: Newest answer. Long press for actions")).perform(longClick())
				onView(withText("Follow up later")).perform(click())
				// AlertDialog's dim layer outlives its item click briefly on API 36;
				// wait for that native dismissal animation so it cannot reject the
				// next real tap as an occluded/untrusted touch.
				Thread.sleep(350)
				onView(withContentDescription("Saved responses")).perform(click())
				waitForDisplayedText("Saved responses")
				onView(withText("↗ Newest answer")).inRoot(isDialog()).perform(scrollTo(), click())
				waitForDisplayedText("Remove")
				onView(withId(android.R.id.button1)).inRoot(isDialog()).perform(click())
				assertTrue(waitForText(scenario, "Following up on your earlier response").any { it.contains("Newest answer") })
				waitForVoiceNotification(notificationManager) { it.hasAction("End") }.action("End").actionIntent.send()
				waitForNoVoiceNotification(notificationManager)
				waitForText(scenario, "Conversation paused")
			}
		} finally {
			voiceSocket?.close(1000, "test complete")
			server.close()
		}
	}

	@Test
	fun structuredPresentationRendersEveryGenericBlock() {
		val instrumentation = InstrumentationRegistry.getInstrumentation()
		buildList {
			add(Manifest.permission.RECORD_AUDIO)
			if (Build.VERSION.SDK_INT >= 31) add(Manifest.permission.BLUETOOTH_CONNECT)
			if (Build.VERSION.SDK_INT >= 33) add(Manifest.permission.POST_NOTIFICATIONS)
		}.forEach { instrumentation.uiAutomation.grantRuntimePermission(context.packageName, it) }
		val server = MockWebServer()
		var voiceSocket: WebSocket? = null
		val voiceUpgrade = MockResponse.Builder().webSocketUpgrade(object : WebSocketListener() {
			override fun onOpen(webSocket: WebSocket, response: Response) {
				voiceSocket = webSocket
				webSocket.send(
					"""{"type":"ready","protocol":"voice.v1","audio_config":{"input":{"encoding":"pcm_s16le","sample_rate":16000,"channels":1},"output":{"encoding":"pcm_s16le","sample_rate":24000,"channels":1},"max_utterance_seconds":120},"call_state":{"session_id":"session-card","chat_id":"voice-card","sessions":[{"id":"session-card","title":"Visual project"}],"chats":[{"id":"voice-card","session_id":"session-card","title":"Visual test","role":"voice"}]}}""",
				)
			}

			override fun onMessage(webSocket: WebSocket, text: String) {
				if (org.json.JSONObject(text).optString("type") != "hello") return
				webSocket.send(
					"""{"type":"render","protocol":"voice.v1","utterance_id":"visual-turn","parts":[{"mime_type":"application/vnd.koder.presentation+json","data":{"version":1,"blocks":[{"kind":"text","text":"Today in Aarhus","style":"heading"},{"kind":"image","uri":"/map.png","title":"Aarhus map","alt":"Map preview"},{"kind":"key_value","items":[{"key":"Event","value":"DHL Stafet"}]},{"kind":"list","items":[{"title":"Road closures","detail":"From 16:00"}]},{"kind":"progress","label":"Route check","value":2,"max":5,"detail":"Two areas checked"},{"kind":"action","label":"Open event details","uri":"https://example.com/event"},{"kind":"file","name":"event.ics","uri":"/event.ics","mime_type":"text/calendar","detail":"Calendar entry"}]},"metadata":{"title":"What is happening nearby","presentation":"true","render_key":"visual-card"}}]}""",
				)
				webSocket.send(
					"""{"type":"message","protocol":"voice.v1","message":{"spoken_text":"Here are the details.","parts":[{"mime_type":"text/plain","data":"Here are the details."},{"id":"tool-1","mime_type":"application/vnd.koder.tool-activity+json","data":{"tool":"phone_photos_thumbs","title":"View phone photo thumbnails","status":"done","summary":"View four candidates"},"metadata":{"surface":"transcript","render_key":"tool:tool-1"}}]}}""",
				)
			}
		}).build()
		val png = Base64.getDecoder().decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
		server.dispatcher = object : Dispatcher() {
			override fun dispatch(request: RecordedRequest): MockResponse = when {
				request.target == "/voice/v1/sessions" -> MockResponse.Builder().body(
					"""{"protocol":"voice.v1","sessions":[{"id":"session-card","title":"Visual project","kind":"regular","chat_count":1,"voice_chat_count":1}],"voice_sessions":[]}""",
				).build()
				request.target == "/voice/v1/sessions/session-card/chats" -> MockResponse.Builder().body(
					"""{"protocol":"voice.v1","chats":[{"id":"voice-card","session_id":"session-card","title":"Visual test","role":"voice"}],"voice_sessions":[]}""",
				).build()
				request.target.startsWith("/voice/v1?") -> voiceUpgrade
				request.target == "/map.png" -> MockResponse.Builder().setHeader("Content-Type", "image/png").body(Buffer().write(png)).build()
				else -> MockResponse.Builder().code(404).build()
			}
		}
		server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		try {
			SecureSettings(context).save(server.url("/").toString(), "")
			ActivityScenario.launch(MainActivity::class.java).use { scenario ->
				waitForText(scenario, "Visual project")
				onView(withContentDescription("Open Visual project. 1 chat · 1 voice")).perform(click())
				waitForText(scenario, "Visual test")
				onView(withContentDescription("Open voice conversation Visual test")).perform(click())
				val labels = waitForText(scenario, "DHL Stafet")
				assertTrue(labels.contains("What is happening nearby"))
				assertTrue(labels.contains("Today in Aarhus"))
				assertTrue(labels.any { it.contains("Road closures") && it.contains("From 16:00") })
				assertTrue(labels.contains("Route check"))
				assertTrue(labels.contains("Two areas checked"))
				assertTrue(labels.any { it.contains("Open event details") })
				assertTrue(labels.any { it.contains("Open event.ics") })
				onView(withContentDescription("Koder structured presentation")).check(matches(isDisplayed()))
				waitForDisplayedDescription("Map preview. Tap for fullscreen")
				onView(withContentDescription("Map preview. Tap for fullscreen")).perform(click())
				onView(withContentDescription("Map preview. Pinch to zoom, drag to pan, double tap to reset")).check(matches(isDisplayed()))
				onView(withContentDescription("Rotate fullscreen image")).perform(click())
				onView(withContentDescription("Reset fullscreen image")).perform(click())
				onView(withContentDescription("Save fullscreen image")).check(matches(isDisplayed()))
				onView(withContentDescription("Share fullscreen image")).check(matches(isDisplayed()))
				onView(withContentDescription("Close fullscreen image")).perform(click())
				onView(withContentDescription("Show transcript")).perform(click())
				onView(withContentDescription("View phone photo thumbnails done View four candidates")).check(matches(isDisplayed()))
			}
		} finally {
			voiceSocket?.close(1000, "test complete")
			server.close()
		}
	}

	@Test
	fun delegatedResultNotifiesInBackgroundAndReturnsToExactTranscript() {
		val instrumentation = InstrumentationRegistry.getInstrumentation()
		buildList {
			add(Manifest.permission.RECORD_AUDIO)
			if (Build.VERSION.SDK_INT >= 31) add(Manifest.permission.BLUETOOTH_CONNECT)
			if (Build.VERSION.SDK_INT >= 33) add(Manifest.permission.POST_NOTIFICATIONS)
		}.forEach { instrumentation.uiAutomation.grantRuntimePermission(context.packageName, it) }
		val server = MockWebServer()
		var voiceSocket: WebSocket? = null
		val voiceSockets = mutableListOf<WebSocket>()
		var connectionCount = 0
		val listener = object : WebSocketListener() {
			override fun onOpen(webSocket: WebSocket, response: Response) {
				voiceSocket = webSocket
				synchronized(voiceSockets) { voiceSockets += webSocket }
				connectionCount++
				val history = if (connectionCount > 1) ""","history":[{"id":"assistant-result-1","role":"assistant","text":"The delegated result is ready."}]""" else ""
				webSocket.send(
					"""{"type":"ready","protocol":"voice.v1","audio_config":{"input":{"encoding":"pcm_s16le","sample_rate":16000,"channels":1},"output":{"encoding":"pcm_s16le","sample_rate":24000,"channels":1},"max_utterance_seconds":120},"call_state":{"session_id":"session-result","chat_id":"voice-result","sessions":[{"id":"session-result","title":"Research"}],"chats":[{"id":"voice-result","session_id":"session-result","title":"Background work","role":"voice"}]$history}}""",
				)
			}
		}
		server.dispatcher = object : Dispatcher() {
			override fun dispatch(request: RecordedRequest): MockResponse = when {
				request.target == "/voice/v1/sessions" -> MockResponse.Builder().body(
					"""{"protocol":"voice.v1","sessions":[{"id":"session-result","title":"Research","kind":"regular","chat_count":1,"voice_chat_count":1}],"voice_sessions":[]}""",
				).build()
				request.target == "/voice/v1/sessions/session-result/chats" -> MockResponse.Builder().body(
					"""{"protocol":"voice.v1","chats":[{"id":"voice-result","session_id":"session-result","title":"Background work","role":"voice"}],"voice_sessions":[]}""",
				).build()
				request.target.startsWith("/voice/v1?") -> MockResponse.Builder().webSocketUpgrade(listener).build()
				else -> MockResponse.Builder().code(404).build()
			}
		}
		server.start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		val notificationManager = context.getSystemService(NotificationManager::class.java)
		val resultNotificationId = VoiceResultNotifier.notificationId("voice-result", "assistant-result-1")
		try {
			SecureSettings(context).save(server.url("/").toString(), "")
			ActivityScenario.launch(MainActivity::class.java).use { scenario ->
				waitForText(scenario, "Research")
				onView(withContentDescription("Open Research. 1 chat · 1 voice")).perform(click())
				waitForText(scenario, "Background work")
				onView(withContentDescription("Open voice conversation Background work")).perform(click())
				waitForText(scenario, "Speaker")
				scenario.moveToState(Lifecycle.State.CREATED)
				checkNotNull(voiceSocket).send(
					"""{"type":"state","protocol":"voice.v1","state":"working","working_on":{"id":"work-1","title":"Research"}}""",
				)
				checkNotNull(voiceSocket).send(
					"""{"type":"message","protocol":"voice.v1","message":{"spoken_text":"The delegated result is ready.","transcript_id":"assistant-result-1","parts":[{"mime_type":"text/plain","data":"The delegated result is ready."}]}}""",
				)
				val completion = waitForNotification(notificationManager, resultNotificationId)
				assertEquals("Background work is ready", completion.extras.getString(Notification.EXTRA_TITLE))
				assertEquals("The delegated result is ready.", completion.extras.getString(Notification.EXTRA_TEXT))
				assertTrue(completion.contentIntent != null)
				waitForVoiceNotification(notificationManager) { it.hasAction("End") }.action("End").actionIntent.send()
				waitForNoVoiceNotification(notificationManager)
			}
			val resultIntent = Intent(context, MainActivity::class.java)
				.putExtra(VoiceResultNotifier.EXTRA_SESSION_ID, "session-result")
				.putExtra(VoiceResultNotifier.EXTRA_VOICE_SESSION_ID, "voice-result")
				.putExtra(VoiceResultNotifier.EXTRA_TRANSCRIPT_ID, "assistant-result-1")
			ActivityScenario.launch<MainActivity>(resultIntent).use { scenario ->
				waitForDisplayedText("The delegated result is ready.")
				waitForNoNotification(notificationManager, resultNotificationId)
				waitForVoiceNotification(notificationManager) { it.hasAction("End") }.action("End").actionIntent.send()
				waitForNoVoiceNotification(notificationManager)
			}
		} finally {
			notificationManager.cancel(resultNotificationId)
			synchronized(voiceSockets) { voiceSockets.toList() }.forEach { it.close(1000, "test complete") }
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

	private fun View.allViews(): List<View> = buildList {
		add(this@allViews)
		if (this@allViews is ViewGroup) repeat(this@allViews.childCount) { index -> addAll(this@allViews.getChildAt(index).allViews()) }
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

	private fun waitForDisplayedDescription(wanted: String) {
		repeat(50) {
			if (runCatching { onView(withContentDescription(wanted)).check(matches(isDisplayed())) }.isSuccess) return
			Thread.sleep(100)
		}
		error("Timed out waiting for displayed content description $wanted")
	}

	private fun waitForVoiceNotification(manager: NotificationManager, predicate: (Notification) -> Boolean): Notification {
		repeat(80) {
			manager.activeNotifications.firstOrNull { it.id == VoiceCallService.NOTIFICATION_ID }?.notification?.let { notification ->
				if (predicate(notification)) return notification
			}
			Thread.sleep(100)
		}
		error("Timed out waiting for Koder voice notification")
	}

	private fun waitForNoVoiceNotification(manager: NotificationManager) {
		repeat(80) {
			if (manager.activeNotifications.none { it.id == VoiceCallService.NOTIFICATION_ID }) return
			Thread.sleep(100)
		}
		error("Koder voice notification was not removed")
	}

	private fun waitForNotification(manager: NotificationManager, id: Int): Notification {
		repeat(80) {
			manager.activeNotifications.firstOrNull { it.id == id }?.notification?.let { return it }
			Thread.sleep(100)
		}
		error("Timed out waiting for notification $id; active=${manager.activeNotifications.joinToString { "${it.id}:${it.notification.extras.getString(Notification.EXTRA_TITLE)}" }}")
	}

	private fun waitForNoNotification(manager: NotificationManager, id: Int) {
		repeat(80) {
			if (manager.activeNotifications.none { it.id == id }) return
			Thread.sleep(100)
		}
		error("Notification $id was not removed")
	}

	private fun Notification.hasAction(title: String): Boolean = actions?.any { it.title.toString() == title } == true

	private fun Notification.action(title: String): Notification.Action = actions?.firstOrNull { it.title.toString() == title }
		?: error("Notification action $title is missing")
}
