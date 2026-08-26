package webui

import (
	"net/http"
	"testing"
)

func closeMemoryHTTPResponse(t *testing.T, response *http.Response) {
	t.Helper()
	if err := response.Body.Close(); err != nil {
		t.Errorf("close Memory HTTP response: %v", err)
	}
}
