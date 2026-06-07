package questiontool

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestNormalizeAndExecute(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty question error")
	}
	req, err := (tool{}).NormalizeArgs(map[string]string{"question": " What next? "})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{}, tools.Request{Args: req})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "What next?" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestFinalizeResult(t *testing.T) {
	result, body, err := tools.FinalizeResult(context.Background(), tools.Runtime{}, tools.Request{
		Tool: domain.ToolKindQuestion,
		Args: map[string]string{"question": "What next?"},
	}, tools.Result{
		Output: "What next?",
		Stored: tools.QuestionStoredResult{Question: "What next?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if body != "What next?" || result.Text != "What next?" {
		t.Fatalf("unexpected result body=%q result=%#v", body, result)
	}
	if _, ok := result.Data.(domain.QuestionStoredResult); !ok {
		t.Fatalf("expected typed question tool payload, got %#v", result.Data)
	}
}
