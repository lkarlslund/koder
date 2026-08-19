package com.lkarlslund.koder

import android.Manifest
import android.annotation.SuppressLint
import android.app.AlertDialog
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.BitmapFactory
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.text.InputType
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.FileProvider
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import com.lkarlslund.koder.update.AndroidAppUpdater
import com.lkarlslund.koder.voice.AppUpdate
import com.lkarlslund.koder.voice.CallController
import com.lkarlslund.koder.voice.VoiceHome
import com.lkarlslund.koder.voice.VoiceMessage
import com.lkarlslund.koder.voice.VoicePart
import com.lkarlslund.koder.voice.VoiceSession
import com.lkarlslund.koder.voice.VoiceSessionClient
import java.io.File

@SuppressLint("SetTextI18n")
class MainActivity : ComponentActivity(), CallController.Listener {
    private enum class Screen { SETUP, LOADING, HOME, CHAT }

    private lateinit var controller: CallController
    private lateinit var secureSettings: SecureSettings
    private lateinit var appUpdater: AndroidAppUpdater
    private val sessionClient = VoiceSessionClient()

    private var screen = Screen.LOADING
    private var settings = SecureSettings.Values("", "")
    private var requestGeneration = 0L
    private var pendingSession: VoiceSession? = null
    private var pendingStart = false
    private var lastAppUpdate: AppUpdate? = null

    private var updateButton: Button? = null
    private var status: TextView? = null
    private var transcript: TextView? = null
    private var feed: LinearLayout? = null
    private var feedScroll: ScrollView? = null
    private var typedMessage: EditText? = null
    private val permissionLauncher = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
        if (!pendingStart) return@registerForActivityResult
        pendingStart = false
        if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) {
            startCall()
        } else {
            status?.text = "Microphone permission is required for voice conversations"
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        secureSettings = SecureSettings(this)
        controller = CallController(this, this)
        appUpdater = AndroidAppUpdater(this, ::showUpdateStatus)
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (screen == Screen.CHAT) {
                    leaveChat()
                } else {
                    isEnabled = false
                    onBackPressedDispatcher.onBackPressed()
                }
            }
        })
        settings = secureSettings.load()
        if (settings.server.isBlank()) showSetup() else loadHome()
    }

    override fun onDestroy() {
        requestGeneration++
        sessionClient.close()
        appUpdater.close()
        controller.close()
        super.onDestroy()
    }

    override fun onSnapshot(snapshot: CallController.Snapshot) {
        runOnUiThread {
            if (screen != Screen.CHAT) return@runOnUiThread
            status?.text = snapshot.detail
            transcript?.apply {
                text = snapshot.partialTranscript
                visibility = if (snapshot.partialTranscript.isBlank()) View.GONE else View.VISIBLE
            }
            if (snapshot.appUpdate != null && snapshot.appUpdate != lastAppUpdate) {
                lastAppUpdate = snapshot.appUpdate
            }
        }
    }

    override fun onUserMessage(text: String) = addBubble("You", text)

    override fun onAssistantMessage(message: VoiceMessage) {
        runOnUiThread {
            if (screen != Screen.CHAT) return@runOnUiThread
            message.parts.ifEmpty {
                listOf(VoicePart(mimeType = "text/plain", data = message.spokenText))
            }.forEach(::addPart)
        }
    }

    private fun showSetup(error: String = "") {
        screen = Screen.SETUP
        clearCallViews()
        val content = column()
        content.addView(logo(), centeredSquare(88, bottom = 18))
        content.addView(title("Welcome to Koder Voice"), matchWrap())
        content.addView(body("Connect this phone to your Koder server. You can change these settings later."), spaced(bottom = 24))

        content.addView(label("Server address"), matchWrap())
        val serverField = EditText(this).apply {
            hint = "http://192.168.1.20:7979"
            contentDescription = "Server address"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
            setSingleLine()
            setText(settings.server)
        }
        content.addView(serverField, matchWrap())
        content.addView(helper("Use the address you open Koder with. Include the port if it is not 80 or 443."), spaced(bottom = 18))

        content.addView(label("Access token"), matchWrap())
        val tokenField = EditText(this).apply {
            hint = "Optional"
            contentDescription = "Access token"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            setSingleLine()
            setText(settings.token)
        }
        content.addView(tokenField, matchWrap())
        content.addView(helper("Leave this empty when your Koder server has no voice token. The token is stored encrypted on this phone."), spaced(bottom = 20))
        if (error.isNotBlank()) content.addView(errorText(error), spaced(bottom = 12))

        content.addView(Button(this).apply {
            text = "Connect"
            contentDescription = "Connect to Koder"
            setOnClickListener {
                val serverAddress = serverField.text.toString().trim()
                if (serverAddress.isBlank()) {
                    serverField.error = "Server address is required"
                    return@setOnClickListener
                }
                settings = SecureSettings.Values(serverAddress, tokenField.text.toString())
                secureSettings.save(settings.server, settings.token)
                loadHome()
            }
        }, matchWrap())
        showScrollable(content)
    }

    private fun loadHome(message: String = "Connecting to Koder…") {
        screen = Screen.LOADING
        clearCallViews()
        val generation = ++requestGeneration
        val content = column(Gravity.CENTER_HORIZONTAL).apply {
            gravity = Gravity.CENTER
            addView(logo(), centeredSquare(88, bottom = 22))
            addView(ProgressBar(this@MainActivity))
            addView(title("Koder Voice").apply { gravity = Gravity.CENTER }, spaced(top = 18))
            addView(body(message).apply { gravity = Gravity.CENTER }, spaced(top = 8))
        }
        showContent(content)
        sessionClient.list(settings.server, settings.token) { result ->
            runOnUiThread {
                if (generation != requestGeneration || isFinishing || isDestroyed) return@runOnUiThread
                result.fold(::showHome, ::showConnectionError)
            }
        }
    }

    private fun showConnectionError(failure: Throwable) {
        screen = Screen.LOADING
        val content = column()
        content.addView(title("Couldn’t connect to Koder"), matchWrap())
        content.addView(body(failure.message ?: "The server could not be reached."), spaced(top = 8, bottom = 20))
        content.addView(Button(this).apply {
            text = "Try again"
            setOnClickListener { loadHome() }
        }, matchWrap())
        content.addView(Button(this).apply {
            text = "Edit connection settings"
            setOnClickListener { showSetup() }
        }, spaced(top = 8))
        showContent(content)
    }

    private fun showHome(home: VoiceHome) {
        screen = Screen.HOME
        clearCallViews()
        val root = column()
        val heading = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(title("Conversations"), LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            addView(Button(this@MainActivity).apply {
                text = "Settings"
                contentDescription = "Connection settings"
                setOnClickListener { showSetup() }
            })
        }
        root.addView(heading, matchWrap())
        root.addView(body("Continue a conversation or start a new one."), spaced(bottom = 14))

        updateButton = Button(this).apply {
            visibility = View.GONE
            setOnClickListener { appUpdater.install() }
        }
        root.addView(updateButton, matchWrap())

        val list = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        if (home.voiceSessions.isEmpty()) {
            list.addView(helper("No voice conversations yet."), spaced(top = 20, bottom = 20))
        } else {
            home.voiceSessions.forEach { session -> list.addView(sessionButton(session), spaced(bottom = 8)) }
        }
        root.addView(ScrollView(this).apply { addView(list, matchWrap()) }, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        root.addView(Button(this).apply {
            text = "New conversation"
            contentDescription = "Create a new voice conversation"
            setOnClickListener { showCreateVoiceSessionDialog() }
        }, spaced(top = 12))
        showContent(root)

        lastAppUpdate = home.appUpdate
        appUpdater.consider(home.appUpdate, settings.server, settings.token)
    }

    private fun sessionButton(session: VoiceSession) = Button(this).apply {
        text = buildString {
            append(session.title.ifBlank { "Untitled conversation" })
            if (session.lastMessage.isNotBlank()) append("\n${session.lastMessage}")
        }
        gravity = Gravity.START or Gravity.CENTER_VERTICAL
        isAllCaps = false
        minHeight = dp(64)
        contentDescription = "Open voice conversation ${session.title.ifBlank { "Untitled" }}"
        setOnClickListener { openChat(session) }
    }

    private fun showCreateVoiceSessionDialog() {
        val titleField = EditText(this).apply {
            hint = "For example: Personal"
            contentDescription = "Conversation name"
            setSingleLine()
        }
        AlertDialog.Builder(this)
            .setTitle("New conversation")
            .setMessage("Give this ongoing voice conversation a name so you can find it again.")
            .setView(titleField)
            .setNegativeButton("Cancel", null)
            .setPositiveButton("Create") { _, _ -> createSession(titleField.text.toString()) }
            .show()
    }

    private fun createSession(title: String) {
        screen = Screen.LOADING
        val generation = ++requestGeneration
        val content = column(Gravity.CENTER_HORIZONTAL).apply {
            gravity = Gravity.CENTER
            addView(ProgressBar(this@MainActivity))
            addView(body("Creating conversation…"), spaced(top = 18))
        }
        showContent(content)
        sessionClient.create(settings.server, settings.token, title.trim().ifBlank { "Voice Chat" }) { result ->
            runOnUiThread {
                if (generation != requestGeneration || isFinishing || isDestroyed) return@runOnUiThread
                result.fold(
                    onSuccess = { home ->
                        home.createdVoiceSession?.let(::openChat) ?: showHome(home)
                    },
                    onFailure = ::showConnectionError,
                )
            }
        }
    }

    private fun openChat(session: VoiceSession) {
        screen = Screen.CHAT
        clearCallViews()
        pendingSession = session
        val root = column()
        val heading = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(Button(this@MainActivity).apply {
                text = "Back"
                contentDescription = "Back to conversations"
                setOnClickListener { leaveChat() }
            })
            addView(title(session.title.ifBlank { "Conversation" }), LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
                marginStart = dp(10)
            })
        }
        root.addView(heading, matchWrap())
        status = body("Preparing conversation…").apply { gravity = Gravity.CENTER_HORIZONTAL }
        root.addView(status, spaced(top = 8))
        transcript = helper("").apply { visibility = View.GONE }
        root.addView(transcript, spaced(top = 4))

        feed = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        feedScroll = ScrollView(this).apply { addView(feed, matchWrap()) }
        root.addView(feedScroll, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))

        val composer = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        typedMessage = EditText(this).apply {
            hint = "Message Koder"
            maxLines = 3
        }
        composer.addView(typedMessage, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        composer.addView(Button(this).apply {
            text = "Send"
            setOnClickListener {
                val message = typedMessage?.text?.toString().orEmpty()
                if (message.isNotBlank()) {
                    controller.submit(message)
                    typedMessage?.text?.clear()
                }
            }
        })
        root.addView(composer, matchWrap())
        showContent(root)
        requestCallStart()
    }

    private fun leaveChat() {
        pendingStart = false
        pendingSession = null
        controller.end()
        loadHome("Refreshing conversations…")
    }

    private fun requestCallStart() {
        val missing = requiredPermissions().filter { checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED }
        if (missing.isEmpty()) startCall() else {
            pendingStart = true
            permissionLauncher.launch(missing.toTypedArray())
        }
    }

    private fun startCall() {
        val session = pendingSession ?: return
        controller.start(settings.server, settings.token, session.id)
    }

    private fun addPart(part: VoicePart) {
        when {
            part.mimeType in DISPLAYABLE_TEXT_TYPES && part.text.isNotBlank() -> addBubble("Koder", part.text)
            part.mimeType.startsWith("image/") -> addImage(part)
            else -> addGenericPart(part)
        }
    }

    private fun addImage(part: VoicePart) {
        val feed = feed ?: return
        val card = card()
        val title = helper(part.name.ifBlank { part.alt.ifBlank { part.mimeType } })
        val image = ImageView(this).apply {
            adjustViewBounds = true
            scaleType = ImageView.ScaleType.CENTER_INSIDE
            minimumHeight = dp(120)
            contentDescription = part.alt
        }
        card.addView(title, matchWrap())
        card.addView(image, matchWrap())
        feed.addView(card, spaced(top = 5, bottom = 5))
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
        val feed = feed ?: return
        val text = buildString {
            append(part.name.ifBlank { "Koder attachment" })
            append("\n")
            append(part.alt.ifBlank { part.mimeType })
            if (part.data != null) append("\n${part.data}")
            if (part.url.isNotBlank()) append("\nOpen ${part.url}")
        }
        val card = card().apply {
            addView(body(text), matchWrap())
            if (part.url.isNotBlank()) setOnClickListener { downloadAndOpen(part) }
        }
        feed.addView(card, spaced(top = 5, bottom = 5))
        scrollToBottom()
    }

    private fun downloadAndOpen(part: VoicePart) {
        controller.loadBytes(part.url) { bytes, error ->
            if (bytes == null) {
                runOnUiThread { Toast.makeText(this, error ?: "Could not download attachment", Toast.LENGTH_LONG).show() }
                return@loadBytes
            }
            try {
                val directory = File(cacheDir, "voice-presentations").apply { mkdirs() }
                val requestedName = part.name.ifBlank { "koder-${part.id.ifBlank { part.url.hashCode().toString() }}" }
                val safeName = requestedName.replace(Regex("[^A-Za-z0-9._-]"), "_").take(96).ifBlank { "koder-attachment" }
                val file = File(directory, safeName)
                file.outputStream().use { it.write(bytes) }
                val uri = FileProvider.getUriForFile(this, "$packageName.presentations", file)
                runOnUiThread { openPresentation(uri, part.mimeType) }
            } catch (failure: Exception) {
                runOnUiThread { Toast.makeText(this, failure.message ?: "Could not open attachment", Toast.LENGTH_LONG).show() }
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

    private fun addBubble(who: String, text: String) = runOnUiThread {
        val feed = feed ?: return@runOnUiThread
        if (screen != Screen.CHAT || text.isBlank()) return@runOnUiThread
        val card = card().apply {
            addView(label(who), matchWrap())
            addView(body(text), spaced(top = 3))
        }
        feed.addView(card, spaced(top = 5, bottom = 5))
        scrollToBottom()
    }

    private fun card() = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(12), dp(10), dp(12), dp(10))
        background = GradientDrawable().apply {
            setColor(themeColor(android.R.attr.colorBackgroundFloating))
            cornerRadius = dp(10).toFloat()
        }
        elevation = dp(1).toFloat()
    }

    private fun scrollToBottom() = feedScroll?.post { feedScroll?.fullScroll(View.FOCUS_DOWN) }

    private fun clearCallViews() {
        updateButton = null
        status = null
        transcript = null
        feed = null
        feedScroll = null
        typedMessage = null
    }

    private fun showUpdateStatus(next: AndroidAppUpdater.Status) = runOnUiThread {
        val button = updateButton ?: return@runOnUiThread
        when (next) {
            AndroidAppUpdater.Status.Hidden -> button.visibility = View.GONE
            is AndroidAppUpdater.Status.Available -> {
                button.text = "Update Koder · ${next.versionName}"
                button.isEnabled = true
                button.visibility = View.VISIBLE
            }
            is AndroidAppUpdater.Status.Busy -> {
                button.text = next.message
                button.isEnabled = false
                button.visibility = View.VISIBLE
            }
            is AndroidAppUpdater.Status.Error -> {
                button.text = "${next.message} · Retry"
                button.isEnabled = true
                button.visibility = View.VISIBLE
            }
        }
    }

    private fun showScrollable(content: View) = showContent(ScrollView(this).apply { addView(content, matchWrap()) })

    private fun showContent(content: View) {
        val baseHorizontal = dp(20)
        val baseVertical = dp(16)
        content.setBackgroundColor(themeColor(android.R.attr.colorBackground))
        ViewCompat.setOnApplyWindowInsetsListener(content) { view, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout())
            view.setPadding(baseHorizontal + bars.left, baseVertical + bars.top, baseHorizontal + bars.right, baseVertical + bars.bottom)
            insets
        }
        setContentView(content)
        ViewCompat.requestApplyInsets(content)
    }

    private fun column(horizontalGravity: Int = Gravity.NO_GRAVITY) = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        gravity = horizontalGravity
    }

    private fun title(text: String) = TextView(this).apply {
        this.text = text
        textSize = 26f
    }

    private fun label(text: String) = TextView(this).apply {
        this.text = text
        textSize = 16f
    }

    private fun body(text: String) = TextView(this).apply {
        this.text = text
        textSize = 16f
    }

    private fun helper(text: String) = TextView(this).apply {
        this.text = text
        textSize = 14f
        alpha = 0.72f
    }

    private fun errorText(text: String) = TextView(this).apply {
        this.text = text
        textSize = 15f
        setTextColor(themeColor(android.R.attr.colorError))
    }

    private fun logo() = ImageView(this).apply {
        setImageResource(R.drawable.ic_koder)
        scaleType = ImageView.ScaleType.FIT_CENTER
        contentDescription = "Koder logo"
    }

    private fun themeColor(attribute: Int): Int {
        val value = TypedValue()
        check(theme.resolveAttribute(attribute, value, true)) { "Theme does not define color attribute $attribute" }
        return if (value.resourceId != 0) getColor(value.resourceId) else value.data
    }

    private fun requiredPermissions(): List<String> = buildList {
        add(Manifest.permission.RECORD_AUDIO)
        if (Build.VERSION.SDK_INT >= 31) add(Manifest.permission.BLUETOOTH_CONNECT)
        if (Build.VERSION.SDK_INT >= 33) add(Manifest.permission.POST_NOTIFICATIONS)
    }

    private fun dp(value: Int): Int = (value * resources.displayMetrics.density).toInt()
    private fun matchWrap() = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT)
    private fun spaced(height: Int = ViewGroup.LayoutParams.WRAP_CONTENT, top: Int = 0, bottom: Int = 0) =
        LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, height).apply {
            topMargin = dp(top)
            bottomMargin = dp(bottom)
        }
    private fun centeredSquare(size: Int, bottom: Int = 0) = LinearLayout.LayoutParams(dp(size), dp(size)).apply {
        gravity = Gravity.CENTER_HORIZONTAL
        bottomMargin = dp(bottom)
    }

    companion object {
        private val DISPLAYABLE_TEXT_TYPES = setOf("text/plain", "text/markdown")
    }
}
