package com.lkarlslund.koder

import android.Manifest
import android.annotation.SuppressLint
import android.app.AlertDialog
import android.content.ClipData
import android.content.ClipboardManager
import android.content.res.ColorStateList
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.BitmapFactory
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings as AndroidSettings
import android.text.TextUtils
import android.text.InputType
import android.text.format.DateUtils
import android.text.method.PasswordTransformationMethod
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.PopupMenu
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.Switch
import android.widget.TextView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.FileProvider
import androidx.core.app.NotificationManagerCompat
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import com.lkarlslund.koder.update.AndroidAppUpdater
import com.lkarlslund.koder.phone.PhoneCapabilities
import com.lkarlslund.koder.phone.PhoneCapability
import com.lkarlslund.koder.voice.AppUpdate
import com.lkarlslund.koder.voice.CallController
import com.lkarlslund.koder.voice.ConversationSurface
import com.lkarlslund.koder.voice.ServerInfo
import com.lkarlslund.koder.voice.SpeechLanguage
import com.lkarlslund.koder.voice.SpeechLanguages
import com.lkarlslund.koder.voice.VOICE_PROTOCOL
import com.lkarlslund.koder.voice.VoiceHome
import com.lkarlslund.koder.voice.VoiceMessage
import com.lkarlslund.koder.voice.VoicePart
import com.lkarlslund.koder.voice.VoiceSession
import com.lkarlslund.koder.voice.VoiceSessionClient
import com.lkarlslund.koder.voice.VoiceTranscriptEntry
import com.lkarlslund.koder.voice.conversationSurface
import java.io.File
import java.time.Duration
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import java.util.Locale
import org.json.JSONTokener
import kotlin.math.abs

@SuppressLint("SetTextI18n")
class MainActivity : ComponentActivity(), CallController.Listener {
    private enum class Screen { SETUP, SETTINGS, LOADING, HOME, CHAT }

    private lateinit var controller: CallController
    private lateinit var secureSettings: SecureSettings
    private lateinit var appUpdater: AndroidAppUpdater
    private val sessionClient = VoiceSessionClient()

    private var screen = Screen.LOADING
    private var settings = SecureSettings.Values("", "")
    private var requestGeneration = 0L
    private var pendingSession: VoiceSession? = null
    private var pendingStart = false
    private var pendingPhoneCapability: PhoneCapability? = null
    private var lastAppUpdate: AppUpdate? = null

    private var updateButton: Button? = null
    private var updateProgress: ProgressBar? = null
    private var status: TextView? = null
    private var transcript: TextView? = null
    private var feed: LinearLayout? = null
    private var feedScroll: ScrollView? = null
    private var feedPlaceholder: View? = null
    private var typedMessage: EditText? = null
	private var activePanel: View? = null
	private var presentationPanel: View? = null
	private var presentationFeed: LinearLayout? = null
	private var presentationShown = false
	private var speechAutomaticCheck: CheckBox? = null
	private var composerView: View? = null
	private var pauseButton: Button? = null
	private var transcriptButton: Button? = null
	private var transcriptShown = false
	private var renderedHistorySession = ""
	private var placeholderTitle: TextView? = null
	private var placeholderDetail: TextView? = null
    private val permissionLauncher = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
        if (!pendingStart) return@registerForActivityResult
        pendingStart = false
        if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) {
            startCall()
        } else {
            status?.text = "Microphone permission is required for voice conversations"
        }
    }
    private val phonePermissionLauncher = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
        val capability = pendingPhoneCapability ?: return@registerForActivityResult
        pendingPhoneCapability = null
        val granted = phoneCapabilityAvailable(capability)
        if (granted) enablePhoneCapability(capability.id) else {
            Toast.makeText(this, "${capability.title} permission was not granted", Toast.LENGTH_LONG).show()
            showSettings()
        }
    }
    private val notificationAccessLauncher = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        val capability = pendingPhoneCapability ?: return@registerForActivityResult
        pendingPhoneCapability = null
        if (notificationAccessGranted()) enablePhoneCapability(capability.id) else showSettings()
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
                } else if (screen == Screen.SETTINGS) {
                    loadHome()
                } else if (screen == Screen.SETUP && settings.server.isNotBlank()) {
                    showSettings()
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
				visibility = if (transcriptShown && snapshot.partialTranscript.isNotBlank()) View.VISIBLE else View.GONE
            }
			if (snapshot.voiceSessionId.isNotBlank() && snapshot.history.isNotEmpty() && renderedHistorySession != snapshot.voiceSessionId) {
				renderHistory(snapshot.voiceSessionId, snapshot.history)
			}
			updateConversationMode(snapshot.stage)
            if (snapshot.appUpdate != null && snapshot.appUpdate != lastAppUpdate) {
                lastAppUpdate = snapshot.appUpdate
            }
        }
    }

    override fun onUserMessage(text: String) = addBubble("You", text)

    override fun onAssistantMessage(message: VoiceMessage) {
        runOnUiThread {
            if (screen != Screen.CHAT) return@runOnUiThread
			val parts = message.parts.ifEmpty {
                listOf(VoicePart(mimeType = "text/plain", data = message.spokenText))
			}
			val (visualParts, transcriptParts) = parts.partition {
				it.isPresentation || it.uri.isNotBlank() || it.mimeType !in DISPLAYABLE_TEXT_TYPES
			}
			if (visualParts.isNotEmpty()) {
				presentationFeed?.removeAllViews()
				visualParts.forEach(::addPresentationPart)
				presentationShown = true
				transcriptShown = false
			}
			transcriptParts.forEach(::addPart)
			updateConversationMode(null)
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
            setSingleLine()
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            transformationMethod = PasswordTransformationMethod.getInstance()
            importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO
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
				settings = settings.copy(server = serverAddress, token = tokenField.text.toString())
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

        val appBar = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(logo(), LinearLayout.LayoutParams(dp(44), dp(44)).apply { marginEnd = dp(12) })
            addView(title("Koder Voice").apply { textSize = 24f }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            addView(TextView(this@MainActivity).apply {
                text = "⋮"
                textSize = 30f
                gravity = Gravity.CENTER
                minWidth = dp(48)
                minHeight = dp(48)
                contentDescription = "More options"
                isClickable = true
                isFocusable = true
                val selectable = TypedValue()
                if (theme.resolveAttribute(android.R.attr.selectableItemBackgroundBorderless, selectable, true)) {
                    setBackgroundResource(selectable.resourceId)
                }
                setOnClickListener(::showHomeMenu)
            })
        }
        root.addView(appBar, matchWrap())
        root.addView(title("Conversations").apply {
            textSize = 30f
            setTypeface(typeface, Typeface.BOLD)
        }, spaced(top = 24))
        root.addView(body("Pick up where you left off, or begin something new."), spaced(top = 4, bottom = 18))

        updateButton = Button(this).apply {
            visibility = View.GONE
            setOnClickListener { appUpdater.install() }
        }
        root.addView(updateButton, matchWrap())
        updateProgress = ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply {
            max = 100
            visibility = View.GONE
            contentDescription = "Update download progress"
        }
        root.addView(updateProgress, spaced(top = 6, bottom = 10))

        val list = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        if (home.voiceSessions.isEmpty()) {
            list.addView(emptyConversationCard(), spaced(top = 8, bottom = 12))
        } else {
            home.voiceSessions.forEach { session -> list.addView(sessionCard(session), spaced(bottom = 12)) }
        }
        val sessionScroll = ScrollView(this).apply { addView(list, matchWrap()) }
        val refresh = SwipeRefreshLayout(this).apply {
            contentDescription = "Conversation list"
            setColorSchemeColors(themeColor(android.R.attr.colorAccent))
            addView(sessionScroll, matchWrap())
            setOnRefreshListener { refreshHome(this) }
        }
        root.addView(refresh, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        root.addView(Button(this).apply {
            text = "+  New conversation"
            isAllCaps = false
            contentDescription = "Create a new voice conversation"
            backgroundTintList = ColorStateList.valueOf(themeColor(android.R.attr.colorAccent))
            setTextColor(themeColor(android.R.attr.colorBackground))
            setOnClickListener { showCreateVoiceSessionDialog() }
        }, spaced(top = 12))
        showContent(root)

        lastAppUpdate = home.appUpdate
        appUpdater.consider(home.appUpdate, settings.server, settings.token)
    }

    private fun showHomeMenu(anchor: View) {
        PopupMenu(this, anchor).apply {
            menu.add("Server info")
            menu.add("Settings")
            menu.add("About")
            setOnMenuItemClickListener { item ->
                when (item.title.toString()) {
                    "Server info" -> loadServerInfo()
                    "Settings" -> showSettings()
                    "About" -> showAbout()
                }
                true
            }
            show()
        }
    }

    private fun showSettings() {
        screen = Screen.SETTINGS
        settings = secureSettings.load()
        clearCallViews()
        val content = column()
        content.addView(LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(TextView(this@MainActivity).apply {
                text = "‹"; textSize = 38f; gravity = Gravity.CENTER; minWidth = dp(48); minHeight = dp(48)
                contentDescription = "Back to conversations"; isClickable = true; setOnClickListener { loadHome() }
            })
            addView(title("Settings"), LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        }, matchWrap())

        content.addView(label("Koder server"), spaced(top = 20, bottom = 5))
        content.addView(body(settings.server).apply { typeface = Typeface.MONOSPACE }, matchWrap())
        content.addView(Button(this).apply {
            text = "Edit connection"; isAllCaps = false; setOnClickListener { showSetup() }
        }, spaced(top = 8, bottom = 24))

		content.addView(title("Speech recognition").apply { textSize = 24f }, matchWrap())
		content.addView(body("Automatic can recognize any language. Select one language for the strongest recognition hint, or several to focus automatic detection on languages you actually speak."), spaced(top = 6, bottom = 12))
		content.addView(speechAutomaticRow(), spaced(bottom = 8))
		SpeechLanguages.all.forEach { language -> content.addView(speechLanguageRow(language), spaced(bottom = 8)) }
		content.addView(helper("Language choices apply the next time a conversation connects. Multiple choices are recognition hints because compatible speech services accept only one hard language setting."), spaced(top = 3, bottom = 24))

        content.addView(title("Phone tools").apply { textSize = 24f }, matchWrap())
        content.addView(body("Choose what the active Koder voice conversation may ask this phone to do. Disabled groups disappear from Koder's available tools. Actions that change something or contact a person are confirmed here first."), spaced(top = 6, bottom = 14))
        PhoneCapabilities.all.forEach { capability -> content.addView(phoneCapabilityRow(capability), spaced(bottom = 10)) }
        content.addView(helper("SMS and call access is intended for this sideloaded personal build. Notification access exposes only notifications currently visible to Android; Koder cannot directly browse arbitrary email inboxes."), spaced(top = 5, bottom = 18))
        showScrollable(content)
    }

	private fun speechAutomaticRow() = CheckBox(this).apply {
		speechAutomaticCheck = this
		text = "Automatic (all languages)"
		contentDescription = "Use automatic speech language detection"
		isChecked = settings.speechLanguages.isEmpty()
		setPadding(dp(12), dp(10), dp(12), dp(10))
		background = cardBackground()
		setOnCheckedChangeListener { _, checked ->
			if (checked && settings.speechLanguages.isNotEmpty()) updateSpeechLanguages(emptySet(), refresh = true)
			else if (!checked && settings.speechLanguages.isEmpty()) isChecked = true
		}
	}

	private fun speechLanguageRow(language: SpeechLanguage) = CheckBox(this).apply {
		text = "${language.name} (${language.code})"
		contentDescription = "Recognize ${language.name}"
		isChecked = language.code in settings.speechLanguages
		setPadding(dp(12), dp(10), dp(12), dp(10))
		background = cardBackground()
		setOnCheckedChangeListener { _, checked ->
			val selected = if (checked) settings.speechLanguages + language.code else settings.speechLanguages - language.code
			updateSpeechLanguages(selected)
		}
	}

	private fun updateSpeechLanguages(languages: Set<String>, refresh: Boolean = false) {
		secureSettings.saveSpeechLanguages(languages)
		settings = settings.copy(speechLanguages = languages)
		if (refresh) showSettings() else speechAutomaticCheck?.isChecked = languages.isEmpty()
	}

    private fun phoneCapabilityRow(capability: PhoneCapability) = LinearLayout(this).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        setPadding(dp(16), dp(13), dp(12), dp(13))
        background = cardBackground()
        addView(LinearLayout(this@MainActivity).apply {
            orientation = LinearLayout.VERTICAL
            addView(body(capability.title).apply { setTypeface(typeface, Typeface.BOLD) }, matchWrap())
            addView(helper(capability.description), spaced(top = 3))
        }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply { marginEnd = dp(8) })
        addView(Switch(this@MainActivity).apply {
            contentDescription = "Allow ${capability.title}"
            isChecked = capability.id in settings.enabledPhoneCapabilities && phoneCapabilityAvailable(capability)
            setOnCheckedChangeListener { _, checked ->
                if (checked) requestPhoneCapability(capability) else disablePhoneCapability(capability.id)
            }
        })
    }

    private fun requestPhoneCapability(capability: PhoneCapability) {
        pendingPhoneCapability = capability
        when {
            capability.notificationAccess && !notificationAccessGranted() -> {
                notificationAccessLauncher.launch(Intent(AndroidSettings.ACTION_NOTIFICATION_LISTENER_SETTINGS))
            }
            capability.permissions.any { checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED } ->
                phonePermissionLauncher.launch(capability.permissions)
            else -> {
                pendingPhoneCapability = null
                enablePhoneCapability(capability.id)
            }
        }
    }

    private fun enablePhoneCapability(id: String) {
        val enabled = settings.enabledPhoneCapabilities + id
        secureSettings.savePhoneCapabilities(enabled)
        settings = settings.copy(enabledPhoneCapabilities = enabled)
        showSettings()
    }

    private fun disablePhoneCapability(id: String) {
        val enabled = settings.enabledPhoneCapabilities - id
        secureSettings.savePhoneCapabilities(enabled)
        settings = settings.copy(enabledPhoneCapabilities = enabled)
    }

    private fun phoneCapabilityAvailable(capability: PhoneCapability) =
        (!capability.notificationAccess || notificationAccessGranted()) &&
            if (capability.id == "location") {
                capability.permissions.any { checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED }
            } else {
                capability.permissions.all { checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED }
            }

    private fun notificationAccessGranted() = packageName in NotificationManagerCompat.getEnabledListenerPackages(this)

    private fun loadServerInfo() {
        val loading = AlertDialog.Builder(this)
            .setTitle("Server info")
            .setView(LinearLayout(this).apply {
                orientation = LinearLayout.HORIZONTAL
                gravity = Gravity.CENTER_VERTICAL
                setPadding(dp(24), dp(12), dp(24), dp(12))
                addView(ProgressBar(this@MainActivity), LinearLayout.LayoutParams(dp(32), dp(32)))
                addView(body("Pinging Koder…"), LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
                    marginStart = dp(16)
                })
            })
            .setNegativeButton("Cancel", null)
            .show()
        sessionClient.serverInfo(settings.server, settings.token) { result ->
            runOnUiThread {
                if (isFinishing || isDestroyed || !loading.isShowing) return@runOnUiThread
                loading.dismiss()
                result.fold(::showServerInfo, ::showServerInfoError)
            }
        }
    }

    private fun showServerInfo(info: ServerInfo) {
        val content = column().apply {
            setPadding(dp(8), dp(4), dp(8), dp(4))
            addView(TextView(this@MainActivity).apply {
                text = "●  ${info.roundTripMillis} ms round trip"
                textSize = 18f
                setTypeface(typeface, Typeface.BOLD)
                setTextColor(themeColor(android.R.attr.colorAccent))
                background = GradientDrawable().apply {
                    setColor(withAlpha(themeColor(android.R.attr.colorAccent), 28))
                    cornerRadius = dp(14).toFloat()
                }
                setPadding(dp(14), dp(10), dp(14), dp(10))
            }, spaced(bottom = 16))

            addDiagnosticsSection("Connection", listOf(
                "Server" to settings.server,
                "Protocol" to VOICE_PROTOCOL,
                "Authentication" to if (info.tokenRequired) "Bearer token required" else "No token required",
                "Server time" to formatTimestamp(info.serverTime),
            ))
            addDiagnosticsSection("Koder", listOf(
                "Version" to info.version,
                "Commit" to info.commit.take(12) + if (info.dirty == "true") " · dirty" else "",
                "Built" to formatTimestamp(info.buildTime),
                "Uptime" to formatUptime(info.uptimeSeconds),
                "Started" to formatTimestamp(info.startedAt),
            ))
            addDiagnosticsSection("Sessions", listOf(
                "Regular" to info.sessionCount.toString(),
                "Voice" to info.voiceSessionCount.toString(),
                "Live voice" to if (info.voiceConnectionActive) {
                    "Active${info.voiceConnectionSince?.let { since ->
                        " · ${formatUptime(Duration.between(since, info.serverTime).seconds)}"
                    }.orEmpty()}"
                } else {
                    "Idle"
                },
            ))
            addDiagnosticsSection("Runtime", listOf(
                "Platform" to info.platform,
                "Go" to info.goVersion,
                "CPU" to "${info.logicalCPUs} logical · ${info.maxProcs} available",
                "Goroutines" to info.goroutines.toString(),
                "Heap" to "${formatBytes(info.heapAllocBytes)} live · ${formatBytes(info.heapSysBytes)} reserved",
                "Heap objects" to String.format(Locale.US, "%,d", info.heapObjects),
                "GC cycles" to String.format(Locale.US, "%,d", info.gcCycles),
            ))
            val installedVersion = packageManager.getPackageInfo(packageName, 0).versionName.orEmpty()
            addDiagnosticsSection("Android client", buildList {
                add("Installed" to installedVersion.ifBlank { "development" })
                lastAppUpdate?.let { update ->
                    add("Server APK" to "${update.versionName} · ${formatBytes(update.apkSize)}")
                    add("Channel" to update.channel)
                }
            }, bottom = 0)
        }
        AlertDialog.Builder(this)
            .setTitle("Server info")
            .setView(ScrollView(this).apply { addView(content, matchWrap()) })
            .setPositiveButton("Copy") { _, _ -> copyServerInfo(info) }
            .setNeutralButton("Refresh") { _, _ -> loadServerInfo() }
            .setNegativeButton("Close", null)
            .show()
    }

    private fun showServerInfoError(failure: Throwable) {
        AlertDialog.Builder(this)
            .setTitle("Server info unavailable")
            .setMessage(failure.message ?: "Koder could not be reached.")
            .setPositiveButton("Retry") { _, _ -> loadServerInfo() }
            .setNegativeButton("Close", null)
            .show()
    }

    private fun LinearLayout.addDiagnosticsSection(
        heading: String,
        rows: List<Pair<String, String>>,
        bottom: Int = 18,
    ) {
        addView(label(heading).apply {
            setTypeface(typeface, Typeface.BOLD)
            setTextColor(themeColor(android.R.attr.colorAccent))
        }, spaced(bottom = 5))
        rows.forEach { (name, value) ->
            addView(LinearLayout(this@MainActivity).apply {
                orientation = LinearLayout.HORIZONTAL
                gravity = Gravity.TOP
                addView(helper(name).apply { alpha = 0.65f }, LinearLayout.LayoutParams(dp(108), ViewGroup.LayoutParams.WRAP_CONTENT))
                addView(body(value).apply {
                    textSize = 14f
                    typeface = Typeface.MONOSPACE
                    setTextIsSelectable(true)
                }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            }, spaced(bottom = 5))
        }
        if (bottom > 0) addView(View(this@MainActivity), spaced(height = dp(bottom)))
    }

    private fun copyServerInfo(info: ServerInfo) {
        val text = buildString {
            appendLine("Koder ${info.version} (${info.commit}${if (info.dirty == "true") ", dirty" else ""})")
            appendLine("Server: ${settings.server}")
            appendLine("Round trip: ${info.roundTripMillis} ms")
            appendLine("Uptime: ${formatUptime(info.uptimeSeconds)}")
            appendLine("Platform: ${info.platform} · ${info.goVersion}")
            appendLine("CPU: ${info.logicalCPUs} logical · ${info.maxProcs} available")
            appendLine("Goroutines: ${info.goroutines}")
            appendLine("Heap: ${formatBytes(info.heapAllocBytes)} live · ${formatBytes(info.heapSysBytes)} reserved")
            appendLine("Sessions: ${info.sessionCount} regular · ${info.voiceSessionCount} voice")
            append("Voice connection: ${if (info.voiceConnectionActive) "active" else "idle"}")
        }
        getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Koder server info", text))
        Toast.makeText(this, "Server info copied", Toast.LENGTH_SHORT).show()
    }

    private fun formatUptime(totalSeconds: Long): String {
        val safeSeconds = totalSeconds.coerceAtLeast(0)
        val days = safeSeconds / 86_400
        val clock = DateUtils.formatElapsedTime(safeSeconds % 86_400)
        return if (days > 0) "${days}d $clock" else clock
    }

    private fun formatTimestamp(value: Instant): String = SERVER_TIMESTAMP.format(value)

    private fun formatTimestamp(value: String): String = runCatching { formatTimestamp(Instant.parse(value)) }
        .getOrDefault(value)

    private fun formatBytes(bytes: Long): String = when {
        bytes >= 1024L * 1024L * 1024L -> String.format(Locale.US, "%.1f GiB", bytes / (1024.0 * 1024.0 * 1024.0))
        bytes >= 1024L * 1024L -> String.format(Locale.US, "%.1f MiB", bytes / (1024.0 * 1024.0))
        bytes >= 1024L -> String.format(Locale.US, "%.1f KiB", bytes / 1024.0)
        else -> "$bytes B"
    }

    private fun showAbout() {
        val version = packageManager.getPackageInfo(packageName, 0).versionName.orEmpty()
        AlertDialog.Builder(this)
            .setTitle("About Koder Voice")
            .setMessage("Native voice conversations for Koder.\n\nVersion ${version.ifBlank { "development" }}")
            .setPositiveButton("Close", null)
            .show()
    }

    private fun sessionCard(session: VoiceSession) = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(18), dp(15), dp(16), dp(15))
        background = cardBackground()
        elevation = dp(2).toFloat()
        isClickable = true
        isFocusable = true
        contentDescription = "Open voice conversation ${session.title.ifBlank { "Untitled" }}"
        val selectable = TypedValue()
        if (theme.resolveAttribute(android.R.attr.selectableItemBackground, selectable, true)) {
            foreground = getDrawable(selectable.resourceId)
        }

        addView(LinearLayout(this@MainActivity).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(TextView(this@MainActivity).apply {
                text = session.title.ifBlank { "Untitled conversation" }
                textSize = 19f
                setTypeface(typeface, Typeface.BOLD)
                maxLines = 1
                ellipsize = TextUtils.TruncateAt.END
            }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            addView(TextView(this@MainActivity).apply {
                text = "›"
                textSize = 30f
                setTextColor(themeColor(android.R.attr.colorAccent))
            })
        }, matchWrap())
        session.updatedAt?.let { updated ->
            addView(helper(lastUsedText(updated.toEpochMilli())).apply {
                setTextColor(themeColor(android.R.attr.colorAccent))
                alpha = 1f
            }, spaced(top = 3))
        }
        if (session.lastMessage.isNotBlank()) {
            addView(body(session.lastMessage).apply {
                maxLines = 2
                ellipsize = TextUtils.TruncateAt.END
                alpha = 0.78f
            }, spaced(top = 8))
        }
        setOnClickListener { openChat(session) }
    }

    private fun emptyConversationCard() = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        gravity = Gravity.CENTER_HORIZONTAL
        setPadding(dp(24), dp(30), dp(24), dp(30))
        background = cardBackground()
        addView(logo(), centeredSquare(58, bottom = 16))
        addView(body("Your conversations will live here.").apply {
            gravity = Gravity.CENTER
            setTypeface(typeface, Typeface.BOLD)
        }, matchWrap())
        addView(helper("Start one below, then return whenever you want to continue.").apply {
            gravity = Gravity.CENTER
        }, spaced(top = 6))
    }

    private fun refreshHome(indicator: SwipeRefreshLayout) {
        val generation = ++requestGeneration
        indicator.isRefreshing = true
        sessionClient.list(settings.server, settings.token) { result ->
            runOnUiThread {
                if (generation != requestGeneration || isFinishing || isDestroyed) return@runOnUiThread
                result.fold(
                    onSuccess = ::showHome,
                    onFailure = {
                        indicator.isRefreshing = false
                        Toast.makeText(this, it.message ?: "Could not refresh conversations", Toast.LENGTH_LONG).show()
                    },
                )
            }
        }
    }

    private fun lastUsedText(timestamp: Long): String {
        val now = System.currentTimeMillis()
        val relative = if (abs(now - timestamp) < DateUtils.MINUTE_IN_MILLIS) {
            "just now"
        } else {
            DateUtils.getRelativeTimeSpanString(
                timestamp,
                now,
                DateUtils.MINUTE_IN_MILLIS,
                DateUtils.FORMAT_ABBREV_RELATIVE,
            )
        }
        return "Last used $relative"
    }

    private fun cardBackground() = GradientDrawable().apply {
        setColor(themeColor(android.R.attr.colorBackgroundFloating))
        setStroke(dp(1), withAlpha(themeColor(android.R.attr.colorAccent), 72))
        cornerRadius = dp(16).toFloat()
    }

    private fun withAlpha(color: Int, alpha: Int) = (color and 0x00ffffff) or (alpha.coerceIn(0, 255) shl 24)

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
		transcriptShown = false
		presentationShown = false
		renderedHistorySession = ""
        val root = column()
        val heading = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(TextView(this@MainActivity).apply {
                text = "‹"
                textSize = 38f
                gravity = Gravity.CENTER
                minWidth = dp(48)
                minHeight = dp(48)
                contentDescription = "Back to conversations"
                isClickable = true
                isFocusable = true
                val selectable = TypedValue()
                if (theme.resolveAttribute(android.R.attr.selectableItemBackgroundBorderless, selectable, true)) {
                    setBackgroundResource(selectable.resourceId)
                }
                setOnClickListener { leaveChat() }
            })
            addView(
                title(session.title.ifBlank { "Conversation" }).apply {
                    textSize = 23f
                    setTypeface(typeface, Typeface.BOLD)
                    maxLines = 1
                    ellipsize = TextUtils.TruncateAt.END
                },
                LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
                    marginStart = dp(10)
                },
            )
			pauseButton = Button(this@MainActivity).apply {
				text = "Pause"
				isAllCaps = false
				contentDescription = "Pause voice conversation"
				setOnClickListener {
					if (text == "Resume") requestCallStart() else controller.end()
				}
			}.also { addView(it) }
        }
        root.addView(heading, matchWrap())
        status = helper("Preparing conversation…").apply {
            setTextColor(themeColor(android.R.attr.colorAccent))
            background = GradientDrawable().apply {
                setColor(withAlpha(themeColor(android.R.attr.colorAccent), 30))
                cornerRadius = dp(18).toFloat()
            }
            setPadding(dp(14), dp(7), dp(14), dp(7))
        }
        root.addView(status, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
            gravity = Gravity.CENTER_HORIZONTAL
            topMargin = dp(10)
            bottomMargin = dp(8)
        })
        transcript = helper("").apply {
            visibility = View.GONE
            gravity = Gravity.CENTER
        }
        root.addView(transcript, spaced(bottom = 4))

		val modeActions = LinearLayout(this).apply {
			orientation = LinearLayout.HORIZONTAL
			gravity = Gravity.CENTER
			transcriptButton = Button(this@MainActivity).apply {
				text = "Show transcript"
				isAllCaps = false
				setOnClickListener {
					transcriptShown = !transcriptShown
					updateConversationMode(null)
				}
			}.also { addView(it) }
		}
		root.addView(modeActions, matchWrap())

		activePanel = LinearLayout(this).apply {
			orientation = LinearLayout.VERTICAL
			gravity = Gravity.CENTER
			addView(logo(), centeredSquare(88, bottom = 18))
			addView(title("Voice is active").apply { gravity = Gravity.CENTER }, matchWrap())
			addView(helper("Just speak — you can interrupt Koder at any time.").apply { gravity = Gravity.CENTER }, spaced(top = 7))
		}
		root.addView(activePanel, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))

		presentationFeed = LinearLayout(this).apply {
			orientation = LinearLayout.VERTICAL
			setPadding(0, dp(4), 0, dp(8))
		}
		presentationPanel = LinearLayout(this).apply {
			orientation = LinearLayout.VERTICAL
			visibility = View.GONE
			addView(LinearLayout(this@MainActivity).apply {
				orientation = LinearLayout.HORIZONTAL
				gravity = Gravity.CENTER_VERTICAL
				addView(title("Shown by Koder").apply { textSize = 22f }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
				addView(Button(this@MainActivity).apply {
					text = "Close"
					isAllCaps = false
					contentDescription = "Close presentation"
					setOnClickListener { presentationShown = false; updateConversationMode(null) }
				})
			}, matchWrap())
			addView(ScrollView(this@MainActivity).apply { addView(presentationFeed, matchWrap()) }, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
		}
		root.addView(presentationPanel, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))

        feed = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            feedPlaceholder = conversationPlaceholder().also { addView(it, matchWrap()) }
        }
        feedScroll = ScrollView(this).apply {
            isFillViewport = true
            addView(feed, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        }
        root.addView(feedScroll, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))

        val composer = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(8), dp(5), dp(5), dp(5))
            background = cardBackground()
            elevation = dp(2).toFloat()
        }
        typedMessage = EditText(this).apply {
            hint = "Message Koder"
            maxLines = 3
            minHeight = dp(48)
            background = null
            setPadding(dp(10), 0, dp(8), 0)
        }
        composer.addView(typedMessage, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        composer.addView(Button(this).apply {
            text = "➤"
            contentDescription = "Send message"
            minWidth = dp(56)
            isAllCaps = false
            backgroundTintList = ColorStateList.valueOf(themeColor(android.R.attr.colorAccent))
            setTextColor(themeColor(android.R.attr.colorBackground))
            setOnClickListener {
                val message = typedMessage?.text?.toString().orEmpty()
                if (message.isNotBlank()) {
                    controller.submit(message)
                    typedMessage?.text?.clear()
                }
            }
        })
		composerView = composer
        root.addView(composer, matchWrap())
        showContent(root)
        requestCallStart()
    }

	private fun updateConversationMode(stage: CallController.Stage?) {
		val active = when (stage) {
			null -> pauseButton?.text != "Resume"
			CallController.Stage.DISCONNECTED, CallController.Stage.ERROR -> false
			else -> true
		}
		pauseButton?.apply {
			text = if (active) "Pause" else "Resume"
			contentDescription = if (active) "Pause voice conversation" else "Resume voice conversation"
		}
		val surface = conversationSurface(active, transcriptShown, presentationShown)
		activePanel?.visibility = if (surface == ConversationSurface.ACTIVE) View.VISIBLE else View.GONE
		presentationPanel?.visibility = if (surface == ConversationSurface.PRESENTATION) View.VISIBLE else View.GONE
		feedScroll?.visibility = if (surface == ConversationSurface.TRANSCRIPT) View.VISIBLE else View.GONE
		composerView?.visibility = if (surface == ConversationSurface.TRANSCRIPT) View.VISIBLE else View.GONE
		transcriptButton?.apply {
			visibility = if (active) View.VISIBLE else View.GONE
			text = if (transcriptShown) "Hide transcript" else "Show transcript"
		}
		placeholderTitle?.text = if (active) "No transcript yet" else "Conversation paused"
		placeholderDetail?.text = if (active) {
			"Your conversation will appear here as you speak."
		} else {
			"Resume when you want to keep talking."
		}
	}

	private fun renderHistory(voiceSessionId: String, history: List<VoiceTranscriptEntry>) {
		renderedHistorySession = voiceSessionId
		if (history.isEmpty()) return
		feed?.removeAllViews()
		feedPlaceholder = null
		feed?.gravity = Gravity.NO_GRAVITY
		history.forEach { entry -> addBubble(if (entry.role == "user") "You" else "Koder", entry.text) }
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
		controller.start(settings.server, settings.token, session.id, settings.speechLanguages)
    }

    private fun addPart(part: VoicePart) {
        when {
            part.mimeType in DISPLAYABLE_TEXT_TYPES && part.text.isNotBlank() -> addBubble("Koder", part.text)
            part.mimeType.startsWith("image/") -> addImage(part)
            else -> addGenericPart(part)
        }
    }

	private fun addPresentationPart(part: VoicePart) {
		when {
			part.mimeType.startsWith("image/") -> addImage(part, presentationFeed)
			part.mimeType in DISPLAYABLE_TEXT_TYPES && part.text.isNotBlank() -> addInlinePresentation(part)
			else -> addGenericPart(part, presentationFeed)
		}
	}

	private fun addInlinePresentation(part: VoicePart) {
		val target = presentationFeed ?: return
		val shown = when (part.mimeType) {
			"text/markdown" -> renderPresentationMarkdown(part.text)
			else -> part.text
		}
		val card = card()
		val heading = part.title.ifBlank { part.name }.ifBlank { "Details" }
		card.addView(label(heading), matchWrap())
		card.addView(body(shown).apply {
			if (part.mimeType == "text/markdown") typeface = Typeface.MONOSPACE
			setTextIsSelectable(true)
			contentDescription = "Koder presentation"
		}, spaced(top = 8))
		target.addView(card, spaced(top = 6, bottom = 6))
	}

    private fun addImage(part: VoicePart, target: LinearLayout? = feed) {
		val target = target ?: return
		if (target === feed) removeFeedPlaceholder()
		val card = card()
		val title = helper(part.title.ifBlank { part.name }.ifBlank { part.alt.ifBlank { part.mimeType } })
		val image = ImageView(this).apply {
			adjustViewBounds = true
			scaleType = ImageView.ScaleType.CENTER_INSIDE
			minimumHeight = dp(120)
			contentDescription = part.alt
		}
		card.addView(title, matchWrap())
		card.addView(image, matchWrap())
		target.addView(card, spaced(top = 5, bottom = 5))
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

    private fun addGenericPart(part: VoicePart, target: LinearLayout? = feed) {
		val target = target ?: return
		if (target === feed) removeFeedPlaceholder()
		val text = buildString {
			append(part.title.ifBlank { part.name }.ifBlank { "Koder attachment" })
			append("\n")
			append(part.alt.ifBlank { part.mimeType })
			if (part.data != null) append("\n${formatGenericPresentationData(part)}")
			if (part.url.isNotBlank()) append("\nOpen ${part.url}")
		}
		val card = card().apply {
			addView(body(text), matchWrap())
			if (part.url.isNotBlank()) setOnClickListener { downloadAndOpen(part) }
		}
		target.addView(card, spaced(top = 5, bottom = 5))
        scrollToBottom()
    }

	private fun formatGenericPresentationData(part: VoicePart): String {
		val raw = part.data?.toString().orEmpty()
		if (part.mimeType != "application/json") return raw
		return runCatching {
			when (val parsed = JSONTokener(raw).nextValue()) {
				is org.json.JSONObject -> parsed.toString(2)
				is org.json.JSONArray -> parsed.toString(2)
				else -> parsed.toString()
			}
		}.getOrDefault(raw)
	}

	private fun renderPresentationMarkdown(markdown: String): String {
		return markdown.lineSequence().mapNotNull { raw ->
			var line = raw.trimEnd()
			if (line.trim().matches(Regex("\\|?\\s*:?-{3,}:?\\s*(\\|\\s*:?-{3,}:?\\s*)+\\|?"))) return@mapNotNull null
			line = line.replace(Regex("^\\s{0,3}#{1,6}\\s+"), "")
			line = line.replace(Regex("^\\s*[-*+]\\s+"), "• ")
			if (line.count { it == '|' } >= 2) {
				line = line.trim().trim('|').split('|').joinToString("    ") { it.trim() }
			}
			line.replace("**", "").replace("__", "").replace("`", "")
		}.joinToString("\n").trim()
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
        removeFeedPlaceholder()
        val fromUser = who == "You"
        val bubble = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(14), dp(10), dp(14), dp(11))
            background = GradientDrawable().apply {
                setColor(
                    if (fromUser) withAlpha(themeColor(android.R.attr.colorAccent), 46)
                    else themeColor(android.R.attr.colorBackgroundFloating),
                )
                cornerRadius = dp(16).toFloat()
            }
            elevation = dp(1).toFloat()
            addView(helper(who).apply {
                setTextColor(themeColor(android.R.attr.colorAccent))
                alpha = 1f
                setTypeface(typeface, Typeface.BOLD)
            }, matchWrap())
            addView(body(text).apply { maxWidth = dp(310) }, spaced(top = 4))
        }
        feed.addView(bubble, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
            gravity = if (fromUser) Gravity.END else Gravity.START
            topMargin = dp(6)
            bottomMargin = dp(6)
        })
        scrollToBottom()
    }

    private fun conversationPlaceholder() = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        gravity = Gravity.CENTER_HORIZONTAL
        setPadding(dp(24), dp(28), dp(24), dp(28))
        addView(ImageView(this@MainActivity).apply {
            setImageResource(android.R.drawable.ic_btn_speak_now)
            imageTintList = ColorStateList.valueOf(themeColor(android.R.attr.colorAccent))
            setPadding(dp(18), dp(18), dp(18), dp(18))
            background = GradientDrawable().apply {
                shape = GradientDrawable.OVAL
                setColor(withAlpha(themeColor(android.R.attr.colorAccent), 32))
            }
            contentDescription = "Voice conversation is ready"
        }, centeredSquare(76, bottom = 18))
		placeholderTitle = title("No transcript yet").apply {
            textSize = 25f
            gravity = Gravity.CENTER
            setTypeface(typeface, Typeface.BOLD)
		}.also { addView(it, matchWrap()) }
		placeholderDetail = helper("Your conversation will appear here as you speak.").apply {
            gravity = Gravity.CENTER
		}.also { addView(it, spaced(top = 7)) }
    }

    private fun removeFeedPlaceholder() {
        feedPlaceholder?.let { feed?.removeView(it) }
        feedPlaceholder = null
        feed?.gravity = Gravity.NO_GRAVITY
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
        updateProgress = null
        status = null
        transcript = null
        feed = null
        feedScroll = null
        feedPlaceholder = null
        typedMessage = null
		activePanel = null
		presentationPanel = null
		presentationFeed = null
		speechAutomaticCheck = null
		composerView = null
		pauseButton = null
		transcriptButton = null
		placeholderTitle = null
		placeholderDetail = null
    }

    internal fun showUpdateStatus(next: AndroidAppUpdater.Status) = runOnUiThread {
        val button = updateButton ?: return@runOnUiThread
        val progress = updateProgress
        when (next) {
            AndroidAppUpdater.Status.Hidden -> {
                button.visibility = View.GONE
                progress?.visibility = View.GONE
            }
            is AndroidAppUpdater.Status.Available -> {
                button.text = "Update Koder · ${next.versionName}"
                button.isEnabled = true
                button.visibility = View.VISIBLE
                progress?.visibility = View.GONE
            }
            is AndroidAppUpdater.Status.Downloading -> {
                val percent = ((next.downloadedBytes * 100) / next.totalBytes).toInt().coerceIn(0, 100)
                button.text = "Downloading ${next.versionName} · $percent%"
                button.isEnabled = false
                button.visibility = View.VISIBLE
                progress?.apply {
                    isIndeterminate = false
                    this.progress = percent
                    visibility = View.VISIBLE
                }
            }
            is AndroidAppUpdater.Status.Busy -> {
                button.text = next.message
                button.isEnabled = false
                button.visibility = View.VISIBLE
                progress?.visibility = View.GONE
            }
            is AndroidAppUpdater.Status.Error -> {
                button.text = "${next.message} · Retry"
                button.isEnabled = true
                button.visibility = View.VISIBLE
                progress?.visibility = View.GONE
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
        private val SERVER_TIMESTAMP = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss 'UTC'", Locale.US)
            .withZone(ZoneOffset.UTC)
    }
}
