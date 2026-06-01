package domain

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/toolkind"
)

// PartPayload is the typed content stored by a message part.
type PartPayload interface {
	PartKind() PartKind
}

// TextPayload stores visible text.
type TextPayload struct {
	Text string `json:"text"`
}

// PartKind returns the payload part kind.
func (TextPayload) PartKind() PartKind { return PartKindText }

// ReasoningPayload stores assistant reasoning text.
type ReasoningPayload struct {
	Text string `json:"text"`
}

// PartKind returns the payload part kind.
func (ReasoningPayload) PartKind() PartKind { return PartKindReasoning }

// AttachmentPayload stores a user attachment reference.
type AttachmentPayload struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	MIME     string `json:"mime,omitempty"`
	Path     string `json:"path,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Source   string `json:"source,omitempty"`
	Original string `json:"original,omitempty"`
}

// PartKind returns the payload part kind.
func (AttachmentPayload) PartKind() PartKind { return PartKindAttachment }

// ReferencePayload stores a user reference to a file or symbol.
type ReferencePayload struct {
	Kind    string `json:"kind,omitempty"`
	Path    string `json:"path,omitempty"`
	Display string `json:"display,omitempty"`
	Start   int    `json:"start,omitempty"`
	End     int    `json:"end,omitempty"`
}

// PartKind returns the payload part kind.
func (ReferencePayload) PartKind() PartKind { return PartKindReference }

// ToolCallPayload stores a model-requested tool call.
type ToolCallPayload struct {
	Tool       ToolKind          `json:"tool"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
}

// PartKind returns the payload part kind.
func (ToolCallPayload) PartKind() PartKind { return PartKindToolCall }

// UnmarshalJSON accepts historical persisted tool names.
func (p *ToolCallPayload) UnmarshalJSON(data []byte) error {
	type encodedToolCallPayload struct {
		Tool       string            `json:"tool"`
		ToolCallID string            `json:"tool_call_id,omitempty"`
		Args       map[string]string `json:"args,omitempty"`
	}
	var encoded encodedToolCallPayload
	if err := json.Unmarshal(data, &encoded); err != nil {
		return err
	}
	tool, err := toolkind.ParsePersisted(encoded.Tool)
	if err != nil {
		tool = 0
	}
	*p = ToolCallPayload{Tool: tool, ToolCallID: encoded.ToolCallID, Args: encoded.Args}
	return nil
}

// ToolResultStatus describes the persisted result state for a tool output.
type ToolResultStatus string

const (
	ToolResultStatusOK     ToolResultStatus = "ok"
	ToolResultStatusDenied ToolResultStatus = "denied"
	ToolResultStatusError  ToolResultStatus = "error"
)

// ToolResultPayload is a typed persisted tool result body.
type ToolResultPayload interface {
	ToolResultPayload()
}

// ToolOutputPayload stores a tool response and its typed result data.
type ToolOutputPayload struct {
	Tool       ToolKind          `json:"tool"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	Status     ToolResultStatus  `json:"status"`
	Text       string            `json:"text,omitempty"`
	Diff       string            `json:"diff,omitempty"`
	Result     any               `json:"result,omitempty"`
}

// PartKind returns the payload part kind.
func (ToolOutputPayload) PartKind() PartKind { return PartKindToolOutput }

// MarshalJSON stores the result payload behind the tool/status discriminator.
func (p ToolOutputPayload) MarshalJSON() ([]byte, error) {
	type encodedToolOutput struct {
		Tool       ToolKind          `json:"tool"`
		ToolCallID string            `json:"tool_call_id,omitempty"`
		Args       map[string]string `json:"args,omitempty"`
		Status     ToolResultStatus  `json:"status"`
		Text       string            `json:"text,omitempty"`
		Diff       string            `json:"diff,omitempty"`
		Result     json.RawMessage   `json:"result,omitempty"`
	}
	rawResult, err := json.Marshal(p.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal tool result %s: %w", p.Tool, err)
	}
	if p.Status == "" {
		p.Status = ToolResultStatusOK
	}
	return json.Marshal(encodedToolOutput{
		Tool:       p.Tool,
		ToolCallID: p.ToolCallID,
		Args:       p.Args,
		Status:     p.Status,
		Text:       p.Text,
		Diff:       p.Diff,
		Result:     rawResult,
	})
}

// UnmarshalJSON loads the typed result payload from the tool/status discriminator.
func (p *ToolOutputPayload) UnmarshalJSON(data []byte) error {
	type encodedToolOutput struct {
		Tool       string            `json:"tool"`
		ToolCallID string            `json:"tool_call_id,omitempty"`
		Args       map[string]string `json:"args,omitempty"`
		Status     ToolResultStatus  `json:"status"`
		Text       string            `json:"text,omitempty"`
		Diff       string            `json:"diff,omitempty"`
		Result     json.RawMessage   `json:"result,omitempty"`
	}
	var encoded encodedToolOutput
	if err := json.Unmarshal(data, &encoded); err != nil {
		return err
	}
	tool, err := toolkind.ParsePersisted(encoded.Tool)
	if err != nil {
		tool = 0
	}
	var result ToolResultPayload
	if tool != 0 {
		result, err = decodeToolResultPayload(tool, encoded.Status, encoded.Result)
		if err != nil {
			return err
		}
	}
	p.Tool = tool
	p.ToolCallID = encoded.ToolCallID
	p.Args = encoded.Args
	p.Status = encoded.Status
	if p.Status == "" {
		p.Status = ToolResultStatusOK
	}
	p.Text = encoded.Text
	p.Diff = encoded.Diff
	p.Result = result
	return nil
}

// DecodeToolResultPayload loads typed tool result data from the tool and status discriminator.
func DecodeToolResultPayload(tool ToolKind, status ToolResultStatus, raw json.RawMessage) (ToolResultPayload, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	switch status {
	case ToolResultStatusDenied:
		return decodeToolResult[DeniedStoredResult](raw)
	case ToolResultStatusError:
		return decodeToolResult[ErrorStoredResult](raw)
	}
	switch tool {
	case ToolKindFileRead:
		return decodeToolResult[ReadStoredResult](raw)
	case ToolKindBash:
		return decodeToolResult[BashStoredResult](raw)
	case ToolKindExecCommand, ToolKindExecStatus, ToolKindExecWriteStdin, ToolKindExecResize, ToolKindExecTerminate:
		return decodeToolResult[ExecStoredResult](raw)
	case ToolKindExecList, ToolKindExecCleanup:
		return decodeToolResult[ExecListStoredResult](raw)
	case ToolKindFileEdit:
		return decodeToolResult[EditStoredResult](raw)
	case ToolKindFileWrite:
		return decodeToolResult[WriteStoredResult](raw)
	case ToolKindLint:
		return decodeToolResult[LintStoredResult](raw)
	case ToolKindFileGlob:
		return decodeToolResult[GlobStoredResult](raw)
	case ToolKindFileGrep:
		return decodeToolResult[GrepStoredResult](raw)
	case ToolKindQuestion:
		return decodeToolResult[QuestionStoredResult](raw)
	case ToolKindTask:
		return decodeToolResult[TaskStoredResult](raw)
	case ToolKindUpdatePlan:
		return decodeToolResult[UpdatePlanStoredResult](raw)
	case ToolKindSkill:
		return decodeToolResult[SkillStoredResult](raw)
	case ToolKindWebFetch:
		return decodeToolResult[WebFetchStoredResult](raw)
	case ToolKindWebSearch:
		return decodeToolResult[WebSearchStoredResult](raw)
	case ToolKindViewImage:
		return decodeToolResult[ViewImageStoredResult](raw)
	case ToolKindShowImage:
		return decodeToolResult[ShowImageStoredResult](raw)
	case ToolKindMilestoneList, ToolKindMilestoneAdd, ToolKindMilestoneUpdate, ToolKindMilestoneWrite, ToolKindMilestonePlan:
		return decodeToolResult[MilestonePlanStoredResult](raw)
	case ToolKindChatList, ToolKindChatStart, ToolKindChatStartDecomposition, ToolKindChatStartExecution, ToolKindChatPoll, ToolKindChatArchive:
		return decodeToolResult[ChatListStoredResult](raw)
	case ToolKindTodoList, ToolKindTodoAddItems, ToolKindTodoUpdateItem, ToolKindTodoFetchNext:
		return decodeToolResult[TodoListStoredResult](raw)
	case ToolKindMCP:
		return decodeToolResult[MCPStoredResult](raw)
	default:
		return nil, fmt.Errorf("unsupported tool result kind %q", tool)
	}
}

func decodeToolResultPayload(tool ToolKind, status ToolResultStatus, raw json.RawMessage) (ToolResultPayload, error) {
	return DecodeToolResultPayload(tool, status, raw)
}

func decodeToolResult[T ToolResultPayload](raw json.RawMessage) (ToolResultPayload, error) {
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ApprovalRequestPayload stores a persisted permission prompt.
type ApprovalRequestPayload struct {
	ApprovalID ID             `json:"approval_id,omitempty"`
	Tool       ToolKind       `json:"tool,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Command    string         `json:"command,omitempty"`
	Status     ApprovalStatus `json:"status,omitempty"`
	Body       string         `json:"body,omitempty"`
}

// PartKind returns the payload part kind.
func (ApprovalRequestPayload) PartKind() PartKind { return PartKindApprovalRequest }

// UnmarshalJSON accepts historical persisted tool names.
func (p *ApprovalRequestPayload) UnmarshalJSON(data []byte) error {
	type encodedApprovalRequestPayload struct {
		ApprovalID ID             `json:"approval_id,omitempty"`
		Tool       string         `json:"tool,omitempty"`
		ToolCallID string         `json:"tool_call_id,omitempty"`
		Command    string         `json:"command,omitempty"`
		Status     ApprovalStatus `json:"status,omitempty"`
		Body       string         `json:"body,omitempty"`
	}
	var encoded encodedApprovalRequestPayload
	if err := json.Unmarshal(data, &encoded); err != nil {
		return err
	}
	tool, err := toolkind.ParsePersisted(encoded.Tool)
	if err != nil {
		tool = 0
	}
	*p = ApprovalRequestPayload{
		ApprovalID: encoded.ApprovalID,
		Tool:       tool,
		ToolCallID: encoded.ToolCallID,
		Command:    encoded.Command,
		Status:     encoded.Status,
		Body:       encoded.Body,
	}
	return nil
}

// CompactionPayload stores a compacted conversation summary.
type CompactionPayload struct {
	Summary             string `json:"summary"`
	Trigger             string `json:"trigger,omitempty"`
	Status              string `json:"status,omitempty"`
	FirstKeptMessageID  ID     `json:"first_kept_message_id,omitempty"`
	BeforeContextTokens int    `json:"before_context_tokens,omitempty"`
	AfterContextTokens  int    `json:"after_context_tokens,omitempty"`
}

// PartKind returns the payload part kind.
func (CompactionPayload) PartKind() PartKind { return PartKindCompaction }

// QuestionPayload stores a user-facing question.
type QuestionPayload struct {
	Question string `json:"question"`
}

// PartKind returns the payload part kind.
func (QuestionPayload) PartKind() PartKind { return PartKindQuestion }

// TaskUpdatePayload stores a task status update.
type TaskUpdatePayload struct {
	Body   string     `json:"body"`
	Status TaskStatus `json:"status,omitempty"`
}

// PartKind returns the payload part kind.
func (TaskUpdatePayload) PartKind() PartKind { return PartKindTaskUpdate }

// PlanStepPayload stores one plan step in a plan update part.
type PlanStepPayload struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

// PlanUpdatePayload stores a plan update.
type PlanUpdatePayload struct {
	Explanation string            `json:"explanation,omitempty"`
	Steps       []PlanStepPayload `json:"steps,omitempty"`
	Output      string            `json:"output,omitempty"`
}

// PartKind returns the payload part kind.
func (PlanUpdatePayload) PartKind() PartKind { return PartKindPlanUpdate }

// UsagePayload stores provider token usage.
type UsagePayload struct {
	Usage Usage `json:"usage"`
}

// PartKind returns the payload part kind.
func (UsagePayload) PartKind() PartKind { return PartKindUsage }

// SystemNoticePayload stores a typed system notice.
type SystemNoticePayload struct {
	Text   string `json:"text"`
	Detail string `json:"detail,omitempty"`
}

// PartKind returns the payload part kind.
func (SystemNoticePayload) PartKind() PartKind { return PartKindSystemNotice }

// EventNoticePayload stores a typed UI/event notice.
type EventNoticePayload struct {
	Text       string   `json:"text"`
	Kind       string   `json:"kind,omitempty"`
	Severity   string   `json:"severity,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Title      string   `json:"title,omitempty"`
	Subtitle   string   `json:"subtitle,omitempty"`
	Tool       ToolKind `json:"tool,omitempty"`
	ToolCallID string   `json:"tool_call_id,omitempty"`
	Count      int      `json:"count,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

// PartKind returns the payload part kind.
func (EventNoticePayload) PartKind() PartKind { return PartKindEventNotice }

// UnmarshalJSON accepts historical persisted tool names.
func (p *EventNoticePayload) UnmarshalJSON(data []byte) error {
	type encodedEventNoticePayload struct {
		Text       string `json:"text"`
		Kind       string `json:"kind,omitempty"`
		Severity   string `json:"severity,omitempty"`
		Reason     string `json:"reason,omitempty"`
		Title      string `json:"title,omitempty"`
		Subtitle   string `json:"subtitle,omitempty"`
		Tool       string `json:"tool,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
		Count      int    `json:"count,omitempty"`
		Limit      int    `json:"limit,omitempty"`
	}
	var encoded encodedEventNoticePayload
	if err := json.Unmarshal(data, &encoded); err != nil {
		return err
	}
	tool, err := toolkind.ParsePersisted(encoded.Tool)
	if err != nil {
		tool = 0
	}
	*p = EventNoticePayload{
		Text:       encoded.Text,
		Kind:       encoded.Kind,
		Severity:   encoded.Severity,
		Reason:     encoded.Reason,
		Title:      encoded.Title,
		Subtitle:   encoded.Subtitle,
		Tool:       tool,
		ToolCallID: encoded.ToolCallID,
		Count:      encoded.Count,
		Limit:      encoded.Limit,
	}
	return nil
}

// Text returns the human-readable text represented by the part payload.
func (p Part) Text() string {
	switch payload := p.Payload.(type) {
	case TextPayload:
		return payload.Text
	case ReasoningPayload:
		return payload.Text
	case AttachmentPayload:
		return payload.Name
	case ReferencePayload:
		return payload.Display
	case ToolCallPayload:
		data, err := json.Marshal(payload)
		if err != nil {
			return payload.Tool.String()
		}
		return string(data)
	case ToolOutputPayload:
		return payload.Text
	case ApprovalRequestPayload:
		return payload.Body
	case CompactionPayload:
		return payload.Summary
	case QuestionPayload:
		return payload.Question
	case TaskUpdatePayload:
		return payload.Body
	case PlanUpdatePayload:
		if strings.TrimSpace(payload.Output) != "" {
			return payload.Output
		}
		return payload.Explanation
	case SystemNoticePayload:
		return payload.Text
	case EventNoticePayload:
		return payload.Text
	default:
		return p.Body
	}
}

// MarshalJSON stores a part as a discriminator plus typed payload.
func (p Part) MarshalJSON() ([]byte, error) {
	kind := p.Kind
	if p.Payload != nil {
		kind = p.Payload.PartKind()
	}
	type encodedPart struct {
		ID        ID              `json:"id"`
		MessageID ID              `json:"message_id"`
		Kind      PartKind        `json:"kind"`
		Payload   json.RawMessage `json:"payload"`
		CreatedAt any             `json:"created_at"`
	}
	rawPayload, err := json.Marshal(p.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal part payload %s: %w", kind.String(), err)
	}
	return json.Marshal(encodedPart{
		ID:        p.ID,
		MessageID: p.MessageID,
		Kind:      kind,
		Payload:   rawPayload,
		CreatedAt: p.CreatedAt,
	})
}

// UnmarshalJSON loads a part from its typed payload discriminator.
func (p *Part) UnmarshalJSON(data []byte) error {
	type encodedPart struct {
		ID        ID              `json:"id"`
		MessageID ID              `json:"message_id"`
		Kind      PartKind        `json:"kind"`
		Payload   json.RawMessage `json:"payload"`
		CreatedAt json.RawMessage `json:"created_at"`
	}
	var encoded encodedPart
	if err := json.Unmarshal(data, &encoded); err != nil {
		return err
	}
	payload, err := decodePartPayload(encoded.Kind, encoded.Payload)
	if err != nil {
		return err
	}
	p.ID = encoded.ID
	p.MessageID = encoded.MessageID
	p.Kind = encoded.Kind
	p.Payload = payload
	p.Body = p.Text()
	if len(encoded.CreatedAt) > 0 {
		if err := json.Unmarshal(encoded.CreatedAt, &p.CreatedAt); err != nil {
			return fmt.Errorf("decode part created_at: %w", err)
		}
	}
	return nil
}

func decodePartPayload(kind PartKind, raw json.RawMessage) (PartPayload, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("part %s missing payload", kind.String())
	}
	switch kind {
	case PartKindText:
		return decodePayload[TextPayload](raw)
	case PartKindReasoning:
		return decodePayload[ReasoningPayload](raw)
	case PartKindAttachment:
		return decodePayload[AttachmentPayload](raw)
	case PartKindReference:
		return decodePayload[ReferencePayload](raw)
	case PartKindToolCall:
		return decodePayload[ToolCallPayload](raw)
	case PartKindToolOutput:
		return decodePayload[ToolOutputPayload](raw)
	case PartKindApprovalRequest:
		return decodePayload[ApprovalRequestPayload](raw)
	case PartKindCompaction:
		return decodePayload[CompactionPayload](raw)
	case PartKindQuestion:
		return decodePayload[QuestionPayload](raw)
	case PartKindTaskUpdate:
		return decodePayload[TaskUpdatePayload](raw)
	case PartKindPlanUpdate:
		return decodePayload[PlanUpdatePayload](raw)
	case PartKindUsage:
		return decodePayload[UsagePayload](raw)
	case PartKindSystemNotice:
		return decodePayload[SystemNoticePayload](raw)
	case PartKindEventNotice:
		return decodePayload[EventNoticePayload](raw)
	default:
		return nil, fmt.Errorf("unsupported part kind %q", kind)
	}
}

func decodePayload[T PartPayload](raw json.RawMessage) (PartPayload, error) {
	var payload T
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}
