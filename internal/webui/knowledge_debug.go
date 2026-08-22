package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
)

type knowledgeDebugResponse struct {
	Status knowledgeService.OperationalStatus `json:"status"`
}

func (s *Server) handleKnowledgeDebug(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path != "/debug/knowledge" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeKnowledgeDebugError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	service := s.controller.KnowledgeService()
	if service == nil {
		writeKnowledgeDebugError(w, http.StatusServiceUnavailable, "knowledge diagnostics unavailable")
		return
	}
	status, err := service.OperationalStatus(r.Context())
	if err != nil {
		if errors.Is(err, knowledgeService.ErrOperationalPolicyDenied) {
			writeKnowledgeDebugError(w, http.StatusForbidden, "knowledge diagnostics denied")
			return
		}
		writeKnowledgeDebugError(w, http.StatusServiceUnavailable, "knowledge diagnostics unavailable")
		return
	}
	writeKnowledgeDebugJSON(w, http.StatusOK, knowledgeDebugResponse{Status: status})
}

func writeKnowledgeDebugJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeKnowledgeDebugError(w http.ResponseWriter, status int, message string) {
	writeKnowledgeDebugJSON(w, status, map[string]string{"error": message})
}
