package webui

import (
	"net/http"
	"testing"
)

func closeKnowledgeHTTPResponse(t *testing.T, response *http.Response) {
	t.Helper()
	if err := response.Body.Close(); err != nil {
		t.Errorf("close Knowledge HTTP response: %v", err)
	}
}
