package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/cobra"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

const structuredOutputToolName = "structured_output"

type execOptions struct {
	cwd               string
	providerID        string
	modelID           string
	jsonSchema        string
	outputSchemaPath  string
	outputLastMessage string
	maxTurns          int
}

type execRunner struct {
	cfg    config.Config
	client *provider.Client
}

type structuredOutputSchema struct {
	raw      json.RawMessage
	compiled *jsonschema.Schema
}

func newExecCommand() *cobra.Command {
	opts := execOptions{maxTurns: 20}
	cmd := &cobra.Command{
		Use:   "exec [prompt]",
		Short: "Run one non-interactive agent task",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt, err := execPrompt(args, cmd.InOrStdin())
			if err != nil {
				return err
			}
			out, err := runExec(cmd.Context(), opts, prompt)
			if err != nil {
				return err
			}
			if opts.outputLastMessage != "" {
				if err := os.WriteFile(opts.outputLastMessage, []byte(out), 0o644); err != nil {
					return fmt.Errorf("write --output-last-message: %w", err)
				}
			}
			if strings.TrimSpace(out) != "" {
				fmt.Fprintln(cmd.OutOrStdout(), out)
			}
			return nil
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.cwd, "cwd", "", "Run in this working directory")
	flags.StringVar(&opts.providerID, "provider", "", "Provider id to use (default: configured default provider)")
	flags.StringVar(&opts.modelID, "model", "", "Model id to use (default: configured default model)")
	flags.StringVar(&opts.jsonSchema, "json-schema", "", "Inline JSON Schema or @path for structured final output")
	flags.StringVar(&opts.outputSchemaPath, "output-schema", "", "Path to a JSON Schema file for structured final output")
	flags.StringVarP(&opts.outputLastMessage, "output-last-message", "o", "", "Write final output to this file")
	flags.IntVar(&opts.maxTurns, "max-turns", opts.maxTurns, "Maximum model turns before failing")
	return cmd
}

func execPrompt(args []string, in io.Reader) (string, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "-" {
		return strings.TrimSpace(strings.Join(args, " ")), nil
	}
	data, err := io.ReadAll(in)
	if err != nil {
		return "", fmt.Errorf("read prompt from stdin: %w", err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", errors.New("prompt is empty")
	}
	return prompt, nil
}

func runExec(ctx context.Context, opts execOptions, prompt string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	if err := syncManagedUserAssets(ctx); err != nil {
		return "", err
	}
	if err := cfg.RequireProvider(); err != nil {
		return "", err
	}
	providerID := firstNonEmpty(strings.TrimSpace(opts.providerID), strings.TrimSpace(cfg.Defaults.ProviderID))
	providerCfg, ok := cfg.Provider(providerID)
	if !ok {
		return "", fmt.Errorf("provider %q not configured", providerID)
	}
	modelID := firstNonEmpty(strings.TrimSpace(opts.modelID), strings.TrimSpace(cfg.Defaults.ModelID))
	if modelID == "" {
		return "", fmt.Errorf("no model configured for provider %q", providerID)
	}
	workdir, err := execWorkdir(opts.cwd)
	if err != nil {
		return "", err
	}
	client, err := provider.New(providerID, providerCfg, nil)
	if err != nil {
		return "", err
	}
	schema, err := loadStructuredOutputSchema(opts)
	if err != nil {
		return "", err
	}
	runner := execRunner{cfg: cfg, client: client}
	return runner.run(ctx, prompt, providerID, modelID, workdir, schema, max(1, opts.maxTurns))
}

func execWorkdir(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		var err error
		trimmed, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workdir must be a directory: %s", abs)
	}
	return abs, nil
}

func loadStructuredOutputSchema(opts execOptions) (*structuredOutputSchema, error) {
	if strings.TrimSpace(opts.jsonSchema) != "" && strings.TrimSpace(opts.outputSchemaPath) != "" {
		return nil, errors.New("--json-schema and --output-schema are mutually exclusive")
	}
	source := strings.TrimSpace(opts.jsonSchema)
	if source == "" && strings.TrimSpace(opts.outputSchemaPath) != "" {
		source = "@" + strings.TrimSpace(opts.outputSchemaPath)
	}
	if source == "" {
		return nil, nil
	}
	raw, err := readSchemaSource(source)
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, errors.New("structured output schema is not valid JSON")
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("decode structured output schema: %w", err)
	}
	if !schemaRootAcceptsObject(root) {
		return nil, errors.New("structured output schema root must accept an object")
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse structured output schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", doc); err != nil {
		return nil, fmt.Errorf("load structured output schema: %w", err)
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile structured output schema: %w", err)
	}
	return &structuredOutputSchema{raw: raw, compiled: compiled}, nil
}

func readSchemaSource(source string) ([]byte, error) {
	if strings.HasPrefix(source, "@") {
		path := strings.TrimSpace(strings.TrimPrefix(source, "@"))
		if path == "" {
			return nil, errors.New("--json-schema @path is empty")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read structured output schema %q: %w", path, err)
		}
		return bytes.TrimSpace(data), nil
	}
	return []byte(source), nil
}

func schemaRootAcceptsObject(schema map[string]any) bool {
	rawType, ok := schema["type"]
	if !ok {
		return true
	}
	switch typed := rawType.(type) {
	case string:
		return typed == "object"
	case []any:
		for _, item := range typed {
			if item == "object" {
				return true
			}
		}
	}
	return false
}

func (r execRunner) run(ctx context.Context, prompt, providerID, modelID, workdir string, schema *structuredOutputSchema, maxTurns int) (string, error) {
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: directSystemPrompt(workdir, schema != nil)},
		{Role: provider.RoleUser, Content: prompt},
	}
	runtime := tools.Runtime{
		Workdir:        workdir,
		Exec:           execruntime.NewManager(),
		AccessSettings: r.cfg.Access,
	}
	defs := tools.Definitions(runtime)
	if schema != nil {
		defs = append(defs, structuredOutputDefinition(schema.raw))
	}
	for turn := 0; turn < maxTurns; turn++ {
		resp, err := r.completeChat(ctx, providerID, provider.ChatRequest{
			Model:      modelID,
			Messages:   messages,
			Tools:      defs,
			ToolChoice: "auto",
			Stream:     false,
			ExtraBody:  provider.RequestExtraBody(r.providerConfig(providerID), r.modelConfig(providerID, modelID)),
		})
		if err != nil {
			return "", err
		}
		if schema != nil {
			out, structuredCalls, validationErr := structuredOutputFromCalls(resp.ToolCalls, schema)
			if out != "" {
				return out, nil
			}
			if len(structuredCalls) > 0 {
				messages = append(messages, provider.Message{
					Role:      provider.RoleAssistant,
					Content:   resp.Text,
					ToolCalls: structuredCalls,
				})
				for _, call := range structuredCalls {
					messages = append(messages, provider.Message{
						Role:       provider.RoleTool,
						ToolCallID: strings.TrimSpace(call.ID),
						Content:    "Error: " + validationErr.Error(),
					})
				}
				continue
			}
		}
		if len(resp.ToolCalls) == 0 {
			text := strings.TrimSpace(resp.Text)
			if schema != nil {
				return "", fmt.Errorf("model produced plain text instead of calling %s", structuredOutputToolName)
			}
			return text, nil
		}
		messages = append(messages, provider.Message{
			Role:      provider.RoleAssistant,
			Content:   resp.Text,
			ToolCalls: resp.ToolCalls,
		})
		if err := r.appendToolResults(ctx, runtime, &messages, resp.ToolCalls); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("model did not finish within %d turns", maxTurns)
}

func (r *execRunner) completeChat(ctx context.Context, providerID string, req provider.ChatRequest) (provider.ChatResponse, error) {
	promptProgressPending := provider.PromptProgressProbePending(r.providerConfig(providerID)) && provider.RequestsPromptProgress(req)
	resp, err := r.client.CompleteChat(ctx, req)
	if err == nil {
		if promptProgressPending {
			r.setPromptProgressSupport(providerID, true)
		}
		return resp, nil
	}
	if promptProgressPending && provider.ShouldRetryWithoutPromptProgress(err) {
		r.setPromptProgressSupport(providerID, false)
		return r.client.CompleteChat(ctx, provider.WithoutPromptProgress(req))
	}
	return resp, err
}

func directSystemPrompt(workdir string, structured bool) string {
	var b strings.Builder
	b.WriteString("You are Koder running in non-interactive script mode.\n")
	b.WriteString("Current working directory: ")
	b.WriteString(workdir)
	b.WriteString("\nUse available tools when needed, then provide the final answer.\n")
	if structured {
		b.WriteString("The final answer must be submitted by calling the structured_output tool with JSON arguments that satisfy its schema. Do not emit the final answer as plain text.\n")
	}
	return b.String()
}

func structuredOutputDefinition(schema json.RawMessage) provider.ToolDefinition {
	return provider.ToolDefinition{
		Type: "function",
		Function: provider.FunctionDefinition{
			Name:        structuredOutputToolName,
			Description: "Submit the final structured JSON result. The first valid call ends the session. Do not use this for intermediate work.",
			Parameters:  schema,
		},
	}
}

func structuredOutputFromCalls(calls []provider.ToolCall, schema *structuredOutputSchema) (string, []provider.ToolCall, error) {
	var firstErr error
	structuredCalls := make([]provider.ToolCall, 0, len(calls))
	for _, call := range calls {
		if strings.TrimSpace(call.Function.Name) != structuredOutputToolName {
			continue
		}
		structuredCalls = append(structuredCalls, call)
		raw := strings.TrimSpace(call.Function.Arguments)
		if raw == "" {
			raw = "{}"
		}
		var payload any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			firstErr = fmt.Errorf("decode %s arguments: %w", structuredOutputToolName, err)
			continue
		}
		if err := schema.compiled.Validate(payload); err != nil {
			firstErr = fmt.Errorf("%s arguments do not match schema: %w", structuredOutputToolName, err)
			continue
		}
		normalized, err := json.Marshal(payload)
		if err != nil {
			return "", structuredCalls, fmt.Errorf("encode structured output: %w", err)
		}
		return string(normalized), structuredCalls, nil
	}
	if firstErr == nil && len(structuredCalls) > 0 {
		firstErr = errors.New("structured output was not valid")
	}
	return "", structuredCalls, firstErr
}

func (r execRunner) appendToolResults(ctx context.Context, runtime tools.Runtime, messages *[]provider.Message, calls []provider.ToolCall) error {
	for _, call := range calls {
		if strings.TrimSpace(call.Function.Name) == structuredOutputToolName {
			continue
		}
		req, err := tools.ParseProviderCall(call)
		if err != nil {
			*messages = append(*messages, provider.Message{Role: provider.RoleTool, ToolCallID: strings.TrimSpace(call.ID), Content: "Error: " + err.Error()})
			continue
		}
		result, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: req})
		content := strings.TrimSpace(result.Output)
		if err != nil {
			content = "Error: " + err.Error()
		}
		if content == "" {
			content = "Tool completed with no output."
		}
		*messages = append(*messages, provider.Message{Role: provider.RoleTool, ToolCallID: req.ToolCallID, Content: content})
	}
	return nil
}

func (r execRunner) providerConfig(providerID string) config.Provider {
	cfg, _ := r.cfg.Provider(providerID)
	return cfg
}

func (r *execRunner) setPromptProgressSupport(providerID string, supported bool) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" || r.cfg.Providers == nil {
		return
	}
	providerCfg, ok := r.cfg.Providers[providerID]
	if !ok {
		return
	}
	if providerCfg.PromptProgressProbed && providerCfg.PromptProgressSupported == supported {
		return
	}
	providerCfg.PromptProgressMode = config.NormalizePromptProgressMode(providerCfg.PromptProgressMode)
	providerCfg.PromptProgressProbed = true
	providerCfg.PromptProgressSupported = supported
	r.cfg.Providers[providerID] = providerCfg
	if strings.TrimSpace(r.cfg.Path()) == "" {
		return
	}
	_ = r.cfg.Save()
}

func (r execRunner) modelConfig(providerID, modelID string) config.ModelConfig {
	model := r.cfg.ModelRequestOptions(providerID, modelID)
	if strings.TrimSpace(model.ProviderID) == "" {
		model.ProviderID = strings.TrimSpace(providerID)
	}
	if strings.TrimSpace(model.ModelID) == "" {
		model.ModelID = strings.TrimSpace(modelID)
	}
	return model
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
