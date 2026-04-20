package tasktool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindTask }
func (tool) BypassesPermission() bool { return true }
func (tool) Definition() (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindTask, "Record a short task for later follow-up", `{"type":"object","properties":{"body":{"type":"string","description":"Short task description"}},"required":["body"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	body := strings.TrimSpace(args["body"])
	if body == "" {
		return nil, errors.New("body is empty")
	}
	return map[string]string{"body": body}, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"body": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["body"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	return tools.Presentation{Title: "Create task", Subtitle: preview, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["body"])
}
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
	msg, err := st.AddMessage(ctx, sessionID, domain.MessageRoleTool, fmt.Sprintf("task:%s", task.Status))
	if err != nil {
		return nil, err
	}
	meta, _ := json.Marshal(map[string]string{"status": string(task.Status)})
	if _, err := st.AddPart(ctx, msg.ID, domain.PartKindTaskUpdate, task.Body, string(meta)); err != nil {
		return nil, err
	}
	out := make(chan domain.Event, 1)
	out <- domain.Event{Kind: domain.EventKindTaskUpdate, Text: task.Body, Tool: req.Tool}
	close(out)
	return out, nil
}
