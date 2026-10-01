package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DecisionRequest asks a decision model typed questions about a state.
type DecisionRequest struct {
	Model     string                      `json:"model"`
	State     map[string]string           `json:"state"`
	Questions map[string]DecisionQuestion `json:"questions"`
}

// DecisionQuestion is one question; a "choice" question picks among the
// criteria by key.
type DecisionQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

// DecisionAnswer is a model's answer to one question.
type DecisionAnswer struct {
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

type DecisionResponse struct {
	Model   string                    `json:"model"`
	Answers map[string]DecisionAnswer `json:"answers"`
}

// Decide sends a decision request to the provider's /systemone endpoint.
func (c *Client) Decide(ctx context.Context, input DecisionRequest) (response DecisionResponse, err error) {
	started := time.Now()
	defer func() { c.observe(input.Model, "decide", started, err) }()
	body, err := json.Marshal(input)
	if err != nil {
		return DecisionResponse{}, fmt.Errorf("encode decision request: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, c.apiPath("/systemone"), bytes.NewReader(body))
	if err != nil {
		return DecisionResponse{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return DecisionResponse{}, fmt.Errorf("decide: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return DecisionResponse{}, &APIError{
			Operation:  "decide",
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(raw)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return DecisionResponse{}, fmt.Errorf("decode decision response: %w", err)
	}
	return response, nil
}
