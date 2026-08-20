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
import kotlin.math.cos
import kotlin.math.max
import kotlin.math.sin
import kotlin.random.Random

enum class VoiceOrbMode { IDLE, LISTENING, USER_SPEAKING, PROCESSING, WORKING, AI_SPEAKING }

fun voiceOrbMode(stage: CallController.Stage?): VoiceOrbMode = when (stage) {
	CallController.Stage.RECORDING -> VoiceOrbMode.USER_SPEAKING
	CallController.Stage.TRANSCRIBING, CallController.Stage.PROCESSING, CallController.Stage.CONNECTING -> VoiceOrbMode.PROCESSING
	CallController.Stage.WORKING -> VoiceOrbMode.WORKING
	CallController.Stage.SPEAKING -> VoiceOrbMode.AI_SPEAKING
	CallController.Stage.LISTENING, CallController.Stage.MUTED -> VoiceOrbMode.LISTENING
	else -> VoiceOrbMode.IDLE
}

fun voiceOrbDescription(mode: VoiceOrbMode): String = when (mode) {
	VoiceOrbMode.IDLE -> "Voice conversation inactive"
	VoiceOrbMode.LISTENING -> "Koder is listening"
	VoiceOrbMode.USER_SPEAKING -> "You are speaking"
	VoiceOrbMode.PROCESSING -> "Koder is thinking"
	VoiceOrbMode.WORKING -> "Koder is using tools"
	VoiceOrbMode.AI_SPEAKING -> "Koder is speaking"
}

fun shouldAnimateVoiceOrb(mode: VoiceOrbMode, systemAnimationsEnabled: Boolean, shown: Boolean): Boolean =
	systemAnimationsEnabled && shown && mode != VoiceOrbMode.IDLE

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

	init {
		setLayerType(LAYER_TYPE_SOFTWARE, null)
		importantForAccessibility = IMPORTANT_FOR_ACCESSIBILITY_YES
		accessibilityLiveRegion = ACCESSIBILITY_LIVE_REGION_POLITE
		contentDescription = voiceOrbDescription(VoiceOrbMode.IDLE)
	}

	var mode: VoiceOrbMode = VoiceOrbMode.IDLE
		set(value) {
			if (field == value) return
			field = value
			lastFrameAt = System.nanoTime()
			contentDescription = voiceOrbDescription(value)
			invalidate()
		}

	fun setAudioLevel(value: Float) {
		level = (level * 0.72f + value.coerceIn(0f, 1f) * 0.28f).coerceAtLeast(0.04f)
	}

	override fun onDraw(canvas: Canvas) {
		super.onDraw(canvas)
		val size = minOf(width, height).toFloat()
		val radius = size * 0.46f
		val cx = width / 2f
		val cy = height / 2f
		bounds.set(cx - radius, cy - radius, cx + radius, cy + radius)
		paint.style = Paint.Style.FILL
		paint.color = Color.rgb(3, 10, 26)
		canvas.drawCircle(cx, cy, radius, paint)
		canvas.save()
		canvas.clipPath(Path().apply { addCircle(cx, cy, radius, Path.Direction.CW) })
		val animationsEnabled = ValueAnimator.areAnimatorsEnabled()
		val now = System.nanoTime()
		val seconds = if (animationsEnabled) (now - startedAt) / 1_000_000_000f else 0f
		if (animationsEnabled && mode != VoiceOrbMode.IDLE) {
			val elapsed = ((now - lastFrameAt) / 1_000_000_000f).coerceIn(0f, 0.1f)
			starTravel += elapsed * voiceStarTravelRate(mode)
		}
		lastFrameAt = now
		drawStars(canvas, cx, cy, radius, starTravel)
		when (mode) {
			VoiceOrbMode.USER_SPEAKING -> drawWave(canvas, cx, cy, radius, seconds, Color.rgb(44, 232, 255))
			VoiceOrbMode.AI_SPEAKING -> drawWave(canvas, cx, cy, radius, seconds, Color.rgb(188, 104, 255))
			VoiceOrbMode.WORKING -> drawSpinner(canvas, cx, cy, radius, seconds)
			else -> Unit
		}
		canvas.restore()
		paint.style = Paint.Style.STROKE
		paint.strokeWidth = max(2f, size * 0.012f)
		paint.color = when (mode) {
			VoiceOrbMode.USER_SPEAKING -> Color.rgb(44, 232, 255)
			VoiceOrbMode.AI_SPEAKING -> Color.rgb(188, 104, 255)
			VoiceOrbMode.WORKING -> Color.rgb(255, 176, 66)
			else -> Color.rgb(75, 121, 255)
		}
		canvas.drawCircle(cx, cy, radius - paint.strokeWidth, paint)
		if (shouldAnimateVoiceOrb(mode, animationsEnabled, isShown)) postInvalidateOnAnimation()
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

	private fun drawWave(canvas: Canvas, cx: Float, cy: Float, radius: Float, seconds: Float, color: Int) {
		wave.reset()
		val amplitude = radius * (0.10f + level * 0.42f)
		val left = cx - radius * 0.78f
		val width = radius * 1.56f
		repeat(65) { index ->
			val fraction = index / 64f
			val envelope = sin(fraction * PI).toFloat()
			val y = cy + sin(fraction * 8f * PI.toFloat() - seconds * 9f) * amplitude * envelope * (0.7f + 0.3f * sin(fraction * 23f + seconds))
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
	if (mode == VoiceOrbMode.PROCESSING) 0.58f else 0.032f

internal fun voiceOrbSizeDp(fontScale: Float): Int = if (fontScale >= 1.3f) 232 else 300
