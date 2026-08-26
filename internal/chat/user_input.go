package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func hasPendingUserInput(timeline []domain.TimelineItem) bool {
	return pendingUserInputCount(timeline) > 0
}

func pendingUserInputCount(timeline []domain.TimelineItem) int {
	for idx := len(timeline) - 1; idx >= 0; idx-- {
		assistant, ok := timeline[idx].Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		count := 0
		for _, call := range assistant.Tools {
			if call.Status == domain.ToolStatusAwaitingInput {
				count++
			}
		}
		return count
	}
	return 0
}

func (r *Chat) AttachToolAwaitingInput(ctx context.Context, toolCallID string) (domain.TimelineItem, error) {
	return r.updateToolCall(ctx, toolCallID, func(call *domain.ToolCall) error {
		call.Status = domain.ToolStatusAwaitingInput
		call.Approval = nil
		call.ApprovalID = ""
		return nil
	})
}

func (r *Chat) pendingUserInputCalls() ([]domain.ToolCall, error) {
	if err := r.EnsureTimeline(context.Background()); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return nil, nil
	}
	items := r.state.SnapshotTimeline()
	for idx := len(items) - 1; idx >= 0; idx-- {
		assistant, ok := items[idx].Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		var calls []domain.ToolCall
		for _, call := range assistant.Tools {
			if call.Status == domain.ToolStatusAwaitingInput {
				calls = append(calls, call)
			}
		}
		return calls, nil
	}
	return nil, nil
}

func validateUserInputAnswers(calls []domain.ToolCall, submitted []tools.UserInputAnswer) (map[string][]tools.UserInputAnswer, error) {
	expected := make(map[string]map[string]tools.UserInputQuestion, len(calls))
	for _, call := range calls {
		questions, err := tools.ParseUserInputQuestions(call.Args["questions"])
		if err != nil {
			return nil, fmt.Errorf("invalid pending request %s: %w", call.ToolCallID, err)
		}
		byID := make(map[string]tools.UserInputQuestion, len(questions))
		for _, question := range questions {
			byID[question.ID] = question
		}
		expected[string(call.ToolCallID)] = byID
	}
	if len(expected) == 0 {
		return nil, fmt.Errorf("chat is not waiting for user input")
	}
	grouped := make(map[string][]tools.UserInputAnswer, len(expected))
	seen := make(map[string]struct{})
	for _, answer := range submitted {
		answer.ToolCallID = strings.TrimSpace(answer.ToolCallID)
		answer.QuestionID = strings.TrimSpace(answer.QuestionID)
		answer.Selected = strings.TrimSpace(answer.Selected)
		answer.Comment = strings.TrimSpace(answer.Comment)
		questions, ok := expected[answer.ToolCallID]
		if !ok {
			return nil, fmt.Errorf("tool call %q is not awaiting input", answer.ToolCallID)
		}
		question, ok := questions[answer.QuestionID]
		if !ok {
			return nil, fmt.Errorf("question %q is not pending for tool call %q", answer.QuestionID, answer.ToolCallID)
		}
		key := answer.ToolCallID + "\x00" + answer.QuestionID
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("question %q was answered more than once", answer.QuestionID)
		}
		seen[key] = struct{}{}
		if answer.Selected == "" && answer.Comment == "" {
			return nil, fmt.Errorf("question %q requires an option, a comment, or both", answer.QuestionID)
		}
		if answer.Selected != "" {
			valid := false
			for _, option := range question.Options {
				if option.Label == answer.Selected {
					valid = true
					break
				}
			}
			if !valid {
				return nil, fmt.Errorf("%q is not an option for question %q", answer.Selected, answer.QuestionID)
			}
		}
		grouped[answer.ToolCallID] = append(grouped[answer.ToolCallID], answer)
	}
	for toolCallID, questions := range expected {
		if len(grouped[toolCallID]) != len(questions) {
			return nil, fmt.Errorf("all questions must be answered before submitting")
		}
	}
	return grouped, nil
}

func (r *Chat) handleSubmitUserInput(answers []tools.UserInputAnswer, reply chan error) {
	calls, err := r.pendingUserInputCalls()
	if err != nil {
		reply <- err
		return
	}
	grouped, err := validateUserInputAnswers(calls, answers)
	if err != nil {
		reply <- err
		return
	}

	events := make([]domain.Event, 0, len(calls))
	for _, call := range calls {
		callAnswers := grouped[string(call.ToolCallID)]
		encoded, marshalErr := json.Marshal(struct {
			Answers []tools.UserInputAnswer `json:"answers"`
		}{Answers: callAnswers})
		if marshalErr != nil {
			reply <- marshalErr
			return
		}
		result := domain.ToolResult{
			Text: string(encoded),
			Data: tools.QuestionStoredResult{Answers: callAnswers},
		}
		item, attachErr := r.AttachToolResult(context.Background(), string(call.ToolCallID), result)
		if attachErr != nil {
			reply <- attachErr
			return
		}
		events = append(events, domain.Event{Kind: domain.EventKindUserInputReply, Tool: call.Tool, ToolCallID: string(call.ToolCallID), Text: string(encoded), Item: item})
	}

	r.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.turnSeq++
	turn := r.turnSeq
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Waiting for LLM response"
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, false, false))
	reply <- nil

	out := make(chan domain.Event, max(32, len(events)+1))
	go func() {
		defer close(out)
		for _, event := range events {
			out <- event
		}
		if !r.shouldStopAfterCompletedStep() {
			r.continueTurnLoop(ctx, nil, nil, out)
		}
	}()
	r.forwardTurnEvents(turn, out)
}
