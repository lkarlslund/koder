package provider

import (
	"errors"
	"net/http"
	"strings"
)

func ShouldRetryWithoutPromptProgress(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
	default:
		return false
	}
	body := strings.ToLower(apiErr.Body)
	if strings.Contains(body, "return_progress") || strings.Contains(body, "prompt_progress") {
		return true
	}
	if strings.Contains(body, "unknown") || strings.Contains(body, "unsupported") || strings.Contains(body, "unrecognized") {
		return true
	}
	return false
}

func WithoutPromptProgress(req ChatRequest) ChatRequest {
	if len(req.ExtraBody) == 0 {
		return req
	}
	next := make(map[string]any, len(req.ExtraBody))
	for key, value := range req.ExtraBody {
		if strings.EqualFold(strings.TrimSpace(key), "return_progress") {
			continue
		}
		next[key] = value
	}
	if len(next) == 0 {
		next = nil
	}
	req.ExtraBody = next
	return req
}

func RequestsPromptProgress(req ChatRequest) bool {
	for key, value := range req.ExtraBody {
		if strings.EqualFold(strings.TrimSpace(key), "return_progress") {
			enabled, ok := value.(bool)
			return !ok || enabled
		}
	}
	return false
}
