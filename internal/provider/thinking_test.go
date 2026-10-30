package provider

import (
	"reflect"
	"testing"
)

func TestWithoutThinking(t *testing.T) {
	kwargs := map[string]any{"enable_thinking": true, "preserve_thinking": true, "other": 1}
	req := ChatRequest{ExtraBody: map[string]any{"chat_template_kwargs": kwargs, "reasoning_effort": "medium", "return_progress": true}}
	next, ok := WithoutThinking(req)
	want := map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false, "preserve_thinking": false, "other": 1}, "return_progress": true}
	if !ok || !reflect.DeepEqual(next.ExtraBody, want) {
		t.Fatalf("extra body = %#v, %v", next.ExtraBody, ok)
	}
	if kwargs["enable_thinking"] != true || req.ExtraBody["reasoning_effort"] != "medium" {
		t.Fatalf("original request changed: %#v", req.ExtraBody)
	}

	next, ok = WithoutThinking(ChatRequest{ExtraBody: map[string]any{"enable_thinking": true}})
	if !ok || next.ExtraBody["enable_thinking"] != false {
		t.Fatalf("DashScope body = %#v, %v", next.ExtraBody, ok)
	}

	if _, ok := WithoutThinking(ChatRequest{ExtraBody: map[string]any{"return_progress": true}}); ok {
		t.Fatal("a request without thinking controls was changed")
	}
}

func TestClientWithoutThinkingAsksCustomServers(t *testing.T) {
	req := ChatRequest{ExtraBody: map[string]any{"return_progress": true}}
	custom := &Client{kind: ProviderKindCompatible}
	next, ok := custom.WithoutThinking(req)
	want := map[string]any{"return_progress": true, "chat_template_kwargs": map[string]any{"enable_thinking": false}}
	if !ok || !reflect.DeepEqual(next.ExtraBody, want) {
		t.Fatalf("custom server body = %#v, %v", next.ExtraBody, ok)
	}
	if _, ok := req.ExtraBody["chat_template_kwargs"]; ok {
		t.Fatal("original request changed")
	}
	if _, ok := (&Client{kind: "openai"}).WithoutThinking(req); ok {
		t.Fatal("a hosted API was sent an unrequested thinking control")
	}
}
