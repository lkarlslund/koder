package com.lkarlslund.koder.update

import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.content.FileProvider
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

@RunWith(AndroidJUnit4::class)
class UpdatePlatformInstrumentedTest {
    @Test
    fun installedApkCanBeInspectedAndUpdateFileCanBeShared() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val installed = packageInfo(context, context.packageName)
        val archive = archiveInfo(context, context.applicationInfo.sourceDir)

        assertNotNull(archive)
        assertEquals(installed.packageName, archive?.packageName)
        assertEquals(installed.longVersionCode, archive?.longVersionCode)
        assertTrue(installed.signingInfo?.apkContentsSigners?.isNotEmpty() == true)
        assertTrue(archive?.signingInfo?.apkContentsSigners?.isNotEmpty() == true)

        val update = File(context.filesDir, "updates/provider-test.apk").apply {
            parentFile?.mkdirs()
            writeText("fixture")
        }
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.presentations", update)
        assertEquals("content", uri.scheme)
        context.contentResolver.openInputStream(uri).use { input ->
            assertEquals("fixture", input?.bufferedReader()?.readText())
        }
    }

    @Suppress("DEPRECATION")
    private fun packageInfo(context: Context, applicationId: String) =
        if (Build.VERSION.SDK_INT >= 33) {
            context.packageManager.getPackageInfo(
                applicationId,
                PackageManager.PackageInfoFlags.of(PackageManager.GET_SIGNING_CERTIFICATES.toLong()),
            )
        } else {
            context.packageManager.getPackageInfo(applicationId, PackageManager.GET_SIGNING_CERTIFICATES)
        }

    @Suppress("DEPRECATION")
    private fun archiveInfo(context: Context, path: String) =
        if (Build.VERSION.SDK_INT >= 33) {
            context.packageManager.getPackageArchiveInfo(
                path,
                PackageManager.PackageInfoFlags.of(PackageManager.GET_SIGNING_CERTIFICATES.toLong()),
            )
        } else {
            context.packageManager.getPackageArchiveInfo(path, PackageManager.GET_SIGNING_CERTIFICATES)
        }
}
