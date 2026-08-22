package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
)

const knowledgeBrowserAssetPath = "assets/knowledge.html"
const knowledgeBrowserConfigPlaceholder = "__KODER_KNOWLEDGE_CONFIG__"

var knowledgeBrowserHTML = mustReadAsset(knowledgeBrowserAssetPath)

type knowledgeBrowserConfig struct {
	APIBase string `json:"api_base"`
	Token   string `json:"token"`
}

func renderKnowledgeBrowserHTML(token string) string {
	config, err := json.Marshal(knowledgeBrowserConfig{APIBase: knowledgeapi.RoutePrefix, Token: token})
	if err != nil {
		panic(fmt.Sprintf("encode Knowledge browser config: %v", err))
	}
	page := strings.ReplaceAll(knowledgeBrowserHTML, assetHashPlaceholder, currentAssetHash)
	return strings.ReplaceAll(page, knowledgeBrowserConfigPlaceholder, string(config))
}

func isKnowledgeBrowserPath(rawPath string) bool {
	return strings.TrimSpace(rawPath) == "/knowledge"
}

func newKnowledgeBrowserToken() (string, error) {
	credential := make([]byte, 32)
	if _, err := rand.Read(credential); err != nil {
		return "", fmt.Errorf("create Knowledge browser credential: %w", err)
	}
	return "kbw1_" + base64.RawURLEncoding.EncodeToString(credential), nil
}

func knowledgeBrowserTokenMatches(want, got string) bool {
	want = strings.TrimSpace(want)
	got = strings.TrimSpace(got)
	return want != "" && len(want) == len(got) && subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}
