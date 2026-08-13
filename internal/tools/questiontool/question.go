package questiontool

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Request user input",
		Description: "Ask the user one to three short questions and wait for their submitted answers.",
		Usage:       "Use request_user_input when a missing user choice would materially change the result. Each question must offer 2-5 mutually exclusive options. The user may select one option, add a comment, or do both. Questions from request_user_input calls in the same assistant response are presented together. Do not combine this tool with executable tools in the same response.",
		Parameters:  `{"type":"object","properties":{"questions":{"type":"array","minItems":1,"maxItems":3,"items":{"type":"object","properties":{"id":{"type":"string","description":"Stable short identifier for matching the answer"},"header":{"type":"string","description":"Short modal heading"},"question":{"type":"string","description":"Question shown to the user"},"options":{"type":"array","minItems":2,"maxItems":5,"items":{"type":"object","properties":{"label":{"type":"string"},"description":{"type":"string"}},"required":["label","description"],"additionalProperties":false}}},"required":["id","header","question","options"],"additionalProperties":false}}},"required":["questions"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) ID() tools.ID             { return tools.RequestUserInput }
func (tool) BypassesPermission() bool { return false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	questions, err := tools.ParseUserInputQuestions(args["questions"])
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(questions)
	if err != nil {
		return nil, err
	}
	return map[string]string{"questions": string(encoded)}, nil
}
func (tool) Preview(req tools.Request) string {
	questions, err := tools.ParseUserInputQuestions(req.Args["questions"])
	if err != nil || len(questions) == 0 {
		return "User input"
	}
	return questions[0].Question
}
func (tool) Call(_ context.Context, opts tools.Options) (tools.Result, error) {
	_ = opts
	return tools.Result{}, errors.New("request_user_input must be handled by the chat runtime")
}
