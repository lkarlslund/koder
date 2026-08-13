package questiontool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/tools"
)

func TestNormalizeAndPreview(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected missing questions error")
	}
	raw := `[{"id":" target ","header":" Target ","question":" Which target? ","options":[{"label":" A ","description":" First "},{"label":" B ","description":" Second "}]}]`
	args, err := (tool{}).NormalizeArgs(map[string]string{"questions": raw})
	if err != nil {
		t.Fatal(err)
	}
	if got := (tool{}).Preview(tools.Request{Args: args}); got != "Which target?" {
		t.Fatalf("unexpected preview: %q", got)
	}
	questions, err := tools.ParseUserInputQuestions(args["questions"])
	if err != nil {
		t.Fatal(err)
	}
	if questions[0].ID != "target" || questions[0].Options[0].Label != "A" {
		t.Fatalf("arguments were not normalized: %#v", questions)
	}
}

func TestToolIsExposedAndRuntimeHandled(t *testing.T) {
	spec := tools.Info(tools.RequestUserInput)
	if !spec.ExposeToLLM || !strings.Contains(spec.Parameters, `"questions"`) {
		t.Fatalf("unexpected tool spec: %#v", spec)
	}
	_, err := (tool{}).Call(context.Background(), tools.Options{})
	if err == nil || !strings.Contains(err.Error(), "chat runtime") {
		t.Fatalf("expected runtime-handled error, got %v", err)
	}
}
