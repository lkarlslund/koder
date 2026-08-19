package com.lkarlslund.koder.update

import com.lkarlslund.koder.voice.AppUpdate
import java.io.File
import java.io.InputStream
import java.security.MessageDigest

data class InstalledApp(
    val applicationId: String,
    val versionCode: Long,
    val signingCertificateSHA256: String,
)

object UpdatePolicy {
    fun rejection(update: AppUpdate, installed: InstalledApp): String? = when {
        update.applicationId != installed.applicationId -> "Update is for a different Koder channel"
        update.versionCode <= installed.versionCode -> "Koder is already current"
        !update.signingCertificateSHA256.equals(installed.signingCertificateSHA256, ignoreCase = true) ->
            "Update was signed by a different Koder channel"
        update.apkSize <= 0L -> "Update size is invalid"
        !update.apkSHA256.isSHA256() -> "Update checksum is invalid"
        else -> null
    }

    fun writeVerified(input: InputStream, destination: File, update: AppUpdate) {
        val digest = MessageDigest.getInstance("SHA-256")
        var total = 0L
        destination.outputStream().buffered().use { output ->
            val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                total += count
                require(total <= update.apkSize) { "Update is larger than advertised" }
                digest.update(buffer, 0, count)
                output.write(buffer, 0, count)
            }
        }
        require(total == update.apkSize) { "Update size is $total bytes; expected ${update.apkSize}" }
        val actual = digest.digest().joinToString("") { "%02x".format(it) }
        require(actual.equals(update.apkSHA256, ignoreCase = true)) { "Update checksum does not match" }
    }

    private fun String.isSHA256() = length == 64 && all { it in '0'..'9' || it in 'a'..'f' }
}
