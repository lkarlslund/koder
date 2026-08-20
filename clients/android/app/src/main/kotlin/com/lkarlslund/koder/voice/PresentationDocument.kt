package com.lkarlslund.koder.voice

import org.json.JSONArray
import org.json.JSONObject
import org.json.JSONTokener

const val KODER_PRESENTATION_MIME = "application/vnd.koder.presentation+json"

data class PresentationDocument(
	val version: Int,
	val blocks: List<PresentationBlock>,
)

sealed interface PresentationBlock {
	data class Text(val text: String, val style: String) : PresentationBlock
	data class Image(val uri: String, val title: String, val alt: String) : PresentationBlock
	data class KeyValue(val items: List<PresentationItem>) : PresentationBlock
	data class ListItems(val items: List<PresentationItem>) : PresentationBlock
	data class Progress(val label: String, val value: Int, val max: Int, val detail: String) : PresentationBlock
	data class Action(val label: String, val uri: String) : PresentationBlock
	data class File(val name: String, val uri: String, val mimeType: String, val detail: String) : PresentationBlock
	data class Unknown(val kind: String, val description: String) : PresentationBlock
}

data class PresentationItem(
	val key: String = "",
	val value: String = "",
	val title: String = "",
	val detail: String = "",
)

object PresentationDocuments {
	fun parse(data: Any?): PresentationDocument? {
		val json = when (data) {
			is JSONObject -> data
			is String -> runCatching { JSONTokener(data).nextValue() as? JSONObject }.getOrNull()
			else -> null
		} ?: return null
		val version = json.optInt("version", -1)
		if (version != 1) return null
		val source = json.optJSONArray("blocks") ?: return null
		val blocks = buildList {
			for (index in 0 until source.length()) {
				val item = source.optJSONObject(index) ?: continue
				add(item.toBlock())
			}
		}
		return PresentationDocument(version, blocks).takeIf { it.blocks.isNotEmpty() }
	}

	private fun JSONObject.toBlock(): PresentationBlock {
		return when (val kind = optString("kind")) {
			"text" -> PresentationBlock.Text(optString("text"), optString("style", "body"))
			"image" -> PresentationBlock.Image(optString("uri"), optString("title"), optString("alt"))
			"key_value" -> PresentationBlock.KeyValue(optJSONArray("items").toItems())
			"list" -> PresentationBlock.ListItems(optJSONArray("items").toItems())
			"progress" -> PresentationBlock.Progress(
				optString("label"),
				optInt("value"),
				optInt("max").coerceAtLeast(1),
				optString("detail"),
			)
			"action" -> PresentationBlock.Action(optString("label"), optString("uri"))
			"file" -> PresentationBlock.File(
				optString("name"),
				optString("uri"),
				optString("mime_type", "application/octet-stream"),
				optString("detail"),
			)
			else -> PresentationBlock.Unknown(kind.ifBlank { "unknown" }, toString())
		}
	}

	private fun JSONArray?.toItems(): List<PresentationItem> {
		if (this == null) return emptyList()
		return buildList {
			for (index in 0 until length()) {
				val item = optJSONObject(index) ?: continue
				add(PresentationItem(item.optString("key"), item.optString("value"), item.optString("title"), item.optString("detail")))
			}
		}
	}
}
