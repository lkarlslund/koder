package sessionctx

import (
	"encoding/json"
	"strings"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tokenestimate"
)

type Metrics struct {
	Used         int
	Max          int
	UsagePercent int
	Estimated    bool
}

func FromMessages(cfg config.Config, session domain.Session, messages []domain.Message, parts map[domain.ID][]domain.Part) (Metrics, bool) {
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

// FromTimeline returns context metrics from timeline usage data.
func FromTimeline(cfg config.Config, session domain.Session, items []domain.TimelineItem) (Metrics, bool) {
	providerCfg, ok := cfg.Provider(session.ProviderID)
	if !ok || providerCfg.ContextWindow <= 0 {
		return Metrics{}, false
	}
	usage, ok := LatestTimelineUsage(items)
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
	return Metrics{Used: usage.TotalTokens, Max: providerCfg.ContextWindow, UsagePercent: percent}, true
}

// LatestTimelineUsage returns the latest usage attached to an assistant item.
func LatestTimelineUsage(items []domain.TimelineItem) (domain.Usage, bool) {
	for idx := len(items) - 1; idx >= 0; idx-- {
		assistant, ok := items[idx].Content.(domain.AssistantMessage)
		if !ok || assistant.Usage == nil {
			continue
		}
		usage := assistant.Usage.Normalized()
		if usage.HasAnyTokens() {
			return usage, true
		}
	}
	return domain.Usage{}, false
}

// EstimateTimelineTailTokens estimates persisted timeline content after the latest context anchor.
func EstimateTimelineTailTokens(items []domain.TimelineItem) (int, bool) {
	anchorIdx, ok := LatestTimelineContextAnchor(items)
	if !ok {
		return 0, false
	}
	total := 0
	for idx := anchorIdx + 1; idx < len(items); idx++ {
		total += estimateTimelineItemTokens(items[idx])
	}
	return total, true
}

// LatestTimelineContextAnchor returns the latest item index that has known context size.
func LatestTimelineContextAnchor(items []domain.TimelineItem) (int, bool) {
	for idx := len(items) - 1; idx >= 0; idx-- {
		switch payload := items[idx].Content.(type) {
		case domain.AssistantMessage:
			if payload.Usage != nil && payload.Usage.Normalized().HasAnyTokens() {
				return idx, true
			}
		case domain.Compaction:
			if payload.Status == "completed" && payload.AfterContextTokens > 0 {
				return idx, true
			}
		}
	}
	return 0, false
}

// TotalTimelineUsage returns total usage attached to timeline items.
func TotalTimelineUsage(items []domain.TimelineItem) (domain.Usage, bool) {
	var total domain.Usage
	var found bool
	for _, item := range items {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok || assistant.Usage == nil {
			continue
		}
		usage := assistant.Usage.Normalized()
		if !usage.HasAnyTokens() {
			continue
		}
		total = total.Add(usage)
		found = true
	}
	return total, found
}

func LatestUsage(messages []domain.Message, parts map[domain.ID][]domain.Part) (domain.Usage, bool) {
	usage, _, _, ok := LatestUsageAnchor(messages, parts)
	return usage, ok
}

// LatestUsageAnchor returns the latest usage payload together with its message
// and part position in the chat history.
func LatestUsageAnchor(messages []domain.Message, parts map[domain.ID][]domain.Part) (domain.Usage, domain.ID, int, bool) {
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
				return usage, msg.ID, partIdx, true
			}
		}
	}
	return domain.Usage{}, "", 0, false
}

// EstimateTailTokens estimates the token cost of persisted chat content added
// after the latest context-bearing part. Usage events and completed compaction
// parts both establish a fresh context anchor for the chat.
func EstimateTailTokens(messages []domain.Message, parts map[domain.ID][]domain.Part) (int, bool) {
	anchorMessageID, anchorPartIdx, ok := LatestContextAnchor(messages, parts)
	if !ok {
		return 0, false
	}
	total := 0
	seenAnchor := false
	for _, msg := range messages {
		messageParts := parts[msg.ID]
		if msg.ID == anchorMessageID {
			seenAnchor = true
			if anchorPartIdx+1 < len(messageParts) {
				total += estimateMessageTokens(msg, messageParts[anchorPartIdx+1:])
			}
			continue
		}
		if !seenAnchor {
			continue
		}
		total += estimateMessageTokens(msg, messageParts)
	}
	return total, true
}

// LatestContextAnchor returns the latest part position that corresponds to a
// known context size for the chat.
func LatestContextAnchor(messages []domain.Message, parts map[domain.ID][]domain.Part) (domain.ID, int, bool) {
	for msgIdx := len(messages) - 1; msgIdx >= 0; msgIdx-- {
		msg := messages[msgIdx]
		messageParts := parts[msg.ID]
		for partIdx := len(messageParts) - 1; partIdx >= 0; partIdx-- {
			part := messageParts[partIdx]
			if usage, ok := usageFromPart(part); ok {
				if usage.Normalized().HasAnyTokens() {
					return msg.ID, partIdx, true
				}
				continue
			}
			if payload, ok := part.Payload.(domain.CompactionPayload); ok {
				if payload.Status == "completed" && payload.AfterContextTokens > 0 {
					return msg.ID, partIdx, true
				}
			}
		}
	}
	return "", 0, false
}

func TotalUsage(messages []domain.Message, parts map[domain.ID][]domain.Part) (domain.Usage, bool) {
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

func estimateMessageTokens(msg domain.Message, parts []domain.Part) int {
	texts := make([]string, 0, len(parts)+2)
	if role := strings.TrimSpace(string(msg.Role)); role != "" {
		texts = append(texts, role)
	}
	if len(parts) == 0 {
		if summary := strings.TrimSpace(msg.Summary); summary != "" {
			texts = append(texts, summary)
		}
	}
	for _, part := range parts {
		if part.Kind == domain.PartKindUsage {
			continue
		}
		if text := strings.TrimSpace(part.Text()); text != "" {
			texts = append(texts, text)
		}
	}
	return tokenestimate.Text(strings.Join(texts, "\n"))
}

func estimateTimelineItemTokens(item domain.TimelineItem) int {
	var texts []string
	switch payload := item.Content.(type) {
	case domain.UserMessage:
		if text := strings.TrimSpace(payload.Text); text != "" {
			texts = append(texts, string(domain.MessageRoleUser), text)
		}
		for _, attachment := range payload.Attachments {
			if attachment.Name != "" {
				texts = append(texts, attachment.Name)
			}
		}
		for _, ref := range payload.References {
			if ref.Display != "" {
				texts = append(texts, ref.Display)
			}
		}
	case domain.AssistantMessage:
		texts = append(texts, string(domain.MessageRoleAssistant))
		if text := strings.TrimSpace(payload.Reasoning.Text); text != "" {
			texts = append(texts, text)
		}
		if text := strings.TrimSpace(payload.Text); text != "" {
			texts = append(texts, text)
		}
		for _, tool := range payload.Tools {
			texts = append(texts, string(tool.Tool), string(tool.ToolCallID))
			if tool.Result != nil {
				texts = append(texts, tool.Result.Text)
			}
			if tool.Error != nil {
				texts = append(texts, tool.Error.Message)
			}
		}
	case domain.Notice:
		texts = append(texts, payload.Text)
	case domain.Compaction:
		texts = append(texts, payload.Summary)
	}
	return tokenestimate.Text(strings.Join(texts, "\n"))
}
