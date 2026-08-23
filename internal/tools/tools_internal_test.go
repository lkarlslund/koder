package tools

import (
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
)

type badStoredResult struct {
	Bad func()
}

func (badStoredResult) storedResultPayload() {}

func TestBuildToolResultKeepsToolOwnedData(t *testing.T) {
	result, _, err := BuildToolResult(Request{
		Tool: domain.ToolKindQuestion,
		Args: map[string]string{"question": "What next?"},
	}, Result{
		Output: "What next?",
		Stored: badStoredResult{Bad: func() {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.Data.(badStoredResult); !ok {
		t.Fatalf("expected tool-owned data to be preserved, got %#v", result.Data)
	}
}

func TestBuildToolResultPreservesExplicitErrorStatus(t *testing.T) {
	result, _, err := BuildToolResult(Request{Tool: FileRead}, Result{Output: "failed", Status: domain.ToolResultStatusError})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.ToolResultStatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
}
