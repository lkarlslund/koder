package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
)

// fakeServer answers GET routes with fixed bodies and POST routes with the
// given status, like a server rejecting an empty request body.
func fakeServer(t *testing.T, gets map[string]string, posts map[string]int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if body, ok := gets[r.URL.Path]; ok {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
				return
			}
		}
		if status, ok := posts[r.URL.Path]; ok && r.Method == http.MethodPost {
			w.WriteHeader(status)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestDetectDecisionServerFromBareURL(t *testing.T) {
	server := fakeServer(t,
		map[string]string{"/v1/models": `{"models":[{"name":"laya","description":"typed decisions"}]}`},
		map[string]int{"/v1/systemone": http.StatusUnprocessableEntity},
	)
	detection, err := Detect(context.Background(), "laya", config.Provider{BaseURL: server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if detection.BaseURL != server.URL+"/v1" {
		t.Fatalf("base url = %q, want /v1 appended", detection.BaseURL)
	}
	if detection.Features.Chat || !detection.Features.Decisions {
		t.Fatalf("features = %+v, want decisions without chat", detection.Features)
	}
	if len(detection.Models) != 1 {
		t.Fatalf("models = %+v", detection.Models)
	}
	model := detection.Models[0]
	if model.ID != "laya" || model.SupportsChat || !model.ChatKnown || !model.SupportsDecisions {
		t.Fatalf("model = %+v, want decision-only laya", model)
	}
	if detection.Transport != config.TransportOpenAI {
		t.Fatalf("transport = %q", detection.Transport)
	}
}

func TestDetectChatServers(t *testing.T) {
	tests := []struct {
		name      string
		gets      map[string]string
		transport string
	}{
		{
			name:      "ninfer",
			gets:      map[string]string{"/v1/models": `{"data":[{"id":"Qwen/Qwen3","owned_by":"ninfer","max_model_len":262144}]}`},
			transport: config.TransportNinfer,
		},
		{
			name: "llama.cpp",
			gets: map[string]string{
				"/v1/models": `{"data":[{"id":"local","owned_by":"llamacpp"}]}`,
				"/props":     `{"default_generation_settings":{"n_ctx":65536}}`,
			},
			transport: config.TransportLlama,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := fakeServer(t, tt.gets, map[string]int{"/v1/chat/completions": http.StatusBadRequest})
			detection, err := Detect(context.Background(), tt.name, config.Provider{BaseURL: server.URL + "/v1"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !detection.Features.Chat || detection.Features.Decisions {
				t.Fatalf("features = %+v, want chat only", detection.Features)
			}
			if detection.Transport != tt.transport {
				t.Fatalf("transport = %q, want %q", detection.Transport, tt.transport)
			}
			if model := detection.Models[0]; !model.SupportsChat || model.SupportsDecisions {
				t.Fatalf("model = %+v, want chat model", model)
			}
		})
	}
}

func TestDetectReportsMissingCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"missing api key"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	_, err := Detect(context.Background(), "cloud", config.Provider{BaseURL: server.URL + "/v1"}, nil)
	if !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("err = %v, want ErrAuthRequired", err)
	}
}

func TestProbeSkipsPromptProgressWithoutChat(t *testing.T) {
	var chatRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"models":[{"name":"laya"}]}`))
		case "/v1/systemone":
			w.WriteHeader(http.StatusUnprocessableEntity)
		case "/v1/chat/completions":
			chatRequests++
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	result, err := Probe(context.Background(), ConnectDraft{ProviderID: "laya", BaseURL: server.URL, Kind: ProviderKindCompatible, PromptProgressMode: "auto"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.PromptProgressProbed || chatRequests != 1 {
		t.Fatalf("probed=%v chat requests=%d, want only the detection request", result.PromptProgressProbed, chatRequests)
	}
	if result.BaseURL != server.URL+"/v1" || result.SelectedModel != "laya" || !result.Features.Decisions {
		t.Fatalf("result = %+v", result)
	}
}
