package com.lkarlslund.koder.voice

/** Converts the deliberately small presentation Markdown surface to Android HTML. */
internal fun markdownToHtml(markdown: String): String {
	val lines = markdown.replace("\r\n", "\n").lines()
	val output = StringBuilder()
	var inCode = false
	var inList = false
	for (raw in lines) {
		val line = raw.trimEnd()
		if (line.trimStart().startsWith("```")) {
			if (inList) { output.append("</ul>"); inList = false }
			output.append(if (inCode) "</code></pre>" else "<pre><code>")
			inCode = !inCode
			continue
		}
		if (inCode) {
			output.append(escapeMarkdownHtml(line)).append('\n')
			continue
		}
		val heading = Regex("^\\s{0,3}(#{1,6})\\s+(.+)$").matchEntire(line)
		val list = Regex("^\\s*[-*+]\\s+(.+)$").matchEntire(line)
		when {
			heading != null -> {
				if (inList) { output.append("</ul>"); inList = false }
				val depth = heading.groupValues[1].length.coerceAtMost(3)
				output.append("<h").append(depth).append('>')
					.append(markdownInlineHtml(heading.groupValues[2]))
					.append("</h").append(depth).append('>')
			}
			list != null -> {
				if (!inList) { output.append("<ul>"); inList = true }
				output.append("<li>").append(markdownInlineHtml(list.groupValues[1])).append("</li>")
			}
			line.isBlank() -> {
				if (inList) { output.append("</ul>"); inList = false }
				output.append("<br>")
			}
			isMarkdownTableDivider(line) -> Unit
			line.count { it == '|' } >= 2 -> {
				if (inList) { output.append("</ul>"); inList = false }
				output.append("<p>").append(line.trim().trim('|').split('|').joinToString(" &nbsp; · &nbsp; ") {
					markdownInlineHtml(it.trim())
				}).append("</p>")
			}
			else -> {
				if (inList) { output.append("</ul>"); inList = false }
				output.append("<p>").append(markdownInlineHtml(line)).append("</p>")
			}
		}
	}
	if (inList) output.append("</ul>")
	if (inCode) output.append("</code></pre>")
	return output.toString()
}

private fun markdownInlineHtml(value: String): String {
	var rendered = escapeMarkdownHtml(value)
	rendered = Regex("\\[([^]]+)]\\((https?://[^ )]+)\\)").replace(rendered, "<a href=\"$2\">$1</a>")
	rendered = Regex("\\*\\*(.+?)\\*\\*|__(.+?)__").replace(rendered) { match ->
		"<b>${match.groups[1]?.value ?: match.groups[2]?.value.orEmpty()}</b>"
	}
	rendered = Regex("(?<!\\*)\\*([^*]+)\\*(?!\\*)|(?<!_)_([^_]+)_(?!_)").replace(rendered) { match ->
		"<i>${match.groups[1]?.value ?: match.groups[2]?.value.orEmpty()}</i>"
	}
	return Regex("`([^`]+)`").replace(rendered, "<tt>$1</tt>")
}

private fun isMarkdownTableDivider(value: String): Boolean =
	value.trim().matches(Regex("\\|?\\s*:?-{3,}:?\\s*(\\|\\s*:?-{3,}:?\\s*)+\\|?"))

private fun escapeMarkdownHtml(value: String): String = value
	.replace("&", "&amp;")
	.replace("<", "&lt;")
	.replace(">", "&gt;")
