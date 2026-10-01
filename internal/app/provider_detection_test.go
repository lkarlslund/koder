package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/provider"
)

func TestProviderDetectionMigratesProvidersAndLegacyDecisionURL(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)
	t.Setenv("HOME", temp)

	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen","owned_by":"ninfer"}]}`))
		case "/v1/chat/completions":
			w.WriteHeader(http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer chat.Close()
	decisions := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"models":[{"name":"Laya"}]}`))
		case "/v1/systemone":
			w.WriteHeader(http.StatusUnprocessableEntity)
		default:
			http.NotFound(w, r)
		}
	}))
	defer decisions.Close()

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers = map[string]config.Provider{"halo": {BaseURL: chat.URL + "/v1", Transport: config.TransportOpenAI}}
	cfg.Browser.LegacyTaskDecisionURL = decisions.URL + "/v1/systemone"
	controller := New(cfg, nil)

	ctx := context.Background()
	detection, err := detectProvider(ctx, "halo", cfg.Providers["halo"])
	if err != nil {
		t.Fatal(err)
	}
	decisionProvider, settled := detectLegacyDecisionProvider(ctx, cfg, cfg.Browser.LegacyTaskDecisionURL)
	if decisionProvider == nil || !settled {
		t.Fatalf("legacy decision endpoint was not detected: %v %v", decisionProvider, settled)
	}
	controller.applyProviderDetections(map[string]provider.Detection{"halo": detection}, decisionProvider, settled)

	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	halo := saved.Providers["halo"]
	if halo.Features == nil || !halo.Features.Chat || halo.Transport != config.TransportNinfer {
		t.Fatalf("halo = %+v", halo)
	}
	laya, ok := saved.Providers["laya"]
	if !ok || laya.BaseURL != decisions.URL+"/v1" || laya.Features == nil || !laya.Features.Decisions {
		t.Fatalf("providers = %+v", saved.Providers)
	}
	if saved.Browser.LegacyTaskDecisionURL != "" {
		t.Fatalf("legacy url kept: %q", saved.Browser.LegacyTaskDecisionURL)
	}
}

func TestLegacyDecisionURLStaysWhileUnreachable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL + "/v1/systemone"
	server.Close()
	if detection, settled := detectLegacyDecisionProvider(context.Background(), config.Default(), url); detection != nil || settled {
		t.Fatalf("unreachable endpoint settled: %v %v", detection, settled)
	}
}

func TestTestProviderReportsAuthAndSuggestsName(t *testing.T) {
	locked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing key", http.StatusUnauthorized)
	}))
	defer locked.Close()
	controller := New(config.Default(), nil)
	result, err := controller.TestProvider(context.Background(), ProviderDraft{ProviderID: "cloud", TemplateID: "openai", Kind: provider.ProviderKindCompatible, BaseURL: locked.URL + "/v1"})
	if err != nil || !result.AuthRequired {
		t.Fatalf("result = %+v, err = %v; want auth required", result, err)
	}

	open := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"models":[{"name":"laya"}]}`))
		case "/v1/systemone":
			w.WriteHeader(http.StatusUnprocessableEntity)
		default:
			http.NotFound(w, r)
		}
	}))
	defer open.Close()
	result, err = controller.TestProvider(context.Background(), ProviderDraft{ProviderID: "openai-compatible", TemplateID: provider.ProviderKindCompatible, Kind: provider.ProviderKindCompatible, BaseURL: open.URL})
	if err != nil {
		t.Fatal(err)
	}
	if result.SuggestedID != "laya" || result.SuggestedName != "laya" || result.BaseURL != open.URL+"/v1" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.ModelDetails) != 1 || result.ModelDetails[0].Kinds[0] != "Decisions" {
		t.Fatalf("model details = %+v", result.ModelDetails)
	}
}
