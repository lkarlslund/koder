package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHealthTrackerKeepsRealModelObservationAcrossDiscovery(t *testing.T) {
	tracker := NewHealthTracker()
	tracker.Observe("provider", "model", "chat", time.Now(), errors.New("inference failed"))
	tracker.ObserveModels("provider", []string{"model", "new-model"}, time.Now(), nil)

	failed := tracker.Model("provider", "model")
	if failed.Status != HealthUnhealthy || failed.Operation != "chat" || failed.Detail != "inference failed" {
		t.Fatalf("real model observation was overwritten by discovery: %#v", failed)
	}
	advertised := tracker.Model("provider", "new-model")
	if advertised.Status != HealthHealthy || advertised.Operation != "list_models" || advertised.LatencyMS != 0 {
		t.Fatalf("unexpected advertised model health: %#v", advertised)
	}
	providerHealth := tracker.Provider("provider")
	if providerHealth.Status != HealthHealthy || providerHealth.Operation != "list_models" || providerHealth.ModelCount != 2 {
		t.Fatalf("unexpected provider discovery health: %#v", providerHealth)
	}
}

func TestHealthTrackerIgnoresCallerCancellation(t *testing.T) {
	tracker := NewHealthTracker()
	tracker.Observe("provider", "model", "chat", time.Now(), nil)
	want := tracker.Model("provider", "model")
	tracker.Observe("provider", "model", "chat", time.Now(), context.Canceled)
	if got := tracker.Model("provider", "model"); got.Status != want.Status || got.CheckedAt != want.CheckedAt {
		t.Fatalf("cancellation changed health: got %#v want %#v", got, want)
	}
}

func TestHealthTrackerRecordsOperationFailure(t *testing.T) {
	tracker := NewHealthTracker()
	tracker.Observe("provider", "model", "tts", time.Now(), errors.New("service unavailable"))
	for name, got := range map[string]RuntimeHealth{
		"provider": tracker.Provider("provider"),
		"model":    tracker.Model("provider", "model"),
	} {
		if got.Status != HealthUnhealthy || got.Operation != "tts" || got.CheckedAt == nil {
			t.Fatalf("unexpected %s health: %#v", name, got)
		}
	}
}
