package questiontool

import (
	"context"
	"errors"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Ask question",
		Description: "Ask the user a clarification question.",
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindQuestion }
func (tool) BypassesPermission() bool { return false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	question := strings.TrimSpace(args["question"])
	if question == "" {
		return nil, errors.New("question is empty")
	}
	return map[string]string{"question": question}, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"question": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["question"] }
func (tool) Execute(_ context.Context, _ tools.Runtime, req tools.Request) (tools.Result, error) {
	return tools.Result{
		Output: req.Args["question"],
		Stored: tools.QuestionStoredResult{Question: req.Args["question"]},
	}, nil
}
