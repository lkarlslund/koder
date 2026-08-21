package com.lkarlslund.koder.voice

import android.content.Context
import android.animation.ValueAnimator
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.Path
import android.graphics.RectF
import android.provider.Settings
import android.util.AttributeSet
import android.view.View
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.cos
import kotlin.math.exp
import kotlin.math.max
import kotlin.math.sin
import kotlin.random.Random

enum class VoiceOrbMode { IDLE, PAUSED, CONNECTING, LISTENING, USER_SPEAKING, PROCESSING, WORKING, AI_SPEAKING }

fun voiceOrbMode(stage: CallController.Stage?): VoiceOrbMode = when (stage) {
	CallController.Stage.RECORDING -> VoiceOrbMode.USER_SPEAKING
	CallController.Stage.CONNECTING -> VoiceOrbMode.CONNECTING
	CallController.Stage.TRANSCRIBING, CallController.Stage.PROCESSING -> VoiceOrbMode.PROCESSING
	CallController.Stage.WORKING -> VoiceOrbMode.WORKING
	CallController.Stage.SPEAKING -> VoiceOrbMode.AI_SPEAKING
	CallController.Stage.LISTENING, CallController.Stage.MUTED -> VoiceOrbMode.LISTENING
	CallController.Stage.DISCONNECTED, CallController.Stage.HELD, CallController.Stage.ERROR -> VoiceOrbMode.PAUSED
	else -> VoiceOrbMode.IDLE
}

fun voiceOrbDescription(mode: VoiceOrbMode): String = when (mode) {
	VoiceOrbMode.IDLE -> "Voice conversation inactive"
	VoiceOrbMode.PAUSED -> "Voice conversation paused"
	VoiceOrbMode.CONNECTING -> "Koder is connecting"
	VoiceOrbMode.LISTENING -> "Koder is listening"
	VoiceOrbMode.USER_SPEAKING -> "You are speaking"
	VoiceOrbMode.PROCESSING -> "Koder is thinking"
	VoiceOrbMode.WORKING -> "Koder is using tools"
	VoiceOrbMode.AI_SPEAKING -> "Koder is speaking"
}

fun shouldAnimateVoiceOrb(mode: VoiceOrbMode, systemAnimationsEnabled: Boolean, shown: Boolean): Boolean =
	systemAnimationsEnabled && shown && mode != VoiceOrbMode.IDLE && mode != VoiceOrbMode.PAUSED

fun highContrastEnabled(context: Context): Boolean = runCatching {
	Settings.Secure.getInt(context.contentResolver, "high_text_contrast_enabled", 0) == 1
}.getOrDefault(false)

class VoiceStateOrbView @JvmOverloads constructor(
	context: Context,
	attrs: AttributeSet? = null,
) : View(context, attrs) {
	private data class Star(val angle: Float, val radius: Float, val size: Float, val speed: Float)

	private val stars = Random(0x4b4f4445).let { random ->
		List(96) { Star(random.nextFloat() * 2f * PI.toFloat(), random.nextFloat(), 0.7f + random.nextFloat() * 1.8f, 0.35f + random.nextFloat()) }
	}
	private val paint = Paint(Paint.ANTI_ALIAS_FLAG)
	private val wave = Path()
	private val bounds = RectF()
	private val startedAt = System.nanoTime()
	private var lastFrameAt = startedAt
	private var starTravel = 0f
	private var level = 0.12f
	private var audioWaveform = FloatArray(65)
	private var waveformGain = 1f
	private var lastWaveformAt = 0L

	init {
		setLayerType(LAYER_TYPE_SOFTWARE, null)
		importantForAccessibility = IMPORTANT_FOR_ACCESSIBILITY_YES
		accessibilityLiveRegion = ACCESSIBILITY_LIVE_REGION_POLITE
		contentDescription = voiceOrbDescription(VoiceOrbMode.IDLE)
	}

	var mode: VoiceOrbMode = VoiceOrbMode.IDLE
		set(value) {
			if (field == value) return
			val previous = field
			field = value
			lastFrameAt = System.nanoTime()
			if (value in SPEAKING_MODES && previous != value) resetWaveform()
			contentDescription = voiceOrbDescription(value)
			invalidate()
		}

	fun setAudioLevel(value: Float) {
		level = (level * 0.72f + value.coerceIn(0f, 1f) * 0.28f).coerceAtLeast(0.04f)
	}

	fun setAudioWaveform(values: FloatArray) {
		if (values.isEmpty()) return
		if (audioWaveform.size != values.size) audioWaveform = FloatArray(values.size)
		val peak = values.maxOf { abs(it) }.coerceIn(0f, 1f)
		val targetGain = voiceOrbWaveformGain(peak)
		val gainBlend = if (targetGain > waveformGain) 0.75f else 0.18f
		waveformGain += (targetGain - waveformGain) * gainBlend
		values.forEachIndexed { index, value ->
			val normalized = (value.coerceIn(-1f, 1f) * waveformGain).coerceIn(-1f, 1f)
			audioWaveform[index] = audioWaveform[index] * 0.20f + normalized * 0.80f
		}
		lastWaveformAt = System.nanoTime()
		invalidate()
	}

	private fun resetWaveform() {
		audioWaveform.fill(0f)
		waveformGain = 1f
		lastWaveformAt = 0L
	}

	override fun onDraw(canvas: Canvas) {
		super.onDraw(canvas)
		val size = minOf(width, height).toFloat()
		val radius = size * 0.46f
		val cx = width / 2f
		val cy = height / 2f
		val rimWidth = max(2f, size * 0.012f)
		val contentRadius = radius - rimWidth * 1.5f
		bounds.set(cx - radius, cy - radius, cx + radius, cy + radius)
		paint.style = Paint.Style.FILL
		paint.color = Color.rgb(3, 10, 26)
		canvas.drawCircle(cx, cy, radius, paint)
		canvas.save()
		canvas.clipPath(Path().apply { addCircle(cx, cy, contentRadius, Path.Direction.CW) })
		val animationsEnabled = ValueAnimator.areAnimatorsEnabled()
		val now = System.nanoTime()
		val seconds = if (animationsEnabled) (now - startedAt) / 1_000_000_000f else 0f
		if (shouldAnimateVoiceOrb(mode, animationsEnabled, isShown)) {
			val elapsed = ((now - lastFrameAt) / 1_000_000_000f).coerceIn(0f, 0.1f)
			starTravel += elapsed * voiceStarTravelRate(mode)
		}
		lastFrameAt = now
		drawStars(canvas, cx, cy, contentRadius, starTravel)
		when (mode) {
			VoiceOrbMode.USER_SPEAKING -> drawWave(canvas, cx, cy, contentRadius, Color.rgb(44, 232, 255), now)
			VoiceOrbMode.AI_SPEAKING -> drawWave(canvas, cx, cy, contentRadius, Color.rgb(188, 104, 255), now)
			VoiceOrbMode.WORKING -> drawSpinner(canvas, cx, cy, radius, seconds)
			VoiceOrbMode.PAUSED -> drawResumeSymbol(canvas, cx, cy, radius)
			else -> Unit
		}
		canvas.restore()
		paint.style = Paint.Style.STROKE
		paint.strokeWidth = rimWidth
		paint.color = when (mode) {
			VoiceOrbMode.PAUSED -> Color.rgb(112, 123, 145)
			VoiceOrbMode.USER_SPEAKING -> Color.rgb(44, 232, 255)
			VoiceOrbMode.AI_SPEAKING -> Color.rgb(188, 104, 255)
			VoiceOrbMode.WORKING -> Color.rgb(255, 176, 66)
			else -> Color.rgb(75, 121, 255)
		}
		canvas.drawCircle(cx, cy, radius - paint.strokeWidth, paint)
		if (shouldAnimateVoiceOrb(mode, animationsEnabled, isShown)) postInvalidateOnAnimation()
	}

	private fun drawResumeSymbol(canvas: Canvas, cx: Float, cy: Float, radius: Float) {
		val triangle = Path().apply {
			moveTo(cx - radius * 0.12f, cy - radius * 0.18f)
			lineTo(cx + radius * 0.20f, cy)
			lineTo(cx - radius * 0.12f, cy + radius * 0.18f)
			close()
		}
		paint.style = Paint.Style.FILL
		paint.color = Color.rgb(178, 190, 214)
		canvas.drawPath(triangle, paint)
	}

	private fun drawStars(canvas: Canvas, cx: Float, cy: Float, radius: Float, travel: Float) {
		val warp = mode == VoiceOrbMode.PROCESSING
		val density = resources.displayMetrics.density
		stars.forEach { star ->
			val motion = voiceStarMotion(mode, star.radius, star.speed, travel)
			val radial = motion.radius
			val angle = star.angle
			val x = cx + cos(angle) * radial * radius
			val y = cy + sin(angle) * radial * radius
			val minimumAlpha = if (highContrastEnabled(context)) 175 else 110
			paint.color = Color.argb((minimumAlpha + radial * (255 - minimumAlpha)).toInt(), 160, 205, 255)
			paint.strokeWidth = star.size * density * (0.45f + radial * 1.25f)
			paint.strokeCap = Paint.Cap.ROUND
			if (warp) {
				val tail = max(3f * density, radial * radius * motion.trailFraction)
				canvas.drawLine(x - cos(angle) * tail, y - sin(angle) * tail, x, y, paint)
			} else canvas.drawCircle(x, y, paint.strokeWidth, paint)
		}
	}

	private fun drawWave(canvas: Canvas, cx: Float, cy: Float, radius: Float, color: Int, now: Long) {
		wave.reset()
		val amplitude = radius * (0.28f + level * 0.32f)
		val ageMillis = if (lastWaveformAt == 0L) Long.MAX_VALUE else ((now - lastWaveformAt).coerceAtLeast(0L) / 1_000_000L)
		val decay = voiceOrbWaveformDecay(ageMillis)
		val left = cx - radius * 0.78f
		val width = radius * 1.56f
		audioWaveform.forEachIndexed { index, sample ->
			val fraction = index / (audioWaveform.size - 1).coerceAtLeast(1).toFloat()
			val envelope = sin(fraction * PI).toFloat()
			val y = cy + sample * decay * amplitude * envelope
			val x = left + width * fraction
			if (index == 0) wave.moveTo(x, y) else wave.lineTo(x, y)
		}
		paint.style = Paint.Style.STROKE
		paint.strokeWidth = max(3f, radius * 0.025f)
		paint.strokeCap = Paint.Cap.ROUND
		paint.color = color
		paint.setShadowLayer(radius * 0.08f, 0f, 0f, color)
		canvas.drawPath(wave, paint)
		paint.clearShadowLayer()
	}

	private fun drawSpinner(canvas: Canvas, cx: Float, cy: Float, radius: Float, seconds: Float) {
		paint.style = Paint.Style.STROKE
		paint.strokeWidth = max(4f, radius * 0.045f)
		paint.strokeCap = Paint.Cap.ROUND
		paint.color = Color.rgb(255, 176, 66)
		val spinner = radius * 0.30f
		canvas.drawArc(RectF(cx - spinner, cy - spinner, cx + spinner, cy + spinner), seconds * 190f, 255f, false, paint)
	}
}

internal data class VoiceStarMotion(val radius: Float, val trailFraction: Float)

// voiceStarMotion keeps the field moving toward the viewer in every active
// state and increases speed and trails only while the model processes.
internal fun voiceStarMotion(mode: VoiceOrbMode, initialRadius: Float, speed: Float, travel: Float): VoiceStarMotion {
	val radius = (initialRadius + travel.coerceAtLeast(0f) * speed.coerceAtLeast(0f)) % 1f
	val trail = if (mode == VoiceOrbMode.PROCESSING) 0.12f + radius * 0.24f else 0f
	return VoiceStarMotion(radius, trail)
}

internal fun voiceStarTravelRate(mode: VoiceOrbMode): Float =
	if (mode == VoiceOrbMode.PROCESSING) 0.58f else 0.075f

internal fun voiceOrbWaveformGain(peak: Float): Float {
	val boundedPeak = peak.coerceIn(0f, 1f)
	if (boundedPeak < 0.004f) return 1f
	return (0.78f / boundedPeak).coerceIn(1f, 10f)
}

internal fun voiceOrbWaveformDecay(ageMillis: Long): Float {
	if (ageMillis <= WAVEFORM_HOLD_MILLIS) return 1f
	val decaySeconds = (ageMillis - WAVEFORM_HOLD_MILLIS) / 1_000f
	return exp(-decaySeconds * 7f).coerceIn(0f, 1f)
}

internal fun voiceOrbSizeDp(fontScale: Float): Int = if (fontScale >= 1.3f) 232 else 300

private val SPEAKING_MODES = setOf(VoiceOrbMode.USER_SPEAKING, VoiceOrbMode.AI_SPEAKING)
private const val WAVEFORM_HOLD_MILLIS = 90L
