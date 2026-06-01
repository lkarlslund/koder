package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// TimelineContent is the typed payload stored by a timeline item.
type TimelineContent interface {
	TimelineKind() TimelineKind
}

// TimelineKind identifies one kind of transcript timeline payload.
type TimelineKind string

const (
	TimelineKindUser       TimelineKind = "user"
	TimelineKindAssistant  TimelineKind = "assistant"
	TimelineKindTool       TimelineKind = "tool"
	TimelineKindNotice     TimelineKind = "notice"
	TimelineKindCompaction TimelineKind = "compaction"
)

// TimelineItem is the durable ordered unit of chat transcript state.
type TimelineItem struct {
	ID        ID
	ChatID    ID
	Seq       int64
	Content   TimelineContent
	CreatedAt time.Time
	UpdatedAt time.Time
	SealedAt  time.Time
}

// Sealed reports whether the item can no longer be mutated.
func (i TimelineItem) Sealed() bool {
	return !i.SealedAt.IsZero()
}

// Seal marks the item complete.
func (i *TimelineItem) Seal(now time.Time) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	i.SealedAt = now
	i.UpdatedAt = now
}

// UserMessage stores one user-facing prompt item.
type UserMessage struct {
	Text        string       `json:"text,omitempty"`
	Source      string       `json:"source,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	References  []Reference  `json:"references,omitempty"`
}

// TimelineKind returns the timeline payload kind.
func (UserMessage) TimelineKind() TimelineKind { return TimelineKindUser }

// Attachment stores a user attachment reference.
type Attachment struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	MIME     string `json:"mime,omitempty"`
	Path     string `json:"path,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Source   string `json:"source,omitempty"`
	Original string `json:"original,omitempty"`
}

// Reference stores a user reference to a file or symbol.
type Reference struct {
	Kind    string `json:"kind,omitempty"`
	Path    string `json:"path,omitempty"`
	Display string `json:"display,omitempty"`
	Start   int    `json:"start,omitempty"`
	End     int    `json:"end,omitempty"`
}

// AssistantMessage stores one assistant response and its owned children.
type AssistantMessage struct {
	Reasoning ReasoningContent `json:"reasoning,omitempty"`
	Text      string           `json:"text,omitempty"`
	Tools     []ToolCall       `json:"tools,omitempty"`
	Usage     *Usage           `json:"usage,omitempty"`
	Error     *ItemError       `json:"error,omitempty"`
	Provider  ProviderTrace    `json:"provider,omitempty"`
}

// TimelineKind returns the timeline payload kind.
func (AssistantMessage) TimelineKind() TimelineKind { return TimelineKindAssistant }

// AppendText appends visible assistant text.
func (m *AssistantMessage) AppendText(delta string) {
	m.Text += delta
}

// AppendReasoning appends assistant reasoning text.
func (m *AssistantMessage) AppendReasoning(delta string) {
	m.Reasoning.Text += delta
}

// AddToolCall appends a model-requested tool call.
func (m *AssistantMessage) AddToolCall(call ToolCall) error {
	call.ToolCallID = ToolCallID(strings.TrimSpace(string(call.ToolCallID)))
	if call.ToolCallID == "" {
		return fmt.Errorf("tool call id is required")
	}
	if m.ToolByID(call.ToolCallID) != nil {
		return fmt.Errorf("duplicate tool call %q", call.ToolCallID)
	}
	if call.Status == "" {
		call.Status = ToolStatusPending
	}
	m.Tools = append(m.Tools, call)
	return nil
}

// SetToolResult attaches a result to the tool call that requested it.
func (m *AssistantMessage) SetToolResult(id ToolCallID, result ToolResult) error {
	tool := m.ToolByID(id)
	if tool == nil {
		return fmt.Errorf("unknown tool call %q", id)
	}
	tool.Result = &result
	tool.Error = nil
	tool.Status = ToolStatusDone
	if tool.CompletedAt.IsZero() {
		tool.CompletedAt = time.Now().UTC()
	}
	return nil
}

// SetToolError attaches an error to the tool call that requested it.
func (m *AssistantMessage) SetToolError(id ToolCallID, toolErr ToolError) error {
	tool := m.ToolByID(id)
	if tool == nil {
		return fmt.Errorf("unknown tool call %q", id)
	}
	tool.Error = &toolErr
	tool.Result = nil
	tool.Status = ToolStatusErrored
	if tool.CompletedAt.IsZero() {
		tool.CompletedAt = time.Now().UTC()
	}
	return nil
}

// ToolByID returns a mutable tool call by provider call id.
func (m *AssistantMessage) ToolByID(id ToolCallID) *ToolCall {
	id = ToolCallID(strings.TrimSpace(string(id)))
	for idx := range m.Tools {
		if m.Tools[idx].ToolCallID == id {
			return &m.Tools[idx]
		}
	}
	return nil
}

// ReasoningContent stores normalized reasoning and provider replay metadata.
type ReasoningContent struct {
	Text      string          `json:"text,omitempty"`
	Summary   []string        `json:"summary,omitempty"`
	Encrypted string          `json:"encrypted,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

// ProviderTrace stores provider-native data for replay/debugging.
type ProviderTrace struct {
	Raw json.RawMessage `json:"raw,omitempty"`
}

// ToolCallID identifies one provider tool call.
type ToolCallID string

// ToolStatus describes the lifecycle of a tool call child.
type ToolStatus string

const (
	ToolStatusPending          ToolStatus = "pending"
	ToolStatusAwaitingApproval ToolStatus = "awaiting_approval"
	ToolStatusRunning          ToolStatus = "running"
	ToolStatusDone             ToolStatus = "done"
	ToolStatusDenied           ToolStatus = "denied"
	ToolStatusErrored          ToolStatus = "errored"
	ToolStatusCanceled         ToolStatus = "canceled"
)

// ToolCall stores one tool call and its result/error.
type ToolCall struct {
	ToolCallID  ToolCallID        `json:"tool_call_id"`
	Tool        ToolKind          `json:"tool"`
	Args        map[string]string `json:"args,omitempty"`
	Status      ToolStatus        `json:"status"`
	Result      *ToolResult       `json:"result,omitempty"`
	Error       *ToolError        `json:"error,omitempty"`
	ApprovalID  string            `json:"approval_id,omitempty"`
	Approval    *ApprovalRequest  `json:"approval,omitempty"` // legacy read path; new state is Status plus ApprovalID.
	StartedAt   time.Time         `json:"started_at,omitempty"`
	CompletedAt time.Time         `json:"completed_at,omitempty"`
}

// MarshalJSON stores typed tool result data behind the tool/status discriminator.
func (c ToolCall) MarshalJSON() ([]byte, error) {
	type encodedToolResult struct {
		Text   string           `json:"text,omitempty"`
		Diff   string           `json:"diff,omitempty"`
		Data   json.RawMessage  `json:"data,omitempty"`
		Status ToolResultStatus `json:"status,omitempty"`
	}
	type encodedToolCall struct {
		ToolCallID  ToolCallID         `json:"tool_call_id"`
		Tool        ToolKind           `json:"tool"`
		Args        map[string]string  `json:"args,omitempty"`
		Status      ToolStatus         `json:"status"`
		Result      *encodedToolResult `json:"result,omitempty"`
		Error       *ToolError         `json:"error,omitempty"`
		ApprovalID  string             `json:"approval_id,omitempty"`
		StartedAt   time.Time          `json:"started_at,omitempty"`
		CompletedAt time.Time          `json:"completed_at,omitempty"`
	}
	var result *encodedToolResult
	if c.Result != nil {
		raw, err := json.Marshal(c.Result.Data)
		if err != nil {
			return nil, fmt.Errorf("marshal tool result %s: %w", c.Tool, err)
		}
		result = &encodedToolResult{
			Text:   c.Result.Text,
			Diff:   c.Result.Diff,
			Data:   raw,
			Status: c.Result.Status,
		}
	}
	return json.Marshal(encodedToolCall{
		ToolCallID: c.ToolCallID, Tool: c.Tool, Args: c.Args, Status: c.Status, Result: result,
		Error: c.Error, ApprovalID: c.ApprovalID, StartedAt: c.StartedAt, CompletedAt: c.CompletedAt,
	})
}

// UnmarshalJSON loads typed tool result data from the tool/status discriminator.
func (c *ToolCall) UnmarshalJSON(data []byte) error {
	type encodedToolResult struct {
		Text   string           `json:"text,omitempty"`
		Diff   string           `json:"diff,omitempty"`
		Data   json.RawMessage  `json:"data,omitempty"`
		Status ToolResultStatus `json:"status,omitempty"`
	}
	type encodedToolCall struct {
		ToolCallID  ToolCallID         `json:"tool_call_id"`
		Tool        string             `json:"tool"`
		Args        map[string]string  `json:"args,omitempty"`
		Status      ToolStatus         `json:"status"`
		Result      *encodedToolResult `json:"result,omitempty"`
		Error       *ToolError         `json:"error,omitempty"`
		ApprovalID  string             `json:"approval_id,omitempty"`
		Approval    *ApprovalRequest   `json:"approval,omitempty"`
		StartedAt   time.Time          `json:"started_at,omitempty"`
		CompletedAt time.Time          `json:"completed_at,omitempty"`
	}
	var in encodedToolCall
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	tool, err := parsePersistedToolKind(in.Tool)
	if err != nil {
		tool = 0
	}
	var result *ToolResult
	if in.Result != nil && tool != 0 {
		decoded, err := DecodeToolResultPayload(tool, in.Result.Status, in.Result.Data)
		if err != nil {
			return fmt.Errorf("decode tool result %s: %w", tool, err)
		}
		result = &ToolResult{Text: in.Result.Text, Diff: in.Result.Diff, Data: decoded, Status: in.Result.Status}
	}
	status := in.Status
	approvalID := strings.TrimSpace(in.ApprovalID)
	if in.Approval != nil && status == ToolStatusPending && result == nil && in.Error == nil {
		status = ToolStatusAwaitingApproval
		if approvalID == "" && in.Approval.ID != "" {
			approvalID = in.Approval.ID
		}
	}
	*c = ToolCall{
		ToolCallID: in.ToolCallID, Tool: tool, Args: in.Args, Status: status, Result: result,
		Error: in.Error, ApprovalID: approvalID, StartedAt: in.StartedAt, CompletedAt: in.CompletedAt,
	}
	return nil
}

// ToolExecution stores a user-initiated tool execution that was not requested by an assistant item.
type ToolExecution struct {
	Tool       ToolKind          `json:"tool"`
	ToolCallID ToolCallID        `json:"tool_call_id,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	Result     *ToolResult       `json:"result,omitempty"`
	Error      *ToolError        `json:"error,omitempty"`
	StartedAt  time.Time         `json:"started_at,omitempty"`
	EndedAt    time.Time         `json:"ended_at,omitempty"`
}

// TimelineKind returns the timeline payload kind.
func (ToolExecution) TimelineKind() TimelineKind { return TimelineKindTool }

// MarshalJSON stores typed standalone tool result data behind the tool/status discriminator.
func (e ToolExecution) MarshalJSON() ([]byte, error) {
	type encodedToolResult struct {
		Text   string           `json:"text,omitempty"`
		Diff   string           `json:"diff,omitempty"`
		Data   json.RawMessage  `json:"data,omitempty"`
		Status ToolResultStatus `json:"status,omitempty"`
	}
	type encodedToolExecution struct {
		Tool       ToolKind           `json:"tool"`
		ToolCallID ToolCallID         `json:"tool_call_id,omitempty"`
		Args       map[string]string  `json:"args,omitempty"`
		Result     *encodedToolResult `json:"result,omitempty"`
		Error      *ToolError         `json:"error,omitempty"`
		StartedAt  time.Time          `json:"started_at,omitempty"`
		EndedAt    time.Time          `json:"ended_at,omitempty"`
	}
	var result *encodedToolResult
	if e.Result != nil {
		raw, err := json.Marshal(e.Result.Data)
		if err != nil {
			return nil, fmt.Errorf("marshal tool result %s: %w", e.Tool, err)
		}
		result = &encodedToolResult{Text: e.Result.Text, Diff: e.Result.Diff, Data: raw, Status: e.Result.Status}
	}
	return json.Marshal(encodedToolExecution{Tool: e.Tool, ToolCallID: e.ToolCallID, Args: e.Args, Result: result, Error: e.Error, StartedAt: e.StartedAt, EndedAt: e.EndedAt})
}

// UnmarshalJSON loads typed standalone tool result data from the tool/status discriminator.
func (e *ToolExecution) UnmarshalJSON(data []byte) error {
	type encodedToolResult struct {
		Text   string           `json:"text,omitempty"`
		Diff   string           `json:"diff,omitempty"`
		Data   json.RawMessage  `json:"data,omitempty"`
		Status ToolResultStatus `json:"status,omitempty"`
	}
	type encodedToolExecution struct {
		Tool       string             `json:"tool"`
		ToolCallID ToolCallID         `json:"tool_call_id,omitempty"`
		Args       map[string]string  `json:"args,omitempty"`
		Result     *encodedToolResult `json:"result,omitempty"`
		Error      *ToolError         `json:"error,omitempty"`
		StartedAt  time.Time          `json:"started_at,omitempty"`
		EndedAt    time.Time          `json:"ended_at,omitempty"`
	}
	var in encodedToolExecution
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	tool, err := parsePersistedToolKind(in.Tool)
	if err != nil {
		tool = 0
	}
	var result *ToolResult
	if in.Result != nil && tool != 0 {
		decoded, err := DecodeToolResultPayload(tool, in.Result.Status, in.Result.Data)
		if err != nil {
			return fmt.Errorf("decode tool result %s: %w", tool, err)
		}
		result = &ToolResult{Text: in.Result.Text, Diff: in.Result.Diff, Data: decoded, Status: in.Result.Status}
	}
	*e = ToolExecution{Tool: tool, ToolCallID: in.ToolCallID, Args: in.Args, Result: result, Error: in.Error, StartedAt: in.StartedAt, EndedAt: in.EndedAt}
	return nil
}

// ToolResult stores one completed tool response.
type ToolResult struct {
	Text   string            `json:"text,omitempty"`
	Diff   string            `json:"diff,omitempty"`
	Data   ToolResultPayload `json:"data,omitempty"`
	Status ToolResultStatus  `json:"status,omitempty"`
}

// ToolError stores one failed tool response.
type ToolError struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// ApprovalRequest stores a permission prompt attached to a tool call.
type ApprovalRequest struct {
	ID     ID             `json:"id,omitempty"`
	Status ApprovalStatus `json:"status,omitempty"`
	Body   string         `json:"body,omitempty"`
}

// ItemError stores an assistant item level error.
type ItemError struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// Notice stores non-chat transcript information.
type Notice struct {
	Level      string   `json:"level,omitempty"`
	Text       string   `json:"text"`
	Kind       string   `json:"kind,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Title      string   `json:"title,omitempty"`
	Subtitle   string   `json:"subtitle,omitempty"`
	Tool       ToolKind `json:"tool,omitempty"`
	ToolCallID string   `json:"tool_call_id,omitempty"`
	Count      int      `json:"count,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

const (
	NoticeKindInterrupted          = "interrupted"
	NoticeReasonUserInterrupted    = "user_interrupted"
	NoticeReasonProcessTerminating = "process_terminating"
	NoticeReasonProcessRestart     = "process_restart"
)

// TimelineKind returns the timeline payload kind.
func (Notice) TimelineKind() TimelineKind { return TimelineKindNotice }

// Compaction stores a compacted history summary.
type Compaction struct {
	Summary             string `json:"summary"`
	Trigger             string `json:"trigger,omitempty"`
	Status              string `json:"status,omitempty"`
	FirstKeptItemID     string `json:"first_kept_item_id,omitempty"`
	BeforeContextTokens int    `json:"before_context_tokens,omitempty"`
	AfterContextTokens  int    `json:"after_context_tokens,omitempty"`
	Usage               *Usage `json:"usage,omitempty"`
}

// TimelineKind returns the timeline payload kind.
func (Compaction) TimelineKind() TimelineKind { return TimelineKindCompaction }

// MarshalJSON stores timeline content behind a discriminator.
func (i TimelineItem) MarshalJSON() ([]byte, error) {
	kind := TimelineKind("")
	if i.Content != nil {
		kind = i.Content.TimelineKind()
	}
	raw, err := json.Marshal(i.Content)
	if err != nil {
		return nil, fmt.Errorf("marshal timeline payload %s: %w", kind, err)
	}
	type encoded struct {
		ID        string          `json:"id"`
		ChatID    ID              `json:"chat_id"`
		Seq       int64           `json:"seq"`
		Kind      TimelineKind    `json:"kind"`
		Content   json.RawMessage `json:"content"`
		CreatedAt time.Time       `json:"created_at"`
		UpdatedAt time.Time       `json:"updated_at"`
		SealedAt  time.Time       `json:"sealed_at,omitempty"`
	}
	return json.Marshal(encoded{
		ID: i.ID, ChatID: i.ChatID, Seq: i.Seq, Kind: kind, Content: raw,
		CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt, SealedAt: i.SealedAt,
	})
}

// UnmarshalJSON loads timeline content from its discriminator.
func (i *TimelineItem) UnmarshalJSON(data []byte) error {
	type encoded struct {
		ID        string          `json:"id"`
		ChatID    ID              `json:"chat_id"`
		Seq       int64           `json:"seq"`
		Kind      TimelineKind    `json:"kind"`
		Content   json.RawMessage `json:"content"`
		CreatedAt time.Time       `json:"created_at"`
		UpdatedAt time.Time       `json:"updated_at"`
		SealedAt  time.Time       `json:"sealed_at,omitempty"`
	}
	var in encoded
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	content, err := decodeTimelineContent(in.Kind, in.Content)
	if err != nil {
		return err
	}
	*i = TimelineItem{
		ID: in.ID, ChatID: in.ChatID, Seq: in.Seq, Content: content,
		CreatedAt: in.CreatedAt, UpdatedAt: in.UpdatedAt, SealedAt: in.SealedAt,
	}
	return nil
}

func decodeTimelineContent(kind TimelineKind, raw json.RawMessage) (TimelineContent, error) {
	switch kind {
	case TimelineKindUser:
		return decodeTimelinePayload[UserMessage](raw)
	case TimelineKindAssistant:
		return decodeTimelinePayload[AssistantMessage](raw)
	case TimelineKindTool:
		return decodeTimelinePayload[ToolExecution](raw)
	case TimelineKindNotice:
		return decodeTimelinePayload[Notice](raw)
	case TimelineKindCompaction:
		return decodeTimelinePayload[Compaction](raw)
	default:
		return nil, fmt.Errorf("unsupported timeline kind %q", kind)
	}
}

func decodeTimelinePayload[T TimelineContent](raw json.RawMessage) (TimelineContent, error) {
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
