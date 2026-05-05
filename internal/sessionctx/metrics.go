package sessionctx

import (
	"encoding/json"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

type Metrics struct {
	Used         int
	Max          int
	UsagePercent int
	Estimated    bool
}

func FromMessages(cfg config.Config, session domain.Session, messages []domain.Message, parts map[int64][]domain.Part) (Metrics, bool) {
	providerCfg, ok := cfg.Provider(session.ProviderID)
	if !ok || providerCfg.ContextWindow <= 0 {
		return Metrics{}, false
	}
	usage, ok := LatestUsage(messages, parts)
	usage = usage.Normalized()
	if !ok || usage.TotalTokens <= 0 {
		return Metrics{}, false
	}
	percent := (usage.TotalTokens * 100) / providerCfg.ContextWindow
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return Metrics{
		Used:         usage.TotalTokens,
		Max:          providerCfg.ContextWindow,
		UsagePercent: percent,
	}, true
}

func LatestUsage(messages []domain.Message, parts map[int64][]domain.Part) (domain.Usage, bool) {
	for msgIdx := len(messages) - 1; msgIdx >= 0; msgIdx-- {
		msg := messages[msgIdx]
		messageParts := parts[msg.ID]
		for partIdx := len(messageParts) - 1; partIdx >= 0; partIdx-- {
			part := messageParts[partIdx]
			usage, ok := usageFromPart(part)
			if !ok {
				continue
			}
			usage = usage.Normalized()
			if usage.HasAnyTokens() {
				return usage, true
			}
		}
	}
	return domain.Usage{}, false
}

func TotalUsage(messages []domain.Message, parts map[int64][]domain.Part) (domain.Usage, bool) {
	var total domain.Usage
	var found bool
	for _, msg := range messages {
		for _, part := range parts[msg.ID] {
			usage, ok := usageFromPart(part)
			if !ok {
				continue
			}
			usage = usage.Normalized()
			if !usage.HasAnyTokens() {
				continue
			}
			total.PromptTokens += usage.PromptTokens
			total.CompletionTokens += usage.CompletionTokens
			total.CachedTokens += usage.CachedTokens
			total.TotalTokens += usage.TotalTokens
			found = true
		}
	}
	return total, found
}

func usageFromPart(part domain.Part) (domain.Usage, bool) {
	if payload, ok := part.Payload.(domain.UsagePayload); ok {
		return payload.Usage, true
	}
	if part.Kind != domain.PartKindSystemNotice || part.Body != "usage" || part.MetaJSON == "" {
		return domain.Usage{}, false
	}
	var usage domain.Usage
	if err := json.Unmarshal([]byte(part.MetaJSON), &usage); err != nil {
		return domain.Usage{}, false
	}
	return usage, true
}
