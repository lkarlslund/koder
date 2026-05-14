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
		{ID: "msg-1"},
		{ID: "msg-2"},
	}
	parts := map[domain.ID][]domain.Part{
		"msg-1": {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{TotalTokens: 1000}}}},
		"msg-2": {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{TotalTokens: 8192}}}},
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
	messages := []domain.Message{{ID: "msg-1"}}
	parts := map[domain.ID][]domain.Part{
		"msg-1": {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{PromptTokens: 1200, CompletionTokens: 300}}}},
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
		{ID: "msg-1"},
		{ID: "msg-2"},
	}
	parts := map[domain.ID][]domain.Part{
		"msg-1": {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{PromptTokens: 100, CompletionTokens: 40, CachedTokens: 10, TotalTokens: 140}}}},
		"msg-2": {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{PromptTokens: 50, CompletionTokens: 25, TotalTokens: 75}}}},
	}

	got, ok := TotalUsage(messages, parts)
	if !ok {
		t.Fatal("expected total usage")
	}
	if got.PromptTokens != 150 || got.CompletionTokens != 65 || got.CachedTokens != 10 || got.TotalTokens != 215 {
		t.Fatalf("unexpected total usage: %#v", got)
	}
}

func TestEstimateTailTokensUsesLatestUsageAnchor(t *testing.T) {
	messages := []domain.Message{
		{ID: "msg-1", Role: domain.MessageRoleUser},
		{ID: "msg-2", Role: domain.MessageRoleAssistant},
		{ID: "msg-3", Role: domain.MessageRoleUser},
		{ID: "msg-4", Role: domain.MessageRoleAssistant},
	}
	parts := map[domain.ID][]domain.Part{
		"msg-1": {{Kind: domain.PartKindText, Payload: domain.TextPayload{Text: "old context should be ignored"}}},
		"msg-2": {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{PromptTokens: 1200, CompletionTokens: 40, TotalTokens: 1240}}}},
		"msg-3": {{Kind: domain.PartKindText, Payload: domain.TextPayload{Text: "new prompt words"}}},
		"msg-4": {{Kind: domain.PartKindReasoning, Payload: domain.ReasoningPayload{Text: "fresh thoughts only"}}},
	}

	got, ok := EstimateTailTokens(messages, parts)
	if !ok {
		t.Fatal("expected latest usage anchor")
	}
	want := estimateMessageTokens(messages[2], parts["msg-3"]) + estimateMessageTokens(messages[3], parts["msg-4"])
	if got != want {
		t.Fatalf("expected tail-only estimate, got %d", got)
	}
}

func TestEstimateTailTokensUsesCompletedCompactionAnchor(t *testing.T) {
	messages := []domain.Message{
		{ID: "msg-1", Role: domain.MessageRoleUser},
		{ID: "msg-2", Role: domain.MessageRoleAssistant},
		{ID: "msg-3", Role: domain.MessageRoleAssistant},
		{ID: "msg-4", Role: domain.MessageRoleUser},
	}
	parts := map[domain.ID][]domain.Part{
		"msg-1": {{Kind: domain.PartKindText, Payload: domain.TextPayload{Text: "large pre-compaction transcript should be ignored"}}},
		"msg-2": {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{PromptTokens: 36000, CompletionTokens: 40, TotalTokens: 36040}}}},
		"msg-3": {{Kind: domain.PartKindCompaction, Payload: domain.CompactionPayload{Summary: "short compacted summary", Status: "completed", AfterContextTokens: 6564}}},
		"msg-4": {{Kind: domain.PartKindText, Payload: domain.TextPayload{Text: "followup after compaction"}}},
	}

	got, ok := EstimateTailTokens(messages, parts)
	if !ok {
		t.Fatal("expected compaction context anchor")
	}
	want := estimateMessageTokens(messages[3], parts["msg-4"])
	if got != want {
		t.Fatalf("expected post-compaction tail only, got %d want %d", got, want)
	}
}

func TestEstimateTailTokensCountsPartsAfterAnchorInSameMessage(t *testing.T) {
	messages := []domain.Message{
		{ID: "msg-1", Role: domain.MessageRoleAssistant},
	}
	parts := map[domain.ID][]domain.Part{
		"msg-1": {
			{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{PromptTokens: 1200, CompletionTokens: 40, TotalTokens: 1240}}},
			{Kind: domain.PartKindText, Payload: domain.TextPayload{Text: "persisted after usage"}},
		},
	}

	got, ok := EstimateTailTokens(messages, parts)
	if !ok {
		t.Fatal("expected context anchor")
	}
	want := estimateMessageTokens(messages[0], parts["msg-1"][1:])
	if got != want {
		t.Fatalf("expected same-message tail, got %d want %d", got, want)
	}
}
