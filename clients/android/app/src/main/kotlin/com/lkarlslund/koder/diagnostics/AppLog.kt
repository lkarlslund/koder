package com.lkarlslund.koder.diagnostics

import android.content.Context
import android.util.Log
import android.widget.Toast
import org.json.JSONObject
import java.io.File
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

data class AppLogEntry(val seq: Long, val time: Instant, val level: String, val tag: String, val message: String) {
	fun toJSON(): JSONObject = JSONObject()
		.put("seq", seq)
		.put("time", time.toString())
		.put("level", level)
		.put("tag", tag)
		.put("message", message)

	companion object {
		fun fromJSON(line: String): AppLogEntry? = runCatching {
			val value = JSONObject(line)
			AppLogEntry(value.getLong("seq"), Instant.parse(value.getString("time")), value.getString("level"), value.getString("tag"), value.getString("message"))
		}.getOrNull()
	}
}

/**
 * The app's own diagnostic log, kept on the phone across restarts so a problem
 * can be read after the fact. Two files of [ROTATE_BYTES] each hold the newest
 * lines. Writes happen on a background thread so audio threads never wait on disk.
 */
object AppLog {
	private const val ROTATE_BYTES = 512 * 1024
	private const val MAX_MESSAGE = 4000
	private const val ANDROID_TAG = "KoderApp"

	@Volatile private var directory: File? = null
	private var seq = 0L
	@Volatile var epoch: String = ""
		private set
	private var bytes = 0L
	private var listener: ((AppLogEntry) -> Unit)? = null
	private val writer: ExecutorService = Executors.newSingleThreadExecutor { runnable ->
		Thread(runnable, "koder-app-log").apply { isDaemon = true }
	}

	@Synchronized
	fun init(context: Context) {
		if (directory != null) return
		val dir = File(context.applicationContext.filesDir, "diagnostics").apply { mkdirs() }
		val preferences = context.getSharedPreferences("koder_diagnostics", Context.MODE_PRIVATE)
		val last = (readFile(File(dir, "log.current")).lastOrNull() ?: readFile(File(dir, "log.previous")).lastOrNull())
		epoch = preferences.getString("log_epoch", "").orEmpty()
		if (last == null || epoch.isBlank()) {
			epoch = UUID.randomUUID().toString()
			preferences.edit().putString("log_epoch", epoch).apply()
			File(dir, "log.current").delete()
			File(dir, "log.previous").delete()
		}
		seq = last?.seq ?: 0L
		bytes = File(dir, "log.current").length()
		directory = dir
		installCrashHandler()
	}

	/** Called on the log thread after each entry, e.g. to sync soon after a warning. */
	@Synchronized
	fun setListener(value: ((AppLogEntry) -> Unit)?) {
		listener = value
	}

	fun d(tag: String, message: String) = log("D", tag, message)
	fun i(tag: String, message: String) = log("I", tag, message)
	fun w(tag: String, message: String, error: Throwable? = null) = log("W", tag, withError(message, error))
	fun e(tag: String, message: String, error: Throwable? = null) = log("E", tag, withError(message, error))

	fun log(level: String, tag: String, message: String, time: Instant = Instant.now()) {
		// Queue under the lock so the file stays in sequence order; a sync
		// that saw a later line would otherwise skip an earlier one for good.
		val entry = synchronized(this) {
			next(level, tag, message, time)?.also { writer.execute { append(it) } }
		} ?: return
		// Captured logcat lines are already in logcat.
		if (tag != LOGCAT_TAG) mirror(entry)
	}

	/** Entries after [afterSeq], oldest first, at most [limit]. Call off the main thread. */
	fun entriesAfter(afterSeq: Long, limit: Int): List<AppLogEntry> {
		flush()
		val dir = directory ?: return emptyList()
		return synchronized(this) {
			(readFile(File(dir, "log.previous")) + readFile(File(dir, "log.current")))
				.filter { it.seq > afterSeq }
				.take(limit)
		}
	}

	/** Waits briefly for queued writes, so a read sees them. */
	fun flush() {
		runCatching { writer.submit {}.get(2, TimeUnit.SECONDS) }
	}

	@Synchronized
	private fun next(level: String, tag: String, message: String, time: Instant = Instant.now()): AppLogEntry? {
		if (directory == null) return null
		seq++
		return AppLogEntry(seq, time, level, tag.take(64), message.take(MAX_MESSAGE))
	}

	private fun append(entry: AppLogEntry) {
		val dir = directory ?: return
		val line = entry.toJSON().toString() + "\n"
		synchronized(this) {
			val current = File(dir, "log.current")
			if (bytes + line.length > ROTATE_BYTES) {
				File(dir, "log.previous").delete()
				current.renameTo(File(dir, "log.previous"))
				bytes = 0
			}
			runCatching { current.appendText(line) }.onSuccess { bytes += line.length }
		}
		val notify = synchronized(this) { listener }
		runCatching { notify?.invoke(entry) }
	}

	private fun mirror(entry: AppLogEntry) {
		val text = "${entry.tag}: ${entry.message}"
		when (entry.level) {
			"E" -> Log.e(ANDROID_TAG, text)
			"W" -> Log.w(ANDROID_TAG, text)
			"I" -> Log.i(ANDROID_TAG, text)
			else -> Log.d(ANDROID_TAG, text)
		}
	}

	private fun readFile(file: File): List<AppLogEntry> =
		if (!file.exists()) emptyList() else runCatching { file.readLines().mapNotNull(AppLogEntry::fromJSON) }.getOrDefault(emptyList())

	private fun withError(message: String, error: Throwable?) =
		if (error == null) message else "$message: ${error.javaClass.simpleName}: ${error.message.orEmpty()}"

	/** Writes the crash synchronously, because the process dies right after. */
	private fun installCrashHandler() {
		val previous = Thread.getDefaultUncaughtExceptionHandler()
		Thread.setDefaultUncaughtExceptionHandler { thread, error ->
			runCatching {
				val entry = next("E", "crash", "Uncaught in ${thread.name}: ${Log.getStackTraceString(error)}")
				if (entry != null) {
					mirror(entry)
					append(entry)
				}
			}
			previous?.uncaughtException(thread, error)
		}
	}

	/** Logcat lines are captured under this tag; the app's own mirror is excluded. */
	internal const val LOGCAT_TAG = "logcat"
	internal val logcatFilter = arrayOf("*:I", "$ANDROID_TAG:S")
}

/** Shows a popup and keeps it in [AppLog], since a toast disappears in seconds. */
fun showPopup(context: Context, message: CharSequence?, duration: Int) {
	val text = message?.toString().orEmpty()
	AppLog.log(if (duration == Toast.LENGTH_LONG) "W" else "I", "popup", text)
	Toast.makeText(context, text, duration).show()
}
