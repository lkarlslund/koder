package claudedriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/claudeapp"
	"github.com/lkarlslund/koder/internal/domain"
)

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// streamMessage covers the stream-json message types a turn reads. Messages
// with a parent tool use belong to a subagent and are not shown.
type streamMessage struct {
	Type            string  `json:"type"`
	Subtype         string  `json:"subtype"`
	SessionID       string  `json:"session_id"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	Message         struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	} `json:"event"`
	IsError bool     `json:"is_error"`
	Result  string   `json:"result"`
	Errors  []string `json:"errors"`
}

type runningTool struct {
	kind  domain.ToolKind
	name  string
	input map[string]any
}

type turnState struct {
	ctx       context.Context
	rt        *chat.Chat
	out       chan<- domain.Event
	item      domain.TimelineItem
	text      strings.Builder
	reasoning strings.Builder
	messages  int
	running   map[string]runningTool
}

func (m *Manager) consumeTurn(ctx context.Context, process *chatProcess, rt *chat.Chat, out chan<- domain.Event) error {
	client := process.client
	state := &turnState{ctx: ctx, rt: rt, out: out, running: map[string]runningTool{}}
	for {
		select {
		case <-ctx.Done():
			m.interrupt(process)
			return ctx.Err()
		case <-client.Done():
			// Everything the process wrote is already queued; keep it.
			for {
				select {
				case msg := <-client.Messages():
					if done, err := m.handleMessage(state, process, msg); done {
						return err
					}
					continue
				default:
				}
				break
			}
			return errors.Join(state.flush(), client.Err())
		case msg := <-client.Messages():
			if done, err := m.handleMessage(state, process, msg); done {
				return err
			}
		}
	}
}

// interrupt stops the running turn and waits for Claude Code to finish it, so
// the next turn does not read this one's result. A process that does not
// stop in time is discarded.
func (m *Manager) interrupt(process *chatProcess) {
	ctx, cancel := context.WithTimeout(context.Background(), m.interruptTimeout)
	defer cancel()
	client := process.client
	if _, err := client.Control(ctx, map[string]any{"subtype": "interrupt"}); err == nil {
		for {
			select {
			case msg := <-client.Messages():
				if msg.Type == "result" {
					return
				}
				if msg.Type == "control_request" {
					var request claudeapp.ControlRequest
					if json.Unmarshal(msg.Raw, &request) == nil {
						_ = client.Respond(request.RequestID, nil, errors.New("the turn was interrupted"))
					}
				}
				continue
			case <-client.Done():
			case <-ctx.Done():
			}
			break
		}
	}
	m.processMu.Lock()
	for chatID, candidate := range m.processes {
		if candidate == process {
			delete(m.processes, chatID)
			if process.idle != nil {
				process.idle.Stop()
			}
		}
	}
	m.processMu.Unlock()
	_ = client.Close()
}

// handleMessage applies one stream message to the timeline. It reports done
// when the turn has ended.
func (m *Manager) handleMessage(state *turnState, process *chatProcess, msg claudeapp.Message) (bool, error) {
	if msg.Type == "control_request" {
		return false, m.handleToolCall(state, process, msg.Raw)
	}
	var value streamMessage
	if err := json.Unmarshal(msg.Raw, &value); err != nil {
		return false, nil
	}
	if value.SessionID != "" {
		process.sessionID = value.SessionID
	}
	if value.ParentToolUseID != nil && *value.ParentToolUseID != "" {
		return false, nil
	}
	switch value.Type {
	case "stream_event":
		if value.Event.Type != "content_block_delta" {
			return false, nil
		}
		switch value.Event.Delta.Type {
		case "text_delta":
			state.ensureItem()
			state.text.WriteString(value.Event.Delta.Text)
			state.out <- domain.Event{Kind: domain.EventKindMessageDelta, Text: value.Event.Delta.Text, Item: state.item}
		case "thinking_delta":
			state.ensureItem()
			state.reasoning.WriteString(value.Event.Delta.Thinking)
			state.out <- domain.Event{Kind: domain.EventKindReasoning, Text: value.Event.Delta.Thinking, Item: state.item}
		}
	case "assistant":
		return false, state.assistant(value.Message.Content)
	case "user":
		return false, state.toolResults(value.Message.Content)
	case "result":
		if err := state.flush(); err != nil {
			return true, err
		}
		if value.IsError || (value.Subtype != "" && value.Subtype != "success") {
			return true, resultError(value)
		}
		if state.messages == 0 {
			return true, errors.New("claude turn completed without a response")
		}
		return true, nil
	}
	return false, nil
}

func resultError(value streamMessage) error {
	detail := strings.TrimSpace(value.Result)
	if len(value.Errors) > 0 {
		detail = strings.TrimSpace(strings.Join(value.Errors, "; "))
	}
	if detail == "" {
		detail = strings.ReplaceAll(value.Subtype, "_", " ")
	}
	return fmt.Errorf("claude turn failed: %s", detail)
}

func (s *turnState) ensureItem() {
	if s.item.ID == "" {
		s.item = s.rt.NextAssistantItem()
	}
}

// flush stores the text and reasoning streamed so far as one assistant message.
func (s *turnState) flush() error {
	if strings.TrimSpace(s.text.String()) == "" && strings.TrimSpace(s.reasoning.String()) == "" {
		return nil
	}
	s.ensureItem()
	item, err := s.rt.AppendAssistantMessage(s.ctx, s.item, domain.AssistantMessage{
		Text:      s.text.String(),
		Reasoning: domain.ReasoningContent{Text: s.reasoning.String()},
	})
	if err != nil {
		return err
	}
	s.out <- domain.Event{Kind: domain.EventKindMessageDone, Item: item}
	s.messages++
	s.text.Reset()
	s.reasoning.Reset()
	s.item = domain.TimelineItem{}
	return nil
}

// assistant handles one complete assistant message. Its text has normally
// been streamed already; the complete block fills in anything that was not.
func (s *turnState) assistant(raw json.RawMessage) error {
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	for _, block := range blocks {
		switch block.Type {
		case "thinking":
			if s.reasoning.Len() == 0 && strings.TrimSpace(block.Thinking) != "" {
				s.ensureItem()
				s.reasoning.WriteString(block.Thinking)
			}
		case "text":
			if s.text.Len() == 0 && strings.TrimSpace(block.Text) != "" {
				s.ensureItem()
				s.text.WriteString(block.Text)
				s.out <- domain.Event{Kind: domain.EventKindMessageDelta, Text: block.Text, Item: s.item}
			}
			if err := s.flush(); err != nil {
				return err
			}
		case "tool_use":
			if err := s.flush(); err != nil {
				return err
			}
			if hiddenTools[block.Name] {
				continue
			}
			var input map[string]any
			_ = json.Unmarshal(block.Input, &input)
			kind := toolKind(block.Name)
			call := domain.ToolCall{
				ToolCallID: domain.ToolCallID(block.ID),
				Tool:       kind,
				Args:       toolArgs(kind, block.Name, input, block.Input),
				Status:     domain.ToolStatusRunning,
				StartedAt:  time.Now().UTC(),
			}
			if _, err := s.rt.AppendAssistantToolCalls(s.ctx, s.rt.NextAssistantItem(), []domain.ToolCall{call}, "", domain.ReasoningContent{}, domain.Usage{}, domain.ModelPerformance{}); err != nil {
				return err
			}
			s.running[block.ID] = runningTool{kind: kind, name: block.Name, input: input}
			s.out <- domain.Event{Kind: domain.EventKindToolStart, Tool: kind, ToolCallID: block.ID}
		}
	}
	return nil
}

// toolResults records results of Claude Code's own tools. Koder's tools
// record their results when they run.
func (s *turnState) toolResults(raw json.RawMessage) error {
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	for _, block := range blocks {
		if block.Type != "tool_result" {
			continue
		}
		tool, ok := s.running[block.ToolUseID]
		if !ok {
			continue
		}
		delete(s.running, block.ToolUseID)
		if !strings.HasPrefix(tool.name, koderToolPrefix) {
			output, truncated := truncateOutput(resultText(block.Content))
			if block.IsError {
				if _, err := s.rt.AttachToolError(s.ctx, block.ToolUseID, domain.ToolError{Message: output}); err != nil {
					return err
				}
			} else {
				data := resultData(tool, output)
				if truncated {
					data["output_truncated"] = true
				}
				if _, err := s.rt.AttachToolResult(s.ctx, block.ToolUseID, domain.ToolResult{Text: output, Status: domain.ToolResultStatusOK, Data: data}); err != nil {
					return err
				}
			}
		}
		s.out <- domain.Event{Kind: domain.EventKindToolResult, Tool: tool.kind, ToolCallID: block.ToolUseID}
	}
	return nil
}

// handleToolCall runs a Koder tool that Claude Code called and answers it.
func (m *Manager) handleToolCall(state *turnState, process *chatProcess, raw json.RawMessage) error {
	var request struct {
		RequestID string `json:"request_id"`
		Request   struct {
			Message struct {
				ID     json.RawMessage `json:"id"`
				Params struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
					Meta      struct {
						ToolUseID string `json:"claudecode/toolUseId"`
					} `json:"_meta"`
				} `json:"params"`
			} `json:"message"`
		} `json:"request"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil
	}
	params := request.Request.Message.Params
	callID := params.Meta.ToolUseID
	if callID == "" {
		callID = request.RequestID
	}
	arguments := params.Arguments
	if len(arguments) == 0 || string(arguments) == "null" {
		arguments = json.RawMessage(`{}`)
	}
	var result domain.ToolResult
	err := errors.New("koder tool bridge is unavailable")
	if m.tools != nil {
		result, err = m.tools.Call(state.ctx, state.rt, params.Name, callID, arguments)
	}
	output := toolOutput(result)
	if err != nil {
		output = "Tool error: " + err.Error()
		if _, attachErr := state.rt.AttachToolError(state.ctx, callID, domain.ToolError{Message: err.Error()}); attachErr != nil {
			return attachErr
		}
	} else if _, attachErr := state.rt.AttachToolResult(state.ctx, callID, result); attachErr != nil {
		return attachErr
	}
	return process.client.Respond(request.RequestID, mcpReply(request.Request.Message.ID, map[string]any{
		"content": []map[string]string{{"type": "text", "text": output}},
		"isError": err != nil,
	}), nil)
}

func toolOutput(result domain.ToolResult) string {
	output := strings.TrimSpace(result.Text)
	if output == "" {
		output = strings.TrimSpace(result.Output)
	}
	if output == "" && result.Data != nil {
		if data, err := json.Marshal(result.Data); err == nil {
			output = string(data)
		}
	}
	if output == "" {
		output = "Tool completed."
	}
	return output
}

const koderToolPrefix = "mcp__" + MCPServerName + "__"

// hiddenTools are Claude Code plumbing rather than work worth showing.
var hiddenTools = map[string]bool{"ToolSearch": true}

func toolKind(name string) domain.ToolKind {
	switch name {
	case "Bash":
		return domain.ToolKindBash
	case "Read":
		return domain.ToolKindFileRead
	case "Write":
		return domain.ToolKindFileWrite
	case "Edit", "MultiEdit", "NotebookEdit":
		return domain.ToolKindFileEdit
	case "Glob":
		return domain.ToolKindFileGlob
	case "Grep":
		return domain.ToolKindFileGrep
	case "WebFetch":
		return domain.ToolKindWebFetch
	case "WebSearch":
		return domain.ToolKindWebSearch
	}
	if kind := domain.ToolKind(strings.TrimPrefix(name, koderToolPrefix)); strings.HasPrefix(name, koderToolPrefix) && domain.IsBuiltinToolKind(kind) {
		return kind
	}
	return domain.ToolKindMCP
}

// toolArgs flattens a tool's input for display. Tools without a Koder
// equivalent are shown as MCP calls of the "claude" server, or of the MCP
// server Claude Code named.
func toolArgs(kind domain.ToolKind, name string, input map[string]any, raw json.RawMessage) map[string]string {
	if kind == domain.ToolKindMCP {
		server, tool := "claude", name
		if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
			if before, after, found := strings.Cut(rest, "__"); found {
				server, tool = before, after
			}
		}
		return map[string]string{"server": server, "tool": tool, "arguments_raw": string(raw)}
	}
	args := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(input)) {
		switch value := input[key].(type) {
		case string:
			args[key] = value
		default:
			if encoded, err := json.Marshal(value); err == nil {
				args[key] = string(encoded)
			}
		}
	}
	for _, key := range []string{"file_path", "notebook_path"} {
		if path := args[key]; path != "" && args["path"] == "" {
			args["path"] = path
		}
	}
	return args
}

func resultText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// resultData shapes a native tool's result the way the Koder tool of the same
// kind reports it, so the timeline renders it the same way.
func resultData(tool runningTool, output string) map[string]any {
	data := map[string]any{}
	stringInput := func(key string) string {
		value, _ := tool.input[key].(string)
		return value
	}
	switch tool.kind {
	case domain.ToolKindBash:
		data["output"] = output
	case domain.ToolKindFileWrite:
		data["path"] = stringInput("file_path")
		data["content"] = stringInput("content")
	case domain.ToolKindFileEdit:
		data["path"] = stringInput("file_path")
		if diff := editDiff(stringInput("file_path"), tool.input); diff != "" {
			data["diff"] = diff
		}
	case domain.ToolKindFileGrep:
		data["output"] = output
	}
	return data
}

func editDiff(path string, input map[string]any) string {
	type edit struct{ old, new string }
	var edits []edit
	if old, ok := input["old_string"].(string); ok {
		next, _ := input["new_string"].(string)
		edits = append(edits, edit{old, next})
	}
	if list, ok := input["edits"].([]any); ok {
		for _, entry := range list {
			if fields, ok := entry.(map[string]any); ok {
				old, _ := fields["old_string"].(string)
				next, _ := fields["new_string"].(string)
				edits = append(edits, edit{old, next})
			}
		}
	}
	if len(edits) == 0 {
		return ""
	}
	var diff strings.Builder
	fmt.Fprintf(&diff, "--- %s\n+++ %s\n", path, path)
	for _, change := range edits {
		diff.WriteString("@@\n")
		for line := range strings.Lines(change.old) {
			diff.WriteString("-" + strings.TrimSuffix(line, "\n") + "\n")
		}
		for line := range strings.Lines(change.new) {
			diff.WriteString("+" + strings.TrimSuffix(line, "\n") + "\n")
		}
	}
	return diff.String()
}

const maxToolOutputBytes = 64 * 1024

func truncateOutput(value string) (string, bool) {
	if len(value) <= maxToolOutputBytes {
		return value, false
	}
	marker := "\n\n... output truncated ...\n\n"
	half := (maxToolOutputBytes - len(marker)) / 2
	headEnd := half
	for headEnd > 0 && value[headEnd]&0xc0 == 0x80 {
		headEnd--
	}
	tailStart := len(value) - half
	for tailStart < len(value) && value[tailStart]&0xc0 == 0x80 {
		tailStart++
	}
	return value[:headEnd] + marker + value[tailStart:], true
}
