package config

import "testing"

func TestInferTransport(t *testing.T) {
	cases := []struct {
		name     string
		provider Provider
		want     string
	}{
		{"local llama.cpp by name", Provider{Name: "Local llama.cpp", BaseURL: "http://127.0.0.1:8888/v1"}, TransportLlama},
		{"unnamed localhost server", Provider{BaseURL: "http://localhost:8080/v1"}, TransportLlama},
		{"ollama template", Provider{TemplateID: "ollama", Name: "Ollama", BaseURL: "http://127.0.0.1:11434/v1"}, TransportOpenAI},
		{"ollama port", Provider{Name: "local", BaseURL: "http://127.0.0.1:11434/v1"}, TransportOpenAI},
		{"ninfer", Provider{Name: "ninfer box", BaseURL: "http://127.0.0.1:9000/v1"}, TransportNinfer},
		{"dashscope", Provider{BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"}, TransportDashScope},
		{"remote openai", Provider{BaseURL: "https://api.openai.com/v1"}, TransportOpenAI},
	}
	for _, tc := range cases {
		if got := InferTransport(tc.provider); got != tc.want {
			t.Errorf("%s: InferTransport = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeKeepsExplicitTransport(t *testing.T) {
	cfg := Default()
	cfg.Providers = map[string]Provider{
		"explicit": {Name: "Local llama.cpp", BaseURL: "http://127.0.0.1:8888/v1", Transport: "OpenAI"},
		"legacy":   {Name: "Local llama.cpp", BaseURL: "http://127.0.0.1:8888/v1"},
	}
	cfg.applyDefaults()
	if got := cfg.Providers["explicit"].Transport; got != TransportOpenAI {
		t.Fatalf("explicit transport = %q", got)
	}
	if got := cfg.Providers["legacy"].Transport; got != TransportLlama {
		t.Fatalf("inferred transport = %q", got)
	}
}
