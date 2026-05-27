package provider

import "testing"

func TestShouldRetryWithoutPromptProgressMatchesRejectedParameter(t *testing.T) {
	err := &APIError{StatusCode: 400, Body: `{"error":{"message":"unknown field return_progress"}}`}
	if !ShouldRetryWithoutPromptProgress(err) {
		t.Fatal("expected prompt progress rejection to be retryable")
	}
}

func TestWithoutPromptProgressRemovesOnlyProgressFlag(t *testing.T) {
	got := WithoutPromptProgress(ChatRequest{
		ExtraBody: map[string]any{
			"return_progress": true,
			"max_tokens":      1,
		},
	})
	if _, ok := got.ExtraBody["return_progress"]; ok {
		t.Fatalf("expected return_progress to be removed: %#v", got.ExtraBody)
	}
	if got.ExtraBody["max_tokens"] != 1 {
		t.Fatalf("expected unrelated fields to remain: %#v", got.ExtraBody)
	}
}

func TestRequestsPromptProgress(t *testing.T) {
	if !RequestsPromptProgress(ChatRequest{ExtraBody: map[string]any{"return_progress": true}}) {
		t.Fatal("expected true return_progress to request prompt progress")
	}
	if RequestsPromptProgress(ChatRequest{ExtraBody: map[string]any{"return_progress": false}}) {
		t.Fatal("did not expect false return_progress to request prompt progress")
	}
}
