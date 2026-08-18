package com.lkarlslund.koder

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.BitmapFactory
import android.graphics.Color
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.text.InputType
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.AdapterView
import android.widget.ArrayAdapter
import android.widget.Button
import android.widget.EditText
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Spinner
import android.widget.TextView
import android.widget.Toast
import androidx.core.content.FileProvider
import com.lkarlslund.koder.voice.CallController
import com.lkarlslund.koder.voice.VoiceMessage
import com.lkarlslund.koder.voice.VoicePart
import com.lkarlslund.koder.voice.VoiceSession
import java.io.File

@SuppressLint("SetTextI18n")
class MainActivity : Activity(), CallController.Listener {
    private lateinit var controller: CallController
	private lateinit var secureSettings: SecureSettings
    private lateinit var server: EditText
    private lateinit var token: EditText
    private lateinit var callButton: Button
    private lateinit var status: TextView
    private lateinit var transcript: TextView
    private lateinit var sessionSpinner: Spinner
    private lateinit var voiceSessionSpinner: Spinner
    private lateinit var feed: LinearLayout
    private lateinit var feedScroll: ScrollView
    private lateinit var typedMessage: EditText
    private var sessions: List<VoiceSession> = emptyList()
    private var voiceSessions: List<VoiceSession> = emptyList()
    private var spinnerUpdating = false
    private var voiceSpinnerUpdating = false
    private var selectedVoiceSessionId = ""
    private var connected = false
    private var pendingStart = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
		secureSettings = SecureSettings(this)
        controller = CallController(this, this)
        buildUi()
        restoreSettings()
    }

    override fun onDestroy() {
        controller.close()
        super.onDestroy()
    }

    override fun onSnapshot(snapshot: CallController.Snapshot) {
        runOnUiThread {
            connected = snapshot.stage != CallController.Stage.DISCONNECTED &&
                snapshot.stage != CallController.Stage.ERROR
            status.text = snapshot.detail
            transcript.text = snapshot.partialTranscript
            transcript.visibility = if (snapshot.partialTranscript.isBlank()) View.GONE else View.VISIBLE
            callButton.text = if (connected) "Hang up" else "Start call"
            server.isEnabled = !connected
            token.isEnabled = !connected
            updateSessions(snapshot.sessions, snapshot.activeSessionId)
            if (snapshot.voiceSessionId.isNotBlank()) selectedVoiceSessionId = snapshot.voiceSessionId
            updateVoiceSessions(snapshot.voiceSessions, snapshot.voiceSessionId)
        }
    }

    override fun onUserMessage(text: String) = addBubble("You", text, 0xFFE3F2FD.toInt())

    override fun onAssistantMessage(message: VoiceMessage) {
        runOnUiThread {
            val visibleParts = message.parts.ifEmpty {
				listOf(VoicePart(mimeType = "text/plain", data = message.spokenText))
            }
            visibleParts.forEach { addPart(it) }
        }
    }

    private fun buildUi() {
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16), dp(12), dp(16), dp(12))
            setBackgroundColor(0xFFF7F7F8.toInt())
        }
        root.addView(TextView(this).apply {
            text = "Koder voice"
            textSize = 24f
            setTextColor(Color.BLACK)
        })
        root.addView(TextView(this).apply {
            text = "A call into your existing Koder chats"
            textSize = 14f
            setTextColor(0xFF555555.toInt())
        }, margins(height = ViewGroup.LayoutParams.WRAP_CONTENT, bottom = 12))

        server = EditText(this).apply {
            hint = "Koder server"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
            setSingleLine()
        }
        token = EditText(this).apply {
            hint = "Voice token (optional locally)"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            setSingleLine()
        }
        root.addView(server, matchWrap())
        root.addView(token, matchWrap())

        callButton = Button(this).apply {
            text = "Start call"
            setOnClickListener { if (connected) controller.end() else requestCallStart() }
        }
        root.addView(callButton, margins(height = ViewGroup.LayoutParams.WRAP_CONTENT, top = 6))

        voiceSessionSpinner = Spinner(this)
        voiceSessionSpinner.onItemSelectedListener = object : AdapterView.OnItemSelectedListener {
            override fun onNothingSelected(parent: AdapterView<*>?) = Unit
            override fun onItemSelected(parent: AdapterView<*>?, view: View?, position: Int, id: Long) {
                if (!voiceSpinnerUpdating && connected) {
                    voiceSessions.getOrNull(position)?.let { controller.selectVoiceSession(it.id) }
                }
            }
        }
        root.addView(voiceSessionSpinner, margins(height = ViewGroup.LayoutParams.WRAP_CONTENT, top = 4))

        status = TextView(this).apply {
            text = "Ready"
            textSize = 16f
            setTextColor(0xFF1B5E20.toInt())
            gravity = Gravity.CENTER_HORIZONTAL
        }
        root.addView(status, margins(height = ViewGroup.LayoutParams.WRAP_CONTENT, top = 8))

        sessionSpinner = Spinner(this)
        sessionSpinner.onItemSelectedListener = object : AdapterView.OnItemSelectedListener {
            override fun onNothingSelected(parent: AdapterView<*>?) = Unit
            override fun onItemSelected(parent: AdapterView<*>?, view: View?, position: Int, id: Long) {
                if (!spinnerUpdating && connected) controller.selectSession(sessions.getOrNull(position - 1)?.id.orEmpty())
            }
        }
        root.addView(sessionSpinner, margins(height = ViewGroup.LayoutParams.WRAP_CONTENT, top = 4))

        transcript = TextView(this).apply {
            setTextColor(0xFF555555.toInt())
            textSize = 15f
            visibility = View.GONE
        }
        root.addView(transcript, margins(height = ViewGroup.LayoutParams.WRAP_CONTENT, top = 4))

        feed = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        feedScroll = ScrollView(this).apply { addView(feed, matchWrap()) }
        root.addView(feedScroll, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))

        val composer = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        typedMessage = EditText(this).apply {
            hint = "Type to test without speech"
            maxLines = 3
        }
        val send = Button(this).apply {
            text = "Send"
            setOnClickListener {
                val text = typedMessage.text.toString()
                if (text.isNotBlank()) {
                    controller.submit(text)
                    typedMessage.text.clear()
                }
            }
        }
        composer.addView(typedMessage, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        composer.addView(send, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        root.addView(composer, matchWrap())
        setContentView(root)
        updateSessions(emptyList(), "")
        updateVoiceSessions(emptyList(), "")
    }

    private fun requestCallStart() {
        saveSettings()
        val missing = requiredPermissions().filter { checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED }
        if (missing.isEmpty()) startCall() else {
            pendingStart = true
            requestPermissions(missing.toTypedArray(), PERMISSION_REQUEST)
        }
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, results: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, results)
        if (requestCode != PERMISSION_REQUEST || !pendingStart) return
        pendingStart = false
        if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) {
            startCall()
        } else {
            Toast.makeText(this, "Microphone permission is required for voice calls", Toast.LENGTH_LONG).show()
        }
    }

    private fun startCall() {
        val address = server.text.toString().trim()
        if (address.isEmpty()) {
            server.error = "Server address is required"
            return
        }
        controller.start(address, token.text.toString(), selectedVoiceSessionId)
    }

    private fun updateSessions(next: List<VoiceSession>, activeId: String) {
        if (next == sessions && sessionSpinner.selectedItemPosition == next.indexOfFirst { it.id == activeId } + 1) return
        sessions = next
        val labels = listOf("Automatic chat selection") + next.map { it.title.ifBlank { "Untitled chat" } }
        spinnerUpdating = true
        sessionSpinner.adapter = ArrayAdapter(this, android.R.layout.simple_spinner_dropdown_item, labels)
        sessionSpinner.setSelection((next.indexOfFirst { it.id == activeId } + 1).coerceAtLeast(0))
        spinnerUpdating = false
    }

    private fun updateVoiceSessions(next: List<VoiceSession>, activeId: String) {
        if (next == voiceSessions && voiceSessionSpinner.selectedItemPosition == next.indexOfFirst { it.id == activeId }) return
        voiceSessions = next
        val labels = next.map { "Voice chat · ${it.title.ifBlank { "Untitled" }}" }.ifEmpty { listOf("Voice chat · loading") }
        voiceSpinnerUpdating = true
        voiceSessionSpinner.adapter = ArrayAdapter(this, android.R.layout.simple_spinner_dropdown_item, labels)
        voiceSessionSpinner.setSelection(next.indexOfFirst { it.id == activeId }.coerceAtLeast(0))
        voiceSpinnerUpdating = false
    }

    private fun addPart(part: VoicePart) {
        when {
            part.mimeType in DISPLAYABLE_TEXT_TYPES && part.text.isNotBlank() ->
                addBubble("Koder", part.text, Color.WHITE)
            part.mimeType.startsWith("image/") -> addImage(part)
            else -> addGenericPart(part)
        }
    }

    private fun addImage(part: VoicePart) {
        val card = card()
        val title = TextView(this).apply {
            text = part.name.ifBlank { part.alt.ifBlank { part.mimeType } }
            setTextColor(Color.DKGRAY)
        }
        val image = ImageView(this).apply {
            adjustViewBounds = true
            scaleType = ImageView.ScaleType.CENTER_INSIDE
            minimumHeight = dp(120)
            contentDescription = part.alt
        }
        card.addView(title, matchWrap())
        card.addView(image, matchWrap())
        feed.addView(card, cardMargins())
        if (part.url.isBlank()) {
            title.text = "${title.text} · no image URL"
        } else {
            controller.loadBytes(part.url) { bytes, error ->
                runOnUiThread {
                    val bitmap = bytes?.let { BitmapFactory.decodeByteArray(it, 0, it.size) }
                    if (bitmap != null) image.setImageBitmap(bitmap)
                    else title.text = "${title.text} · ${error ?: "invalid image"}"
                }
            }
        }
        scrollToBottom()
    }

    private fun addGenericPart(part: VoicePart) {
        val label = buildString {
            append(part.name.ifBlank { "Koder attachment" })
            append("\n")
            append(part.alt.ifBlank { part.mimeType })
			if (part.data != null) append("\n${part.data}")
            if (part.url.isNotBlank()) append("\nOpen ${part.url}")
        }
        val card = card().apply {
            addView(TextView(this@MainActivity).apply {
                text = label
                setTextColor(0xFF0D47A1.toInt())
            }, matchWrap())
            if (part.url.isNotBlank()) setOnClickListener { downloadAndOpen(part) }
        }
        feed.addView(card, cardMargins())
        scrollToBottom()
    }

    private fun downloadAndOpen(part: VoicePart) {
        controller.loadBytes(part.url) { bytes, error ->
            if (bytes == null) {
                runOnUiThread {
                    Toast.makeText(this, error ?: "Could not download attachment", Toast.LENGTH_LONG).show()
                }
                return@loadBytes
            }
            try {
                val directory = File(cacheDir, "voice-presentations").apply { mkdirs() }
                val requestedName = part.name.ifBlank { "koder-${part.id.ifBlank { part.url.hashCode().toString() }}" }
                val safeName = requestedName.replace(Regex("[^A-Za-z0-9._-]"), "_").take(96)
                    .ifBlank { "koder-attachment" }
                val file = File(directory, safeName)
                file.outputStream().use { it.write(bytes) }
                val uri = FileProvider.getUriForFile(this, "$packageName.presentations", file)
                runOnUiThread { openPresentation(uri, part.mimeType) }
            } catch (failure: Exception) {
                runOnUiThread {
                    Toast.makeText(
                        this,
                        failure.message ?: "Could not open attachment",
                        Toast.LENGTH_LONG,
                    ).show()
                }
            }
        }
    }

    private fun openPresentation(uri: Uri, mimeType: String) {
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, mimeType.ifBlank { "application/octet-stream" })
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        try {
            startActivity(intent)
        } catch (_: Exception) {
            Toast.makeText(this, "No app can open ${mimeType.ifBlank { "this attachment" }}", Toast.LENGTH_LONG).show()
        }
    }

    private fun addBubble(who: String, text: String, color: Int) = runOnUiThread {
        if (text.isBlank()) return@runOnUiThread
        val card = card().apply {
            setBackgroundColor(color)
            addView(TextView(this@MainActivity).apply {
                this.text = "$who\n$text"
                setTextColor(Color.BLACK)
                textSize = 16f
            }, matchWrap())
        }
        feed.addView(card, cardMargins())
        scrollToBottom()
    }

    private fun card() = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(12), dp(10), dp(12), dp(10))
        setBackgroundColor(Color.WHITE)
        elevation = dp(1).toFloat()
    }

    private fun scrollToBottom() = feedScroll.post { feedScroll.fullScroll(View.FOCUS_DOWN) }

    private fun restoreSettings() {
		val values = secureSettings.load()
		server.setText(values.server)
		token.setText(values.token)
    }

    private fun saveSettings() {
		secureSettings.save(server.text.toString(), token.text.toString())
    }

    private fun requiredPermissions(): List<String> = buildList {
        add(Manifest.permission.RECORD_AUDIO)
        if (Build.VERSION.SDK_INT >= 31) add(Manifest.permission.BLUETOOTH_CONNECT)
        if (Build.VERSION.SDK_INT >= 33) add(Manifest.permission.POST_NOTIFICATIONS)
    }

    private fun dp(value: Int): Int = (value * resources.displayMetrics.density).toInt()
    private fun matchWrap() = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT)
    private fun margins(height: Int, top: Int = 0, bottom: Int = 0) =
        LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, height).apply {
            topMargin = dp(top)
            bottomMargin = dp(bottom)
        }
    private fun cardMargins() = margins(ViewGroup.LayoutParams.WRAP_CONTENT, top = 5, bottom = 5)

    companion object {
        private const val PERMISSION_REQUEST = 80
        private val DISPLAYABLE_TEXT_TYPES = setOf("text/plain", "text/markdown")
    }
}
