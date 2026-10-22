package com.lkarlslund.koder.diagnostics

import android.content.Context
import android.util.Log
import android.widget.Toast
import org.json.JSONObject
import java.io.File
import java.time.Instant
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
	private var bytes = 0L
	private var logcatAfterMillis = 0L
	private val writer: ExecutorService = Executors.newSingleThreadExecutor { runnable ->
		Thread(runnable, "koder-app-log").apply { isDaemon = true }
	}

	@Synchronized
	fun init(context: Context) {
		if (directory != null) return
		val dir = File(context.applicationContext.filesDir, "diagnostics").apply { mkdirs() }
		val last = readFile(File(dir, "log.current")).lastOrNull() ?: readFile(File(dir, "log.previous")).lastOrNull()
		seq = last?.seq ?: 0L
		logcatAfterMillis = (readFile(File(dir, "log.previous")) + readFile(File(dir, "log.current")))
			.lastOrNull { it.tag == LOGCAT_TAG }?.time?.toEpochMilli() ?: 0L
		bytes = File(dir, "log.current").length()
		directory = dir
		installCrashHandler()
	}

	fun d(tag: String, message: String) = log("D", tag, message)
	fun i(tag: String, message: String) = log("I", tag, message)
	fun w(tag: String, message: String, error: Throwable? = null) = log("W", tag, withError(message, error))
	fun e(tag: String, message: String, error: Throwable? = null) = log("E", tag, withError(message, error))

	fun log(level: String, tag: String, message: String, time: Instant = Instant.now()) {
		// Queue under the lock so the file stays in sequence order.
		val entry = synchronized(this) {
			next(level, tag, message, time)?.also { writer.execute { append(it) } }
		} ?: return
		// Captured logcat lines are already in logcat.
		if (tag != LOGCAT_TAG) mirror(entry)
	}

	/**
	 * The newest [limit] entries matching [filter], oldest first, after copying
	 * in the app process's newer logcat lines. Call off the main thread.
	 */
	fun query(filter: AppLogFilter, limit: Int): List<AppLogEntry> {
		captureLogcat()
		flush()
		val dir = directory ?: return emptyList()
		val entries = synchronized(this) { readFile(File(dir, "log.previous")) + readFile(File(dir, "log.current")) }
		return entries.filter(filter::matches).takeLast(limit)
	}

	/** Copies this process's newer logcat lines (Android's own audio and system messages) into the log. */
	private fun captureLogcat() {
		val lines = runCatching {
			val process = ProcessBuilder(listOf("logcat", "-v", "epoch", "-t", LOGCAT_LINES.toString(), "--pid=${android.os.Process.myPid()}") + logcatFilter)
				.redirectErrorStream(true).start()
			val output = process.inputStream.bufferedReader().readLines()
			process.waitFor(5, TimeUnit.SECONDS)
			output
		}.getOrDefault(emptyList())
		synchronized(this) {
			val fresh = parseLogcat(lines, logcatAfterMillis)
			fresh.forEach { (time, level, message) -> log(level, LOGCAT_TAG, message, Instant.ofEpochMilli(time)) }
			fresh.lastOrNull()?.let { logcatAfterMillis = it.first }
		}
	}

	private val logcatLine = Regex("""^\s*(\d+)\.(\d{3})\s+\d+\s+\d+\s+([VDIWEF])\s+(.*?)\s*: (.*)$""")

	/** Parses `logcat -v epoch` lines newer than [afterMillis] into time, level, and message. */
	internal fun parseLogcat(lines: List<String>, afterMillis: Long): List<Triple<Long, String, String>> = lines.mapNotNull { line ->
		val match = logcatLine.matchEntire(line) ?: return@mapNotNull null
		val (seconds, millis, level, tag, message) = match.destructured
		val time = seconds.toLong() * 1000 + millis.toLong()
		if (time <= afterMillis) return@mapNotNull null
		val appLevel = when (level) { "V", "D" -> "D"; "I" -> "I"; "W" -> "W"; else -> "E" }
		Triple(time, appLevel, "$tag: $message")
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
	private const val LOGCAT_LINES = 2000
}

/** Selects log entries for the phone_logs tool. Levels are D, I, W and E. */
data class AppLogFilter(
	val sinceMillis: Long = 0,
	val minLevel: String = "D",
	val tag: String = "",
	val query: String = "",
) {
	fun matches(entry: AppLogEntry): Boolean =
		entry.time.toEpochMilli() >= sinceMillis &&
			LEVELS.indexOf(entry.level).coerceAtLeast(0) >= LEVELS.indexOf(minLevel).coerceAtLeast(0) &&
			(tag.isBlank() || entry.tag == tag) &&
			(query.isBlank() || entry.tag.contains(query, ignoreCase = true) || entry.message.contains(query, ignoreCase = true))

	companion object {
		val LEVELS = listOf("D", "I", "W", "E")
	}
}

/** Shows a popup and keeps it in [AppLog], since a toast disappears in seconds. */
fun showPopup(context: Context, message: CharSequence?, duration: Int) {
	val text = message?.toString().orEmpty()
	AppLog.log(if (duration == Toast.LENGTH_LONG) "W" else "I", "popup", text)
	Toast.makeText(context, text, duration).show()
}
