package agent

import (
	"fmt"
	"testing"
	"time"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/knowledge"
)

func TestCompletedTurnSignalsDetectCorrectionPreferenceAndToolRecovery(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	user := domain.TimelineItem{ID: "00000000-0000-7000-8000-000000000081", SealedAt: now, Content: domain.UserMessage{Text: "Actually, I prefer sfdisk and I don't want fdisk."}}
	failed := domain.TimelineItem{ID: "00000000-0000-7000-8000-000000000082", SealedAt: now, Content: domain.AssistantMessage{Tools: []domain.ToolCall{{Tool: domain.ToolKindExecCommand, Error: &domain.ToolError{Message: "missing"}}}}}
	succeeded := domain.TimelineItem{ID: "00000000-0000-7000-8000-000000000083", SealedAt: now, Content: domain.AssistantMessage{Text: "sfdisk worked", Tools: []domain.ToolCall{{Tool: domain.ToolKindWebSearch, Result: &domain.ToolResult{Text: "found docs"}}}}}
	signals := completedTurnSignals(chatpkg.CompletedTurn{User: user, Assistant: succeeded, Items: []domain.TimelineItem{user, failed, succeeded}})
	kinds := make(map[knowledge.CurationSignalKind]bool, len(signals))
	for _, signal := range signals {
		kinds[signal.Kind] = true
		if len(signal.SourceItemIDs) != 3 {
			t.Fatalf("signal source IDs = %#v", signal.SourceItemIDs)
		}
	}
	for _, kind := range []knowledge.CurationSignalKind{
		knowledge.CurationSignalKindUserCorrection, knowledge.CurationSignalKindExplicitPersonalPreference,
		knowledge.CurationSignalKindFailedThenSucceeded, knowledge.CurationSignalKindResearchedThenSucceeded,
	} {
		if !kinds[kind] {
			t.Fatalf("missing signal %s in %#v", kind, signals)
		}
	}
}

func TestCompletedTurnSignalsIgnoreOrdinaryConversation(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	user := domain.TimelineItem{ID: "00000000-0000-7000-8000-000000000081", SealedAt: now, Content: domain.UserMessage{Text: "Hello"}}
	assistant := domain.TimelineItem{ID: "00000000-0000-7000-8000-000000000082", SealedAt: now, Content: domain.AssistantMessage{Text: "Hi"}}
	if signals := completedTurnSignals(chatpkg.CompletedTurn{User: user, Assistant: assistant, Items: []domain.TimelineItem{user, assistant}}); len(signals) != 0 {
		t.Fatalf("ordinary conversation signals = %#v", signals)
	}
}

func TestBoundedCurationSourceIDsPreservesStartAndEnd(t *testing.T) {
	t.Parallel()
	ids := make([]string, 80)
	for index := range ids {
		ids[index] = fmt.Sprintf("item-%02d", index)
	}
	bounded := boundedCurationSourceIDs(ids)
	if len(bounded) != 64 || bounded[0] != "item-00" || bounded[31] != "item-31" || bounded[32] != "item-48" || bounded[63] != "item-79" {
		t.Fatalf("bounded IDs = %#v", bounded)
	}
}
