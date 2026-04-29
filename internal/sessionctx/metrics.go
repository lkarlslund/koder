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
}

func FromMessages(cfg config.Config, session domain.Session, messages []domain.Message, parts map[int64][]domain.Part) (Metrics, bool) {
	providerCfg, ok := cfg.Provider(session.ProviderID)
	if !ok || providerCfg.ContextWindow <= 0 {
		return Metrics{}, false
	}
	usage, ok := LatestUsage(messages, parts)
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
			if part.Kind != domain.PartKindSystemNotice || part.Body != "usage" || part.MetaJSON == "" {
				continue
			}
			var usage domain.Usage
			if err := json.Unmarshal([]byte(part.MetaJSON), &usage); err != nil {
				continue
			}
			if usage.TotalTokens > 0 {
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
			if part.Kind != domain.PartKindSystemNotice || part.Body != "usage" || part.MetaJSON == "" {
				continue
			}
			var usage domain.Usage
			if err := json.Unmarshal([]byte(part.MetaJSON), &usage); err != nil {
				continue
			}
			if usage.TotalTokens <= 0 && usage.PromptTokens <= 0 && usage.CompletionTokens <= 0 && usage.CachedTokens <= 0 {
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
