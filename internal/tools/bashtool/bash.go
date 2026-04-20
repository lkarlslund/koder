package bashtool

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindBash }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition() (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindBash, "Run a shell command in the workspace", `{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute"}},"required":["command"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	command := strings.TrimSpace(args["command"])
	if command == "" {
		return nil, errors.New("command is empty")
	}
	return map[string]string{"command": command}, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"command": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["command"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	return tools.Presentation{Title: "Run command", Subtitle: preview, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["command"])
}
func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	cmd := exec.CommandContext(ctx, "bash", "-lc", req.Args["command"])
	cmd.Dir = runtime.Workdir
	output, err := cmd.CombinedOutput()
	return tools.Result{Output: string(output)}, err
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}
