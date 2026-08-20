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
		List(72) { Star(random.nextFloat() * 2f * PI.toFloat(), random.nextFloat(), 0.7f + random.nextFloat() * 1.8f, 0.35f + random.nextFloat()) }
	}
	private val paint = Paint(Paint.ANTI_ALIAS_FLAG)
	private val wave = Path()
	private val bounds = RectF()
	private var startedAt = System.nanoTime()
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
			startedAt = System.nanoTime()
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
		val seconds = if (animationsEnabled) (System.nanoTime() - startedAt) / 1_000_000_000f else 0f
		drawStars(canvas, cx, cy, radius, seconds)
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

	private fun drawStars(canvas: Canvas, cx: Float, cy: Float, radius: Float, seconds: Float) {
		val warp = mode == VoiceOrbMode.PROCESSING
		stars.forEachIndexed { index, star ->
			val drift = if (warp) seconds * star.speed * 1.8f else seconds * star.speed * 0.055f
			val radial = if (warp) (star.radius + drift) % 1f else star.radius
			val angle = star.angle + if (warp) 0f else drift + index * 0.0007f
			val x = cx + cos(angle) * radial * radius
			val y = cy + sin(angle) * radial * radius
			val minimumAlpha = if (highContrastEnabled(context)) 175 else 110
			paint.color = Color.argb((minimumAlpha + radial * (255 - minimumAlpha)).toInt(), 160, 205, 255)
			paint.strokeWidth = star.size
			if (warp) {
				val tail = max(5f, radial * radius * 0.24f)
				canvas.drawLine(x - cos(angle) * tail, y - sin(angle) * tail, x, y, paint)
			} else canvas.drawCircle(x, y, star.size, paint)
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
