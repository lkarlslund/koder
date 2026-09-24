package provider

import (
	"errors"
	"net/http"
	"strings"
)

// IsContextWindowExceeded reports compatible-provider errors that mean the
// request prompt does not fit. Providers use different codes and wording.
func IsContextWindowExceeded(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
	default:
		return false
	}
	body := strings.ToLower(apiErr.Body)
	for _, marker := range []string{
		"context_length_exceeded",
		"context window",
		"maximum context length",
		"too many tokens",
		"prompt is too long",
		"input is too long",
	} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}
