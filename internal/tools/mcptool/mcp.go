package mcptool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "MCP",
		Description: "Run a tool exposed by a connected MCP server.",
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindMCP }
func (tool) BypassesPermission() bool { return false }

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	serverID := strings.TrimSpace(tools.FirstArg(args, "server", "server_id"))
	toolName := strings.TrimSpace(tools.FirstArg(args, "tool", "tool_name"))
	if serverID == "" {
		return nil, errors.New("mcp server is required")
	}
	if toolName == "" {
		return nil, errors.New("mcp tool is required")
	}
	raw := strings.TrimSpace(args["arguments_raw"])
	if raw == "" {
		raw = "{}"
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("mcp tool arguments must be a JSON object: %w", err)
	}
	return map[string]string{
		"server":        serverID,
		"tool":          toolName,
		"arguments_raw": raw,
	}, nil
}

func (tool) LegacyArgs(raw string) map[string]string {
	return map[string]string{"arguments_raw": raw}
}

func (tool) Preview(req tools.Request) string {
	serverID := strings.TrimSpace(req.Args["server"])
	toolName := strings.TrimSpace(req.Args["tool"])
	switch {
	case serverID != "" && toolName != "":
		return serverID + "/" + toolName
	case toolName != "":
		return toolName
	default:
		return string(req.Tool)
	}
}

func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	if runtime.MCP == nil {
		return tools.Result{}, errors.New("mcp manager is unavailable")
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(req.Args["arguments_raw"]), &args); err != nil {
		return tools.Result{}, fmt.Errorf("decode mcp arguments: %w", err)
	}
	return runtime.MCP.ExecuteTool(ctx, req.Args["server"], req.Args["tool"], args)
}

func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	preview := tool{}.Preview(req)
	body := strings.TrimSpace(result.Output)
	if body == "" {
		body = "MCP tool completed with no output"
	}
	return "mcp:" + preview, body
}
