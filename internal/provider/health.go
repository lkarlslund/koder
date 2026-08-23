package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// HealthStatus describes the latest observed runtime availability.
type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy   HealthStatus = "healthy"
	HealthUnhealthy HealthStatus = "unhealthy"
	HealthDisabled  HealthStatus = "disabled"
)

// RuntimeHealth is the latest process-local observation for a provider or
// model. It is runtime state and is never persisted as configuration.
type RuntimeHealth struct {
	Status     HealthStatus `json:"status"`
	Detail     string       `json:"detail,omitempty"`
	Operation  string       `json:"operation,omitempty"`
	CheckedAt  *time.Time   `json:"checked_at,omitempty"`
	LatencyMS  int64        `json:"latency_ms,omitempty"`
	ModelCount int          `json:"model_count,omitempty"`
}

// HealthTracker owns provider and model observations shared by every runtime
// client in this process.
type HealthTracker struct {
	mu         sync.RWMutex
	providers  map[string]RuntimeHealth
	models     map[string]RuntimeHealth
	advertised map[string]map[string]struct{}
}

func NewHealthTracker() *HealthTracker {
	return &HealthTracker{
		providers:  map[string]RuntimeHealth{},
		models:     map[string]RuntimeHealth{},
		advertised: map[string]map[string]struct{}{},
	}
}

func (t *HealthTracker) Provider(providerID string) RuntimeHealth {
	if t == nil {
		return unknownHealth()
	}
	t.mu.RLock()
	health, ok := t.providers[strings.TrimSpace(providerID)]
	t.mu.RUnlock()
	if !ok {
		return unknownHealth()
	}
	return health
}

func (t *HealthTracker) Model(providerID, modelID string) RuntimeHealth {
	if t == nil {
		return unknownHealth()
	}
	t.mu.RLock()
	health, ok := t.models[healthModelKey(providerID, modelID)]
	t.mu.RUnlock()
	if !ok {
		return unknownHealth()
	}
	return health
}

// Advertises reports whether the latest successful model discovery included
// modelID. The second result is false until at least one discovery has
// succeeded, so callers can distinguish "missing" from "not checked yet".
func (t *HealthTracker) Advertises(providerID, modelID string) (bool, bool) {
	if t == nil {
		return false, false
	}
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	t.mu.RLock()
	models, known := t.advertised[providerID]
	_, advertised := models[modelID]
	t.mu.RUnlock()
	return advertised, known
}

// Observe records the outcome of one real provider operation. A caller-driven
// cancellation is not evidence that the provider or model became unhealthy.
func (t *HealthTracker) Observe(providerID, modelID, operation string, started time.Time, operationErr error) {
	if t == nil {
		return
	}
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" || errors.Is(operationErr, context.Canceled) {
		return
	}
	checkedAt := time.Now()
	health := RuntimeHealth{
		Status:    HealthHealthy,
		Detail:    "Last operation succeeded",
		Operation: strings.TrimSpace(operation),
		CheckedAt: &checkedAt,
		LatencyMS: time.Since(started).Milliseconds(),
	}
	if operationErr != nil {
		health.Status = HealthUnhealthy
		health.Detail = operationErr.Error()
	}
	t.mu.Lock()
	t.providers[providerID] = health
	if modelID != "" {
		t.models[healthModelKey(providerID, modelID)] = health
	}
	t.mu.Unlock()
}

// ObserveModels records provider discovery and makes newly advertised models
// available without overwriting a model's newer real-operation observation.
func (t *HealthTracker) ObserveModels(providerID string, modelIDs []string, started time.Time, operationErr error) {
	if t == nil {
		return
	}
	t.Observe(providerID, "", "list_models", started, operationErr)
	if operationErr != nil {
		return
	}
	providerID = strings.TrimSpace(providerID)
	checkedAt := time.Now()
	t.mu.Lock()
	providerHealth := t.providers[providerID]
	providerHealth.Detail = "Discovered " + modelCountLabel(len(modelIDs))
	providerHealth.ModelCount = len(modelIDs)
	t.providers[providerID] = providerHealth
	advertised := make(map[string]struct{}, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		advertised[modelID] = struct{}{}
		key := healthModelKey(providerID, modelID)
		if existing, ok := t.models[key]; ok && existing.Operation != "list_models" {
			continue
		}
		t.models[key] = RuntimeHealth{
			Status:    HealthHealthy,
			Detail:    "Advertised by provider",
			Operation: "list_models",
			CheckedAt: &checkedAt,
		}
	}
	t.advertised[providerID] = advertised
	t.mu.Unlock()
}

func unknownHealth() RuntimeHealth {
	return RuntimeHealth{Status: HealthUnknown, Detail: "Awaiting a provider operation"}
}

func healthModelKey(providerID, modelID string) string {
	return strings.TrimSpace(providerID) + "\x00" + strings.TrimSpace(modelID)
}

func modelCountLabel(count int) string {
	if count == 1 {
		return "1 model"
	}
	return fmt.Sprintf("%d models", count)
}
