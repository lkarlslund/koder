package com.lkarlslund.koder.voice

data class SpeechLanguage(val code: String, val name: String)

object SpeechLanguages {
	val all = listOf(
		SpeechLanguage("da", "Danish"),
		SpeechLanguage("en", "English"),
		SpeechLanguage("sv", "Swedish"),
		SpeechLanguage("no", "Norwegian"),
		SpeechLanguage("fi", "Finnish"),
		SpeechLanguage("nl", "Dutch"),
		SpeechLanguage("de", "German"),
		SpeechLanguage("fr", "French"),
		SpeechLanguage("es", "Spanish"),
		SpeechLanguage("it", "Italian"),
		SpeechLanguage("pt", "Portuguese"),
		SpeechLanguage("pl", "Polish"),
	)

	val codes: Set<String> = all.mapTo(linkedSetOf(), SpeechLanguage::code)
}
