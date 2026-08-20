package codexapp

import "context"

type Model struct {
	ID                       string            `json:"id"`
	Model                    string            `json:"model"`
	DisplayName              string            `json:"displayName"`
	Description              string            `json:"description"`
	Hidden                   bool              `json:"hidden"`
	DefaultReasoningEffort   string            `json:"defaultReasoningEffort"`
	SupportedReasoningEffort []ReasoningEffort `json:"supportedReasoningEfforts"`
	InputModalities          []string          `json:"inputModalities"`
	SupportsPersonality      bool              `json:"supportsPersonality"`
	IsDefault                bool              `json:"isDefault"`
}

type ReasoningEffort struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}

func (c *Client) Models(ctx context.Context) ([]Model, error) {
	if err := c.Start(ctx); err != nil {
		return nil, err
	}
	var response struct {
		Data []Model `json:"data"`
	}
	if err := c.Call(ctx, "model/list", map[string]any{"limit": 100}, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}
