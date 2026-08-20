import java.net.URI
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.security.MessageDigest
import java.util.Properties
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
val koderVersionCode = providers.gradleProperty("koderVersionCode").getOrElse("1").toInt()
val koderVersionName = providers.gradleProperty("koderVersionName").getOrElse("0.1.0-dev")
val koderTargetAbis = providers.gradleProperty("koderTargetAbis").orNull
    ?.split(',')
    ?.map(String::trim)
    ?.filter(String::isNotEmpty)
    .orEmpty()

fun signingProperties(path: String?): Properties? {
    if (path.isNullOrBlank()) return null
    val file = rootProject.file(path)
    if (!file.isFile) return null
    return Properties().apply { file.inputStream().use(::load) }
}

val localSigning = signingProperties(providers.gradleProperty("koderLocalSigningProperties").orNull)
val releaseSigning = signingProperties(providers.gradleProperty("koderReleaseSigningProperties").orNull)

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

    buildFeatures {
        resValues = true
    }

    defaultConfig {
        applicationId = "com.lkarlslund.koder"
        minSdk = 28
        targetSdk = 36
        versionCode = koderVersionCode
        versionName = koderVersionName
        resValue("string", "app_name", "Koder")
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        if (koderTargetAbis.isNotEmpty()) {
            ndk {
                abiFilters += koderTargetAbis
            }
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    sourceSets.getByName("main").assets.directories.add(
        sileroVadAssets.get().asFile.absolutePath,
    )
    sourceSets.getByName("test").resources.directories.add(
        rootProject.layout.projectDirectory.dir("../../protocol/voice/v1/testdata").asFile.absolutePath,
    )

    signingConfigs {
        if (localSigning != null) {
            create("localDevelopment") {
                storeFile = rootProject.file(localSigning.getProperty("storeFile"))
                storePassword = localSigning.getProperty("storePassword")
                keyAlias = localSigning.getProperty("keyAlias")
                keyPassword = localSigning.getProperty("keyPassword")
            }
        }
        if (releaseSigning != null) {
            create("officialRelease") {
                storeFile = rootProject.file(releaseSigning.getProperty("storeFile"))
                storePassword = releaseSigning.getProperty("storePassword")
                keyAlias = releaseSigning.getProperty("keyAlias")
                keyPassword = releaseSigning.getProperty("keyPassword")
            }
        }
    }

    buildTypes {
        debug {
            applicationIdSuffix = ".dev"
            resValue("string", "app_name", "Koder Dev")
            localSigning?.let { signingConfig = signingConfigs.getByName("localDevelopment") }
        }
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            releaseSigning?.let { signingConfig = signingConfigs.getByName("officialRelease") }
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }

    packaging {
        jniLibs {
            // Koder is sideloaded, so favor a smaller embedded/downloaded APK over
            // mmap-in-place native libraries. Android extracts these at install time.
            useLegacyPackaging = true
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
				create("voiceApi28Small") {
					device = "Small Phone"
					apiLevel = 28
					systemImageSource = "google"
					testedAbi = "x86"
				}
				create("voiceApi36Tablet") {
					device = "Medium Tablet"
					apiLevel = 36
					systemImageSource = "google"
					testedAbi = "x86_64"
				}
            }
			groups {
				create("voiceCompatibility") {
					targetDevices.addAll(listOf(
						localDevices["voiceApi28Small"],
						localDevices["voiceApi36"],
						localDevices["voiceApi36Tablet"],
					))
				}
			}
        }
    }
}

tasks.named("preBuild").configure {
    dependsOn(prepareSileroVadAssets)
}

dependencies {
    implementation("androidx.activity:activity-ktx:1.12.2")
    implementation("androidx.core:core:1.17.0")
    implementation("androidx.core:core-telecom:1.0.1")
    implementation("androidx.swiperefreshlayout:swiperefreshlayout:1.1.0")
    implementation("com.squareup.okhttp3:okhttp:5.3.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.11.0")
    implementation("com.microsoft.onnxruntime:onnxruntime-android:1.29.0")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20250517")

    androidTestImplementation("androidx.test:core:1.7.0")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test:runner:1.7.0")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.7.0")
    androidTestImplementation("com.squareup.okhttp3:mockwebserver3:5.3.0")
}
