package tui

import (
	"encoding/json"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/ui"
)

func TestToolRunOutputUsesToolNameForStoredErrors(t *testing.T) {
	req := tools.Request{
		Tool: domain.ToolKindTodoUpdateItem,
		Args: map[string]string{
			"id":     "0",
			"status": "in_progress",
		},
	}
	meta, err := json.Marshal(tools.MetaWithStoredResult(req.Meta(), domain.PartKindToolOutput, req.Tool, tools.StoredResultStatusError, tools.ErrorStoredResult{
		Message: "todo_update_item failed: id must be a non-negative integer",
	}))
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}

	run := toolRunOutput(domain.Part{
		Kind:     domain.PartKindToolOutput,
		Body:     "todo_update_item failed: id must be a non-negative integer",
		MetaJSON: string(meta),
	}, nil, domain.Message{Summary: string(req.Tool)})

	if run.Status != ui.ToolRunStatusFailed {
		t.Fatalf("expected failed status, got %q", run.Status)
	}
	if run.Title != "todo_update_item" {
		t.Fatalf("expected tool name title, got %q", run.Title)
	}
	if run.Output != "id must be a non-negative integer" {
		t.Fatalf("expected trimmed error output, got %q", run.Output)
	}
}
