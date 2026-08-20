package com.lkarlslund.koder

import android.Manifest
import android.content.Context
import android.content.pm.ActivityInfo
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.os.Build
import android.view.View
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.net.InetAddress
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/** Configuration checks intended to run on every device in voiceCompatibility. */
@RunWith(AndroidJUnit4::class)
class DeviceMatrixInstrumentedTest {
	private val instrumentation = InstrumentationRegistry.getInstrumentation()
	private val context: Context get() = instrumentation.targetContext

	@After
	fun restoreDeviceState() {
		// Changing the global UI mode kills instrumentation on Android 9.
		if (Build.VERSION.SDK_INT >= 29) shell("cmd uimode night auto")
		context.getSharedPreferences("koder_voice", Context.MODE_PRIVATE).edit().clear().commit()
	}

	@Test
	fun homeStartsWithoutPreemptivelyRequestingMicrophoneOnEverySupportedApi() {
		assertTrue("device API ${Build.VERSION.SDK_INT} is below the supported minimum", Build.VERSION.SDK_INT >= 28)
		if (Build.VERSION.SDK_INT >= 29) shell("cmd uimode night no")
		withHomeServer { server ->
			SecureSettings(context).save(server.url("/").toString(), "")
			ActivityScenario.launch(MainActivity::class.java).use { scenario ->
				waitForText(scenario, "Matrix conversation")
				assertEquals(PackageManager.PERMISSION_DENIED, context.checkSelfPermission(Manifest.permission.RECORD_AUDIO))
				scenario.onActivity { activity ->
					assertEquals(Configuration.UI_MODE_NIGHT_NO, activity.resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK)
					assertFalse(activity.window.attributes.flags and android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON != 0)
					assertTrue(activity.findViewById<View>(android.R.id.content).width > 0)
				}
			}
		}
	}

	@Test
	fun darkThemeAndLandscapeReflowRenderOnEveryMatrixShape() {
		if (Build.VERSION.SDK_INT >= 29) {
			shell("cmd uimode night yes")
		} else {
			val darkConfiguration = Configuration(context.resources.configuration).apply {
				uiMode = uiMode and Configuration.UI_MODE_NIGHT_MASK.inv() or Configuration.UI_MODE_NIGHT_YES
			}
			val darkContext = context.createConfigurationContext(darkConfiguration)
			assertEquals(Configuration.UI_MODE_NIGHT_YES, darkContext.resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK)
		}
		withHomeServer { server ->
			SecureSettings(context).save(server.url("/").toString(), "")
			ActivityScenario.launch(MainActivity::class.java).use { scenario ->
				waitForText(scenario, "Matrix conversation")
				scenario.onActivity { activity ->
					if (Build.VERSION.SDK_INT >= 29) {
						assertEquals(Configuration.UI_MODE_NIGHT_YES, activity.resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK)
						assertEquals(0, activity.window.decorView.systemUiVisibility and View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR)
					}
					activity.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE
				}
				waitForLandscape(scenario)
				scenario.onActivity { activity ->
					val content = activity.findViewById<View>(android.R.id.content)
					assertTrue("landscape content did not reflow: ${content.width}x${content.height}", content.width > content.height)
					assertTrue(content.findByDescription("Create a new voice conversation").isShown)
				}
			}
		}
	}

	private fun withHomeServer(block: (MockWebServer) -> Unit) {
		val server = MockWebServer().apply {
			dispatcher = object : Dispatcher() {
				override fun dispatch(request: RecordedRequest) = MockResponse.Builder().body(
					"""{"protocol":"voice.v1","voice_sessions":[{"id":"matrix-1","title":"Matrix conversation"}]}""",
				).build()
			}
			start(InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)), 0)
		}
		try { block(server) } finally { server.close() }
	}

	private fun waitForText(scenario: ActivityScenario<MainActivity>, wanted: String) {
		repeat(50) {
			var found = false
			scenario.onActivity { activity -> found = activity.findViewById<View>(android.R.id.content).containsText(wanted) }
			if (found) return
			Thread.sleep(100)
		}
		error("Timed out waiting for $wanted")
	}

	private fun waitForLandscape(scenario: ActivityScenario<MainActivity>) {
		repeat(50) {
			var landscape = false
			scenario.onActivity { activity -> landscape = activity.resources.configuration.orientation == Configuration.ORIENTATION_LANDSCAPE }
			if (landscape) return
			Thread.sleep(100)
		}
		error("Activity did not rotate to landscape")
	}

	private fun shell(command: String) {
		instrumentation.uiAutomation.executeShellCommand(command).close()
		Thread.sleep(250)
	}

	private fun View.containsText(wanted: String): Boolean =
		(this as? android.widget.TextView)?.text?.toString()?.contains(wanted) == true ||
			(this as? android.view.ViewGroup)?.let { group -> (0 until group.childCount).any { group.getChildAt(it).containsText(wanted) } } == true

	private fun View.findByDescription(wanted: String): View {
		if (contentDescription?.toString() == wanted) return this
		if (this is android.view.ViewGroup) repeat(childCount) { index ->
			runCatching { return getChildAt(index).findByDescription(wanted) }
		}
		error("No view with content description $wanted")
	}
}
