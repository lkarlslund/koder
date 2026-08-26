package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
)

const memoryBrowserAssetPath = "assets/memory.html"
const memoryBrowserConfigPlaceholder = "__KODER_MEMORY_CONFIG__"

var memoryBrowserHTML = mustReadAsset(memoryBrowserAssetPath)

type memoryBrowserConfig struct {
	APIBase string `json:"api_base"`
	Token   string `json:"token"`
}

func renderMemoryBrowserHTML(token string) string {
	config, err := json.Marshal(memoryBrowserConfig{APIBase: memoryapi.RoutePrefix, Token: token})
	if err != nil {
		panic(fmt.Sprintf("encode Memory browser config: %v", err))
	}
	page := strings.ReplaceAll(memoryBrowserHTML, assetHashPlaceholder, currentAssetHash)
	return strings.ReplaceAll(page, memoryBrowserConfigPlaceholder, string(config))
}

func isMemoryBrowserPath(rawPath string) bool {
	return strings.TrimSpace(rawPath) == "/memory"
}

func newMemoryBrowserToken() (string, error) {
	credential := make([]byte, 32)
	if _, err := rand.Read(credential); err != nil {
		return "", fmt.Errorf("create Memory browser credential: %w", err)
	}
	return "kbw1_" + base64.RawURLEncoding.EncodeToString(credential), nil
}

func memoryBrowserTokenMatches(want, got string) bool {
	want = strings.TrimSpace(want)
	got = strings.TrimSpace(got)
	return want != "" && len(want) == len(got) && subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}
