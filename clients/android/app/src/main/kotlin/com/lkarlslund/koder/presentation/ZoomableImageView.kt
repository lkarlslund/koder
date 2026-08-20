package com.lkarlslund.koder.presentation

import android.content.Context
import android.graphics.Matrix
import android.graphics.drawable.Drawable
import android.view.GestureDetector
import android.view.MotionEvent
import android.view.ScaleGestureDetector
import android.widget.ImageView
import kotlin.math.min

class ZoomableImageView(context: Context) : ImageView(context) {
	private val transform = Matrix()
	private var scale = 1f
	private val scaleDetector = ScaleGestureDetector(context, object : ScaleGestureDetector.SimpleOnScaleGestureListener() {
		override fun onScale(detector: ScaleGestureDetector): Boolean {
			val next = (scale * detector.scaleFactor).coerceIn(MIN_SCALE, MAX_SCALE)
			val factor = next / scale
			scale = next
			transform.postScale(factor, factor, detector.focusX, detector.focusY)
			imageMatrix = transform
			return true
		}
	})
	private val gestureDetector = GestureDetector(context, object : GestureDetector.SimpleOnGestureListener() {
		override fun onDown(event: MotionEvent): Boolean = true

		override fun onScroll(first: MotionEvent?, current: MotionEvent, distanceX: Float, distanceY: Float): Boolean {
			transform.postTranslate(-distanceX, -distanceY)
			imageMatrix = transform
			return true
		}

		override fun onDoubleTap(event: MotionEvent): Boolean {
			resetImage()
			return true
		}
	})

	init {
		scaleType = ScaleType.MATRIX
		contentDescription = "Fullscreen image. Pinch to zoom, drag to pan, double tap to reset"
	}

	override fun onTouchEvent(event: MotionEvent): Boolean {
		parent?.requestDisallowInterceptTouchEvent(event.actionMasked != MotionEvent.ACTION_UP && event.actionMasked != MotionEvent.ACTION_CANCEL)
		val scaled = scaleDetector.onTouchEvent(event)
		val gestured = gestureDetector.onTouchEvent(event)
		return scaled || gestured || super.onTouchEvent(event)
	}

	override fun onSizeChanged(width: Int, height: Int, oldWidth: Int, oldHeight: Int) {
		super.onSizeChanged(width, height, oldWidth, oldHeight)
		if (width > 0 && height > 0) resetImage()
	}

	override fun setImageDrawable(drawable: Drawable?) {
		super.setImageDrawable(drawable)
		post(::resetImage)
	}

	fun rotateClockwise() {
		transform.postRotate(90f, width / 2f, height / 2f)
		imageMatrix = transform
	}

	fun resetImage() {
		val source = drawable ?: return
		if (width <= 0 || height <= 0 || source.intrinsicWidth <= 0 || source.intrinsicHeight <= 0) return
		val fit = min(width.toFloat() / source.intrinsicWidth, height.toFloat() / source.intrinsicHeight)
		val shownWidth = source.intrinsicWidth * fit
		val shownHeight = source.intrinsicHeight * fit
		transform.reset()
		transform.postScale(fit, fit)
		transform.postTranslate((width - shownWidth) / 2f, (height - shownHeight) / 2f)
		scale = 1f
		imageMatrix = transform
	}

	companion object {
		private const val MIN_SCALE = 1f
		private const val MAX_SCALE = 8f
	}
}
