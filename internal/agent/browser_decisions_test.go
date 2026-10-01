package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
)

func TestResolveBrowserRankerPicksDetectedDecisionModel(t *testing.T) {
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"models":[{"name":"laya"}]}`))
		case "/v1/systemone":
			var body struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotModel = body.Model
			_, _ = w.Write([]byte(`{"answers":{"next":{"choice":"c1","probabilities":{"c0":0.2,"c1":0.8}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers["chat"] = config.Provider{BaseURL: server.URL + "/v1", Features: &config.ProviderFeatures{Chat: true}}
	cfg.Providers["decisions"] = config.Provider{BaseURL: server.URL + "/v1", Features: &config.ProviderFeatures{Decisions: true}}
	engine := New(cfg, nil, nil, nil)

	ranker, name, err := engine.resolveBrowserRanker(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if name != "decisions/laya" {
		t.Fatalf("name = %q", name)
	}
	probabilities, err := ranker(context.Background(), map[string]string{"goal": "find the manual"}, "rank", map[string]string{"c0": "a", "c1": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if gotModel != "laya" || probabilities["c1"] != 0.8 {
		t.Fatalf("model = %q, probabilities = %#v", gotModel, probabilities)
	}
}

func TestResolveBrowserRankerReportsMissingDecisionModel(t *testing.T) {
	engine := New(testConfig(t), nil, nil, nil)
	if _, _, err := engine.resolveBrowserRanker(context.Background()); err == nil {
		t.Fatal("expected an error without a decision provider")
	}
}
