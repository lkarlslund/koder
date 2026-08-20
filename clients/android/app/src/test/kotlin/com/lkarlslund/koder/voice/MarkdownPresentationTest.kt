package com.lkarlslund.koder.voice

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class MarkdownPresentationTest {
	@Test
	fun preservesMarkdownMeaningAsStyledHtml() {
		val rendered = markdownToHtml("# Fairphone 6\n\n- **Price:** €599\n- [Product](https://example.com)\n\n| Item | Value |\n|---|---|\n| RAM | 8 GB |")
		assertTrue(rendered.contains("<h1>Fairphone 6</h1>"))
		assertTrue(rendered.contains("<li><b>Price:</b> €599</li>"))
		assertTrue(rendered.contains("<a href=\"https://example.com\">Product</a>"))
		assertTrue(rendered.contains("RAM &nbsp; · &nbsp; 8 GB"))
		assertFalse(rendered.contains("|---|"))
	}

	@Test
	fun escapesRawHtmlAndKeepsCodeLiteral() {
		val rendered = markdownToHtml("`a < b`\n\n```\n<script>\n```")
		assertTrue(rendered.contains("<tt>a &lt; b</tt>"))
		assertTrue(rendered.contains("&lt;script&gt;"))
		assertFalse(rendered.contains("<script>"))
	}
}
