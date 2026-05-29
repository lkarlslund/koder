package tasktool

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
		Title:       "Create task",
		Description: "Create a pending background task.",
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindTask }
func (tool) BypassesPermission() bool { return true }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	body := strings.TrimSpace(args["body"])
	if body == "" {
		return nil, errors.New("body is empty")
	}
	return map[string]string{"body": body}, nil
}
func (tool) Preview(req tools.Request) string { return req.Args["body"] }
func (tool) Execute(_ context.Context, _ tools.Runtime, req tools.Request) (tools.Result, error) {
	return tools.Result{Output: req.Args["body"]}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "task", req.Args["body"]
}
func (tool) PersistResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	control, err := tools.RequireSessionControl(runtime)
	if err != nil {
		return nil, err
	}
	task, err := control.AddTask(ctx, runtime.SessionID, req.Args["body"], domain.TaskStatusPending)
	if err != nil {
		return nil, err
	}
	result.Stored = domain.TaskStoredResult{
		Body:   task.Body,
		Status: task.Status,
	}
	events, err := tools.PersistStandardResult(ctx, runtime, req, result)
	if err != nil {
		return nil, err
	}
	return taskUpdateEvents(task.Body, req.Tool, events), nil
}

func taskUpdateEvents(body string, tool domain.ToolKind, events <-chan domain.Event) <-chan domain.Event {
	out := make(chan domain.Event)
	go func() {
		defer close(out)
		for evt := range events {
			if evt.Kind == domain.EventKindToolResult {
				evt.Kind = domain.EventKindTaskUpdate
				evt.Text = body
				evt.Tool = tool
			}
			out <- evt
		}
	}()
	return out
}
