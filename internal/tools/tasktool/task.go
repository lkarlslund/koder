package tasktool

import (
	"context"
	"errors"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
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
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"body": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["body"] }
func (tool) Execute(_ context.Context, _ tools.Runtime, req tools.Request) (tools.Result, error) {
	return tools.Result{Output: req.Args["body"]}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "task", req.Args["body"]
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	task, err := st.AddTask(ctx, sessionID, req.Args["body"], domain.TaskStatusPending)
	if err != nil {
		return nil, err
	}
	chatID, ok := tools.ChatIDFromContext(ctx)
	if !ok || chatID <= 0 {
		chat, err := st.DefaultChat(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		chatID = chat.ID
	}
	resultItem, err := st.AppendTimeline(ctx, chatID, domain.ToolExecution{
		Tool: req.Tool,
		Args: req.Meta(),
		Result: &domain.ToolResult{
			Text:   task.Body,
			Status: domain.ToolResultStatusOK,
			Data: domain.TaskStoredResult{
				Body:   task.Body,
				Status: task.Status,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	resultItem.Seal(resultItem.UpdatedAt)
	if err := st.Timeline().Put(ctx, resultItem); err != nil {
		return nil, err
	}
	out := make(chan domain.Event, 1)
	out <- domain.Event{Kind: domain.EventKindTaskUpdate, Text: task.Body, Tool: req.Tool, Item: resultItem}
	close(out)
	return out, nil
}
