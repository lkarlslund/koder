package com.lkarlslund.koder

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import com.lkarlslund.koder.voice.BuiltInAudioRoute
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

class SecureSettings(context: Context) {
    data class Values(
        val server: String,
        val token: String,
        val enabledPhoneCapabilities: Set<String> = emptySet(),
		val speechLanguages: Set<String> = emptySet(),
		val vadSensitivityPercent: Int = 50,
		val vadSilenceMilliseconds: Int = 600,
		val builtInAudioRoute: BuiltInAudioRoute = BuiltInAudioRoute.SPEAKER,
    )

    private val preferences = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
    private val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }

    fun load(): Values {
        // Remove the pre-v1 plaintext credential if this is an upgraded install.
        if (preferences.contains("token")) preferences.edit().remove("token").apply()
        val server = preferences.getString(SERVER, DEFAULT_SERVER).orEmpty()
        val token = try {
            decrypt(
                preferences.getString(TOKEN, "").orEmpty(),
                preferences.getString(TOKEN_IV, "").orEmpty(),
            )
        } catch (_: Exception) {
            preferences.edit().remove(TOKEN).remove(TOKEN_IV).apply()
            ""
        }
		val languages = preferences.getStringSet(SPEECH_LANGUAGES, emptySet()).orEmpty()
			.map(String::lowercase)
			.filterTo(linkedSetOf()) { it in com.lkarlslund.koder.voice.SpeechLanguages.codes }
		return Values(
			server,
			token,
			preferences.getStringSet(PHONE_CAPABILITIES, emptySet()).orEmpty().toSet(),
			languages,
			preferences.getInt(VAD_SENSITIVITY, 50).coerceIn(35, 75),
			preferences.getInt(VAD_SILENCE, 600).coerceIn(300, 1_200),
			BuiltInAudioRoute.fromStorage(preferences.getString(AUDIO_ROUTE, null)),
		)
    }

    fun save(server: String, token: String) {
        val edit = preferences.edit().putString(SERVER, server.trim())
        if (token.isBlank()) {
            edit.remove(TOKEN).remove(TOKEN_IV).apply()
            return
        }
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, secretKey())
        edit.putString(TOKEN, cipher.doFinal(token.toByteArray(Charsets.UTF_8)).base64())
            .putString(TOKEN_IV, cipher.iv.base64())
            .apply()
    }

    fun savePhoneCapabilities(enabled: Set<String>) {
        preferences.edit().putStringSet(PHONE_CAPABILITIES, enabled.toSet()).apply()
    }

	fun saveSpeechLanguages(languages: Set<String>) {
		preferences.edit().putStringSet(SPEECH_LANGUAGES, languages.toSet()).apply()
	}

	fun saveVadSensitivity(percent: Int) {
		preferences.edit().putInt(VAD_SENSITIVITY, percent.coerceIn(35, 75)).apply()
	}

	fun saveVadSilence(milliseconds: Int) {
		preferences.edit().putInt(VAD_SILENCE, milliseconds.coerceIn(300, 1_200)).apply()
	}

	fun saveBuiltInAudioRoute(route: BuiltInAudioRoute) {
		preferences.edit().putString(AUDIO_ROUTE, route.storageValue).apply()
	}

    private fun decrypt(ciphertext: String, iv: String): String {
        if (ciphertext.isBlank() || iv.isBlank()) return ""
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, secretKey(), GCMParameterSpec(128, iv.fromBase64()))
        return cipher.doFinal(ciphertext.fromBase64()).toString(Charsets.UTF_8)
    }

    private fun secretKey(): SecretKey {
        (keyStore.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(
                KEY_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .build(),
        )
        return generator.generateKey()
    }

    private fun ByteArray.base64(): String = Base64.encodeToString(this, Base64.NO_WRAP)
    private fun String.fromBase64(): ByteArray = Base64.decode(this, Base64.NO_WRAP)

    private companion object {
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val KEY_ALIAS = "koder.voice.token.v1"
        const val PREFERENCES = "koder_voice"
        const val SERVER = "server"
        const val TOKEN = "token_encrypted"
        const val TOKEN_IV = "token_iv"
        const val PHONE_CAPABILITIES = "phone_capabilities"
		const val SPEECH_LANGUAGES = "speech_languages"
		const val VAD_SENSITIVITY = "vad_sensitivity"
		const val VAD_SILENCE = "vad_silence"
		const val AUDIO_ROUTE = "audio_route"
        const val DEFAULT_SERVER = ""
    }
}
