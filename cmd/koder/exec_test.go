package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lkarlslund/koder/internal/toolkind"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestLoadStructuredOutputSchemaRejectsNonObjectRoot(t *testing.T) {
	_, err := loadStructuredOutputSchema(execOptions{jsonSchema: `{"type":"array"}`})
	if err == nil || !strings.Contains(err.Error(), "root must accept an object") {
		t.Fatalf("expected object root error, got %v", err)
	}
}

func TestRunExecSubmitsStructuredOutputTool(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		requests = append(requests, payload)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"structured_output","arguments":"{\"answer\":\"ok\"}"}}]}}]}`))
	}))
	defer server.Close()

	withExecTestConfig(t, server.URL+"/v1")
	got, err := runExec(context.Background(), execOptions{
		cwd:        t.TempDir(),
		jsonSchema: `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`,
		maxTurns:   3,
	}, "return answer")
	if err != nil {
		t.Fatalf("runExec: %v", err)
	}
	if got != `{"answer":"ok"}` {
		t.Fatalf("unexpected structured output %s", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	tools, _ := requests[0]["tools"].([]any)
	if !requestHasTool(tools, "structured_output") {
		t.Fatalf("request did not expose structured_output: %#v", tools)
	}
}

func TestRunExecHidesStructuredOutputWithoutSchema(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		requests = append(requests, payload)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"plain answer"}}]}`))
	}))
	defer server.Close()

	withExecTestConfig(t, server.URL+"/v1")
	got, err := runExec(context.Background(), execOptions{cwd: t.TempDir(), maxTurns: 1}, "return answer")
	if err != nil {
		t.Fatalf("runExec: %v", err)
	}
	if got != "plain answer" {
		t.Fatalf("unexpected output %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	requestTools, _ := requests[0]["tools"].([]any)
	if requestHasTool(requestTools, structuredOutputToolName) {
		t.Fatalf("request exposed %s without schema: %#v", structuredOutputToolName, requestTools)
	}
}

func TestRunExecRetriesInvalidStructuredOutput(t *testing.T) {
	var turns int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turns++
		w.Header().Set("Content-Type", "application/json")
		if turns == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"bad_1","type":"function","function":{"name":"structured_output","arguments":"{\"answer\":1}"}}]}}]}`))
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !requestContainsToolError(payload, "bad_1") {
			t.Fatalf("second request did not include structured output validation error: %#v", payload["messages"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"good_1","type":"function","function":{"name":"structured_output","arguments":"{\"answer\":\"ok\"}"}}]}}]}`))
	}))
	defer server.Close()

	withExecTestConfig(t, server.URL+"/v1")
	got, err := runExec(context.Background(), execOptions{
		cwd:        t.TempDir(),
		jsonSchema: `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`,
		maxTurns:   3,
	}, "return answer")
	if err != nil {
		t.Fatalf("runExec: %v", err)
	}
	if got != `{"answer":"ok"}` {
		t.Fatalf("unexpected structured output %s", got)
	}
	if turns != 2 {
		t.Fatalf("expected retry, got %d turns", turns)
	}
}

func TestRunExecPlainTextFailsWhenStructuredOutputRequired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"plain answer"}}]}`))
	}))
	defer server.Close()

	withExecTestConfig(t, server.URL+"/v1")
	_, err := runExec(context.Background(), execOptions{
		cwd:        t.TempDir(),
		jsonSchema: `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`,
		maxTurns:   1,
	}, "return answer")
	if err == nil || !strings.Contains(err.Error(), "plain text") {
		t.Fatalf("expected plain text structured output error, got %v", err)
	}
}

func TestStructuredOutputToolNotRegisteredGlobally(t *testing.T) {
	if kind, err := toolkind.KindString(structuredOutputToolName); err == nil {
		if _, ok := tools.DefinitionFor(kind, tools.Runtime{}); ok {
			t.Fatalf("%s should not be registered as a normal tool", structuredOutputToolName)
		}
	}
}

func withExecTestConfig(t *testing.T, baseURL string) {
	t.Helper()
	home := t.TempDir()
	configRoot := filepath.Join(home, "config")
	stateRoot := filepath.Join(home, "state")
	cacheRoot := filepath.Join(home, "cache")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	cfgDir := filepath.Join(configRoot, "koder")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	cfg := `default_provider = "test"
default_model = "model"

[providers.test]
base_url = "` + baseURL + `"
api_key = "test-key"
default_model = "model"
stream = false
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func requestHasTool(tools []any, name string) bool {
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		if fn["name"] == name {
			return true
		}
	}
	return false
}

func requestContainsToolError(payload map[string]any, toolCallID string) bool {
	messages, _ := payload["messages"].([]any)
	for _, item := range messages {
		msg, _ := item.(map[string]any)
		content, _ := msg["content"].(string)
		role := strings.ToLower(fmt.Sprint(msg["role"]))
		if role == "tool" && msg["tool_call_id"] == toolCallID && strings.Contains(content, "Error:") {
			return true
		}
	}
	return false
}
