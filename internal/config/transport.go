package config

import (
	"net/url"
	"strings"
)

// Request dialects a provider speaks beyond the OpenAI chat-completions base.
// Model overlays map options (thinking, sampling) onto the dialect's fields.
const (
	TransportOpenAI    = "openai"
	TransportLlama     = "llama"
	TransportNinfer    = "ninfer"
	TransportDashScope = "dashscope"
)

// NormalizeTransport returns a known transport name, or "" when value is
// blank or unrecognized.
func NormalizeTransport(value string) string {
	switch value = strings.ToLower(strings.TrimSpace(value)); value {
	case TransportOpenAI, TransportLlama, TransportNinfer, TransportDashScope:
		return value
	default:
		return ""
	}
}

// InferTransport guesses the dialect of a provider saved before transports
// were recorded. Config loading stores the result, so the guess is made once
// per provider rather than on every request.
func InferTransport(provider Provider) string {
	parsed, _ := url.Parse(strings.TrimSpace(provider.BaseURL))
	host, port := "", ""
	if parsed != nil {
		host, port = strings.ToLower(parsed.Hostname()), parsed.Port()
	}
	if strings.Contains(host, "dashscope.aliyuncs.com") || strings.Contains(host, "dashscope-intl.aliyuncs.com") {
		return TransportDashScope
	}
	if nameContains("ninfer", provider.TemplateID, provider.Name) {
		return TransportNinfer
	}
	if strings.EqualFold(strings.TrimSpace(provider.TemplateID), "ollama") || port == "11434" {
		return TransportOpenAI
	}
	if nameContains("llama", provider.Kind, provider.TemplateID, provider.Name) {
		return TransportLlama
	}
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return TransportLlama
	default:
		return TransportOpenAI
	}
}

func nameContains(needle string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(strings.TrimSpace(value)), needle) {
			return true
		}
	}
	return false
}
