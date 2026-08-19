package com.lkarlslund.koder.phone

import android.content.Context
import android.os.Build
import okhttp3.Request
import org.json.JSONObject
import java.util.Locale
import java.util.UUID

data class PhoneIdentity(
	val installationId: String,
	val name: String,
	val manufacturer: String,
	val model: String,
	val androidVersion: String,
	val appVersion: String,
	val appId: String,
) {
	fun applyTo(builder: Request.Builder): Request.Builder = builder
		.header("X-Koder-Device-ID", installationId.asHeaderValue())
		.header("X-Koder-Device-Name", name.asHeaderValue())
		.header("X-Koder-Device-Manufacturer", manufacturer.asHeaderValue())
		.header("X-Koder-Device-Model", model.asHeaderValue())
		.header("X-Koder-Android-Version", androidVersion.asHeaderValue())
		.header("X-Koder-App-Version", appVersion.asHeaderValue())
		.header("X-Koder-App-ID", appId.asHeaderValue())

	fun toJSON(): JSONObject = JSONObject()
		.put("installation_id", installationId)
		.put("name", name)
		.put("manufacturer", manufacturer)
		.put("model", model)
		.put("android_version", androidVersion)
		.put("app_version", appVersion)
		.put("app_id", appId)

	private fun String.asHeaderValue() = map { character ->
		if (character.code in 0x20..0x7e) character else '?'
	}.joinToString("")

	companion object {
		fun from(context: Context): PhoneIdentity {
			val preferences = context.getSharedPreferences("koder_device_identity", Context.MODE_PRIVATE)
			val installationId = preferences.getString("installation_id", "").orEmpty().ifBlank {
				UUID.randomUUID().toString().also { preferences.edit().putString("installation_id", it).apply() }
			}
			val manufacturer = Build.MANUFACTURER.trim().ifBlank { "Android" }
			val model = Build.MODEL.trim().ifBlank { "phone" }
			val packageInfo = context.packageManager.getPackageInfo(context.packageName, 0)
			val displayManufacturer = manufacturer.replaceFirstChar {
				if (it.isLowerCase()) it.titlecase(Locale.getDefault()) else it.toString()
			}
			return PhoneIdentity(
				installationId = installationId,
				name = "$displayManufacturer $model".trim(),
				manufacturer = manufacturer,
				model = model,
				androidVersion = Build.VERSION.RELEASE.orEmpty().ifBlank { Build.VERSION.SDK_INT.toString() },
				appVersion = packageInfo.versionName.orEmpty(),
				appId = context.packageName,
			)
		}
	}
}
