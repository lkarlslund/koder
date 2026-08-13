package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

type UserInputOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type UserInputQuestion struct {
	ID       string            `json:"id"`
	Header   string            `json:"header"`
	Question string            `json:"question"`
	Options  []UserInputOption `json:"options"`
}

type UserInputAnswer struct {
	ToolCallID string `json:"tool_call_id"`
	QuestionID string `json:"question_id"`
	Selected   string `json:"selected,omitempty"`
	Comment    string `json:"comment,omitempty"`
}

func ParseUserInputQuestions(raw string) ([]UserInputQuestion, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("questions are required")
	}
	var questions []UserInputQuestion
	if err := json.Unmarshal([]byte(raw), &questions); err != nil {
		return nil, fmt.Errorf("decode questions: %w", err)
	}
	if len(questions) < 1 || len(questions) > 3 {
		return nil, fmt.Errorf("questions must contain between 1 and 3 items")
	}
	seen := make(map[string]struct{}, len(questions))
	for idx := range questions {
		question := &questions[idx]
		question.ID = strings.TrimSpace(question.ID)
		question.Header = strings.TrimSpace(question.Header)
		question.Question = strings.TrimSpace(question.Question)
		if question.ID == "" || question.Header == "" || question.Question == "" {
			return nil, fmt.Errorf("question %d requires id, header, and question", idx+1)
		}
		if _, ok := seen[question.ID]; ok {
			return nil, fmt.Errorf("question id %q is duplicated", question.ID)
		}
		seen[question.ID] = struct{}{}
		if len(question.Options) < 2 || len(question.Options) > 5 {
			return nil, fmt.Errorf("question %q must offer between 2 and 5 options", question.ID)
		}
		labels := make(map[string]struct{}, len(question.Options))
		for optionIdx := range question.Options {
			option := &question.Options[optionIdx]
			option.Label = strings.TrimSpace(option.Label)
			option.Description = strings.TrimSpace(option.Description)
			if option.Label == "" || option.Description == "" {
				return nil, fmt.Errorf("question %q option %d requires label and description", question.ID, optionIdx+1)
			}
			if _, ok := labels[option.Label]; ok {
				return nil, fmt.Errorf("question %q option %q is duplicated", question.ID, option.Label)
			}
			labels[option.Label] = struct{}{}
		}
	}
	return questions, nil
}
