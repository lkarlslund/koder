import java.net.URI
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.security.MessageDigest
import org.gradle.api.DefaultTask
import org.gradle.api.file.RegularFileProperty
import org.gradle.api.provider.Property
import org.gradle.api.tasks.Input
import org.gradle.api.tasks.OutputFile
import org.gradle.api.tasks.TaskAction

abstract class DownloadVerifiedFile : DefaultTask() {
    @get:Input
    abstract val sourceUrl: Property<String>

    @get:Input
    abstract val expectedSHA256: Property<String>

    @get:OutputFile
    abstract val destination: RegularFileProperty

    @TaskAction
    fun download() {
        val target = destination.get().asFile.toPath()
        val expectedDigest = expectedSHA256.get()
        Files.createDirectories(target.parent)
        if (Files.exists(target) && target.sha256() == expectedDigest) return

        val temporary = Files.createTempFile(target.parent, "verified-download-", ".tmp")
        try {
            URI(sourceUrl.get()).toURL().openStream().use { input ->
                Files.copy(input, temporary, StandardCopyOption.REPLACE_EXISTING)
            }
            check(temporary.sha256() == expectedDigest) {
                "downloaded file failed SHA-256 verification"
            }
            Files.move(
                temporary,
                target,
                StandardCopyOption.ATOMIC_MOVE,
                StandardCopyOption.REPLACE_EXISTING,
            )
        } finally {
            Files.deleteIfExists(temporary)
        }
    }

    private fun java.nio.file.Path.sha256(): String {
        val digest = MessageDigest.getInstance("SHA-256")
        Files.newInputStream(this).use { input ->
            val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                digest.update(buffer, 0, count)
            }
        }
        return digest.digest().joinToString("") { "%02x".format(it) }
    }
}

plugins {
    id("com.android.application")
}

val sileroVadCommit = "806dcba3f0b5d95282d0889a074954a2f8c6397b"
val sileroVadSHA256 = "1a153a22f4509e292a94e67d6f9b85e8deb25b4988682b7e174c65279d8788e3"
val sileroVadAssets = layout.buildDirectory.dir("generated/sileroVadAssets")

val prepareSileroVadAssets by tasks.registering(DownloadVerifiedFile::class) {
    sourceUrl.set(
        URI(
            "https://raw.githubusercontent.com/snakers4/silero-vad/" +
                "$sileroVadCommit/src/silero_vad/data/silero_vad.onnx",
        ).toString(),
    )
    expectedSHA256.set(sileroVadSHA256)
    destination.set(sileroVadAssets.map { it.file("silero_vad.onnx") })
}

android {
    namespace = "com.lkarlslund.koder"
    compileSdk = 36
    buildToolsVersion = "37.0.0"

    defaultConfig {
        applicationId = "com.lkarlslund.koder"
        minSdk = 28
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    sourceSets.getByName("main").assets.directories.add(
        sileroVadAssets.get().asFile.absolutePath,
    )

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }

    testOptions {
        managedDevices {
            localDevices {
                create("voiceApi36") {
                    device = "Medium Phone"
                    apiLevel = 36
                    systemImageSource = "google"
                    testedAbi = "x86_64"
                }
            }
        }
    }
}

tasks.named("preBuild").configure {
    dependsOn(prepareSileroVadAssets)
}

dependencies {
    implementation("com.microsoft.onnxruntime:onnxruntime-android:1.29.0")

    testImplementation("junit:junit:4.13.2")

    androidTestImplementation("androidx.test:core:1.7.0")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test:runner:1.7.0")
}
