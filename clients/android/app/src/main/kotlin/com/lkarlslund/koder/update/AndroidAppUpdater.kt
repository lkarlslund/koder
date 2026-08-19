package com.lkarlslund.koder.update

import android.app.Activity
import android.content.Intent
import android.content.pm.PackageInfo
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.FileProvider
import com.lkarlslund.koder.voice.AppUpdate
import com.lkarlslund.koder.voice.VoiceProtocol
import okhttp3.Call
import okhttp3.Callback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import java.io.File
import java.io.IOException
import java.security.MessageDigest
import java.util.concurrent.TimeUnit

class AndroidAppUpdater(
    private val activity: Activity,
    private val listener: (Status) -> Unit,
    private val client: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(20, TimeUnit.SECONDS)
        .readTimeout(5, TimeUnit.MINUTES)
        .build(),
) {
    sealed interface Status {
        data object Hidden : Status
        data class Available(val versionName: String) : Status
        data class Busy(val message: String) : Status
        data class Error(val message: String) : Status
    }

    private data class Candidate(val update: AppUpdate, val server: String, val token: String)

    private var candidate: Candidate? = null
    private var download: Call? = null

    fun consider(update: AppUpdate?, server: String, token: String) {
        val next = update?.let { Candidate(it, server.trim(), token.trim()) }
        val rejection = next?.let { candidate ->
            runCatching { UpdatePolicy.rejection(candidate.update, installedApp()) }
                .getOrElse { it.message ?: "Could not verify installed Koder" }
        }
        candidate = next?.takeIf { rejection == null }
        listener(candidate?.let { Status.Available(it.update.versionName) } ?: Status.Hidden)
    }

    fun install() {
        val selected = candidate ?: return
        if (!activity.packageManager.canRequestPackageInstalls()) {
            listener(Status.Error("Allow Koder to install updates, then tap Update again"))
            activity.startActivity(
                Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${activity.packageName}")),
            )
            return
        }
        val resolved = runCatching {
            VoiceProtocol.resourceUrl(selected.server, selected.update.downloadUri)
        }.getOrElse {
            listener(Status.Error(it.message ?: "Update address is invalid"))
            return
        }
        if (!VoiceProtocol.isSameOrigin(selected.server, resolved)) {
            listener(Status.Error("Update download is not on the Koder server"))
            return
        }
        listener(Status.Busy("Downloading ${selected.update.versionName}…"))
        val request = Request.Builder().url(resolved)
            .apply { if (selected.token.isNotBlank()) header("Authorization", "Bearer ${selected.token}") }
            .build()
        download?.cancel()
        download = client.newCall(request).also { call ->
            call.enqueue(object : Callback {
                override fun onFailure(call: Call, e: IOException) {
                    activity.runOnUiThread {
                        listener(Status.Error(e.message ?: "Update download failed"))
                    }
                }

                override fun onResponse(call: Call, response: Response) {
                    response.use {
                        try {
                            require(it.isSuccessful) { "Update download returned HTTP ${it.code}" }
                            require(VoiceProtocol.isSameOrigin(selected.server, it.request.url.toString())) {
                                "Update download redirected away from Koder"
                            }
                            val directory = File(activity.filesDir, "updates").apply { mkdirs() }
                            val temporary = File(directory, "koder-${selected.update.versionCode}.apk.part")
                            val complete = File(directory, "koder-${selected.update.versionCode}.apk")
                            try {
                                it.body.byteStream().use { input ->
                                    UpdatePolicy.writeVerified(input, temporary, selected.update)
                                }
                                verifyArchive(temporary, selected.update)
                                if (complete.exists()) require(complete.delete()) { "Could not replace cached update" }
                                require(temporary.renameTo(complete)) { "Could not finalize downloaded update" }
                            } finally {
                                temporary.delete()
                            }
                            activity.runOnUiThread { openInstaller(complete) }
                        } catch (failure: Exception) {
                            activity.runOnUiThread {
                                listener(Status.Error(failure.message ?: "Update verification failed"))
                            }
                        }
                    }
                }
            })
        }
    }

    fun close() {
        download?.cancel()
        download = null
    }

    private fun installedApp(): InstalledApp {
        val info = packageInfo(activity.packageName)
        return InstalledApp(activity.packageName, info.longVersionCode, certificateSHA256(info))
    }

    private fun verifyArchive(file: File, update: AppUpdate) {
        val info = archiveInfo(file) ?: error("Downloaded file is not an Android application")
        require(info.packageName == update.applicationId) { "Downloaded update has a different application ID" }
        require(info.longVersionCode == update.versionCode) { "Downloaded update has a different version" }
        require(certificateSHA256(info).equals(update.signingCertificateSHA256, ignoreCase = true)) {
            "Downloaded update has a different signing certificate"
        }
    }

    private fun openInstaller(file: File) {
        listener(Status.Busy("Opening Android installer…"))
        val uri = FileProvider.getUriForFile(activity, "${activity.packageName}.presentations", file)
        activity.startActivity(Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, "application/vnd.android.package-archive")
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        })
    }

    @Suppress("DEPRECATION")
    private fun packageInfo(applicationId: String): PackageInfo =
        if (Build.VERSION.SDK_INT >= 33) {
            activity.packageManager.getPackageInfo(applicationId, PackageManager.PackageInfoFlags.of(PackageManager.GET_SIGNING_CERTIFICATES.toLong()))
        } else {
            activity.packageManager.getPackageInfo(applicationId, PackageManager.GET_SIGNING_CERTIFICATES)
        }

    @Suppress("DEPRECATION")
    private fun archiveInfo(file: File): PackageInfo? =
        if (Build.VERSION.SDK_INT >= 33) {
            activity.packageManager.getPackageArchiveInfo(file.path, PackageManager.PackageInfoFlags.of(PackageManager.GET_SIGNING_CERTIFICATES.toLong()))
        } else {
            activity.packageManager.getPackageArchiveInfo(file.path, PackageManager.GET_SIGNING_CERTIFICATES)
        }

    private fun certificateSHA256(info: PackageInfo): String {
        val signers = requireNotNull(info.signingInfo).apkContentsSigners
        require(signers.size == 1) { "Koder must have exactly one APK signer" }
        return MessageDigest.getInstance("SHA-256").digest(signers.single().toByteArray())
            .joinToString("") { "%02x".format(it) }
    }
}
