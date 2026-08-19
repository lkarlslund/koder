package com.lkarlslund.koder.update

import com.lkarlslund.koder.voice.AppUpdate
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.ByteArrayInputStream
import java.security.MessageDigest

class UpdatePolicyTest {
    private val bytes = "signed apk".toByteArray()
    private val sha = MessageDigest.getInstance("SHA-256").digest(bytes)
        .joinToString("") { "%02x".format(it) }
    private val installed = InstalledApp("com.lkarlslund.koder.dev", 10, "a".repeat(64))

    @Test
    fun onlyAcceptsNewerSameApplicationAndSigner() {
        assertNull(UpdatePolicy.rejection(update(), installed))
        assertEquals("Koder is already current", UpdatePolicy.rejection(update(versionCode = 10), installed))
        assertEquals(
            "Update is for a different Koder channel",
            UpdatePolicy.rejection(update(applicationId = "com.lkarlslund.koder"), installed),
        )
        assertEquals(
            "Update was signed by a different Koder channel",
            UpdatePolicy.rejection(update(certificate = "b".repeat(64)), installed),
        )
    }

    @Test
    fun streamsAndVerifiesExactBytes() {
        val destination = kotlin.io.path.createTempFile().toFile().apply { deleteOnExit() }
        UpdatePolicy.writeVerified(ByteArrayInputStream(bytes), destination, update())
        assertTrue(destination.readBytes().contentEquals(bytes))
    }

    @Test(expected = IllegalArgumentException::class)
    fun rejectsCorruptDownload() {
        val destination = kotlin.io.path.createTempFile().toFile().apply { deleteOnExit() }
        UpdatePolicy.writeVerified(ByteArrayInputStream("tampered!".toByteArray()), destination, update())
    }

    private fun update(
        applicationId: String = installed.applicationId,
        versionCode: Long = 11,
        certificate: String = installed.signingCertificateSHA256,
    ) = AppUpdate(
        channel = "local",
        applicationId = applicationId,
        versionCode = versionCode,
        versionName = "0.1.0-local.test",
        signingCertificateSHA256 = certificate,
        apkSHA256 = sha,
        apkSize = bytes.size.toLong(),
        downloadUri = "/voice/v1/android/koder.apk",
    )
}
