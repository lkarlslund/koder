package presenttool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/tools"
)

const parameters = `{"type":"object","properties":{"title":{"type":"string","description":"Short heading for the visual card"},"mime_type":{"type":"string","enum":["text/plain","text/markdown","application/json"],"description":"Generic representation type"},"content":{"type":"string","description":"The material to display; Markdown tables and lists are allowed here"}},"required":["mime_type","content"],"additionalProperties":false}`

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Present on companion screen",
		Description: "Deliberately show visual material separately from the spoken response.",
		Usage:       "Use when the user asks to see something, or when a table, list, code, structured data, or longer detail is useful. Put the visual material here, then give a short plain conversational final response that refers to it. Do not copy the presentation into the final response. Use text/markdown for human-readable tables or structured layouts, text/plain for unformatted notes, and application/json for machine-readable data.",
		Parameters:  parameters,
		ExposeToLLM: true,
	})
}

func (tool) ID() tools.ID             { return tools.Present }
func (tool) BypassesPermission() bool { return true }
func (tool) Preview(req tools.Request) string {
	return strings.TrimSpace(req.Args["title"])
}

func (tool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return spec, runtime.ChatRole == chatrole.Voice
}

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	mimeType := strings.TrimSpace(strings.ToLower(args["mime_type"]))
	switch mimeType {
	case "text/plain", "text/markdown", "application/json":
	default:
		return nil, fmt.Errorf("unsupported presentation MIME type %q", mimeType)
	}
	content := strings.TrimSpace(args["content"])
	if content == "" {
		return nil, errors.New("presentation content is required")
	}
	if len(content) > 64*1024 {
		return nil, errors.New("presentation content exceeds 64 KiB")
	}
	out := map[string]string{"mime_type": mimeType, "content": content}
	if title := strings.TrimSpace(args["title"]); title != "" {
		out["title"] = truncate(title, 120)
	}
	return out, nil
}

func (tool) Call(_ context.Context, opts tools.Options) (tools.Result, error) {
	req := opts.Request.Args
	stored := tools.PresentationStoredResult{Title: req["title"], MIMEType: req["mime_type"], Content: req["content"]}
	label := "Presented visual content"
	if stored.Title != "" {
		label = "Presented " + stored.Title
	}
	return tools.Result{Output: label, Stored: stored}, nil
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit]))
}
