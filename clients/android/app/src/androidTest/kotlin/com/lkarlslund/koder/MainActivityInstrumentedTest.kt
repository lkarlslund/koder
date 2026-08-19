package com.lkarlslund.koder

import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class MainActivityInstrumentedTest {
    @Test
    fun launchesUsableCallAndTypedMessageControls() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            scenario.onActivity { activity ->
                val labels = activity.findViewById<View>(android.R.id.content).allText()
                assertTrue(labels.contains("Koder voice"))
                assertTrue(labels.contains("Start call"))
                assertFalse(labels.contains("Automatic chat selection"))
                assertTrue(labels.contains("New"))
                assertTrue(labels.contains("Type to test without speech"))
                assertTrue(labels.contains("Send"))
            }
        }
    }

    private fun View.allText(): List<String> = buildList {
        if (this@allText is TextView) {
            add(this@allText.text.toString())
            add(this@allText.hint?.toString().orEmpty())
        }
        if (this@allText is ViewGroup) {
            repeat(this@allText.childCount) { index ->
                addAll(this@allText.getChildAt(index).allText())
            }
        }
    }
}
