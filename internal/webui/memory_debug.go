package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	memoryService "github.com/lkarlslund/koder/internal/memory/service"
)

type memoryDebugResponse struct {
	Status memoryService.OperationalStatus `json:"status"`
}

func (s *Server) handleMemoryDebug(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path != "/debug/memory" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeMemoryDebugError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	service := s.controller.MemoryService()
	if service == nil {
		writeMemoryDebugError(w, http.StatusServiceUnavailable, "memory diagnostics unavailable")
		return
	}
	status, err := service.OperationalStatus(r.Context())
	if err != nil {
		if errors.Is(err, memoryService.ErrOperationalPolicyDenied) {
			writeMemoryDebugError(w, http.StatusForbidden, "memory diagnostics denied")
			return
		}
		writeMemoryDebugError(w, http.StatusServiceUnavailable, "memory diagnostics unavailable")
		return
	}
	writeMemoryDebugJSON(w, http.StatusOK, memoryDebugResponse{Status: status})
}

func writeMemoryDebugJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeMemoryDebugError(w http.ResponseWriter, status int, message string) {
	writeMemoryDebugJSON(w, status, map[string]string{"error": message})
}
