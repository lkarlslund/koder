package presenttool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/lkarlslund/koder/internal/tools"
)

const (
	presentationMIME = "application/vnd.koder.presentation+json"
	parameters       = `{"type":"object","properties":{"title":{"type":"string","description":"Short heading for the visual card"},"mime_type":{"type":"string","enum":["text/plain","text/markdown","application/json"],"description":"Representation type for legacy free-form content"},"content":{"type":"string","description":"Legacy free-form material to display"},"card":{"type":"object","description":"Versioned generic visual card. Prefer this for structured content.","properties":{"version":{"type":"integer","enum":[1]},"blocks":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"object","properties":{"kind":{"type":"string","enum":["text","image","key_value","list","progress","action","file"]},"text":{"type":"string"},"style":{"type":"string","enum":["body","heading","caption","code"]},"uri":{"type":"string"},"title":{"type":"string"},"alt":{"type":"string"},"label":{"type":"string"},"detail":{"type":"string"},"name":{"type":"string"},"mime_type":{"type":"string"},"value":{"type":"integer"},"max":{"type":"integer"},"items":{"type":"array","maxItems":100,"items":{"type":"object","properties":{"key":{"type":"string"},"value":{"type":"string"},"title":{"type":"string"},"detail":{"type":"string"}},"additionalProperties":false}}},"required":["kind"],"additionalProperties":false}}},"required":["version","blocks"],"additionalProperties":false}},"anyOf":[{"required":["mime_type","content"]},{"required":["card"]}],"additionalProperties":false}`
)

type document struct {
	Version int     `json:"version"`
	Blocks  []block `json:"blocks"`
}

type block struct {
	Kind     string `json:"kind"`
	Text     string `json:"text,omitempty"`
	Style    string `json:"style,omitempty"`
	URI      string `json:"uri,omitempty"`
	Title    string `json:"title,omitempty"`
	Alt      string `json:"alt,omitempty"`
	Label    string `json:"label,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Name     string `json:"name,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	Value    *int   `json:"value,omitempty"`
	Max      *int   `json:"max,omitempty"`
	Items    []item `json:"items,omitempty"`
}

type item struct {
	Key    string `json:"key,omitempty"`
	Value  string `json:"value,omitempty"`
	Title  string `json:"title,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Present on companion screen",
		Description: "Deliberately show visual material separately from the spoken response.",
		Usage:       "Use when the user asks to see something, or when visual detail is useful. Prefer card with version 1 and generic blocks: text {text,style}, image {uri,title,alt}, key_value {items:[{key,value}]}, list {items:[{title,detail}]}, progress {label,value,max,detail}, action {label,uri}, or file {name,uri,mime_type,detail}. Then give a short plain conversational final response that refers to it; do not copy the presentation into that response. Legacy text/plain, text/markdown, and application/json content remain available.",
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
	return spec, runtime.VoiceInteraction()
}

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	if raw := strings.TrimSpace(args["card"]); raw != "" {
		if strings.TrimSpace(args["content"]) != "" || strings.TrimSpace(args["mime_type"]) != "" {
			return nil, errors.New("presentation card cannot be combined with legacy content")
		}
		canonical, err := normalizeDocument(raw)
		if err != nil {
			return nil, err
		}
		out := map[string]string{"mime_type": presentationMIME, "content": canonical}
		if title := strings.TrimSpace(args["title"]); title != "" {
			out["title"] = truncate(title, 120)
		}
		return out, nil
	}
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

func normalizeDocument(raw string) (string, error) {
	if len(raw) > 64*1024 {
		return "", errors.New("presentation card exceeds 64 KiB")
	}
	var doc document
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return "", fmt.Errorf("invalid presentation card: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("invalid presentation card: trailing JSON value")
	}
	if doc.Version != 1 {
		return "", fmt.Errorf("unsupported presentation card version %d", doc.Version)
	}
	if len(doc.Blocks) == 0 || len(doc.Blocks) > 32 {
		return "", errors.New("presentation card must contain 1 to 32 blocks")
	}
	for index := range doc.Blocks {
		if err := validateBlock(&doc.Blocks[index]); err != nil {
			return "", fmt.Errorf("presentation block %d: %w", index+1, err)
		}
	}
	canonical, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("encode presentation card: %w", err)
	}
	return string(canonical), nil
}

func validateBlock(value *block) error {
	value.Kind = strings.TrimSpace(strings.ToLower(value.Kind))
	value.Text = truncate(value.Text, 16*1024)
	value.Style = strings.TrimSpace(strings.ToLower(value.Style))
	value.URI = strings.TrimSpace(value.URI)
	value.Title = truncate(value.Title, 200)
	value.Alt = truncate(value.Alt, 500)
	value.Label = truncate(value.Label, 200)
	value.Detail = truncate(value.Detail, 2*1024)
	value.Name = truncate(value.Name, 240)
	value.MIMEType = truncate(strings.TrimSpace(strings.ToLower(value.MIMEType)), 200)
	if len(value.Items) > 100 {
		return errors.New("contains more than 100 items")
	}
	for index := range value.Items {
		value.Items[index].Key = truncate(value.Items[index].Key, 200)
		value.Items[index].Value = truncate(value.Items[index].Value, 2*1024)
		value.Items[index].Title = truncate(value.Items[index].Title, 500)
		value.Items[index].Detail = truncate(value.Items[index].Detail, 2*1024)
	}
	switch value.Kind {
	case "text":
		if strings.TrimSpace(value.Text) == "" {
			return errors.New("text block requires text")
		}
		if value.Style != "" && value.Style != "body" && value.Style != "heading" && value.Style != "caption" && value.Style != "code" {
			return fmt.Errorf("unsupported text style %q", value.Style)
		}
	case "image":
		if err := validateURI(value.URI); err != nil {
			return fmt.Errorf("image: %w", err)
		}
	case "key_value":
		if len(value.Items) == 0 {
			return errors.New("key_value block requires items")
		}
	case "list":
		if len(value.Items) == 0 {
			return errors.New("list block requires items")
		}
	case "progress":
		if value.Value == nil || value.Max == nil || *value.Max <= 0 || *value.Value < 0 || *value.Value > *value.Max {
			return errors.New("progress requires value between zero and a positive max")
		}
	case "action":
		if strings.TrimSpace(value.Label) == "" {
			return errors.New("action block requires label")
		}
		if err := validateURI(value.URI); err != nil {
			return fmt.Errorf("action: %w", err)
		}
	case "file":
		if strings.TrimSpace(value.Name) == "" {
			return errors.New("file block requires name")
		}
		if err := validateURI(value.URI); err != nil {
			return fmt.Errorf("file: %w", err)
		}
	default:
		return fmt.Errorf("unsupported kind %q", value.Kind)
	}
	return nil
}

func validateURI(raw string) error {
	if raw == "" {
		return errors.New("requires uri")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("has invalid uri")
	}
	if parsed.IsAbs() && parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("uri must be relative, HTTP, or HTTPS")
	}
	if strings.HasPrefix(raw, "//") {
		return errors.New("scheme-relative uri is not allowed")
	}
	return nil
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
