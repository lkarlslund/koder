package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/config"
)

func TestResolveBrowserDeciderPicksDetectedDecisionModel(t *testing.T) {
	var mu sync.Mutex
	var questions []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"models":[{"name":"laya"}]}`))
		case "/v1/systemone":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			questions = append(questions, body)
			mu.Unlock()
			yes := 0.1
			if state, _ := body["state"].(map[string]any); strings.Contains(state["link"].(string), "manual") {
				yes = 0.9
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"q": map[string]any{"probabilities": map[string]float64{"yes": yes, "no": 1 - yes}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers["chat"] = config.Provider{BaseURL: server.URL + "/v1", Features: &config.ProviderFeatures{Chat: true}}
	cfg.Providers["decisions"] = config.Provider{BaseURL: server.URL + "/v1", Features: &config.ProviderFeatures{Decisions: true}}
	engine := New(cfg, nil, nil, nil)

	decider, name, err := engine.resolveBrowserDecider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if name != "decisions/laya" {
		t.Fatalf("name = %q", name)
	}
	scores, err := decider.ScoreLinks(context.Background(), "download the manual", "https://example.com", []string{"Contact — /contact", "User manual — /manual.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if scores[0] != 0.1 || scores[1] != 0.9 {
		t.Fatalf("scores = %v", scores)
	}
	// One short yes/no question per link keeps each inside the model's input.
	if len(questions) != 2 || questions[0]["model"] != "laya" {
		t.Fatalf("questions = %v", questions)
	}
}

func TestResolveBrowserDeciderUsesPinnedChatModel(t *testing.T) {
	var prompts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			MaxTokens int `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		prompt := body.Messages[0].Content
		prompts = append(prompts, prompt)
		reply := "```json\n{\"0\": 0.2, \"1\": 0.8}\n```"
		if strings.Contains(prompt, "relevance") {
			reply = `{"relevance": 0.75}`
		}
		if body.MaxTokens <= 0 {
			t.Errorf("decision prompt has no max_tokens")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers["local"] = config.Provider{BaseURL: server.URL + "/v1", Features: &config.ProviderFeatures{Chat: true}}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "local", ModelID: "qwen", ContextWindow: 32768})
	cfg.Browser.TaskDecisionProviderID, cfg.Browser.TaskDecisionModelID = "local", "qwen"
	engine := New(cfg, nil, nil, nil)

	decider, name, err := engine.resolveBrowserDecider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if name != "local/qwen (chat model)" {
		t.Fatalf("name = %q", name)
	}
	scores, err := decider.ScoreLinks(context.Background(), "find the CPR leak facts", "https://dr.dk", []string{"Trafik — /trafik", "CPR-læk — /cpr"})
	if err != nil {
		t.Fatal(err)
	}
	if scores[0] != 0.2 || scores[1] != 0.8 || len(prompts) != 1 {
		t.Fatalf("scores = %v after %d prompts, want both links in one prompt", scores, len(prompts))
	}
	relevance, err := decider.PageRelevance(context.Background(), "find the CPR leak facts", "https://dr.dk/cpr", "8,8 millioner CPR-numre")
	if err != nil || relevance != 0.75 {
		t.Fatalf("relevance = %v, %v", relevance, err)
	}
}

func TestResolveBrowserDeciderReportsMissingDecisionModel(t *testing.T) {
	engine := New(testConfig(t), nil, nil, nil)
	if _, _, err := engine.resolveBrowserDecider(context.Background()); err == nil {
		t.Fatal("expected an error without a decision provider")
	}
}

func TestParseScoresRejectsRepliesWithoutNumbers(t *testing.T) {
	if _, err := parseScores("I think the first link is best."); err == nil {
		t.Fatal("expected an error for a reply without JSON")
	}
	if _, err := parseScores(`{"0": "high"}`); err == nil {
		t.Fatal("expected an error for non-numeric scores")
	}
}

func TestDecisionPageRelevanceShortensTextUntilTheModelReadsIt(t *testing.T) {
	var lengths []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"models":[{"name":"laya"}]}`))
		case "/v1/systemone":
			var body struct {
				State map[string]string `json:"state"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			lengths = append(lengths, len(body.State["text"]))
			if len(body.State["text"]) > 1600 {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"error":{"message":"Question 'q' exceeds state context limit (486 tokens)","status":422}}`))
				return
			}
			_, _ = w.Write([]byte(`{"answers":{"q":{"probabilities":{"yes":0.7,"no":0.3}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers["decisions"] = config.Provider{BaseURL: server.URL + "/v1", Features: &config.ProviderFeatures{Decisions: true}}
	decider, _, err := New(cfg, nil, nil, nil).resolveBrowserDecider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	score, err := decider.PageRelevance(context.Background(), "rail plan", "https://example.com", strings.Repeat("word ", 2000))
	if err != nil || score != 0.7 {
		t.Fatalf("score = %v, %v", score, err)
	}
	if len(lengths) != 3 || lengths[0] != 6000 || lengths[2] != 1500 {
		t.Fatalf("excerpt lengths = %v, want 6000 halved until it fits", lengths)
	}
}

func TestBrowserExtractorUsesTheChatModelAndReadsNone(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		prompt := body["messages"].([]any)[0].(map[string]any)["content"].(string)
		reply := "- Lisbon: €6,124/m² (Q1 2026)"
		if strings.Contains(prompt, "cookie policy") {
			reply = "NONE."
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers["local"] = config.Provider{BaseURL: server.URL + "/v1", Features: &config.ProviderFeatures{Chat: true}}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "local", ModelID: "qwen", ContextWindow: 32768})
	extractor, name, err := New(cfg, nil, nil, nil).resolveBrowserExtractor(context.Background(), browserapi.Chat{ProviderID: "local", ModelID: "qwen"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "local/qwen" {
		t.Fatalf("name = %q", name)
	}
	facts, err := extractor.ExtractFacts(context.Background(), "house prices", "https://example.com/prices", "Lisbon asking prices reached €6,124/m² in Q1 2026.")
	if err != nil || facts != "- Lisbon: €6,124/m² (Q1 2026)" {
		t.Fatalf("facts = %q, %v", facts, err)
	}
	if facts, err := extractor.ExtractFacts(context.Background(), "house prices", "https://example.com/cookies", "Our cookie policy."); err != nil || facts != "" {
		t.Fatalf("facts for an empty page = %q, %v", facts, err)
	}
	if bodies[0]["max_tokens"] == nil {
		t.Fatal("extraction has no max_tokens")
	}
}
