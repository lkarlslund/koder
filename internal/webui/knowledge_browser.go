package webui

import "strings"

const knowledgeBrowserAssetPath = "assets/knowledge.html"

var knowledgeBrowserHTML = mustReadAsset(knowledgeBrowserAssetPath)

func renderKnowledgeBrowserHTML() string {
	return strings.ReplaceAll(knowledgeBrowserHTML, assetHashPlaceholder, currentAssetHash)
}

func isKnowledgeBrowserPath(rawPath string) bool {
	return strings.TrimSpace(rawPath) == "/knowledge"
}
