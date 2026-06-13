package tasktool

import (
	"context"
	"errors"
	"strings"

	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Create task",
		Description: "Create a pending background task.",
	})
}

func (tool) ID() tools.ID             { return tools.Task }
func (tool) BypassesPermission() bool { return true }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	body := strings.TrimSpace(args["body"])
	if body == "" {
		return nil, errors.New("body is empty")
	}
	return map[string]string{"body": body}, nil
}
func (tool) Preview(req tools.Request) string { return req.Args["body"] }
func (tool) Call(_ context.Context, opts tools.Options) (tools.Result, error) {
	req := opts.Request
	return tools.Result{Output: req.Args["body"]}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "task", req.Args["body"]
}
func (tool) FinalizeResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (tools.Result, error) {
	control, err := tools.RequireTaskControl(runtime)
	if err != nil {
		return tools.Result{}, err
	}
	task, err := control.AddTask(ctx, runtime.SessionID, req.Args["body"], planning.LegacyTaskStatusPending)
	if err != nil {
		return tools.Result{}, err
	}
	result.Stored = tools.TaskStoredResult{
		Body:   task.Body,
		Status: task.Status,
	}
	return result, nil
}
