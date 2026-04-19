package sessionctx

import (
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

func TestFromMessagesUsesLatestUsageAndContextWindow(t *testing.T) {
	cfg := config.Default()
	session := domain.Session{ProviderID: cfg.DefaultProvider}
	messages := []domain.Message{
		{ID: 1},
		{ID: 2},
	}
	parts := map[int64][]domain.Part{
		1: {{Kind: domain.PartKindSystemNotice, Body: "usage", MetaJSON: `{"TotalTokens":1000}`}},
		2: {{Kind: domain.PartKindSystemNotice, Body: "usage", MetaJSON: `{"TotalTokens":8192}`}},
	}

	got, ok := FromMessages(cfg, session, messages, parts)
	if !ok {
		t.Fatal("expected metrics")
	}
	if got.Used != 8192 || got.Max != 32768 || got.UsagePercent != 25 {
		t.Fatalf("unexpected metrics: %#v", got)
	}
}

func TestFromMessagesSkipsMissingContextWindow(t *testing.T) {
	cfg := config.Default()
	cfg.Providers[cfg.DefaultProvider] = config.Provider{}
	session := domain.Session{ProviderID: cfg.DefaultProvider}

	if _, ok := FromMessages(cfg, session, nil, nil); ok {
		t.Fatal("expected missing metrics without context window")
	}
}
