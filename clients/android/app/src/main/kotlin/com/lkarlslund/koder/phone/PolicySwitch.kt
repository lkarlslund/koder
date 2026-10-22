package com.lkarlslund.koder.phone

import android.animation.ValueAnimator
import android.annotation.SuppressLint
import android.content.Context
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.Path
import android.graphics.RectF
import android.graphics.Typeface
import android.view.HapticFeedbackConstants
import android.view.MotionEvent
import android.view.View
import android.view.ViewConfiguration
import android.view.animation.DecelerateInterpolator
import androidx.core.graphics.ColorUtils
import androidx.core.view.ViewCompat
import androidx.core.view.accessibility.AccessibilityNodeInfoCompat.AccessibilityActionCompat
import kotlin.math.abs
import kotlin.math.roundToInt

/**
 * A sliding No / Ask / Yes switch for a phone action policy. The thumb glides
 * to the word that is tapped, or follows a drag and snaps to the nearest word,
 * and its color blends from red through yellow to green on the way.
 */
@SuppressLint("ViewConstructor")
class PolicySwitch(context: Context, private val trackColor: Int, private val labelColor: Int) : View(context) {
	/** Called when the user picks a policy, not when [select] sets one. */
	var onChange: ((PhoneActionPolicy) -> Unit)? = null

	var policy = PhoneActionPolicy.OFF
		private set

	/** Thumb position: 0 is No, 1 is Ask, 2 is Yes. */
	private var position = 0f
	private var animator: ValueAnimator? = null
	private var downX = 0f
	private var dragging = false
	private val touchSlop = ViewConfiguration.get(context).scaledTouchSlop
	private val density = resources.displayMetrics.density
	private val trackPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply { color = trackColor }
	private val thumbPaint = Paint(Paint.ANTI_ALIAS_FLAG)
	private val textPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
		textAlign = Paint.Align.CENTER
		textSize = 14 * resources.displayMetrics.scaledDensity
		typeface = Typeface.create(Typeface.DEFAULT, Typeface.BOLD)
	}
	private val rect = RectF()
	private val thumbPath = Path()

	init {
		isFocusable = true
		isClickable = true
		POLICIES.forEachIndexed { index, value ->
			ViewCompat.addAccessibilityAction(this, LABELS[index]) { _, _ -> pick(value); true }
		}
		updateState()
	}

	/** Shows [value] without notifying [onChange], e.g. when a permission was refused. */
	fun select(value: PhoneActionPolicy, animate: Boolean = isAttachedToWindow) {
		if (value == policy && animator == null && position == index(value).toFloat()) return
		policy = value
		moveTo(index(value).toFloat(), animate)
		updateState()
	}

	private fun pick(value: PhoneActionPolicy) {
		val changed = value != policy
		select(value)
		if (changed) {
			performHapticFeedback(HapticFeedbackConstants.CLOCK_TICK)
			onChange?.invoke(value)
		}
	}

	private fun index(value: PhoneActionPolicy) = POLICIES.indexOf(value).coerceAtLeast(0)

	private fun moveTo(target: Float, animate: Boolean) {
		animator?.cancel()
		animator = null
		if (!animate) {
			position = target
			invalidate()
			return
		}
		animator = ValueAnimator.ofFloat(position, target).apply {
			duration = (180 + 90 * abs(target - position)).toLong()
			interpolator = DecelerateInterpolator()
			addUpdateListener {
				position = it.animatedValue as Float
				invalidate()
			}
			start()
		}
	}

	private fun updateState() {
		ViewCompat.setStateDescription(this, LABELS[index(policy)])
	}

	override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
		setMeasuredDimension(
			resolveSize((216 * density).roundToInt(), widthMeasureSpec),
			resolveSize((40 * density).roundToInt(), heightMeasureSpec),
		)
	}

	override fun onDraw(canvas: Canvas) {
		val inset = 3 * density
		val segment = width / 3f
		val radius = height / 2f
		rect.set(0f, 0f, width.toFloat(), height.toFloat())
		canvas.drawRoundRect(rect, radius, radius, trackPaint)
		val thumb = colorAt(position)
		thumbPaint.color = thumb
		rect.set(position * segment + inset, inset, (position + 1) * segment - inset, height - inset)
		canvas.drawRoundRect(rect, radius - inset, radius - inset, thumbPaint)
		// Labels are drawn plainly, then again in the thumb's text color
		// clipped to the thumb, so each letter is crisp wherever it sits.
		drawLabels(canvas, segment, labelColor)
		thumbPath.reset()
		thumbPath.addRoundRect(rect, radius - inset, radius - inset, Path.Direction.CW)
		canvas.save()
		canvas.clipPath(thumbPath)
		drawLabels(canvas, segment, if (ColorUtils.calculateLuminance(thumb) > 0.35) DARK_TEXT else Color.WHITE)
		canvas.restore()
	}

	private fun drawLabels(canvas: Canvas, segment: Float, color: Int) {
		textPaint.color = color
		val baseline = height / 2f - (textPaint.descent() + textPaint.ascent()) / 2
		LABELS.forEachIndexed { index, label -> canvas.drawText(label, segment * (index + 0.5f), baseline, textPaint) }
	}

	/** Blends hue, not RGB, so red turns orange and yellow turns lime rather than brown or olive. */
	private fun colorAt(position: Float): Int =
		if (position <= 1) blendHue(RED, YELLOW, position) else blendHue(YELLOW, GREEN, position - 1)

	private fun blendHue(from: Int, to: Int, fraction: Float): Int {
		val a = FloatArray(3).also { ColorUtils.colorToHSL(from, it) }
		val b = FloatArray(3).also { ColorUtils.colorToHSL(to, it) }
		var delta = b[0] - a[0]
		if (delta > 180) delta -= 360
		if (delta < -180) delta += 360
		val hue = (a[0] + delta * fraction + 360) % 360
		return ColorUtils.HSLToColor(floatArrayOf(hue, a[1] + (b[1] - a[1]) * fraction, a[2] + (b[2] - a[2]) * fraction))
	}

	@SuppressLint("ClickableViewAccessibility")
	override fun onTouchEvent(event: MotionEvent): Boolean {
		if (!isEnabled) return false
		val segment = width / 3f
		when (event.actionMasked) {
			MotionEvent.ACTION_DOWN -> {
				downX = event.x
				dragging = false
			}
			MotionEvent.ACTION_MOVE -> {
				if (!dragging && abs(event.x - downX) > touchSlop) {
					dragging = true
					animator?.cancel()
					parent?.requestDisallowInterceptTouchEvent(true)
				}
				if (dragging) {
					position = (event.x / segment - 0.5f).coerceIn(0f, 2f)
					invalidate()
				}
			}
			MotionEvent.ACTION_UP -> {
				val target = if (dragging) position.roundToInt() else (event.x / segment).toInt().coerceIn(0, 2)
				dragging = false
				// Also settles a dragged thumb when the policy did not change.
				pick(POLICIES[target])
				performClick()
			}
			MotionEvent.ACTION_CANCEL -> {
				dragging = false
				moveTo(index(policy).toFloat(), true)
			}
		}
		return true
	}

	override fun performClick(): Boolean {
		super.performClick()
		return true
	}

	companion object {
		private val POLICIES = listOf(PhoneActionPolicy.OFF, PhoneActionPolicy.ASK, PhoneActionPolicy.ON)
		val LABELS = listOf("No", "Ask", "Yes")
		private const val RED = 0xffc2414b.toInt()
		private const val YELLOW = 0xffe5a50a.toInt()
		private const val GREEN = 0xff16856b.toInt()
		private const val DARK_TEXT = 0xff1f1f1f.toInt()
	}
}
