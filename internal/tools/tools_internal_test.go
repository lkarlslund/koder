package tools

import (
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
)

type badStoredResult struct {
	Bad func()
}

func (badStoredResult) storedResultPayload() {}

func TestBuildToolResultReturnsStoredMarshalError(t *testing.T) {
	_, _, err := BuildToolResult(Request{
		Tool: domain.ToolKindQuestion,
		Args: map[string]string{"question": "What next?"},
	}, Result{
		Output: "What next?",
		Stored: badStoredResult{Bad: func() {}},
	})
	if err == nil {
		t.Fatal("expected stored result marshal error")
	}
}
