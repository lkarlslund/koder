package sessionctx

import (
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

func TestFromMessagesUsesLatestUsageAndContextWindow(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProvider = "test"
	cfg.Providers["test"] = config.Provider{ContextWindow: 32768}
	session := domain.Session{ProviderID: cfg.DefaultProvider}
	messages := []domain.Message{
		{ID: 1},
		{ID: 2},
	}
	parts := map[int64][]domain.Part{
		1: {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{TotalTokens: 1000}}}},
		2: {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{TotalTokens: 8192}}}},
	}

	got, ok := FromMessages(cfg, session, messages, parts)
	if !ok {
		t.Fatal("expected metrics")
	}
	if got.Used != 8192 || got.Max != 32768 || got.UsagePercent != 25 {
		t.Fatalf("unexpected metrics: %#v", got)
	}
}

func TestFromMessagesSynthesizesTotalFromPromptAndCompletion(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProvider = "test"
	cfg.Providers["test"] = config.Provider{ContextWindow: 32768}
	session := domain.Session{ProviderID: cfg.DefaultProvider}
	messages := []domain.Message{{ID: 1}}
	parts := map[int64][]domain.Part{
		1: {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{PromptTokens: 1200, CompletionTokens: 300}}}},
	}

	got, ok := FromMessages(cfg, session, messages, parts)
	if !ok {
		t.Fatal("expected metrics")
	}
	if got.Used != 1500 || got.Max != 32768 || got.UsagePercent != 4 {
		t.Fatalf("unexpected metrics: %#v", got)
	}
}

func TestFromMessagesSkipsMissingContextWindow(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProvider = "test"
	cfg.Providers["test"] = config.Provider{}
	session := domain.Session{ProviderID: cfg.DefaultProvider}

	if _, ok := FromMessages(cfg, session, nil, nil); ok {
		t.Fatal("expected missing metrics without context window")
	}
}

func TestTotalUsageAccumulatesAllUsageNotices(t *testing.T) {
	messages := []domain.Message{
		{ID: 1},
		{ID: 2},
	}
	parts := map[int64][]domain.Part{
		1: {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{PromptTokens: 100, CompletionTokens: 40, CachedTokens: 10, TotalTokens: 140}}}},
		2: {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{PromptTokens: 50, CompletionTokens: 25, TotalTokens: 75}}}},
	}

	got, ok := TotalUsage(messages, parts)
	if !ok {
		t.Fatal("expected total usage")
	}
	if got.PromptTokens != 150 || got.CompletionTokens != 65 || got.CachedTokens != 10 || got.TotalTokens != 215 {
		t.Fatalf("unexpected total usage: %#v", got)
	}
}
