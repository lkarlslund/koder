package questiontool

import (
	"context"
	"errors"
	"strings"

	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Ask question",
		Description: "Ask the user a clarification question.",
	})
}

func (tool) ID() tools.ID             { return tools.Question }
func (tool) BypassesPermission() bool { return false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	question := strings.TrimSpace(args["question"])
	if question == "" {
		return nil, errors.New("question is empty")
	}
	return map[string]string{"question": question}, nil
}
func (tool) Preview(req tools.Request) string { return req.Args["question"] }
func (tool) Call(_ context.Context, opts tools.Options) (tools.Result, error) {
	req := opts.Request
	return tools.Result{
		Output: req.Args["question"],
		Stored: tools.QuestionStoredResult{Question: req.Args["question"]},
	}, nil
}
