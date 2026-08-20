package com.lkarlslund.koder

import android.Manifest
import android.animation.ValueAnimator
import android.annotation.SuppressLint
import android.app.AlertDialog
import android.content.ClipData
import android.content.ClipboardManager
import android.content.res.ColorStateList
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.provider.Settings as AndroidSettings
import android.text.TextUtils
import android.text.InputType
import android.text.Html
import android.text.format.DateUtils
import android.text.method.LinkMovementMethod
import android.text.method.PasswordTransformationMethod
import android.util.TypedValue
import android.view.Gravity
import android.view.HapticFeedbackConstants
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.widget.Button
import android.widget.ArrayAdapter
import android.widget.AdapterView
import android.widget.CheckBox
import android.widget.EditText
import android.widget.ImageView
import android.widget.ImageButton
import android.widget.LinearLayout
import android.widget.PopupMenu
import android.widget.ProgressBar
import android.widget.RadioButton
import android.widget.RadioGroup
import android.widget.ScrollView
import android.widget.SeekBar
import android.widget.Spinner
import android.widget.TextView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.activity.result.contract.ActivityResultContracts
import androidx.annotation.DrawableRes
import androidx.core.content.FileProvider
import androidx.core.app.NotificationManagerCompat
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import com.lkarlslund.koder.update.AndroidAppUpdater
import com.lkarlslund.koder.phone.PhoneCapabilities
import com.lkarlslund.koder.phone.PhoneCapability
import com.lkarlslund.koder.phone.permissionsForCurrentDevice
import com.lkarlslund.koder.phone.PhoneActionPolicy
import com.lkarlslund.koder.phone.actionTitle
import com.lkarlslund.koder.phone.PhoneBindingClient
import com.lkarlslund.koder.phone.PhoneIdentity
import com.lkarlslund.koder.phone.permissionAvailability
import com.lkarlslund.koder.presentation.ZoomableImageView
import com.lkarlslund.koder.voice.AppUpdate
import com.lkarlslund.koder.voice.AudioDiagnostics
import com.lkarlslund.koder.voice.CallController
import com.lkarlslund.koder.voice.BuiltInAudioRoute
import com.lkarlslund.koder.voice.ConversationAvailability
import com.lkarlslund.koder.voice.ConversationSurface
import com.lkarlslund.koder.voice.KODER_PRESENTATION_MIME
import com.lkarlslund.koder.voice.PresentationBlock
import com.lkarlslund.koder.voice.PresentationDocuments
import com.lkarlslund.koder.voice.ServerInfo
import com.lkarlslund.koder.voice.SpeechLanguage
import com.lkarlslund.koder.voice.SpeechLanguages
import com.lkarlslund.koder.voice.VOICE_PROTOCOL
import com.lkarlslund.koder.voice.VoiceHome
import com.lkarlslund.koder.voice.VoiceMessage
import com.lkarlslund.koder.voice.VoicePart
import com.lkarlslund.koder.voice.VoiceSession
import com.lkarlslund.koder.voice.VoiceSessionClient
import com.lkarlslund.koder.voice.VoiceChatCreateSpec
import com.lkarlslund.koder.voice.VoiceChatBackendOption
import com.lkarlslund.koder.voice.VoiceTranscriptEntry
import com.lkarlslund.koder.voice.VoiceTranscriptSearchResult
import com.lkarlslund.koder.voice.VoiceAudioEndpointType
import com.lkarlslund.koder.voice.VoiceAudioFormat
import com.lkarlslund.koder.voice.VoiceResponsePacing
import com.lkarlslund.koder.voice.VoiceProtocol
import com.lkarlslund.koder.voice.VoiceResultNotifier
import com.lkarlslund.koder.voice.VoiceReadinessCheck
import com.lkarlslund.koder.voice.AndroidVoiceHaptics
import com.lkarlslund.koder.voice.VoiceHapticCue
import com.lkarlslund.koder.voice.VoiceHaptics
import com.lkarlslund.koder.voice.VoiceStateOrbView
import com.lkarlslund.koder.voice.SavedVoiceResponse
import com.lkarlslund.koder.voice.SavedVoiceResponseKind
import com.lkarlslund.koder.voice.audioRouteChipText
import com.lkarlslund.koder.voice.conversationAvailability
import com.lkarlslund.koder.voice.conversationSurface
import com.lkarlslund.koder.voice.conversationStatusText
import com.lkarlslund.koder.voice.conversationTimeLabel
import com.lkarlslund.koder.voice.isNearConversationBottom
import com.lkarlslund.koder.voice.isVoiceChat
import com.lkarlslund.koder.voice.highContrastEnabled
import com.lkarlslund.koder.voice.latestConversationLabel
import com.lkarlslund.koder.voice.markdownToHtml
import com.lkarlslund.koder.voice.primaryVoiceControlLabel
import com.lkarlslund.koder.voice.shouldNotifyCompletedWork
import com.lkarlslund.koder.voice.voiceOrbMode
import com.lkarlslund.koder.voice.voiceOrbSizeDp
import java.io.File
import java.time.Duration
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import java.util.Locale
import kotlin.math.sin
import org.json.JSONTokener
import kotlin.math.abs

@SuppressLint("SetTextI18n")
class MainActivity : ComponentActivity(), CallController.Listener {
	private enum class Screen { SETUP, SETTINGS, PERMISSIONS, READINESS, LOADING, HOME, SESSION, CHAT }
	private enum class ConversationFilter { ACTIVE, FAVORITES, ARCHIVED }
	private data class PresentedImage(val bytes: ByteArray, val bitmap: Bitmap, val name: String, val mimeType: String, val title: String, val alt: String)

    private lateinit var controller: CallController
    private lateinit var secureSettings: SecureSettings
    private lateinit var appUpdater: AndroidAppUpdater
	private lateinit var phoneIdentity: PhoneIdentity
	private lateinit var bindingClient: PhoneBindingClient
	private lateinit var sessionClient: VoiceSessionClient
	private lateinit var voiceHaptics: VoiceHaptics
	private var readinessCheck: VoiceReadinessCheck? = null
	private var readinessHome: VoiceHome? = null
	private var readinessReturnToSettings = false
	private var readinessStep = 0

    private var screen = Screen.LOADING
    private var settings = SecureSettings.Values("", "")
    private var requestGeneration = 0L
    private var pendingSession: VoiceSession? = null
	private var selectedKoderSession: VoiceSession? = null
	private var currentSessionChats: List<VoiceSession> = emptyList()
	private var lastRecoveryError = ""
	private var lastTurnErrorId = ""
	private var startAfterModelRecovery = false
    private var pendingStart = false
    private var pendingPhoneCapability: PhoneCapability? = null
	private var pendingPhoneAction = ""
	private var pendingPhonePolicy = PhoneActionPolicy.OFF
    private var lastAppUpdate: AppUpdate? = null
	private var latestUpdateStatus: AndroidAppUpdater.Status = AndroidAppUpdater.Status.Hidden
	private var resumedOnce = false
	private var updateCheckGeneration = 0L
	private var conversationFilter = ConversationFilter.ACTIVE

	private var updateIndicator: TextView? = null
	private var updateDialog: AlertDialog? = null
	private var updateDialogDetail: TextView? = null
	private var updateDialogProgress: ProgressBar? = null
    private var status: TextView? = null
    private var transcript: TextView? = null
    private var feed: LinearLayout? = null
    private var feedScroll: ScrollView? = null
    private var feedPlaceholder: View? = null
    private var typedMessage: EditText? = null
	private var activePanel: View? = null
	private var voiceOrb: VoiceStateOrbView? = null
	private var voiceOrbDetail: TextView? = null
	private var presentationPanel: View? = null
	private var presentationFeed: LinearLayout? = null
	private var presentationShown = false
	private var speechAutomaticCheck: CheckBox? = null
	private val phoneActionGroups = mutableMapOf<String, RadioGroup>()
	private var composerView: View? = null
	private var pauseButton: ImageButton? = null
	private var transcriptButton: ImageButton? = null
	private var muteButton: ImageButton? = null
	private var audioButton: TextView? = null
	private var searchButton: ImageButton? = null
	private var savedButton: ImageButton? = null
	private var latestCallSnapshot = CallController.Snapshot()
	private var transcriptShown = false
	private var transcriptOpened = false
	private var followConversationBottom = true
	private var unreadConversationMessages = 0
	private var latestButton: Button? = null
	private var renderedHistorySession = ""
	private val renderedHistoryIDs = linkedSetOf<String>()
	private val renderedPartKeys = linkedSetOf<String>()
	private var cachedConversationHistory = emptyList<VoiceTranscriptEntry>()
	private var searchContextShown = false
	private var savedResponses: List<SavedVoiceResponse> = emptyList()
	private var placeholderTitle: TextView? = null
	private var placeholderDetail: TextView? = null
	private var pendingImageSave: PresentedImage? = null
	private var appVisible = false
	private var delegatedWorkPending = false
	private var pendingResultSessionId = ""
	private var pendingResultOwnerSessionId = ""
	private var pendingResultTranscriptId = ""
    private val permissionLauncher = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
        if (!pendingStart) return@registerForActivityResult
        pendingStart = false
        if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) {
            startCall()
        } else {
			updateConversationStatus(CallController.Stage.ERROR, "Microphone permission is required for voice conversations")
			updateConversationMode(CallController.Stage.ERROR)
        }
    }
	private val readinessPermissionLauncher = registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
		if (screen != Screen.READINESS) return@registerForActivityResult
		if (granted) startReadinessCheck() else {
			voiceHaptics.play(VoiceHapticCue.FAILURE)
			showReadiness(readinessHome ?: return@registerForActivityResult, "Microphone permission is required to test voice.")
		}
	}
    private val phonePermissionLauncher = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
        val capability = pendingPhoneCapability ?: return@registerForActivityResult
        pendingPhoneCapability = null
        val granted = phoneCapabilityAvailable(capability)
        if (granted) applyPhoneActionPolicy(pendingPhoneAction, pendingPhonePolicy) else {
            Toast.makeText(this, "${capability.title} permission was not granted", Toast.LENGTH_LONG).show()
			selectPhoneActionPolicy(pendingPhoneAction, PhoneActionPolicy.OFF)
        }
    }
    private val notificationAccessLauncher = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        val capability = pendingPhoneCapability ?: return@registerForActivityResult
        pendingPhoneCapability = null
	        if (notificationAccessGranted()) applyPhoneActionPolicy(pendingPhoneAction, pendingPhonePolicy)
		else selectPhoneActionPolicy(pendingPhoneAction, PhoneActionPolicy.OFF)
    }
	private val imageSaveLauncher = registerForActivityResult(ActivityResultContracts.CreateDocument("image/*")) { uri ->
		val image = pendingImageSave
		pendingImageSave = null
		if (uri == null || image == null) return@registerForActivityResult
		runCatching { contentResolver.openOutputStream(uri)?.use { it.write(image.bytes) } ?: error("Could not open destination") }
			.onSuccess { Toast.makeText(this, "Saved ${image.name}", Toast.LENGTH_SHORT).show() }
			.onFailure { Toast.makeText(this, it.message ?: "Could not save image", Toast.LENGTH_LONG).show() }
	}

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        secureSettings = SecureSettings(this)
		phoneIdentity = PhoneIdentity.from(this)
		bindingClient = PhoneBindingClient(phoneIdentity)
		sessionClient = VoiceSessionClient(identity = phoneIdentity)
		voiceHaptics = AndroidVoiceHaptics(this)
		controller = CallController(
			this,
			this,
			phoneIdentity = phoneIdentity,
			onBuiltInAudioRouteSelected = ::rememberBuiltInAudioRoute,
		)
        appUpdater = AndroidAppUpdater(this, ::showUpdateStatus)
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (screen == Screen.CHAT) {
                    leaveChat()
				} else if (screen == Screen.SESSION) {
					loadHome()
                } else if (screen == Screen.SETTINGS) {
                    loadHome()
				} else if (screen == Screen.PERMISSIONS) {
					loadHome()
				} else if (screen == Screen.READINESS) {
					readinessCheck?.close()
					if (readinessReturnToSettings) showSettings() else readinessHome?.let(::showHome) ?: loadHome()
                } else if (screen == Screen.SETUP && settings.server.isNotBlank()) {
                    showSettings()
                } else {
                    isEnabled = false
                    onBackPressedDispatcher.onBackPressed()
                }
            }
        })
        settings = secureSettings.load()
		rememberResultIntent(intent)
		if (!bindFromIntent(intent)) {
			if (settings.server.isBlank()) showSetup() else loadHome()
		}
    }

	override fun onNewIntent(intent: Intent) {
		super.onNewIntent(intent)
		handleVoiceResultIntent(intent)
	}

	private fun handleVoiceResultIntent(intent: Intent) {
		setIntent(intent)
		if (!bindFromIntent(intent) && rememberResultIntent(intent)) openPendingResult()
	}

	override fun onStart() {
		super.onStart()
		appVisible = true
	}

	override fun onResume() {
		super.onResume()
		if (resumedOnce) refreshForegroundState() else resumedOnce = true
	}

	override fun onStop() {
		appVisible = false
		super.onStop()
	}

    override fun onDestroy() {
        requestGeneration++
		updateCheckGeneration++
		readinessCheck?.close()
        sessionClient.close()
		bindingClient.close()
        appUpdater.close()
		updateDialog?.dismiss()
		updateDialog = null
        controller.close()
        super.onDestroy()
    }

	private fun bindFromIntent(intent: Intent?): Boolean {
		val uri = intent?.data ?: return false
		if (uri.scheme != "koder" || uri.host != "bind") return false
		showBindingProgress()
		bindingClient.bind(uri) { result ->
			runOnUiThread {
				if (isFinishing || isDestroyed) return@runOnUiThread
				result.fold(
					onSuccess = { binding ->
						secureSettings.save(binding.server, binding.token)
						settings = secureSettings.load()
						loadHome("Phone connected · loading conversations…", offerReadiness = true)
					},
					onFailure = { failure -> showSetup("Couldn’t bind this phone: ${failure.message ?: "unknown error"}") },
				)
			}
		}
		return true
	}

	private fun rememberResultIntent(intent: Intent?): Boolean {
		val sessionId = intent?.getStringExtra(VoiceResultNotifier.EXTRA_VOICE_SESSION_ID).orEmpty()
		if (sessionId.isBlank()) return false
		pendingResultSessionId = sessionId
		pendingResultOwnerSessionId = intent?.getStringExtra(VoiceResultNotifier.EXTRA_SESSION_ID).orEmpty()
		pendingResultTranscriptId = intent?.getStringExtra(VoiceResultNotifier.EXTRA_TRANSCRIPT_ID).orEmpty()
		VoiceResultNotifier.cancel(this, pendingResultSessionId, pendingResultTranscriptId)
		return true
	}

	private fun openPendingResult() {
		if (pendingResultSessionId.isBlank()) return
		if (screen == Screen.CHAT && pendingSession?.id == pendingResultSessionId) {
			focusPendingResult()
		} else if (settings.server.isNotBlank()) {
			loadHome("Opening completed work…")
		}
	}

	private fun showBindingProgress() {
		screen = Screen.LOADING
		clearCallViews()
		showContent(column(Gravity.CENTER_HORIZONTAL).apply {
			gravity = Gravity.CENTER
			addView(logo(), centeredSquare(88, bottom = 22))
			addView(ProgressBar(this@MainActivity))
			addView(title("Connecting this phone").apply { gravity = Gravity.CENTER }, spaced(top = 18))
			addView(body("Creating a private device registration in Koder…").apply { gravity = Gravity.CENTER }, spaced(top = 8))
		})
	}

	override fun onSnapshot(snapshot: CallController.Snapshot) {
		if (snapshot.stage == CallController.Stage.WORKING) delegatedWorkPending = true
		if (snapshot.stage == CallController.Stage.RECORDING) delegatedWorkPending = false
		runOnUiThread {
			latestCallSnapshot = snapshot
			val recoveryErrorKey = snapshot.turnErrorId.ifBlank { snapshot.detail }
			if (snapshot.errorCode == "model_unavailable" && recoveryErrorKey != lastRecoveryError) {
				lastRecoveryError = recoveryErrorKey
				pendingSession?.let(::showChatModelDialog)
			}
			if (snapshot.turnErrorId.isNotBlank() && snapshot.turnErrorId != lastTurnErrorId) {
				lastTurnErrorId = snapshot.turnErrorId
				Toast.makeText(this, snapshot.detail.ifBlank { "Voice request failed" }, Toast.LENGTH_LONG).show()
			}
			if (screen != Screen.CHAT) return@runOnUiThread
			voiceOrb?.mode = voiceOrbMode(snapshot.stage)
			voiceOrbDetail?.apply {
				text = snapshot.detail
				contentDescription = snapshot.detail
			}
			snapshot.voiceSessions.firstOrNull { it.id == snapshot.voiceSessionId }?.let {
				secureSettings.markVoiceSessionRead(it.id, it.resultCount)
			}
			updateConversationStatus(snapshot.stage, snapshot.detail)
            transcript?.apply {
                text = snapshot.partialTranscript
				visibility = if (transcriptShown && snapshot.partialTranscript.isNotBlank()) View.VISIBLE else View.GONE
            }
			if (snapshot.voiceSessionId.isNotBlank() && snapshot.history.isNotEmpty() && renderedHistorySession != snapshot.voiceSessionId) {
				cachedConversationHistory = snapshot.history
				renderHistory(snapshot.voiceSessionId, snapshot.history)
			} else if (snapshot.history.isNotEmpty()) {
				cachedConversationHistory = snapshot.history
			}
			updateConversationMode(snapshot.stage)
			muteButton?.apply {
				contentDescription = if (snapshot.microphoneMuted) "Unmute microphone" else "Mute microphone"
				setImageResource(if (snapshot.microphoneMuted) R.drawable.ic_voice_mic_off else R.drawable.ic_voice_mic)
				setActionAppearance(this, if (snapshot.microphoneMuted) ACTION_RED else ACTION_BLUE, snapshot.microphoneMuted)
				ViewCompat.setTooltipText(this, contentDescription)
			}
			audioButton?.apply {
				isEnabled = snapshot.audioEndpoints.isNotEmpty()
				text = audioRouteChipText(snapshot.audioEndpointName)
				contentDescription = "Audio route: ${snapshot.audioEndpointName.ifBlank { "loading" }}. Tap to switch"
				setAudioRouteAppearance(this)
				ViewCompat.setTooltipText(this, contentDescription)
			}
            if (snapshot.appUpdate != null && snapshot.appUpdate != lastAppUpdate) {
                lastAppUpdate = snapshot.appUpdate
            }
        }
    }

    override fun onUserMessage(text: String) = addBubble("You", text)

	override fun onAssistantMessage(message: VoiceMessage) {
		val sessionId = pendingSession?.id.orEmpty().ifBlank { latestCallSnapshot.voiceSessionId }
		if (shouldNotifyCompletedWork(appVisible, delegatedWorkPending, sessionId)) {
			val title = pendingSession?.title.orEmpty().ifBlank {
				latestCallSnapshot.voiceSessions.firstOrNull { it.id == sessionId }?.title.orEmpty()
			}
			VoiceResultNotifier.show(this, latestCallSnapshot.activeSessionId, sessionId, title, message.transcriptId, message.spokenText)
		}
		delegatedWorkPending = false
		runOnUiThread {
            if (screen != Screen.CHAT) return@runOnUiThread
			val parts = message.parts.ifEmpty {
                listOf(VoicePart(mimeType = "text/plain", data = message.spokenText))
			}
			val (visualParts, transcriptParts) = parts.partition {
				!it.isTranscriptOnly && (it.isPresentation || it.uri.isNotBlank() || it.mimeType !in DISPLAYABLE_TEXT_TYPES)
			}
			if (visualParts.isNotEmpty()) {
				addPresentationTranscriptLink(visualParts)
				presentationFeed?.removeAllViews()
				visualParts.filter(::rememberRenderPart).forEach(::addPresentationPart)
				presentationShown = true
				transcriptShown = false
			}
			transcriptParts.filter(::rememberRenderPart).forEach { addPart(it, message.transcriptId) }
			updateConversationMode(null)
			focusPendingResult()
        }
    }

	override fun onHistoryPage(entries: List<VoiceTranscriptEntry>) {
		runOnUiThread {
			prependHistory(entries)
			focusPendingResult()
		}
	}

	override fun onRender(parts: List<VoicePart>) {
		runOnUiThread {
			if (screen != Screen.CHAT) return@runOnUiThread
			val fresh = parts.filter(::rememberRenderPart)
			val visual = fresh.filterNot(VoicePart::isTranscriptOnly)
			val transcriptOnly = fresh.filter(VoicePart::isTranscriptOnly)
			transcriptOnly.forEach { addPart(it) }
			if (visual.isNotEmpty()) {
				addPresentationTranscriptLink(visual)
				presentationFeed?.removeAllViews()
				visual.forEach(::addPresentationPart)
				presentationShown = true
				transcriptShown = false
				updateConversationMode(latestCallSnapshot.stage)
			}
		}
	}

	override fun onAudioLevel(level: Float, user: Boolean) {
		runOnUiThread {
			if (screen != Screen.CHAT) return@runOnUiThread
			val expected = if (user) CallController.Stage.RECORDING else CallController.Stage.SPEAKING
			if (latestCallSnapshot.stage == expected) voiceOrb?.setAudioLevel(level)
		}
	}

	override fun onAudioWaveform(samples: FloatArray, user: Boolean) {
		runOnUiThread {
			if (screen != Screen.CHAT) return@runOnUiThread
			val expected = if (user) CallController.Stage.RECORDING else CallController.Stage.SPEAKING
			if (latestCallSnapshot.stage == expected) voiceOrb?.setAudioWaveform(samples)
		}
	}

	override fun onHistorySearch(results: List<VoiceTranscriptSearchResult>, error: String?) {
		runOnUiThread {
			if (screen != Screen.CHAT) return@runOnUiThread
			if (error != null) {
				Toast.makeText(this, error, Toast.LENGTH_LONG).show()
				return@runOnUiThread
			}
			showTranscriptSearchResults(results)
		}
	}

    private fun showSetup(error: String = "") {
        screen = Screen.SETUP
        clearCallViews()
        val content = column()
        content.addView(logo(), centeredSquare(88, bottom = 18))
        content.addView(title("Welcome to Koder Voice"), matchWrap())
		content.addView(body("Open Koder on your computer, tap the phone icon, and scan its Bind phone QR code with this phone's camera. Koder Voice will open and connect automatically."), spaced(bottom = 12))
		content.addView(helper("You can also enter an existing server credential manually below."), spaced(bottom = 24))

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
				loadHome(offerReadiness = true)
            }
        }, matchWrap())
        showScrollable(content)
    }

    private fun loadHome(message: String = "Connecting to Koder…", offerReadiness: Boolean = false) {
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
				result.fold(
					onSuccess = { home ->
						if (offerReadiness && !secureSettings.readinessComplete(settings.server)) showReadiness(home, fromSettings = false, step = 0) else showHome(home)
					},
					onFailure = ::showConnectionError,
				)
            }
        }
    }

	private fun showReadiness(home: VoiceHome, error: String = "", fromSettings: Boolean = readinessReturnToSettings, step: Int = readinessStep) {
		screen = Screen.READINESS
		readinessHome = home
		readinessReturnToSettings = fromSettings
		readinessStep = step.coerceIn(0, 3)
		readinessCheck?.close()
		readinessCheck = null
		clearCallViews()
		val content = column(Gravity.CENTER_HORIZONTAL)
		content.addView(logo(), centeredSquare(82, bottom = 14))
		content.addView(title("Voice adjustments").apply { gravity = Gravity.CENTER }, matchWrap())
		content.addView(helper("Step ${readinessStep + 1} of 4").apply { gravity = Gravity.CENTER }, spaced(top = 5, bottom = 8))
		content.addView(body(if (fromSettings) "Tune how Koder recognizes and answers you, then rerun the complete voice check." else "Let’s tune voice recognition and response style before your first conversation.").apply { gravity = Gravity.CENTER }, spaced(bottom = 22))
		if (readinessStep < 3) {
			addVoiceAdjustmentStep(content, readinessStep)
		} else {
			content.addView(title("4. Live voice check").apply { textSize = 24f }, spaced(bottom = 6))
			content.addView(body("This does not create a conversation. Say a sentence; Koder will recognize it and speak a confirmation over the current audio route."), spaced(bottom = 14))
			content.addView(readinessRow("✓", "Server and authentication", "Connected to Koder"), matchWrap())
			content.addView(readinessRow("○", "Microphone", "Waiting to test"), spaced(top = 7))
			content.addView(readinessRow("○", "Voice detection", "On-device Silero VAD"), spaced(top = 7))
			content.addView(readinessRow("○", "Speech recognition", "Remote streaming service"), spaced(top = 7))
			content.addView(readinessRow("○", "Voice playback", "Current phone audio route"), spaced(top = 7, bottom = 16))
			if (error.isNotBlank()) content.addView(errorText(error), spaced(bottom = 12))
			content.addView(Button(this).apply {
				text = "Start voice check"
				isAllCaps = false
				contentDescription = "Start voice readiness check"
				setOnClickListener {
					if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) startReadinessCheck()
					else readinessPermissionLauncher.launch(Manifest.permission.RECORD_AUDIO)
				}
			}, matchWrap())
		}
		content.addView(LinearLayout(this).apply {
			orientation = LinearLayout.HORIZONTAL
			if (readinessStep > 0) addView(Button(this@MainActivity).apply {
				text = "Previous"
				contentDescription = "Previous voice adjustment step"
				isAllCaps = false
				setOnClickListener { showReadiness(home, fromSettings = fromSettings, step = readinessStep - 1) }
			}, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
			if (readinessStep < 3) addView(Button(this@MainActivity).apply {
				text = "Continue"
				contentDescription = "Continue voice adjustments"
				isAllCaps = false
				setOnClickListener { showReadiness(home, fromSettings = fromSettings, step = readinessStep + 1) }
			}, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply { if (readinessStep > 0) marginStart = dp(8) })
		}, spaced(top = 12))
		content.addView(Button(this).apply {
			text = if (fromSettings) "Back to settings" else "Do this later"
			contentDescription = if (fromSettings) "Exit voice adjustments to settings" else "Skip voice adjustments for now"
			isAllCaps = false
			setOnClickListener { if (fromSettings) showSettings() else showHome(home) }
		}, spaced(top = 7))
		showScrollable(content)
	}

	private fun startReadinessCheck() {
		val home = readinessHome ?: return
		readinessCheck?.close()
		val rows = linkedMapOf<VoiceReadinessCheck.Step, TextView>()
		val content = column(Gravity.CENTER_HORIZONTAL)
		content.addView(logo(), centeredSquare(72, bottom = 12))
		content.addView(title("Say a short sentence").apply { gravity = Gravity.CENTER }, matchWrap())
		content.addView(body("Speak naturally, then pause. This checks the complete voice path without creating a conversation.").apply { gravity = Gravity.CENTER }, spaced(top = 8, bottom = 18))
		VoiceReadinessCheck.Step.entries.forEach { step ->
			val title = when (step) {
				VoiceReadinessCheck.Step.SERVER -> "Server and authentication"
				VoiceReadinessCheck.Step.MICROPHONE -> "Microphone"
				VoiceReadinessCheck.Step.VAD -> "Voice detection"
				VoiceReadinessCheck.Step.STT -> "Speech recognition"
				VoiceReadinessCheck.Step.PLAYBACK -> "Voice playback"
			}
			val row = readinessRow("○", title, "Waiting")
			rows[step] = row
			content.addView(row, spaced(bottom = 7))
		}
		val result = body("Listening for you…").apply { gravity = Gravity.CENTER }
		content.addView(result, spaced(top = 12, bottom = 12))
		content.addView(Button(this).apply {
			text = "Cancel"
			isAllCaps = false
			setOnClickListener { showReadiness(home) }
		}, matchWrap())
		showScrollable(content)
		readinessCheck = VoiceReadinessCheck(this, phoneIdentity, object : VoiceReadinessCheck.Listener {
			override fun onProgress(step: VoiceReadinessCheck.Step, detail: String) = runOnUiThread {
				if (screen != Screen.READINESS) return@runOnUiThread
				rows[step]?.apply {
					val heading = tag?.toString().orEmpty()
					text = "✓  $heading\n$detail"
					contentDescription = "$heading. Complete. $detail"
				}
				result.text = detail
			}

			override fun onComplete(transcript: String) = runOnUiThread {
				if (screen != Screen.READINESS) return@runOnUiThread
				secureSettings.markReadinessComplete(settings.server)
				voiceHaptics.play(VoiceHapticCue.SUCCESS)
				readinessCheck?.close()
				readinessCheck = null
				showReadinessSuccess(home, transcript)
			}

			override fun onFailure(message: String) = runOnUiThread {
				if (screen == Screen.READINESS) {
					voiceHaptics.play(VoiceHapticCue.FAILURE)
					showReadiness(home, message, readinessReturnToSettings)
				}
			}
		})
		readinessCheck?.start(settings.server, settings.token, settings.speechLanguages, settings.vadSensitivityPercent, settings.vadSilenceMilliseconds)
	}

	private fun showReadinessSuccess(home: VoiceHome, heard: String) {
		screen = Screen.READINESS
		val content = column(Gravity.CENTER_HORIZONTAL).apply { gravity = Gravity.CENTER }
		content.addView(logo(), centeredSquare(88, bottom = 16))
		content.addView(title("Voice is ready").apply { gravity = Gravity.CENTER }, matchWrap())
		content.addView(body("Koder heard: “$heard”\n\nMicrophone, voice detection, speech recognition, and playback all worked.").apply { gravity = Gravity.CENTER }, spaced(top = 10, bottom = 20))
		content.addView(Button(this).apply {
			text = if (readinessReturnToSettings) "Back to settings" else "Continue to conversations"
			isAllCaps = false
			setOnClickListener { if (readinessReturnToSettings) showSettings() else showHome(home) }
		}, matchWrap())
		showContent(content)
	}

	private fun readinessRow(mark: String, heading: String, detail: String) = TextView(this).apply {
		text = "$mark  $heading\n$detail"
		tag = heading
		contentDescription = "$heading. $detail"
		textSize = 16f
		setPadding(dp(16), dp(12), dp(16), dp(12))
		background = GradientDrawable().apply {
			cornerRadius = dp(15).toFloat()
			setColor(withAlpha(themeColor(android.R.attr.colorAccent), 20))
		}
	}

    private fun showConnectionError(failure: Throwable) {
		voiceHaptics.play(VoiceHapticCue.FAILURE)
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
		readinessHome = home
		if (pendingResultSessionId.isNotBlank()) {
			if (pendingResultOwnerSessionId.isNotBlank()) {
				home.sessions.firstOrNull { it.id == pendingResultOwnerSessionId }?.let {
					loadSession(it)
					return
				}
			}
			home.voiceSessions.firstOrNull { it.id == pendingResultSessionId }?.let {
				openChat(it)
				return
			}
			pendingResultSessionId = ""
			pendingResultOwnerSessionId = ""
			pendingResultTranscriptId = ""
		}
        screen = Screen.HOME
        selectedKoderSession = null
        clearCallViews()
        val root = column()

        val appBar = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(logo(), LinearLayout.LayoutParams(dp(44), dp(44)).apply { marginEnd = dp(12) })
            addView(title("Koder Voice").apply { textSize = 24f }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
			addView(updateAction(), LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(38)).apply { marginEnd = dp(4) })
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
		root.addView(appBar, spaced(bottom = 6))
		val availableSessions = home.sessions.ifEmpty { home.voiceSessions }
		val counts = mapOf(
			ConversationFilter.ACTIVE to availableSessions.count { !it.archived },
			ConversationFilter.FAVORITES to availableSessions.count { it.favorite && !it.archived },
			ConversationFilter.ARCHIVED to availableSessions.count { it.archived },
		)
		root.addView(LinearLayout(this).apply {
			orientation = LinearLayout.HORIZONTAL
			ConversationFilter.entries.forEach { filter ->
				val label = when (filter) {
					ConversationFilter.ACTIVE -> "Active"
					ConversationFilter.FAVORITES -> "Starred"
					ConversationFilter.ARCHIVED -> "Archived"
				}
				addView(TextView(this@MainActivity).apply {
					text = "$label ${counts[filter]}"
					gravity = Gravity.CENTER
					textSize = 13f
					setTypeface(typeface, if (conversationFilter == filter) Typeface.BOLD else Typeface.NORMAL)
					setPadding(dp(5), dp(9), dp(5), dp(9))
					minHeight = dp(48)
					contentDescription = "$label conversations, ${counts[filter]}"
					isClickable = true
					isFocusable = true
					background = GradientDrawable().apply {
						cornerRadius = dp(13).toFloat()
						setColor(if (conversationFilter == filter) withAlpha(themeColor(android.R.attr.colorAccent), 42) else 0x00000000)
					}
					setOnClickListener {
						conversationFilter = filter
						showHome(home)
					}
				}, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
			}
		}, spaced(bottom = 6))
		val visibleSessions = availableSessions.filter { session ->
			when (conversationFilter) {
				ConversationFilter.ACTIVE -> !session.archived
				ConversationFilter.FAVORITES -> session.favorite && !session.archived
				ConversationFilter.ARCHIVED -> session.archived
			}
		}
        val list = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
		if (visibleSessions.isEmpty()) {
			list.addView(emptyConversationCard(conversationFilter), spaced(top = 8, bottom = 12))
        } else {
			visibleSessions.forEach { session -> list.addView(koderSessionCard(session), spaced(bottom = 4)) }
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
			text = "+  New temporary conversation"
            isAllCaps = false
            contentDescription = "Create a new voice conversation"
            backgroundTintList = ColorStateList.valueOf(themeColor(android.R.attr.colorAccent))
            setTextColor(themeColor(android.R.attr.colorBackground))
			setOnClickListener { showCreateTemporaryConversationDialog() }
		}, spaced(top = 8))
        showContent(root)

        lastAppUpdate = home.appUpdate
        appUpdater.consider(home.appUpdate, settings.server, settings.token)
    }

    private fun showHomeMenu(anchor: View) {
        PopupMenu(this, anchor).apply {
            menu.add("Server info")
			menu.add("Audio diagnostics")
			menu.add("Permission health")
            menu.add("Settings")
            menu.add("About")
            setOnMenuItemClickListener { item ->
                when (item.title.toString()) {
					"Server info" -> loadServerInfo()
					"Audio diagnostics" -> showAudioDiagnostics()
					"Permission health" -> showPermissionHealth()
                    "Settings" -> showSettings()
                    "About" -> showAbout()
                }
                true
            }
            show()
        }
    }

	private fun showPermissionHealth() {
		screen = Screen.PERMISSIONS
		settings = secureSettings.load()
		clearCallViews()
		val uses = secureSettings.phoneActionUses()
		val content = column()
		content.addView(LinearLayout(this).apply {
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
				setOnClickListener { loadHome() }
			})
			addView(title("Permission health"), LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
		}, matchWrap())
		content.addView(body("This is the effective access Koder has through this phone. A tool must be switched on and have any required Android access before it is offered to the voice conversation."), spaced(top = 12, bottom = 16))
		content.addView(Button(this).apply {
			text = "Manage phone tools"
			isAllCaps = false
			contentDescription = "Manage phone tool permissions"
			setOnClickListener { showSettings() }
		}, spaced(bottom = 16))
		PhoneCapabilities.all.forEach { capability ->
			val androidPermissions = capability.permissionsForCurrentDevice()
			val policies = capability.actions.associateWith { settings.phoneActionPolicies[it] ?: PhoneActionPolicy.OFF }
			val enabled = policies.values.any { it != PhoneActionPolicy.OFF }
			val granted = phoneCapabilityAvailable(capability)
			val requiresAndroidAccess = capability.notificationAccess || androidPermissions.isNotEmpty()
			val availability = permissionAvailability(enabled, granted, requiresAndroidAccess, latestCallSnapshot.phoneToolsConnected)
			val state = availability.label
			val stateColor = when {
				availability.remotelyAvailable -> ACTION_GREEN
				enabled && !granted -> ACTION_RED
				else -> ACTION_NEUTRAL
			}
			val lastUse = capability.actions.mapNotNull(uses::get).maxOrNull()?.takeIf { it > 0 }
			val androidAccess = when {
				capability.notificationAccess -> "Android access: notification listener"
				androidPermissions.isNotEmpty() -> "Android access: ${androidPermissions.joinToString { it.substringAfterLast('.').lowercase().replace('_', ' ') }}"
				else -> "Android access: none required"
			}
			content.addView(card().apply {
				contentDescription = "Permission health for ${capability.title}: $state"
				addView(label(capability.title).apply { setTypeface(typeface, Typeface.BOLD) }, matchWrap())
				addView(body(state).apply { setTextColor(stateColor); setTypeface(typeface, Typeface.BOLD) }, spaced(top = 4))
				addView(helper(capability.description), spaced(top = 6))
				addView(helper(androidAccess), spaced(top = 5))
				val remote = policies.filterValues { it != PhoneActionPolicy.OFF }
					.entries.sortedBy { it.key }.joinToString { "${actionTitle(it.key)} (${it.value.label})" }
				addView(helper("Remote access: ${remote.ifBlank { "none" }}"), spaced(top = 3))
				addView(helper(lastUse?.let {
					"Last used ${DateUtils.getRelativeTimeSpanString(it, System.currentTimeMillis(), DateUtils.MINUTE_IN_MILLIS)}"
				} ?: "Never used on this phone"), spaced(top = 3))
			}, spaced(bottom = 8))
		}
		showScrollable(content)
	}

	private fun showAudioDiagnostics() {
		val content = column().apply { setPadding(dp(8), dp(4), dp(8), dp(4)) }
		val scroll = ScrollView(this).apply { addView(content, matchWrap()) }
		val dialog = AlertDialog.Builder(this)
			.setTitle("Audio diagnostics")
			.setView(scroll)
			.setNegativeButton("Close", null)
			.create()
		val refreshHandler = Handler(Looper.getMainLooper())
		val refresh = object : Runnable {
			override fun run() {
				if (!dialog.isShowing) return
				controller.refreshAudioDiagnostics()
				renderAudioDiagnostics(content, controller.audioDiagnostics())
				refreshHandler.postDelayed(this, 1_000)
			}
		}
		dialog.setOnDismissListener { refreshHandler.removeCallbacks(refresh) }
		dialog.setOnShowListener { refresh.run() }
		dialog.show()
	}

	private fun renderAudioDiagnostics(content: LinearLayout, diagnostics: AudioDiagnostics) {
		content.removeAllViews()
		content.addView(TextView(this).apply {
			text = if (diagnostics.active) "●  Live conversation audio" else "○  No active conversation"
			textSize = 18f
			setTypeface(typeface, Typeface.BOLD)
			setTextColor(themeColor(android.R.attr.colorAccent))
			background = GradientDrawable().apply {
				setColor(withAlpha(themeColor(android.R.attr.colorAccent), 28))
				cornerRadius = dp(14).toFloat()
			}
			setPadding(dp(14), dp(10), dp(14), dp(10))
			contentDescription = if (diagnostics.active) "Audio diagnostics live" else "Audio diagnostics inactive"
		}, spaced(bottom = 16))

		content.addDiagnosticsSection("Connection", listOf(
			"State" to latestCallSnapshot.stage.name.lowercase().replaceFirstChar(Char::uppercase),
			"Round trip" to (diagnostics.roundTripMillis?.let { "$it ms" } ?: "Measuring…"),
			"Reconnects" to diagnostics.reconnects.toString(),
		))
		content.addDiagnosticsSection("Microphone / VAD", listOf(
			"Level" to String.format(Locale.US, "%s  %.1f dBFS", audioLevelBar(diagnostics.microphoneLevelDbfs), diagnostics.microphoneLevelDbfs),
			"VAD" to "${diagnostics.vadState} · ${diagnostics.vadProbabilityPercent}%",
			"Route" to diagnostics.inputRoute,
			"Format" to formatAudio(diagnostics.inputFormat),
			"Frames" to String.format(Locale.US, "%,d captured", diagnostics.capturedFrames),
		))
		content.addDiagnosticsSection("Speech output", listOf(
			"Route" to diagnostics.outputRoute,
			"Format" to formatAudio(diagnostics.outputFormat),
			"Jitter" to String.format(Locale.US, "%.2f ms", diagnostics.outputJitterMillis),
			"Frames" to String.format(Locale.US, "%,d received", diagnostics.receivedFrames),
			"Dropped" to diagnostics.droppedOutputFrames.toString(),
			"Duplicates" to diagnostics.duplicateOutputFrames.toString(),
		), bottom = 0)
	}

	private fun formatAudio(format: VoiceAudioFormat?): String = format?.let {
		"${it.sampleRate} Hz · ${if (it.channels == 1) "mono" else "${it.channels} ch"} · ${it.encoding}"
	} ?: "Unavailable"

	private fun audioLevelBar(dbfs: Double): String {
		val lit = (((dbfs.coerceIn(-60.0, 0.0) + 60.0) / 60.0) * 12).toInt()
		return "▮".repeat(lit) + "▯".repeat(12 - lit)
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

		content.addView(title("Voice").apply { textSize = 24f }, matchWrap())
		content.addView(body("Adjust recognition languages, answer length, voice detection sensitivity, and end-of-speech timing. You can also rerun the microphone, STT, TTS, and playback check."), spaced(top = 6, bottom = 10))
		content.addView(Button(this).apply {
			text = "Voice adjustments & readiness"
			isAllCaps = false
			contentDescription = "Open voice adjustments and readiness check"
			setOnClickListener {
				val home = readinessHome
				if (home == null) Toast.makeText(this@MainActivity, "Conversations are still loading", Toast.LENGTH_SHORT).show()
				else showReadiness(home, fromSettings = true, step = 0)
			}
		}, spaced(bottom = 24))

        content.addView(title("Phone tools").apply { textSize = 24f }, matchWrap())
		content.addView(body("Choose a policy for every phone tool. Off hides it from Koder. Ask requests confirmation on this phone each time. On lets the active voice conversation use it without an extra Koder prompt; Android's own protected screens still apply."), spaced(top = 6, bottom = 14))
        PhoneCapabilities.all.forEach { capability -> content.addView(phoneCapabilityRow(capability), spaced(bottom = 10)) }
        content.addView(helper("SMS and call access is intended for this sideloaded personal build. Notification access exposes only notifications currently visible to Android; Koder cannot directly browse arbitrary email inboxes."), spaced(top = 5, bottom = 18))
		showScrollable(content)
    }

	private fun addVoiceAdjustmentStep(content: LinearLayout, step: Int) {
		if (step == 0) {
		content.addView(title("1. Recognition languages").apply { textSize = 24f }, matchWrap())
		content.addView(body("Automatic can recognize any language. Select one language for the strongest hint, or several to focus detection on languages you actually speak."), spaced(top = 6, bottom = 12))
		content.addView(speechAutomaticRow(), spaced(bottom = 8))
		SpeechLanguages.all.forEach { language -> content.addView(speechLanguageRow(language), spaced(bottom = 8)) }
		content.addView(helper("Changes apply when the next conversation connects and to the live check below."), spaced(top = 3, bottom = 24))
		return
		}

		if (step == 1) {
		content.addView(title("2. Spoken answer length").apply { textSize = 24f }, matchWrap())
		content.addView(body("Choose how much Koder normally says aloud. This changes voice delivery, not the durable conversation history."), spaced(top = 6, bottom = 10))
		content.addView(RadioGroup(this).apply {
			orientation = RadioGroup.VERTICAL
			VoiceResponsePacing.entries.forEach { pacing ->
				addView(RadioButton(this@MainActivity).apply {
					id = View.generateViewId()
					text = "${pacing.title}\n${pacing.description}"
					contentDescription = "Use ${pacing.title.lowercase()} spoken answer length. ${pacing.description}"
					isChecked = settings.responsePacing == pacing
					minHeight = dp(48)
					setOnClickListener {
						settings = settings.copy(responsePacing = pacing)
						secureSettings.saveResponsePacing(pacing)
					}
				}, matchWrap())
			}
		}, spaced(bottom = 24))
		return
		}

		content.addView(title("3. Voice detection").apply { textSize = 24f }, matchWrap())
		val sensitivityValue = helper("Sensitivity · ${settings.vadSensitivityPercent}%").apply { accessibilityLiveRegion = View.ACCESSIBILITY_LIVE_REGION_POLITE }
		content.addView(body("Increase this if your voice is missed. Reduce it when background noise starts conversations."), spaced(top = 6, bottom = 10))
		content.addView(sensitivityValue, matchWrap())
		content.addView(SeekBar(this).apply {
			max = 40
			progress = settings.vadSensitivityPercent - 35
			contentDescription = "Voice detection sensitivity, ${settings.vadSensitivityPercent} percent"
			setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
				override fun onProgressChanged(seekBar: SeekBar?, progress: Int, fromUser: Boolean) {
					val value = progress + 35
					sensitivityValue.text = "Sensitivity · $value%"
					seekBar?.contentDescription = "Voice detection sensitivity, $value percent"
					if (!fromUser) return
					settings = settings.copy(vadSensitivityPercent = value)
					secureSettings.saveVadSensitivity(value)
				}
				override fun onStartTrackingTouch(seekBar: SeekBar?) = Unit
				override fun onStopTrackingTouch(seekBar: SeekBar?) = Unit
			})
		}, spaced(height = dp(48), bottom = 12))
		val pauseValue = helper("End-of-speech pause · ${settings.vadSilenceMilliseconds} ms").apply { accessibilityLiveRegion = View.ACCESSIBILITY_LIVE_REGION_POLITE }
		content.addView(pauseValue, matchWrap())
		content.addView(SeekBar(this).apply {
			max = 18
			progress = (settings.vadSilenceMilliseconds - 300) / 50
			contentDescription = "End of speech pause, ${settings.vadSilenceMilliseconds} milliseconds"
			setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
				override fun onProgressChanged(seekBar: SeekBar?, progress: Int, fromUser: Boolean) {
					val value = 300 + progress * 50
					pauseValue.text = "End-of-speech pause · $value ms"
					seekBar?.contentDescription = "End of speech pause, $value milliseconds"
					if (!fromUser) return
					settings = settings.copy(vadSilenceMilliseconds = value)
					secureSettings.saveVadSilence(value)
				}
				override fun onStartTrackingTouch(seekBar: SeekBar?) = Unit
				override fun onStopTrackingTouch(seekBar: SeekBar?) = Unit
			})
		}, spaced(height = dp(48), bottom = 24))
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
		if (refresh) {
			if (screen == Screen.READINESS) readinessHome?.let { showReadiness(it, fromSettings = readinessReturnToSettings) }
			else showSettings()
		} else speechAutomaticCheck?.isChecked = languages.isEmpty()
	}

    private fun phoneCapabilityRow(capability: PhoneCapability) = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(16), dp(13), dp(12), dp(13))
        background = cardBackground()
		addView(body(capability.title).apply { setTypeface(typeface, Typeface.BOLD) }, matchWrap())
		addView(helper(capability.description), spaced(top = 3, bottom = 8))
		capability.actions.sorted().forEach { action ->
			addView(helper(actionTitle(action)).apply { setTypeface(typeface, Typeface.BOLD) }, spaced(top = 7))
			addView(RadioGroup(this@MainActivity).apply {
				orientation = RadioGroup.HORIZONTAL
				gravity = Gravity.END
				phoneActionGroups[action] = this
				PhoneActionPolicy.entries.forEach { policy ->
					addView(RadioButton(this@MainActivity).apply {
						id = View.generateViewId()
						tag = policy
						text = policy.label
						contentDescription = "${policy.label} ${actionTitle(action)}"
					})
				}
				selectPhoneActionPolicy(action, settings.phoneActionPolicies[action] ?: PhoneActionPolicy.OFF)
				setOnCheckedChangeListener { group, checkedID ->
					val policy = group.findViewById<RadioButton>(checkedID)?.tag as? PhoneActionPolicy ?: return@setOnCheckedChangeListener
					requestPhoneActionPolicy(capability, action, policy)
				}
			}, matchWrap())
		}
    }

	private fun requestPhoneActionPolicy(capability: PhoneCapability, action: String, policy: PhoneActionPolicy) {
		if (policy == PhoneActionPolicy.OFF) {
			applyPhoneActionPolicy(action, policy)
			return
		}
		pendingPhoneCapability = capability
		pendingPhoneAction = action
		pendingPhonePolicy = policy
		val permissions = capability.permissionsForCurrentDevice()
		when {
            capability.notificationAccess && !notificationAccessGranted() -> {
                notificationAccessLauncher.launch(Intent(AndroidSettings.ACTION_NOTIFICATION_LISTENER_SETTINGS))
            }
			permissions.any { checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED } ->
				phonePermissionLauncher.launch(permissions)
            else -> {
                pendingPhoneCapability = null
				applyPhoneActionPolicy(action, policy)
            }
        }
    }

	private fun applyPhoneActionPolicy(action: String, policy: PhoneActionPolicy) {
		secureSettings.savePhoneActionPolicy(action, policy)
		settings = secureSettings.load()
		selectPhoneActionPolicy(action, policy)
	}

	private fun selectPhoneActionPolicy(action: String, policy: PhoneActionPolicy) {
		val group = phoneActionGroups[action] ?: return
		val button = (0 until group.childCount).map(group::getChildAt)
			.filterIsInstance<RadioButton>().firstOrNull { it.tag == policy } ?: return
		if (group.checkedRadioButtonId != button.id) group.check(button.id)
	}

    private fun phoneCapabilityAvailable(capability: PhoneCapability) =
        (!capability.notificationAccess || notificationAccessGranted()) &&
            if (capability.id == "location") {
				capability.permissionsForCurrentDevice().any { checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED }
            } else {
				capability.permissionsForCurrentDevice().all { checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED }
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
				"Live devices" to if (info.voiceConnectionActive) {
					"${info.voiceConnectionCount} active${info.voiceConnectionSince?.let { since ->
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
			append("Voice connections: ${info.voiceConnectionCount} active devices")
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

	private fun koderSessionCard(session: VoiceSession) = LinearLayout(this).apply {
		orientation = LinearLayout.HORIZONTAL
		gravity = Gravity.CENTER_VERTICAL
		setPadding(dp(14), dp(12), dp(14), dp(12))
		background = cardBackground()
		isClickable = true
		isFocusable = true
		val titleText = session.title.ifBlank { "Untitled session" }
		val kind = when (session.kind) {
			"quick" -> "Temporary"
			"voice" -> "Voice"
			else -> "Session"
		}
		val counts = "${session.chatCount} chat${if (session.chatCount == 1) "" else "s"} · ${session.voiceChatCount} voice"
		val time = session.updatedAt?.let { compactLastUsedText(it.toEpochMilli()) }.orEmpty()
		addView(LinearLayout(this@MainActivity).apply {
			orientation = LinearLayout.VERTICAL
			addView(label(if (session.favorite) "★  $titleText" else titleText).apply {
				setTypeface(typeface, Typeface.BOLD)
				maxLines = 1
				ellipsize = TextUtils.TruncateAt.END
			}, matchWrap())
			addView(helper(listOf(kind, counts, time).filter(String::isNotBlank).joinToString(" · ")).apply {
				maxLines = 1
				ellipsize = TextUtils.TruncateAt.END
			}, spaced(top = 3))
		}, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
		addView(TextView(this@MainActivity).apply {
			text = "⋮"
			textSize = 24f
			gravity = Gravity.CENTER
			minWidth = dp(48)
			minHeight = dp(48)
			contentDescription = "Session options for $titleText"
			setOnClickListener { showKoderSessionMenu(it, session) }
		})
		contentDescription = "Open $titleText. $counts"
		setOnClickListener { loadSession(session) }
	}

	private fun showKoderSessionMenu(anchor: View, session: VoiceSession) {
		PopupMenu(this, anchor).apply {
			menu.add(if (session.favorite) "Remove star" else "Add star")
			menu.add("Rename")
			menu.add(if (session.archived) "Restore from archive" else "Archive")
			menu.add("Delete")
			setOnMenuItemClickListener { item ->
				when (item.title.toString()) {
					"Add star" -> updateKoderSession(session, favorite = true)
					"Remove star" -> updateKoderSession(session, favorite = false)
					"Rename" -> showRenameKoderSessionDialog(session)
					"Archive" -> showArchiveKoderSessionDialog(session)
					"Restore from archive" -> updateKoderSession(session, archived = false)
					"Delete" -> showDeleteKoderSessionDialog(session)
				}
				true
			}
			show()
		}
	}

	private fun updateKoderSession(session: VoiceSession, title: String? = null, archived: Boolean? = null, favorite: Boolean? = null) {
		sessionClient.update(settings.server, settings.token, session.id, title = title, archived = archived, favorite = favorite) { result ->
			runOnUiThread { result.fold(onSuccess = ::showHome, onFailure = ::showManagementError) }
		}
	}

	private fun showArchiveKoderSessionDialog(session: VoiceSession) {
		AlertDialog.Builder(this)
			.setTitle("Archive session?")
			.setMessage("${session.title.ifBlank { "This session" }} will move to Archived. You can restore it later.")
			.setNegativeButton("Cancel", null)
			.setPositiveButton("Archive") { _, _ -> updateKoderSession(session, archived = true) }
			.show()
	}

	private fun showRenameKoderSessionDialog(session: VoiceSession) {
		val field = EditText(this).apply { setText(session.title); setSingleLine(); selectAll() }
		AlertDialog.Builder(this)
			.setTitle("Rename session")
			.setView(field)
			.setNegativeButton("Cancel", null)
			.setPositiveButton("Rename") { _, _ -> updateKoderSession(session, title = field.text.toString()) }
			.show()
	}

	private fun showDeleteKoderSessionDialog(session: VoiceSession) {
		AlertDialog.Builder(this)
			.setTitle("Delete session?")
			.setMessage("${session.title.ifBlank { "This session" }} and all of its chats and history will be removed. This cannot be undone.")
			.setNegativeButton("Cancel", null)
			.setPositiveButton("Delete") { _, _ ->
				sessionClient.delete(settings.server, settings.token, session.id) { result ->
					runOnUiThread { result.fold(onSuccess = ::showHome, onFailure = ::showManagementError) }
				}
			}
			.show()
	}

	private fun loadSession(session: VoiceSession) {
		selectedKoderSession = session
		screen = Screen.LOADING
		showContent(column(Gravity.CENTER_HORIZONTAL).apply {
			gravity = Gravity.CENTER
			addView(logo(), centeredSquare(72, bottom = 18))
			addView(ProgressBar(this@MainActivity))
			addView(body("Loading chats…"), spaced(top = 12))
		})
		val generation = ++requestGeneration
		sessionClient.listChats(settings.server, settings.token, session.id) { result ->
			runOnUiThread {
				if (generation != requestGeneration || isFinishing || isDestroyed) return@runOnUiThread
				result.fold(
					onSuccess = { showSession(session, it) },
					onFailure = ::showConnectionError,
				)
			}
		}
	}

	private fun showSession(session: VoiceSession, home: VoiceHome) {
		currentSessionChats = home.chats
		if (pendingResultSessionId.isNotBlank() && pendingResultOwnerSessionId == session.id) {
			home.chats.firstOrNull { it.id == pendingResultSessionId && it.isVoiceChat }?.let {
				selectedKoderSession = session
				openChat(it)
				return
			}
		}
		selectedKoderSession = session
		screen = Screen.SESSION
		clearCallViews()
		val root = column()
		root.addView(LinearLayout(this).apply {
			orientation = LinearLayout.HORIZONTAL
			gravity = Gravity.CENTER_VERTICAL
			addView(iconActionButton(R.drawable.ic_voice_back, "Back to sessions", ACTION_NEUTRAL).apply {
				setOnClickListener { loadHome() }
			}, actionLayout())
			addView(title(session.title.ifBlank { "Session" }).apply {
				textSize = 23f
				maxLines = 1
				ellipsize = TextUtils.TruncateAt.END
			}, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply { marginStart = dp(10) })
			addView(TextView(this@MainActivity).apply {
				text = "⋮"
				textSize = 26f
				gravity = Gravity.CENTER
				minWidth = dp(48)
				minHeight = dp(48)
				contentDescription = "Session options"
				setOnClickListener { showKoderSessionMenu(it, session) }
			})
		}, spaced(bottom = 10))
		root.addView(helper("Voice conversations can use this session’s workspace, tools, and other chats."), spaced(bottom = 12))
		val list = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
		if (home.chats.isEmpty()) {
			list.addView(helper("This session has no chats."), spaced(top = 12, bottom = 12))
		} else {
			home.chats.forEach { chat -> list.addView(koderChatCard(session, chat), spaced(bottom = 5)) }
		}
		root.addView(ScrollView(this).apply { addView(list, matchWrap()) }, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
		root.addView(Button(this).apply {
			text = "+  New voice conversation"
			isAllCaps = false
			isEnabled = session.kind != "quick" && !session.archived
			contentDescription = when {
				session.archived -> "Restore this session before creating a conversation"
				isEnabled -> "Create a voice conversation in this session"
				else -> "Temporary sessions contain one chat"
			}
			setOnClickListener { showCreateVoiceSessionDialog() }
		}, spaced(top = 8))
		showContent(root)
	}

	private fun koderChatCard(session: VoiceSession, chat: VoiceSession) = LinearLayout(this).apply {
		orientation = LinearLayout.HORIZONTAL
		gravity = Gravity.CENTER_VERTICAL
		setPadding(dp(14), dp(11), dp(14), dp(11))
		background = cardBackground()
		val selectable = chat.isVoiceChat && !chat.archived && !session.archived
		alpha = if (selectable) 1f else 0.72f
		val titleText = chat.title.ifBlank { "Untitled chat" }
		val detail = listOf(chat.role.ifBlank { "chat" }.replaceFirstChar { it.titlecase() }, chat.statusText.ifBlank { chat.status }, chat.updatedAt?.let { compactLastUsedText(it.toEpochMilli()) }.orEmpty())
			.filter(String::isNotBlank).joinToString(" · ")
		addView(LinearLayout(this@MainActivity).apply {
			orientation = LinearLayout.VERTICAL
			addView(label(titleText).apply {
				setTypeface(typeface, if (selectable) Typeface.BOLD else Typeface.NORMAL)
			}, matchWrap())
			addView(helper(detail), spaced(top = 3))
		}, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
		addView(TextView(this@MainActivity).apply {
			text = "⋮"
			textSize = 24f
			gravity = Gravity.CENTER
			minWidth = dp(48)
			minHeight = dp(48)
			contentDescription = "Chat options for $titleText"
			setOnClickListener { showKoderChatMenu(it, chat) }
		})
		isClickable = true
		isFocusable = true
		contentDescription = if (selectable) "Open voice conversation ${chat.title}" else "${chat.title}, ${chat.role} chat, visible but not selectable"
		setOnClickListener { view -> if (selectable) openChat(chat) else showKoderChatMenu(view, chat) }
	}

	private fun showKoderChatMenu(anchor: View, chat: VoiceSession) {
		PopupMenu(this, anchor).apply {
			menu.add("Rename")
			if (chat.backend == "koder") menu.add("Change model")
			menu.add(if (chat.archived) "Restore from archive" else "Archive")
			if (chat.archived) menu.add("Delete")
			setOnMenuItemClickListener { item ->
				when (item.title.toString()) {
					"Rename" -> showRenameKoderChatDialog(chat)
					"Change model" -> showChatModelDialog(chat)
					"Archive" -> showArchiveKoderChatDialog(chat)
					"Restore from archive" -> updateKoderChat(chat, archived = false)
					"Delete" -> showDeleteKoderChatDialog(chat)
				}
				true
			}
			show()
		}
	}

	private fun showChatModelDialog(chat: VoiceSession) {
		val backend = readinessHome?.chatBackends.orEmpty().firstOrNull { it.id == "koder" }
		val models = backend?.models.orEmpty()
		if (models.isEmpty()) {
			Toast.makeText(this, "Koder could not load any available chat models", Toast.LENGTH_LONG).show()
			return
		}
		val labels = models.map { model ->
			buildString {
				append(model.name)
				if (model.isDefault) append(" — system default")
			}
		}.toTypedArray()
		AlertDialog.Builder(this)
			.setTitle("Choose an available model · system default first")
			.setItems(labels) { dialog, which ->
				val model = models[which]
				val ownerSessionId = chat.sessionId.ifBlank { selectedKoderSession?.id.orEmpty() }
				sessionClient.updateChat(
					settings.server, settings.token, ownerSessionId, chat.id,
					providerId = model.providerId, modelId = model.id,
				) { result ->
					runOnUiThread {
						result.fold(
							onSuccess = { home ->
								val updated = home.chats.firstOrNull { it.id == chat.id }
								if (updated != null) {
									currentSessionChats = currentSessionChats.map { if (it.id == updated.id) updated else it }
									if (pendingSession?.id == updated.id) pendingSession = updated
								}
								dialog.dismiss()
								lastRecoveryError = ""
								if (startAfterModelRecovery) {
									startAfterModelRecovery = false
									requestCallStart()
								} else if (::controller.isInitialized) controller.resumeAfterRecovery()
								Toast.makeText(this, "Model changed to ${model.name}", Toast.LENGTH_SHORT).show()
							},
							onFailure = ::showManagementError,
						)
					}
				}
			}
			.setNegativeButton("Cancel", null)
			.show()
	}

	private fun updateKoderChat(chat: VoiceSession, title: String? = null, archived: Boolean? = null) {
		val session = selectedKoderSession ?: return
		sessionClient.updateChat(settings.server, settings.token, session.id, chat.id, title, archived) { result ->
			runOnUiThread { result.fold(onSuccess = { showSession(session, it) }, onFailure = ::showManagementError) }
		}
	}

	private fun showRenameKoderChatDialog(chat: VoiceSession) {
		val field = EditText(this).apply { setText(chat.title); setSingleLine(); selectAll() }
		AlertDialog.Builder(this)
			.setTitle("Rename chat")
			.setView(field)
			.setNegativeButton("Cancel", null)
			.setPositiveButton("Rename") { _, _ -> updateKoderChat(chat, title = field.text.toString()) }
			.show()
	}

	private fun showArchiveKoderChatDialog(chat: VoiceSession) {
		AlertDialog.Builder(this)
			.setTitle("Archive chat?")
			.setMessage("${chat.title.ifBlank { "This chat" }} will move to Archived. You can restore it later.")
			.setNegativeButton("Cancel", null)
			.setPositiveButton("Archive") { _, _ -> updateKoderChat(chat, archived = true) }
			.show()
	}

	private fun showDeleteKoderChatDialog(chat: VoiceSession) {
		val session = selectedKoderSession ?: return
		AlertDialog.Builder(this)
			.setTitle("Delete chat?")
			.setMessage("${chat.title.ifBlank { "This chat" }} and its history will be removed. This cannot be undone.")
			.setNegativeButton("Cancel", null)
			.setPositiveButton("Delete") { _, _ ->
				sessionClient.deleteChat(settings.server, settings.token, session.id, chat.id) { result ->
					runOnUiThread { result.fold(onSuccess = { showSession(session, it) }, onFailure = ::showManagementError) }
				}
			}
			.show()
	}

	private fun showManagementError(error: Throwable) {
		Toast.makeText(this, error.message ?: "The change could not be completed", Toast.LENGTH_LONG).show()
	}

    private fun showAbout() {
        val version = packageManager.getPackageInfo(packageName, 0).versionName.orEmpty()
        AlertDialog.Builder(this)
            .setTitle("About Koder Voice")
            .setMessage("Native voice conversations for Koder.\n\nVersion ${version.ifBlank { "development" }}")
            .setPositiveButton("Close", null)
            .show()
    }

    private fun sessionCard(session: VoiceSession, unreadResults: Long = 0) = LinearLayout(this).apply {
		orientation = LinearLayout.HORIZONTAL
		gravity = Gravity.CENTER_VERTICAL
		setPadding(dp(12), dp(4), dp(7), dp(4))
        background = cardBackground()
		elevation = dp(1).toFloat()
        isClickable = true
        isFocusable = true
		val titleText = session.title.ifBlank { "Untitled conversation" }
		val unreadLabel = when {
			unreadResults > 99 -> "99+ new"
			unreadResults > 0 -> "$unreadResults new"
			else -> ""
		}
		val stateLabel = when (session.status) {
			"waiting_approval", "waiting_input" -> "● Needs attention"
			else -> if (session.busy) "● Working" else ""
		}
		val preview = session.lastMessage.replace(Regex("\\s+"), " ").trim()
		val time = session.updatedAt?.let { compactLastUsedText(it.toEpochMilli()) }.orEmpty()
		val detail = listOf(stateLabel, unreadLabel, time, preview).filter(String::isNotBlank).joinToString(" · ")
		contentDescription = listOf(
			if (session.archived || session.deleted) "Manage voice conversation $titleText" else "Open voice conversation $titleText",
			detail,
		).filter(String::isNotBlank).joinToString(". ")
        val selectable = TypedValue()
        if (theme.resolveAttribute(android.R.attr.selectableItemBackground, selectable, true)) {
            foreground = getDrawable(selectable.resourceId)
        }

		addView(LinearLayout(this@MainActivity).apply {
			orientation = LinearLayout.VERTICAL
			gravity = Gravity.CENTER_VERTICAL
			addView(TextView(this@MainActivity).apply {
				val markers = buildString {
					if (session.pinned) append("◆ ")
					if (session.favorite) append("★ ")
				}
				text = markers + titleText
				textSize = 16f
				setTypeface(typeface, Typeface.BOLD)
				maxLines = 1
				ellipsize = TextUtils.TruncateAt.END
			}, matchWrap())
			addView(helper(detail.ifBlank {
				when {
					session.deleted -> "Deleted"
					session.archived -> "Archived"
					else -> "No messages yet"
				}
			}).apply {
				textSize = 12f
				maxLines = 1
				ellipsize = TextUtils.TruncateAt.END
				alpha = 1f
				setTextColor(when {
					session.status == "waiting_approval" || session.status == "waiting_input" -> ACTION_RED
					session.busy -> ACTION_ORANGE
					unreadResults > 0 -> ACTION_BLUE
					else -> themeColor(android.R.attr.textColorSecondary)
				})
			}, matchWrap())
		}, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
		addView(TextView(this@MainActivity).apply {
			text = "⋮"
			textSize = 24f
			gravity = Gravity.CENTER
			minWidth = dp(48)
			minHeight = dp(48)
			contentDescription = "Options for ${session.title}"
			isClickable = true
			isFocusable = true
			ViewCompat.setTooltipText(this, contentDescription)
			setOnClickListener { anchor -> showVoiceSessionMenu(anchor, session) }
		})
		setOnClickListener { view ->
			if (session.archived || session.deleted) showVoiceSessionMenu(view, session) else openChat(session)
		}
    }

	private fun showVoiceSessionMenu(anchor: View, session: VoiceSession) {
		PopupMenu(this, anchor).apply {
			when {
				session.deleted -> menu.add("Undo delete")
				session.archived -> menu.add("Restore from archive")
				else -> menu.add(if (session.pinned) "Unpin" else "Pin to top")
			}
			if (!session.deleted) {
				menu.add(if (session.favorite) "Remove star" else "Add star")
				if (!session.archived) menu.add("Archive")
				menu.add("Rename")
				menu.add("Delete")
			}
			setOnMenuItemClickListener {
				when (it.title.toString()) {
					"Pin to top" -> updateVoiceSession(session, pinned = true)
					"Unpin" -> updateVoiceSession(session, pinned = false)
					"Add star" -> updateVoiceSession(session, favorite = true)
					"Remove star" -> updateVoiceSession(session, favorite = false)
					"Archive" -> showArchiveVoiceSessionDialog(session)
					"Restore from archive" -> updateVoiceSession(session, archived = false)
					"Undo delete" -> updateVoiceSession(session, deleted = false)
					"Rename" -> showRenameVoiceSessionDialog(session)
					"Delete" -> showDeleteVoiceSessionDialog(session)
				}
				true
			}
			show()
		}
	}

	private fun showArchiveVoiceSessionDialog(session: VoiceSession) {
		AlertDialog.Builder(this)
			.setTitle("Archive conversation?")
			.setMessage("${session.title.ifBlank { "This conversation" }} will move to Archived. You can restore it later.")
			.setNegativeButton("Cancel", null)
			.setPositiveButton("Archive") { _, _ ->
				updateVoiceSession(session, archived = true) { home ->
					showHome(home)
					showOrganizationUndo("Conversation archived", session) { updateVoiceSession(session, archived = false) }
				}
			}
			.show()
	}

	private fun showDeleteVoiceSessionDialog(session: VoiceSession) {
		AlertDialog.Builder(this)
			.setTitle("Delete conversation?")
			.setMessage("${session.title.ifBlank { "This conversation" }} will move to Deleted. Its transcript remains recoverable.")
			.setNegativeButton("Cancel", null)
			.setPositiveButton("Delete") { _, _ ->
					sessionClient.delete(settings.server, settings.token, session.id) { result ->
						runOnUiThread {
							result.fold(onSuccess = { home ->
								showHome(home)
								showOrganizationUndo("Conversation deleted", session) { updateVoiceSession(session, deleted = false) }
							}, onFailure = { Toast.makeText(this, it.message, Toast.LENGTH_LONG).show() })
						}
					}
			}
			.show()
	}

	private fun updateVoiceSession(
		session: VoiceSession,
		archived: Boolean? = null,
		pinned: Boolean? = null,
		favorite: Boolean? = null,
		deleted: Boolean? = null,
		onSuccess: ((VoiceHome) -> Unit)? = null,
	) {
		sessionClient.update(
			settings.server, settings.token, session.id,
			archived = archived, pinned = pinned, favorite = favorite, deleted = deleted,
		) { result ->
			runOnUiThread {
				result.fold(onSuccess = onSuccess ?: ::showHome, onFailure = { Toast.makeText(this, it.message, Toast.LENGTH_LONG).show() })
			}
		}
	}

	private fun showOrganizationUndo(title: String, session: VoiceSession, undo: () -> Unit) {
		AlertDialog.Builder(this)
			.setTitle(title)
			.setMessage(session.title.ifBlank { "Voice conversation" })
			.setPositiveButton("Undo") { _, _ -> undo() }
			.setNegativeButton("Done", null)
			.show()
	}

	private fun showRenameVoiceSessionDialog(session: VoiceSession) {
		val field = EditText(this).apply { setText(session.title); setSingleLine(); selectAll() }
		AlertDialog.Builder(this)
			.setTitle("Rename conversation")
			.setView(field)
			.setNegativeButton("Cancel", null)
			.setPositiveButton("Rename") { _, _ ->
				sessionClient.rename(settings.server, settings.token, session.id, field.text.toString().trim()) { result ->
					runOnUiThread { result.fold(onSuccess = ::showHome, onFailure = { Toast.makeText(this, it.message, Toast.LENGTH_LONG).show() }) }
				}
			}
			.show()
	}

    private fun emptyConversationCard(filter: ConversationFilter = ConversationFilter.ACTIVE) = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        gravity = Gravity.CENTER_HORIZONTAL
        setPadding(dp(24), dp(30), dp(24), dp(30))
        background = cardBackground()
        addView(logo(), centeredSquare(58, bottom = 16))
		val heading = when (filter) {
			ConversationFilter.ACTIVE -> "Your conversations will live here."
			ConversationFilter.FAVORITES -> "No starred conversations yet."
			ConversationFilter.ARCHIVED -> "Nothing is archived."
		}
		addView(body(heading).apply {
            gravity = Gravity.CENTER
            setTypeface(typeface, Typeface.BOLD)
        }, matchWrap())
		addView(helper(if (filter == ConversationFilter.ACTIVE) "Start one below, then return whenever you want to continue." else "Use a conversation’s ⋮ menu to organize it.").apply {
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

	private fun refreshForegroundState() {
		settings = secureSettings.load()
		if (settings.server.isBlank() || isFinishing || isDestroyed) return
		val generation = ++updateCheckGeneration
		sessionClient.list(settings.server, settings.token) { result ->
			runOnUiThread {
				if (generation != updateCheckGeneration || isFinishing || isDestroyed) return@runOnUiThread
				result.onSuccess { home ->
					if (screen == Screen.HOME) {
						showHome(home)
					} else {
						lastAppUpdate = home.appUpdate
						appUpdater.consider(home.appUpdate, settings.server, settings.token)
					}
				}
			}
		}
	}

	private fun compactLastUsedText(timestamp: Long): String {
        val now = System.currentTimeMillis()
        val relative = if (abs(now - timestamp) < DateUtils.MINUTE_IN_MILLIS) {
			"now"
        } else {
            DateUtils.getRelativeTimeSpanString(
                timestamp,
                now,
                DateUtils.MINUTE_IN_MILLIS,
                DateUtils.FORMAT_ABBREV_RELATIVE,
            )
        }
		return relative.toString()
    }

    private fun cardBackground() = GradientDrawable().apply {
        setColor(themeColor(android.R.attr.colorBackgroundFloating))
        setStroke(dp(1), withAlpha(themeColor(android.R.attr.colorAccent), 72))
        cornerRadius = dp(16).toFloat()
    }

    private fun withAlpha(color: Int, alpha: Int) = (color and 0x00ffffff) or (alpha.coerceIn(0, 255) shl 24)

    private fun showCreateVoiceSessionDialog() {
		val session = selectedKoderSession ?: return
		showChatCreator("New conversation", "Create a voice conversation inside ${session.title}.") { spec -> createVoiceChat(session, spec) }
    }

	private fun showCreateTemporaryConversationDialog() {
		showChatCreator("New temporary conversation", "Koder will create a quick session with a private scratch folder and one fully tooled voice chat.") { spec -> createTemporaryConversation(spec) }
	}

	private fun showChatCreator(dialogTitle: String, message: String, create: (VoiceChatCreateSpec) -> Unit) {
		val backends = readinessHome?.chatBackends.orEmpty().filter { it.available }.ifEmpty {
			listOf(VoiceChatBackendOption("koder", "Koder", true))
		}
		val titleField = EditText(this).apply {
			hint = "Conversation name"
			contentDescription = if (dialogTitle.contains("temporary", ignoreCase = true)) "Temporary conversation name" else "Conversation name"
			setSingleLine()
		}
		val backendSpinner = Spinner(this)
		val roleSpinner = Spinner(this)
		val modelSpinner = Spinner(this)
		val permissionSpinner = Spinner(this)
		val milestoneField = EditText(this).apply { hint = "Optional milestone key"; setSingleLine() }
		val taskField = EditText(this).apply { hint = "Optional task reference"; setSingleLine() }
		val toolContainer = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
		val toolSection = LinearLayout(this).apply {
			orientation = LinearLayout.VERTICAL
			addView(label("Koder additions"), spaced(top = 12))
			addView(toolContainer)
			addView(helper("Codex keeps its native tools. These Koder capabilities can be disabled for this conversation."), spaced(top = 4))
		}
		val toolChecks = linkedMapOf<String, CheckBox>()
		val roles = listOf("orchestrator", "planning", "execution", "standalone")
		backendSpinner.adapter = ArrayAdapter(this, android.R.layout.simple_spinner_dropdown_item, backends.map { it.label })
		roleSpinner.adapter = ArrayAdapter(this, android.R.layout.simple_spinner_dropdown_item, roles.map { it.replaceFirstChar(Char::titlecase) })
		fun updateBackendOptions(position: Int) {
			val backend = backends[position.coerceIn(backends.indices)]
			val models = backend.models
			val temporary = dialogTitle.contains("temporary", ignoreCase = true)
			val sessionDefault = currentSessionChats.firstOrNull { it.backend == backend.id }
			val inheritedAvailable = sessionDefault == null || models.any { it.providerId == sessionDefault.providerId && it.id == sessionDefault.modelId }
			val inheritedLabel = if (temporary) {
				models.firstOrNull { it.isDefault }?.name?.let { "System default — $it" } ?: "System default"
			} else if (!inheritedAvailable) {
				"Session default unavailable — choose a model"
			} else {
				sessionDefault?.let { "Session default — ${it.providerId} / ${it.modelId}" } ?: "Session default"
			}
			modelSpinner.adapter = ArrayAdapter(this, android.R.layout.simple_spinner_dropdown_item, listOf(inheritedLabel) + models.map { model ->
				model.name + if (model.isDefault) " — system default" else ""
			})
			modelSpinner.isEnabled = models.isNotEmpty()
			modelSpinner.setSelection(if (!temporary && !inheritedAvailable) (models.indexOfFirst { it.isDefault }.coerceAtLeast(0) + 1) else 0)
			permissionSpinner.adapter = ArrayAdapter(
				this, android.R.layout.simple_spinner_dropdown_item,
				listOf("Inherit session policy") + backend.permissionProfiles.map { profile ->
					if (profile.description.isBlank()) profile.label else "${profile.label} — ${profile.description}"
				},
			)
			toolContainer.removeAllViews()
			toolChecks.clear()
			backend.additionalTools.forEach { tool ->
				val check = CheckBox(this).apply {
					text = tool.label
					contentDescription = if (tool.description.isBlank()) tool.label else "${tool.label}. ${tool.description}"
					isChecked = true
				}
				toolChecks[tool.id] = check
				toolContainer.addView(check)
			}
			toolSection.visibility = if (backend.additionalTools.isEmpty()) View.GONE else View.VISIBLE
		}
		backendSpinner.onItemSelectedListener = object : AdapterView.OnItemSelectedListener {
			override fun onItemSelected(parent: AdapterView<*>?, view: View?, position: Int, id: Long) = updateBackendOptions(position)
			override fun onNothingSelected(parent: AdapterView<*>?) = Unit
		}
		updateBackendOptions(0)
		val fields = LinearLayout(this).apply {
			orientation = LinearLayout.VERTICAL
			setPadding(dp(20), dp(8), dp(20), dp(8))
			addView(helper(message), spaced(bottom = 12))
			addView(label("Name")); addView(titleField, spaced(bottom = 10))
			addView(label("Turn backend")); addView(backendSpinner, spaced(bottom = 10))
			addView(label("Chat role")); addView(roleSpinner, spaced(bottom = 10))
			addView(label("Model")); addView(modelSpinner, spaced(bottom = 10))
			addView(label("Permission profile")); addView(permissionSpinner, spaced(bottom = 10))
			addView(label("Execution scope")); addView(milestoneField); addView(taskField)
			addView(helper("Choose a milestone or a task, not both. Scope is used by execution chats."), spaced(top = 4))
			addView(toolSection)
		}
		AlertDialog.Builder(this).setTitle(dialogTitle).setView(ScrollView(this).apply { addView(fields) })
			.setNegativeButton("Cancel", null)
			.setPositiveButton("Create") { _, _ ->
				val backend = backends[backendSpinner.selectedItemPosition.coerceIn(backends.indices)]
				val modelIndex = modelSpinner.selectedItemPosition - 1
				val permission = backend.permissionProfiles.getOrNull(permissionSpinner.selectedItemPosition - 1)?.id.orEmpty()
				val milestone = milestoneField.text.toString().trim()
				val task = taskField.text.toString().trim()
				if (milestone.isNotBlank() && task.isNotBlank()) {
					Toast.makeText(this, "Choose a milestone or a task, not both", Toast.LENGTH_LONG).show()
					return@setPositiveButton
				}
				create(VoiceChatCreateSpec(
					title = titleField.text.toString().trim().ifBlank { "Voice conversation" }, backend = backend.id,
					workflowRole = roles[roleSpinner.selectedItemPosition],
					providerId = backend.models.getOrNull(modelIndex)?.providerId.orEmpty(),
					modelId = backend.models.getOrNull(modelIndex)?.id.orEmpty(),
					permissionProfile = permission, milestoneKey = milestone, taskRef = task,
					toolStates = toolChecks.mapValues { (_, check) -> check.isChecked },
				))
			}.show()
	}

	private fun createVoiceChat(session: VoiceSession, spec: VoiceChatCreateSpec) {
		showConversationCreationProgress("Creating voice conversation…")
		val generation = ++requestGeneration
		sessionClient.createVoiceChat(settings.server, settings.token, session.id, spec) { result ->
			runOnUiThread {
				if (generation != requestGeneration || isFinishing || isDestroyed) return@runOnUiThread
				result.fold(
					onSuccess = { home -> home.createdChat?.let(::openChat) ?: showSession(session, home) },
					onFailure = {
						Toast.makeText(this, it.message, Toast.LENGTH_LONG).show()
						loadSession(session)
					},
				)
			}
		}
	}

	private fun createTemporaryConversation(spec: VoiceChatCreateSpec) {
		showConversationCreationProgress("Creating temporary conversation…")
		val generation = ++requestGeneration
		sessionClient.createTemporary(settings.server, settings.token, spec.copy(title = spec.title.ifBlank { "Temporary conversation" })) { result ->
			runOnUiThread {
				if (generation != requestGeneration || isFinishing || isDestroyed) return@runOnUiThread
				result.fold(
					onSuccess = { home ->
					val chat = home.createdChat
					val session = home.createdSession
					if (chat != null && session != null) {
						selectedKoderSession = session
						openChat(chat)
					} else showCreateFailure(IllegalStateException("Koder did not return the temporary conversation"))
				},
					onFailure = ::showCreateFailure,
				)
			}
		}
	}

	private fun showConversationCreationProgress(message: String) {
		screen = Screen.LOADING
		showContent(column(Gravity.CENTER_HORIZONTAL).apply {
			gravity = Gravity.CENTER
			addView(logo(), centeredSquare(72, bottom = 18))
			addView(ProgressBar(this@MainActivity))
			addView(body(message), spaced(top = 12))
		})
	}

    private fun openChat(session: VoiceSession, startConnection: Boolean = true, initialDetail: String = "Connecting…") {
        screen = Screen.CHAT
        clearCallViews()
        pendingSession = session
		secureSettings.markVoiceSessionRead(session.id, session.resultCount)
		transcriptShown = false
		transcriptOpened = false
		followConversationBottom = true
		unreadConversationMessages = 0
		presentationShown = false
		renderedHistorySession = ""
		renderedHistoryIDs.clear()
		renderedPartKeys.clear()
		cachedConversationHistory = emptyList()
		searchContextShown = false
		savedResponses = secureSettings.savedVoiceResponses(session.id)
        val root = column()
        val heading = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
	            addView(iconActionButton(R.drawable.ic_voice_back, "Back to conversations", ACTION_NEUTRAL).apply {
					setOnClickListener { leaveChat() }
				}, actionLayout())
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
			pauseButton = iconActionButton(R.drawable.ic_voice_pause, "Pause voice conversation", ACTION_ORANGE).apply {
				tag = "Pause"
				contentDescription = "Pause voice conversation"
				setOnClickListener {
					if (tag == "Pause") controller.setPaused(true)
					else if (latestCallSnapshot.stage == CallController.Stage.HELD) controller.setPaused(false)
					else requestCallStart()
				}
			}.also { addView(it, actionLayout(start = 6)) }
			muteButton = iconActionButton(R.drawable.ic_voice_mic, "Mute microphone", ACTION_BLUE).apply {
					contentDescription = "Mute microphone"
					setOnClickListener { controller.setMicrophoneMuted(!latestCallSnapshot.microphoneMuted) }
				}.also { addView(it, actionLayout(start = 6)) }
			}
        root.addView(heading, matchWrap())
		status = helper(initialDetail).apply {
			accessibilityLiveRegion = View.ACCESSIBILITY_LIVE_REGION_POLITE
			alpha = 1f
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
			transcriptButton = iconActionButton(R.drawable.ic_voice_transcript, "Show transcript", ACTION_BLUE).apply {
				setOnClickListener {
					transcriptShown = !transcriptShown
					updateConversationMode(null)
				}
			}.also { addView(it, actionLayout()) }
			audioButton = audioRouteButton().apply {
				isEnabled = false
				setOnClickListener(::showAudioRouteMenu)
			}.also { addView(it, routeActionLayout(start = 8)) }
			searchButton = iconActionButton(R.drawable.ic_voice_search, "Search transcript", ACTION_BLUE).apply {
				setOnClickListener { showTranscriptSearchDialog() }
			}.also { addView(it, actionLayout(start = 8)) }
			savedButton = iconActionButton(R.drawable.ic_voice_saved, "Saved responses", ACTION_ORANGE).apply {
				setOnClickListener { showSavedResponses() }
			}.also { addView(it, actionLayout(start = 8)) }
		}
		root.addView(modeActions, matchWrap())

		activePanel = LinearLayout(this).apply {
			orientation = LinearLayout.VERTICAL
			gravity = Gravity.CENTER
			voiceOrb = VoiceStateOrbView(this@MainActivity).apply {
				mode = voiceOrbMode(CallController.Stage.CONNECTING)
			}.also { addView(it, centeredSquare(voiceOrbSizeDp(resources.configuration.fontScale), bottom = 22)) }
			voiceOrbDetail = body(initialDetail).apply {
				gravity = Gravity.CENTER
				textSize = 17f
				accessibilityLiveRegion = View.ACCESSIBILITY_LIVE_REGION_POLITE
			}.also { addView(it, matchWrap()) }
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
			setOnScrollChangeListener { _, _, scrollY, _, oldScrollY ->
				val child = getChildAt(0)
				followConversationBottom = child == null || isNearConversationBottom(child.height, scrollY, height, dp(48))
				if (followConversationBottom) {
					unreadConversationMessages = 0
					latestButton?.visibility = View.GONE
				}
				if (scrollY < oldScrollY && scrollY <= dp(24) && visibility == View.VISIBLE) controller.loadOlderHistory()
			}
        }
        root.addView(feedScroll, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
		latestButton = Button(this).apply {
			text = latestConversationLabel(0)
			isAllCaps = false
			visibility = View.GONE
			contentDescription = "Jump to latest message"
			setOnClickListener {
				if (searchContextShown) renderHistory(
					latestCallSnapshot.voiceSessionId,
					latestCallSnapshot.history.ifEmpty { cachedConversationHistory },
				)
				else scrollToBottom(force = true)
			}
		}
		root.addView(latestButton, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
			gravity = Gravity.CENTER_HORIZONTAL
			bottomMargin = dp(4)
		})

        val composer = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(8), dp(5), dp(5), dp(5))
            background = cardBackground()
            elevation = dp(2).toFloat()
        }
        typedMessage = EditText(this).apply {
            hint = "Message Koder"
			contentDescription = "Text message to Koder"
            maxLines = 3
            minHeight = dp(48)
            background = null
            setPadding(dp(10), 0, dp(8), 0)
        }
        composer.addView(typedMessage, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        composer.addView(ImageButton(this).apply {
            setImageResource(R.drawable.ic_voice_send)
            contentDescription = "Send message"
            imageTintList = ColorStateList.valueOf(themeColor(android.R.attr.colorAccent))
            background = null
            scaleType = ImageView.ScaleType.CENTER
            setPadding(dp(12), dp(12), dp(12), dp(12))
            minimumWidth = dp(48)
            minimumHeight = dp(48)
            ViewCompat.setTooltipText(this, "Send message")
            setOnClickListener {
                val message = typedMessage?.text?.toString().orEmpty()
                if (message.isNotBlank()) {
                    controller.submit(message)
                    typedMessage?.text?.clear()
                }
            }
        }, LinearLayout.LayoutParams(dp(48), dp(48)))
		composerView = composer
        root.addView(composer, matchWrap())
        showContent(root)
		updateConversationStatus(CallController.Stage.CONNECTING, initialDetail)
		updateConversationMode(CallController.Stage.CONNECTING)
		if (startConnection) {
			val catalog = readinessHome?.chatBackends.orEmpty().firstOrNull { it.id == "koder" }?.models.orEmpty()
			val available = session.backend != "koder" || catalog.isEmpty() ||
				catalog.any { it.providerId == session.providerId && it.id == session.modelId }
			if (available) requestCallStart() else {
				startAfterModelRecovery = true
				updateConversationStatus(CallController.Stage.ERROR, "This conversation's model is no longer available")
				root.post { showChatModelDialog(session) }
			}
		}
    }

	private fun showCreateFailure(failure: Throwable) {
		if (screen != Screen.CHAT) return
		updateConversationStatus(CallController.Stage.ERROR, failure.message ?: "Could not create conversation")
		updateConversationMode(CallController.Stage.ERROR)
	}

	private fun updateConversationStatus(stage: CallController.Stage?, detail: String) {
		val availability = conversationAvailability(stage, detail)
		status?.apply {
			text = conversationStatusText(stage, detail)
			contentDescription = when (availability) {
				ConversationAvailability.CONNECTING -> "Conversation connecting"
				ConversationAvailability.RETRYING -> "Conversation retrying connection"
				ConversationAvailability.ONLINE -> "Conversation online"
				ConversationAvailability.PAUSED -> "Conversation paused"
				ConversationAvailability.OFFLINE -> "Conversation offline"
			}
			val color = when (availability) {
				ConversationAvailability.ONLINE -> ACTION_GREEN
				ConversationAvailability.OFFLINE -> ACTION_RED
				ConversationAvailability.RETRYING, ConversationAvailability.CONNECTING -> ACTION_ORANGE
				ConversationAvailability.PAUSED -> ACTION_NEUTRAL
			}
			setTextColor(color)
			background = GradientDrawable().apply {
				setColor(withAlpha(color, 30))
				cornerRadius = dp(18).toFloat()
			}
		}
	}

	private fun updateConversationMode(stage: CallController.Stage?) {
		val active = when (stage) {
			null -> pauseButton?.tag == "Pause"
			CallController.Stage.DISCONNECTED, CallController.Stage.HELD, CallController.Stage.ERROR -> false
			else -> true
		}
		if (active) {
			window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
		} else {
			window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
		}
		pauseButton?.apply {
			val label = primaryVoiceControlLabel(stage, active)
			tag = label
			setImageResource(when (label) {
				"Pause" -> R.drawable.ic_voice_pause
				"Retry" -> R.drawable.ic_voice_retry
				else -> R.drawable.ic_voice_play
			})
			contentDescription = when (label) {
				"Pause" -> "Pause voice conversation"
				"Retry" -> "Retry voice connection"
				else -> "Resume voice conversation"
			}
			setActionAppearance(this, if (label == "Retry") ACTION_RED else ACTION_ORANGE, label != "Pause")
			ViewCompat.setTooltipText(this, contentDescription)
		}
		val surface = conversationSurface(active, transcriptShown, presentationShown)
		status?.visibility = if (surface == ConversationSurface.ACTIVE) View.GONE else View.VISIBLE
		activePanel?.visibility = if (surface == ConversationSurface.ACTIVE) View.VISIBLE else View.GONE
		presentationPanel?.visibility = if (surface == ConversationSurface.PRESENTATION) View.VISIBLE else View.GONE
		feedScroll?.visibility = if (surface == ConversationSurface.TRANSCRIPT) View.VISIBLE else View.GONE
		composerView?.visibility = if (surface == ConversationSurface.TRANSCRIPT) View.VISIBLE else View.GONE
		if (surface == ConversationSurface.TRANSCRIPT && !transcriptOpened) {
			transcriptOpened = true
			followConversationBottom = true
			scrollToBottom(force = true)
		}
		transcriptButton?.apply {
			visibility = if (active) View.VISIBLE else View.GONE
			contentDescription = if (transcriptShown) "Hide transcript" else "Show transcript"
			setActionAppearance(this, ACTION_BLUE, transcriptShown)
			ViewCompat.setTooltipText(this, contentDescription)
		}
		audioButton?.visibility = if (active) View.VISIBLE else View.GONE
		searchButton?.visibility = if (active) View.VISIBLE else View.GONE
		savedButton?.visibility = View.VISIBLE
		placeholderTitle?.text = when {
			active -> "No transcript yet"
			stage == CallController.Stage.ERROR -> "Conversation offline"
			else -> "Conversation paused"
		}
		placeholderDetail?.text = if (active) {
			"Your conversation will appear here as you speak."
		} else if (stage == CallController.Stage.ERROR) {
			"Check the connection, then tap retry."
		} else {
			"Resume when you want to keep talking."
		}
	}

	private fun showAudioRouteMenu(anchor: View) {
		val endpoints = latestCallSnapshot.audioEndpoints
		val diagnosticsId = endpoints.size
		PopupMenu(this, anchor).apply {
			endpoints.forEachIndexed { index, endpoint ->
				menu.add(0, index, index, (if (endpoint.current) "✓  " else "") + endpoint.name)
			}
			menu.add(1, diagnosticsId, diagnosticsId, "Audio diagnostics…")
			setOnMenuItemClickListener { item ->
				if (item.groupId == 1) {
					showAudioDiagnostics()
					return@setOnMenuItemClickListener true
				}
				val endpoint = endpoints.getOrNull(item.itemId) ?: return@setOnMenuItemClickListener false
				when (endpoint.type) {
					VoiceAudioEndpointType.EARPIECE -> rememberBuiltInAudioRoute(BuiltInAudioRoute.EARPIECE)
					VoiceAudioEndpointType.SPEAKER -> rememberBuiltInAudioRoute(BuiltInAudioRoute.SPEAKER)
					else -> Unit
				}
				controller.selectAudioEndpoint(endpoint.id)
				true
			}
			show()
		}
	}

	private fun showTranscriptSearchDialog() {
		val query = EditText(this).apply {
			hint = "Words in this conversation"
			contentDescription = "Transcript search query"
			setSingleLine()
		}
		AlertDialog.Builder(this)
			.setTitle("Search transcript")
			.setView(query)
			.setNegativeButton("Cancel", null)
			.setPositiveButton("Search") { _, _ ->
				val text = query.text.toString().trim()
				if (text.isNotBlank()) controller.searchHistory(text)
			}
			.show()
	}

	private fun showTranscriptSearchResults(results: List<VoiceTranscriptSearchResult>) {
		if (results.isEmpty()) {
			AlertDialog.Builder(this).setTitle("No matches").setMessage("Nothing in this conversation matched your search.").setPositiveButton("OK", null).show()
			return
		}
		val labels = results.map { result ->
			val speaker = if (result.match.role == "user") "You" else "Koder"
			"$speaker · ${result.match.text.replace('\n', ' ').take(90)}"
		}.toTypedArray()
		AlertDialog.Builder(this)
			.setTitle("Transcript matches")
			.setItems(labels) { _, index -> showTranscriptSearchContext(results[index]) }
			.setNegativeButton("Close", null)
			.show()
	}

	private fun showTranscriptSearchContext(result: VoiceTranscriptSearchResult) {
		val targetFeed = feed ?: return
		targetFeed.removeAllViews()
		targetFeed.gravity = Gravity.NO_GRAVITY
		feedPlaceholder = null
		renderedHistoryIDs.clear()
		renderedPartKeys.clear()
		var target: View? = null
		result.context.forEach { entry ->
			renderedHistoryIDs += entry.id
			val first = appendTranscriptEntry(targetFeed, entry, entry.id == result.match.id)
			if (entry.id == result.match.id) target = first
		}
		searchContextShown = true
		transcriptShown = true
		presentationShown = false
		updateConversationMode(latestCallSnapshot.stage)
		latestButton?.apply {
			text = "Back to latest"
			visibility = View.VISIBLE
		}
		feedScroll?.post { target?.let { feedScroll?.smoothScrollTo(0, it.top) } }
	}

	private fun rememberBuiltInAudioRoute(route: BuiltInAudioRoute) {
		settings = settings.copy(builtInAudioRoute = route)
		secureSettings.saveBuiltInAudioRoute(route)
	}

	private fun renderHistory(voiceSessionId: String, history: List<VoiceTranscriptEntry>) {
		renderedHistorySession = voiceSessionId
		if (history.isEmpty()) return
		searchContextShown = false
		cachedConversationHistory = history
		feed?.removeAllViews()
		feedPlaceholder = null
		feed?.gravity = Gravity.NO_GRAVITY
		renderedHistoryIDs.clear()
		renderedPartKeys.clear()
		history.forEach { entry ->
			renderedHistoryIDs += entry.id
			feed?.let { appendTranscriptEntry(it, entry) }
		}
		latestButton?.visibility = View.GONE
		scrollToBottom(force = true)
		focusPendingResult()
	}

	private fun focusPendingResult() {
		if (pendingResultSessionId.isBlank() || pendingSession?.id != pendingResultSessionId) return
		transcriptShown = true
		presentationShown = false
		updateConversationMode(latestCallSnapshot.stage)
		val target = feed?.findTaggedView("message:$pendingResultTranscriptId")
		if (pendingResultTranscriptId.isNotBlank() && target == null) {
			controller.loadOlderHistory()
			return
		}
		feedScroll?.post { target?.let { feedScroll?.smoothScrollTo(0, it.top) } }
		pendingResultSessionId = ""
		pendingResultOwnerSessionId = ""
		pendingResultTranscriptId = ""
	}

	private fun prependHistory(entries: List<VoiceTranscriptEntry>) {
		val feed = feed ?: return
		val scroll = feedScroll ?: return
		val older = entries.filter { it.id !in renderedHistoryIDs }
		if (older.isEmpty()) return
		removeFeedPlaceholder()
		val previousHeight = feed.height
		val previousY = scroll.scrollY
		var insertion = 0
		older.forEach { entry ->
			renderedHistoryIDs += entry.id
			val staged = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
			appendTranscriptEntry(staged, entry)
			while (staged.childCount > 0) {
				val child = staged.getChildAt(0)
				staged.removeViewAt(0)
				feed.addView(child, insertion++)
			}
		}
		scroll.post { scroll.scrollTo(0, previousY + feed.height - previousHeight) }
	}

	private fun appendTranscriptEntry(target: LinearLayout, entry: VoiceTranscriptEntry, highlighted: Boolean = false): View? {
		var first: View? = null
		if (entry.text.isNotBlank()) {
			val bubble = conversationBubble(
				if (entry.role == "user") "You" else "Koder", entry.text, entry.createdAt, entry.id, highlighted,
			)
			target.addView(bubble, bubbleLayout(entry.role == "user"))
			first = bubble
		}
		val presentationParts = entry.parts.filterNot(VoicePart::isTranscriptOnly).filter {
			it.isPresentation || it.uri.isNotBlank() || it.mimeType !in DISPLAYABLE_TEXT_TYPES
		}
		if (presentationParts.isNotEmpty()) {
			val fresh = presentationParts.filter(::rememberRenderPart)
			if (fresh.isNotEmpty()) {
				val link = addPresentationTranscriptLink(fresh, target)
				if (first == null) first = link
			}
		}
		entry.parts.filter(VoicePart::isTranscriptOnly).forEach { part ->
			if (!rememberRenderPart(part)) return@forEach
			val before = target.childCount
			addToolActivity(part, target)
			if (first == null && target.childCount > before) first = target.getChildAt(before)
		}
		return first
	}

	private fun addPresentationTranscriptLink(parts: List<VoicePart>, target: LinearLayout? = feed): View? {
		val target = target ?: return null
		if (target === feed) removeFeedPlaceholder()
		val heading = parts.firstNotNullOfOrNull { it.title.takeIf(String::isNotBlank) ?: it.name.takeIf(String::isNotBlank) }
			?: if (parts.size == 1) "Presented by Koder" else "${parts.size} items presented by Koder"
		val formats = parts.map(VoicePart::mimeType).filter(String::isNotBlank).distinct().joinToString()
		val view = card().apply {
			isClickable = true
			isFocusable = true
			contentDescription = "$heading. Open presentation"
			addView(label(heading).apply { setTypeface(typeface, Typeface.BOLD) }, matchWrap())
			addView(helper(if (formats.isBlank()) "Tap to view" else "$formats · Tap to view"), spaced(top = 4))
			setOnClickListener { showPresentation(parts) }
		}
		target.addView(view, spaced(top = 5, bottom = 5))
		return view
	}

	private fun showPresentation(parts: List<VoicePart>) {
		presentationFeed?.removeAllViews()
		parts.forEach(::addPresentationPart)
		presentationShown = true
		transcriptShown = false
		updateConversationMode(latestCallSnapshot.stage)
	}

    private fun leaveChat() {
		val ownerSession = selectedKoderSession
        pendingStart = false
        pendingSession = null
        controller.end()
		window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
		if (ownerSession != null) loadSession(ownerSession) else loadHome("Refreshing sessions…")
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
		if (session.id.isBlank()) return
		val ownerSessionId = session.sessionId.ifBlank { selectedKoderSession?.id.orEmpty() }
		if (ownerSessionId.isBlank()) {
			updateConversationStatus(CallController.Stage.ERROR, "This conversation is missing its Koder session")
			return
		}
		controller.start(
			settings.server,
			settings.token,
			ownerSessionId,
			session.id,
			settings.speechLanguages,
			settings.vadSensitivityPercent,
			settings.vadSilenceMilliseconds,
			settings.builtInAudioRoute,
			settings.responsePacing,
		)
    }

	private fun addPart(part: VoicePart, transcriptId: String = "") {
        when {
			part.mimeType == KODER_TOOL_ACTIVITY_MIME -> addToolActivity(part)
			part.mimeType in DISPLAYABLE_TEXT_TYPES && part.text.isNotBlank() -> addBubble("Koder", part.text, entryId = transcriptId)
            part.mimeType.startsWith("image/") -> addImage(part)
            else -> addGenericPart(part)
        }
    }

	private fun addPresentationPart(part: VoicePart) {
		when {
			part.mimeType == KODER_PRESENTATION_MIME -> addStructuredPresentation(part)
			part.mimeType.startsWith("image/") -> addImage(part, presentationFeed)
			part.mimeType in DISPLAYABLE_TEXT_TYPES && part.text.isNotBlank() -> addInlinePresentation(part)
			else -> addGenericPart(part, presentationFeed)
		}
	}

	private fun rememberRenderPart(part: VoicePart): Boolean {
		val key = part.renderKey
		return key.isBlank() || renderedPartKeys.add(key)
	}

	private fun addToolActivity(part: VoicePart, target: LinearLayout? = feed) {
		val target = target ?: return
		if (target === feed) removeFeedPlaceholder()
		val data = part.data as? org.json.JSONObject
		val title = data?.optString("title")?.takeIf(String::isNotBlank) ?: "Koder tool"
		val status = data?.optString("status").orEmpty().replace('_', ' ')
		val summary = data?.optString("summary").orEmpty()
		target.addView(card().apply {
			contentDescription = "$title $status $summary"
			addView(label(title).apply { setTypeface(typeface, Typeface.BOLD) }, matchWrap())
			if (status.isNotBlank()) addView(helper(status.replaceFirstChar(Char::uppercase)), spaced(top = 3))
			if (summary.isNotBlank()) addView(body(summary), spaced(top = 5))
		}, spaced(top = 5, bottom = 5))
		scrollToBottom()
	}

	private fun addStructuredPresentation(part: VoicePart) {
		val target = presentationFeed ?: return
		val document = PresentationDocuments.parse(part.data)
		if (document == null) {
			addGenericPart(part, target)
			return
		}
		val container = card().apply { contentDescription = "Koder structured presentation" }
		part.title.takeIf(String::isNotBlank)?.let { container.addView(label(it), matchWrap()) }
		document.blocks.forEach { block -> addPresentationBlock(container, block) }
		target.addView(container, spaced(top = 6, bottom = 6))
	}

	private fun addPresentationBlock(target: LinearLayout, block: PresentationBlock) {
		when (block) {
			is PresentationBlock.Text -> target.addView(body(block.text).apply {
				when (block.style) {
					"heading" -> { textSize = 20f; setTypeface(typeface, Typeface.BOLD) }
					"caption" -> { textSize = 14f; alpha = 0.72f }
					"code" -> typeface = Typeface.MONOSPACE
				}
				setTextIsSelectable(true)
			}, spaced(top = 8))
			is PresentationBlock.Image -> addPresentationImage(target, block)
			is PresentationBlock.KeyValue -> block.items.forEach { item ->
				target.addView(LinearLayout(this).apply {
					orientation = LinearLayout.HORIZONTAL
					addView(helper(item.key.ifBlank { item.title }).apply { setTypeface(typeface, Typeface.BOLD) }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 0.4f))
					addView(body(item.value.ifBlank { item.detail }).apply { gravity = Gravity.END; setTextIsSelectable(true) }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 0.6f))
				}, spaced(top = 7))
			}
			is PresentationBlock.ListItems -> block.items.forEach { item ->
				val title = item.title.ifBlank { item.value.ifBlank { item.key } }
				val detail = item.detail
				target.addView(body(buildString {
					append("• ").append(title)
					if (detail.isNotBlank()) append("\n  ").append(detail)
				}), spaced(top = 6))
			}
			is PresentationBlock.Progress -> {
				target.addView(body(block.label.ifBlank { "Progress" }).apply { setTypeface(typeface, Typeface.BOLD) }, spaced(top = 8))
				target.addView(ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply {
					max = block.max
					progress = block.value.coerceIn(0, block.max)
					contentDescription = "${block.label.ifBlank { "Progress" }} ${block.value} of ${block.max}"
				}, spaced(top = 4))
				if (block.detail.isNotBlank()) target.addView(helper(block.detail), spaced(top = 3))
			}
			is PresentationBlock.Action -> target.addView(Button(this).apply {
				text = block.label
				contentDescription = "Presentation action ${block.label}"
				setOnClickListener { openPresentationLink(block.uri) }
			}, spaced(top = 8))
			is PresentationBlock.File -> target.addView(Button(this).apply {
				text = buildString {
					append("Open ").append(block.name)
					if (block.detail.isNotBlank()) append(" · ").append(block.detail)
				}
				contentDescription = "Presentation file ${block.name}"
				setOnClickListener {
					downloadAndOpen(VoicePart(mimeType = block.mimeType, uri = block.uri, metadata = mapOf("name" to block.name)))
				}
			}, spaced(top = 8))
			is PresentationBlock.Unknown -> target.addView(helper("Unsupported ${block.kind} content"), spaced(top = 8))
		}
	}

	private fun addPresentationImage(target: LinearLayout, block: PresentationBlock.Image) {
		if (block.title.isNotBlank()) target.addView(helper(block.title), spaced(top = 8))
		var presented: PresentedImage? = null
		val imageDescription = block.alt.ifBlank { block.title.ifBlank { "Presentation image" } }
		val image = ImageView(this).apply {
			adjustViewBounds = true
			scaleType = ImageView.ScaleType.CENTER_INSIDE
			minimumHeight = dp(120)
			contentDescription = "$imageDescription. Loading"
			isClickable = true
			setOnClickListener { presented?.let(::showImageViewer) }
		}
		target.addView(image, spaced(top = 4))
		controller.loadBytes(block.uri) { bytes, error ->
			runOnUiThread {
				val bitmap = bytes?.let { BitmapFactory.decodeByteArray(it, 0, it.size) }
				if (bitmap != null) {
					image.setImageBitmap(bitmap)
					presented = PresentedImage(bytes, bitmap, imageName(block.uri, block.title), imageMIME(block.uri), block.title, block.alt)
					image.contentDescription = "$imageDescription. Tap for fullscreen"
				}
				else image.contentDescription = "$imageDescription. ${error ?: "Invalid image"}"
			}
		}
	}

	private fun openPresentationLink(resource: String) {
		val resolved = runCatching { VoiceProtocol.resourceUrl(settings.server, resource) }.getOrElse {
			Toast.makeText(this, it.message ?: "Invalid link", Toast.LENGTH_LONG).show()
			return
		}
		runCatching { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(resolved))) }
			.onFailure { Toast.makeText(this, "No app can open this link", Toast.LENGTH_LONG).show() }
	}

	private fun showImageViewer(image: PresentedImage) {
		val zoom = ZoomableImageView(this).apply {
			setImageBitmap(image.bitmap)
			contentDescription = image.alt.ifBlank { image.title.ifBlank { "Fullscreen image" } } + ". Pinch to zoom, drag to pan, double tap to reset"
		}
		var dialog: AlertDialog? = null
		val actions = LinearLayout(this).apply {
			orientation = LinearLayout.HORIZONTAL
			listOf(
				"Rotate" to { zoom.rotateClockwise() },
				"Reset" to { zoom.resetImage() },
				"Save" to { savePresentedImage(image) },
				"Share" to { sharePresentedImage(image) },
				"Close" to { dialog?.dismiss() },
			).forEach { (text, action) ->
				addView(Button(this@MainActivity).apply {
					this.text = text
					contentDescription = "$text fullscreen image"
					setOnClickListener { action() }
				}, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
			}
		}
		val content = LinearLayout(this).apply {
			orientation = LinearLayout.VERTICAL
			setPadding(dp(8), dp(8), dp(8), dp(8))
			addView(zoom, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
			addView(helper("Pinch to zoom · drag to pan · double tap to reset").apply { gravity = Gravity.CENTER }, spaced(top = 6))
			addView(actions, spaced(top = 4))
		}
		dialog = AlertDialog.Builder(this)
			.setTitle(image.title.ifBlank { image.name })
			.setView(content)
			.create()
		dialog?.setOnShowListener {
			dialog?.window?.setLayout(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
		}
		dialog?.show()
	}

	private fun savePresentedImage(image: PresentedImage) {
		pendingImageSave = image
		imageSaveLauncher.launch(image.name)
	}

	private fun sharePresentedImage(image: PresentedImage) {
		runCatching {
			val directory = File(cacheDir, "voice-presentations").apply { mkdirs() }
			val file = File(directory, image.name.replace(Regex("[^A-Za-z0-9._-]"), "_").take(96).ifBlank { "koder-image" })
			file.outputStream().use { it.write(image.bytes) }
			val uri = FileProvider.getUriForFile(this, "$packageName.presentations", file)
			val share = Intent(Intent.ACTION_SEND).apply {
				type = image.mimeType.ifBlank { "image/*" }
				putExtra(Intent.EXTRA_STREAM, uri)
				addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
			}
			startActivity(Intent.createChooser(share, "Share ${image.name}"))
		}.onFailure { Toast.makeText(this, it.message ?: "Could not share image", Toast.LENGTH_LONG).show() }
	}

	private fun imageName(uri: String, title: String): String {
		val fromURI = runCatching { Uri.parse(uri).lastPathSegment.orEmpty() }.getOrDefault("")
		val base = fromURI.ifBlank { title.ifBlank { "koder-image" } }.replace(Regex("[^A-Za-z0-9._-]"), "_").take(90).ifBlank { "koder-image" }
		return if (base.contains('.')) base else "$base.png"
	}

	private fun imageMIME(uri: String): String = when (Uri.parse(uri).lastPathSegment.orEmpty().substringAfterLast('.', "").lowercase()) {
		"jpg", "jpeg" -> "image/jpeg"
		"webp" -> "image/webp"
		"gif" -> "image/gif"
		else -> "image/png"
	}

	private fun addInlinePresentation(part: VoicePart) {
		val target = presentationFeed ?: return
		val card = card()
		val heading = part.title.ifBlank { part.name }.ifBlank { "Details" }
		card.addView(label(heading), matchWrap())
		card.addView(body(part.text).apply {
			if (part.mimeType == "text/markdown") {
				text = Html.fromHtml(markdownToHtml(part.text), Html.FROM_HTML_MODE_LEGACY)
				movementMethod = LinkMovementMethod.getInstance()
			}
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
		var presented: PresentedImage? = null
		val imageDescription = part.alt.ifBlank { part.title.ifBlank { part.name.ifBlank { "Presented image" } } }
		val image = ImageView(this).apply {
			adjustViewBounds = true
			scaleType = ImageView.ScaleType.CENTER_INSIDE
			minimumHeight = dp(120)
			contentDescription = "$imageDescription. Loading"
			isClickable = true
			setOnClickListener { presented?.let(::showImageViewer) }
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
					if (bitmap != null) {
						image.setImageBitmap(bitmap)
						presented = PresentedImage(
							bytes,
							bitmap,
							part.name.ifBlank { imageName(part.url, part.title) },
							part.mimeType,
							part.title,
							part.alt,
						)
						image.contentDescription = "$imageDescription. Tap for fullscreen"
					}
					else {
						title.text = "${title.text} · ${error ?: "invalid image"}"
						image.contentDescription = "$imageDescription. ${error ?: "Invalid image"}"
					}
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

    private fun addBubble(who: String, text: String, createdAt: Instant? = Instant.now(), entryId: String = "") = runOnUiThread {
        val feed = feed ?: return@runOnUiThread
        if (screen != Screen.CHAT || text.isBlank()) return@runOnUiThread
        removeFeedPlaceholder()
		feed.addView(conversationBubble(who, text, createdAt, entryId), bubbleLayout(who == "You"))
		if (!followConversationBottom) {
			unreadConversationMessages++
			latestButton?.apply {
				this.text = latestConversationLabel(unreadConversationMessages)
				visibility = View.VISIBLE
			}
		}
		scrollToBottom()
    }

	private fun copyMessage(who: String, text: String) {
		(getSystemService(ClipboardManager::class.java)).setPrimaryClip(ClipData.newPlainText("$who message", text))
		Toast.makeText(this, "Message copied", Toast.LENGTH_SHORT).show()
	}

	private fun showResponseActions(bubble: View, entryId: String, text: String) {
		val bookmarked = savedResponses.any { it.messageId == entryId && it.kind == SavedVoiceResponseKind.BOOKMARK }
		val followUp = savedResponses.any { it.messageId == entryId && it.kind == SavedVoiceResponseKind.FOLLOW_UP }
		val actions = arrayOf("Copy", if (bookmarked) "Remove bookmark" else "Bookmark", if (followUp) "Clear follow-up" else "Follow up later")
		AlertDialog.Builder(this).setTitle("Response actions").setItems(actions) { _, index ->
			when (index) {
				0 -> copyMessage("Koder", text)
				1 -> toggleSavedResponse(bubble, entryId, text, SavedVoiceResponseKind.BOOKMARK)
				2 -> toggleSavedResponse(bubble, entryId, text, SavedVoiceResponseKind.FOLLOW_UP)
			}
		}.setNegativeButton("Cancel", null).show()
	}

	private fun toggleSavedResponse(bubble: View?, entryId: String, text: String, kind: SavedVoiceResponseKind) {
		val sessionId = pendingSession?.id.orEmpty().ifBlank { latestCallSnapshot.voiceSessionId }
		if (sessionId.isBlank()) return
		val saved = secureSettings.toggleSavedVoiceResponse(SavedVoiceResponse(sessionId, entryId, text, kind))
		savedResponses = secureSettings.savedVoiceResponses(sessionId)
		bubble?.findTaggedView("saved:$entryId")?.let { indicator ->
			if (indicator is TextView) {
				indicator.text = savedResponseLabels(entryId)
				indicator.visibility = if (indicator.text.isBlank()) View.GONE else View.VISIBLE
			}
		}
		Toast.makeText(this, if (saved) "${kind.label} saved" else "${kind.label} removed", Toast.LENGTH_SHORT).show()
	}

	private fun savedResponseLabels(entryId: String): String = savedResponses
		.filter { it.messageId == entryId }
		.map { if (it.kind == SavedVoiceResponseKind.BOOKMARK) "★ Bookmarked" else "↗ Follow up" }
		.joinToString("  ")

	private fun showSavedResponses() {
		val sessionId = pendingSession?.id.orEmpty().ifBlank { latestCallSnapshot.voiceSessionId }
		savedResponses = secureSettings.savedVoiceResponses(sessionId)
		if (savedResponses.isEmpty()) {
			AlertDialog.Builder(this).setTitle("Saved responses").setMessage("Long-press a Koder response to bookmark it or mark it for follow-up.").setPositiveButton("OK", null).show()
			return
		}
		val labels = savedResponses.map {
			(if (it.kind == SavedVoiceResponseKind.BOOKMARK) "★ " else "↗ ") + it.text.replace('\n', ' ').take(90)
		}.toTypedArray()
		AlertDialog.Builder(this).setTitle("Saved responses").setItems(labels) { _, index ->
			showSavedResponse(savedResponses[index])
		}.setNegativeButton("Close", null).show()
	}

	private fun showSavedResponse(saved: SavedVoiceResponse) {
		val action = if (saved.kind == SavedVoiceResponseKind.FOLLOW_UP) "Follow up" else "Show"
		AlertDialog.Builder(this)
			.setTitle(saved.kind.label)
			.setMessage(saved.text)
			.setPositiveButton(action) { _, _ ->
				if (saved.kind == SavedVoiceResponseKind.FOLLOW_UP) beginFollowUp(saved)
				else showTranscriptSearchContext(VoiceTranscriptSearchResult(
					match = VoiceTranscriptEntry(saved.messageId, "assistant", saved.text),
					context = listOf(VoiceTranscriptEntry(saved.messageId, "assistant", saved.text)),
				))
			}
			.setNeutralButton("Remove") { _, _ -> toggleSavedResponse(null, saved.messageId, saved.text, saved.kind) }
			.setNegativeButton("Close", null)
			.show()
	}

	private fun beginFollowUp(saved: SavedVoiceResponse) {
		transcriptShown = true
		presentationShown = false
		updateConversationMode(latestCallSnapshot.stage)
		typedMessage?.apply {
			setText("Following up on your earlier response, “${saved.text.replace('\n', ' ').take(140)}”: ")
			setSelection(text.length)
			requestFocus()
		}
	}

	private fun View.findTaggedView(wanted: String): View? {
		if (tag == wanted) return this
		if (this is ViewGroup) repeat(childCount) { index -> getChildAt(index).findTaggedView(wanted)?.let { return it } }
		return null
	}

	private fun conversationBubble(who: String, text: String, createdAt: Instant?, entryId: String = "", highlighted: Boolean = false): View {
		val fromUser = who == "You"
		return LinearLayout(this).apply {
			tag = entryId.takeIf(String::isNotBlank)?.let { "message:$it" }
            orientation = LinearLayout.VERTICAL
            setPadding(dp(14), dp(10), dp(14), dp(11))
            background = GradientDrawable().apply {
                setColor(
					if (highlighted) withAlpha(ACTION_ORANGE, 72)
					else if (fromUser) withAlpha(themeColor(android.R.attr.colorAccent), 46)
                    else themeColor(android.R.attr.colorBackgroundFloating),
                )
                cornerRadius = dp(16).toFloat()
            }
            elevation = dp(1).toFloat()
			isLongClickable = true
			contentDescription = if (!fromUser && entryId.isNotBlank()) "$who message: $text. Long press for actions" else "$who message: $text. Long press to copy"
			setOnLongClickListener {
				performHapticFeedback(HapticFeedbackConstants.LONG_PRESS)
				if (!fromUser && entryId.isNotBlank()) showResponseActions(this, entryId, text)
				else copyMessage(who, text)
				true
			}
            addView(helper(who).apply {
                setTextColor(themeColor(android.R.attr.colorAccent))
                alpha = 1f
                setTypeface(typeface, Typeface.BOLD)
            }, matchWrap())
			addView(body(text).apply { maxWidth = dp(310) }, spaced(top = 4))
			addView(helper(savedResponseLabels(entryId)).apply {
				tag = "saved:$entryId"
				visibility = if (text.isBlank()) View.GONE else View.VISIBLE
				setTextColor(ACTION_ORANGE)
				alpha = 1f
			}, spaced(top = 5))
			conversationTimeLabel(createdAt).takeIf(String::isNotBlank)?.let { time ->
				addView(helper(time).apply {
					gravity = Gravity.END
					alpha = 0.72f
					textSize = 11f
				}, spaced(top = 5))
			}
        }
	}

	private fun bubbleLayout(fromUser: Boolean) = LinearLayout.LayoutParams(
		ViewGroup.LayoutParams.WRAP_CONTENT,
		ViewGroup.LayoutParams.WRAP_CONTENT,
	).apply {
		gravity = if (fromUser) Gravity.END else Gravity.START
		topMargin = dp(6)
		bottomMargin = dp(6)
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

	private fun iconActionButton(@DrawableRes icon: Int, description: String, color: Int): ImageButton = ImageButton(this).apply {
		setImageResource(icon)
		contentDescription = description
		isFocusable = true
		scaleType = ImageView.ScaleType.CENTER
		setPadding(dp(12), dp(12), dp(12), dp(12))
		elevation = dp(2).toFloat()
		setActionAppearance(this, color, false)
		ViewCompat.setTooltipText(this, description)
	}

	private fun setActionAppearance(button: ImageButton, color: Int, selected: Boolean) {
		button.imageTintList = ColorStateList.valueOf(color)
		button.background = GradientDrawable().apply {
			shape = GradientDrawable.OVAL
			setColor(withAlpha(color, if (selected) 62 else 28))
			if (selected) setStroke(dp(2), withAlpha(color, 180))
		}
		button.alpha = if (button.isEnabled) 1f else 0.45f
	}

	private fun audioRouteButton(): TextView = TextView(this).apply {
		text = audioRouteChipText("")
		setCompoundDrawablesRelativeWithIntrinsicBounds(R.drawable.ic_voice_audio, 0, 0, 0)
		compoundDrawablePadding = dp(6)
		gravity = Gravity.CENTER
		maxLines = 1
		ellipsize = TextUtils.TruncateAt.END
		textSize = 13f
		setTypeface(typeface, Typeface.BOLD)
		isFocusable = true
		setPadding(dp(11), 0, dp(11), 0)
		elevation = dp(2).toFloat()
		setAudioRouteAppearance(this)
	}

	private fun setAudioRouteAppearance(button: TextView) {
		button.setTextColor(ACTION_GREEN)
		button.compoundDrawableTintList = ColorStateList.valueOf(ACTION_GREEN)
		button.background = GradientDrawable().apply {
			cornerRadius = dp(24).toFloat()
			setColor(withAlpha(ACTION_GREEN, 28))
			setStroke(dp(1), withAlpha(ACTION_GREEN, 100))
		}
		button.alpha = if (button.isEnabled) 1f else 0.45f
	}

	private fun actionLayout(start: Int = 0) = LinearLayout.LayoutParams(dp(48), dp(48)).apply {
		marginStart = dp(start)
	}

	private fun routeActionLayout(start: Int = 0) = LinearLayout.LayoutParams(dp(120), dp(48)).apply {
		marginStart = dp(start)
	}

	private fun scrollToBottom(force: Boolean = false) {
		if (!force && !followConversationBottom) return
		feedScroll?.post {
			feedScroll?.fullScroll(View.FOCUS_DOWN)
			followConversationBottom = true
		}
	}

    private fun clearCallViews() {
		updateIndicator?.clearAnimation()
		updateIndicator = null
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
		phoneActionGroups.clear()
		composerView = null
		pauseButton = null
		transcriptButton = null
		muteButton = null
		audioButton = null
		searchButton = null
		savedButton = null
		latestCallSnapshot = CallController.Snapshot()
		latestButton = null
		placeholderTitle = null
		placeholderDetail = null
		searchContextShown = false
		cachedConversationHistory = emptyList()
		savedResponses = emptyList()
    }

    internal fun showUpdateStatus(next: AndroidAppUpdater.Status) = runOnUiThread {
		latestUpdateStatus = next
		renderUpdateIndicator(next)
		renderUpdateDialog(next)
	}

	private fun updateAction(): TextView = object : TextView(this) {
		private val pulsePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply { color = 0xFFFFF3C4.toInt() }

		override fun onDraw(canvas: Canvas) {
			super.onDraw(canvas)
			if (!ValueAnimator.areAnimatorsEnabled() || !isShown) return
			val wave = (sin(SystemClock.uptimeMillis() / 340.0) + 1.0) / 2.0
			pulsePaint.alpha = (80 + wave * 175).toInt()
			canvas.drawCircle(dp(12).toFloat(), height / 2f, dp(4).toFloat(), pulsePaint)
			postInvalidateOnAnimation()
		}
	}.apply {
		text = "Update!"
		textSize = 13f
		setTypeface(typeface, Typeface.BOLD)
		gravity = Gravity.CENTER
		setPadding(dp(22), 0, dp(10), 0)
		minWidth = dp(82)
		isClickable = true
		isFocusable = true
		background = GradientDrawable().apply {
			cornerRadius = dp(19).toFloat()
			setColor(0xFFFFB300.toInt())
		}
		setTextColor(0xFF2B1B00.toInt())
		setOnClickListener { beginAppUpdate() }
		updateIndicator = this
		renderUpdateIndicator(latestUpdateStatus)
	}

	private fun renderUpdateIndicator(next: AndroidAppUpdater.Status) {
		val indicator = updateIndicator ?: return
		if (next == AndroidAppUpdater.Status.Hidden) {
			indicator.clearAnimation()
			indicator.visibility = View.GONE
			return
		}
		indicator.visibility = View.VISIBLE
		indicator.contentDescription = when (next) {
			is AndroidAppUpdater.Status.Available -> "Update available: ${next.versionName}"
			is AndroidAppUpdater.Status.Downloading -> "Downloading update ${next.versionName}"
			is AndroidAppUpdater.Status.Busy -> next.message
			is AndroidAppUpdater.Status.Error -> "Update failed: ${next.message}"
			AndroidAppUpdater.Status.Hidden -> ""
		}
	}

	private fun beginAppUpdate() {
		val versionName = when (val status = latestUpdateStatus) {
			is AndroidAppUpdater.Status.Available -> status.versionName
			is AndroidAppUpdater.Status.Downloading -> status.versionName
			is AndroidAppUpdater.Status.Error, is AndroidAppUpdater.Status.Busy -> lastAppUpdate?.versionName.orEmpty()
			AndroidAppUpdater.Status.Hidden -> ""
		}
		if (versionName.isBlank()) return
		showUpdateDownloadDialog(versionName)
		if (latestUpdateStatus is AndroidAppUpdater.Status.Available || latestUpdateStatus is AndroidAppUpdater.Status.Error) {
			appUpdater.install()
		} else {
			renderUpdateDialog(latestUpdateStatus)
		}
	}

	private fun showUpdateDownloadDialog(versionName: String) {
		updateDialog?.dismiss()
		val content = column().apply {
			setPadding(dp(4), dp(4), dp(4), 0)
			addView(body(versionName).apply { setTypeface(typeface, Typeface.BOLD) }, matchWrap())
			addView(ProgressBar(this@MainActivity, null, android.R.attr.progressBarStyleHorizontal).apply {
				max = 100
				progress = 0
				contentDescription = "Update download progress"
				updateDialogProgress = this
			}, spaced(top = 16))
			addView(helper("Starting download…").apply {
				contentDescription = "Update download details"
				updateDialogDetail = this
			}, spaced(top = 8))
		}
		updateDialog = AlertDialog.Builder(this)
			.setTitle("Downloading update…")
			.setView(content)
			.setPositiveButton("Retry", null)
			.setNegativeButton("Cancel") { _, _ -> appUpdater.cancel() }
			.setCancelable(false)
			.create()
			.apply {
				setOnDismissListener {
					updateDialog = null
					updateDialogDetail = null
					updateDialogProgress = null
				}
				show()
				getButton(AlertDialog.BUTTON_POSITIVE)?.visibility = View.GONE
			}
	}

	private fun renderUpdateDialog(next: AndroidAppUpdater.Status) {
		val dialog = updateDialog ?: return
		val detail = updateDialogDetail
		val progress = updateDialogProgress
		when (next) {
            AndroidAppUpdater.Status.Hidden -> {
				dialog.dismiss()
            }
            is AndroidAppUpdater.Status.Available -> {
				dialog.setTitle("Update available")
				detail?.text = next.versionName
				progress?.visibility = View.GONE
				dialog.getButton(AlertDialog.BUTTON_NEGATIVE)?.text = "Cancel"
				dialog.getButton(AlertDialog.BUTTON_POSITIVE)?.apply {
					text = "Download"
					visibility = View.VISIBLE
					setOnClickListener { appUpdater.install() }
				}
            }
            is AndroidAppUpdater.Status.Downloading -> {
				val percent = if (next.totalBytes > 0) ((next.downloadedBytes * 100) / next.totalBytes).toInt().coerceIn(0, 100) else 0
				dialog.setTitle("Downloading update…")
				dialog.getButton(AlertDialog.BUTTON_NEGATIVE)?.text = "Cancel"
				detail?.text = if (next.totalBytes > 0) {
					"${next.versionName} · $percent% · ${formatUpdateBytes(next.downloadedBytes)} / ${formatUpdateBytes(next.totalBytes)}"
				} else {
					"${next.versionName} · ${formatUpdateBytes(next.downloadedBytes)}"
				}
                progress?.apply {
					isIndeterminate = next.totalBytes <= 0
                    this.progress = percent
                    visibility = View.VISIBLE
                }
				dialog.getButton(AlertDialog.BUTTON_POSITIVE)?.visibility = View.GONE
            }
            is AndroidAppUpdater.Status.Busy -> {
				dialog.setTitle("Preparing update…")
				dialog.getButton(AlertDialog.BUTTON_NEGATIVE)?.text = "Cancel"
				detail?.text = next.message
				progress?.apply { isIndeterminate = true; visibility = View.VISIBLE }
				dialog.getButton(AlertDialog.BUTTON_POSITIVE)?.visibility = View.GONE
            }
            is AndroidAppUpdater.Status.Error -> {
				dialog.setTitle("Update failed")
				detail?.text = next.message
                progress?.visibility = View.GONE
				dialog.getButton(AlertDialog.BUTTON_NEGATIVE)?.text = "Close"
				dialog.getButton(AlertDialog.BUTTON_POSITIVE)?.apply {
					text = "Retry"
					visibility = View.VISIBLE
					setOnClickListener { appUpdater.install() }
				}
            }
        }
    }

	private fun formatUpdateBytes(bytes: Long): String = when {
		bytes >= 1024L * 1024L -> String.format(Locale.getDefault(), "%.1f MB", bytes / (1024.0 * 1024.0))
		bytes >= 1024L -> String.format(Locale.getDefault(), "%.1f KB", bytes / 1024.0)
		else -> "$bytes B"
	}

    private fun showScrollable(content: View) = showContent(ScrollView(this).apply { addView(content, matchWrap()) })

    private fun showContent(content: View) {
        val baseHorizontal = dp(20)
        val baseVertical = dp(16)
        content.setBackgroundColor(themeColor(android.R.attr.colorBackground))
		ViewCompat.setAccessibilityPaneTitle(content, when (screen) {
			Screen.SETUP -> "Koder connection setup"
			Screen.SETTINGS -> "Koder settings"
			Screen.PERMISSIONS -> "Phone permission health"
			Screen.READINESS -> "Voice adjustments"
			Screen.LOADING -> "Koder loading"
			Screen.HOME -> "Voice conversations"
			Screen.SESSION -> selectedKoderSession?.title?.ifBlank { "Koder session" } ?: "Koder session"
			Screen.CHAT -> pendingSession?.title?.ifBlank { "Voice conversation" } ?: "Voice conversation"
		})
        ViewCompat.setOnApplyWindowInsetsListener(content) { view, insets ->
			val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout())
			val ime = insets.getInsets(WindowInsetsCompat.Type.ime())
			view.setPadding(
				baseHorizontal + bars.left,
				baseVertical + bars.top,
				baseHorizontal + bars.right,
				baseVertical + maxOf(bars.bottom, ime.bottom),
			)
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
		ViewCompat.setAccessibilityHeading(this, true)
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
		alpha = if (highContrastEnabled(this@MainActivity)) 1f else 0.72f
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
		private const val ACTION_BLUE = 0xff3867d6.toInt()
		private const val ACTION_GREEN = 0xff16856b.toInt()
		private const val ACTION_ORANGE = 0xffd97706.toInt()
		private const val ACTION_RED = 0xffc2414b.toInt()
		private const val ACTION_NEUTRAL = 0xff596273.toInt()
        private val DISPLAYABLE_TEXT_TYPES = setOf("text/plain", "text/markdown")
		private const val KODER_TOOL_ACTIVITY_MIME = "application/vnd.koder.tool-activity+json"
        private val SERVER_TIMESTAMP = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss 'UTC'", Locale.US)
            .withZone(ZoneOffset.UTC)
    }
}
