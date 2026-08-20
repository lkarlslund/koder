package com.lkarlslund.koder.voice

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PresentationDocumentTest {
	@Test
	fun parsesEveryGenericBlockFromObjectData() {
		val source = JSONObject(
			"""{"version":1,"blocks":[{"kind":"text","text":"Today","style":"heading"},{"kind":"image","uri":"/map.png","title":"Aarhus","alt":"Map"},{"kind":"key_value","items":[{"key":"Event","value":"DHL Stafet"}]},{"kind":"list","items":[{"title":"Road closures","detail":"From 16:00"}]},{"kind":"progress","label":"Route","value":2,"max":5,"detail":"Checking"},{"kind":"action","label":"Open details","uri":"https://example.com"},{"kind":"file","name":"event.ics","uri":"/event.ics","mime_type":"text/calendar","detail":"Calendar entry"}]}""",
		)
		val document = requireNotNull(PresentationDocuments.parse(source))
		assertEquals(1, document.version)
		assertEquals(7, document.blocks.size)
		assertEquals("Today", (document.blocks[0] as PresentationBlock.Text).text)
		assertEquals("DHL Stafet", (document.blocks[2] as PresentationBlock.KeyValue).items.single().value)
		assertEquals(5, (document.blocks[4] as PresentationBlock.Progress).max)
		assertEquals("event.ics", (document.blocks[6] as PresentationBlock.File).name)
	}

	@Test
	fun parsesStringDataAndKeepsUnknownBlocksVisible() {
		val document = requireNotNull(PresentationDocuments.parse("""{"version":1,"blocks":[{"kind":"future_chart","series":[1,2]}]}"""))
		assertTrue(document.blocks.single() is PresentationBlock.Unknown)
	}

	@Test
	fun rejectsMalformedOrUnsupportedDocumentsForGenericFallback() {
		assertNull(PresentationDocuments.parse("not json"))
		assertNull(PresentationDocuments.parse("""{"version":2,"blocks":[{"kind":"text","text":"Hi"}]}"""))
		assertNull(PresentationDocuments.parse("""{"version":1,"blocks":[]}"""))
	}
}
