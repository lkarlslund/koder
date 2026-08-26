package agent

import (
	"fmt"
	"slices"
	"testing"
	"time"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/memory"
)

func TestCompletedTurnSignalsDetectCorrectionPreferenceAndToolRecovery(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	user := domain.TimelineItem{ID: "00000000-0000-7000-8000-000000000081", SealedAt: now, Content: domain.UserMessage{Text: "Actually, I prefer sfdisk and I don't want fdisk."}}
	failed := domain.TimelineItem{ID: "00000000-0000-7000-8000-000000000082", SealedAt: now, Content: domain.AssistantMessage{Tools: []domain.ToolCall{{Tool: domain.ToolKindExecCommand, Error: &domain.ToolError{Message: "missing"}}}}}
	succeeded := domain.TimelineItem{ID: "00000000-0000-7000-8000-000000000083", SealedAt: now, Content: domain.AssistantMessage{Text: "sfdisk worked", Tools: []domain.ToolCall{{Tool: domain.ToolKindWebSearch, Result: &domain.ToolResult{Text: "found docs"}}}}}
	signals := completedTurnSignals(chatpkg.CompletedTurn{User: user, Assistant: succeeded, Items: []domain.TimelineItem{user, failed, succeeded}})
	kinds := make(map[memory.CurationSignalKind]bool, len(signals))
	for _, signal := range signals {
		kinds[signal.Kind] = true
		if len(signal.SourceItemIDs) != 3 {
			t.Fatalf("signal source IDs = %#v", signal.SourceItemIDs)
		}
	}
	for _, kind := range []memory.CurationSignalKind{
		memory.CurationSignalKindUserCorrection, memory.CurationSignalKindExplicitPersonalPreference,
		memory.CurationSignalKindFailedThenSucceeded, memory.CurationSignalKindResearchedThenSucceeded,
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

func TestCurationFlowsDetectCorrectionContradictionAndPersonalPreference(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	tests := []struct {
		name string
		text string
		want memory.CurationSignalKind
	}{
		{name: "correction", text: "Actually, that is wrong; use sfdisk.", want: memory.CurationSignalKindUserCorrection},
		{name: "contradiction", text: "This contradicts what the manual says.", want: memory.CurationSignalKindContradictingEvidence},
		{name: "personal preference", text: "I prefer concise answers.", want: memory.CurationSignalKindExplicitPersonalPreference},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			user := domain.TimelineItem{ID: "00000000-0000-7000-8000-000000000081", SealedAt: now, Content: domain.UserMessage{Text: test.text}}
			assistant := domain.TimelineItem{ID: "00000000-0000-7000-8000-000000000082", SealedAt: now, Content: domain.AssistantMessage{Text: "Understood."}}
			signals := completedTurnSignals(chatpkg.CompletedTurn{User: user, Assistant: assistant, Items: []domain.TimelineItem{user, assistant}})
			if !slices.ContainsFunc(signals, func(signal memory.CurationSignal) bool { return signal.Kind == test.want }) {
				t.Fatalf("signals = %#v, want %s", signals, test.want)
			}
		})
	}
}

func TestRepeatedWorkaroundRequiresDifferentSessions(t *testing.T) {
	t.Parallel()
	engine := &Engine{}
	now := time.Now().UTC()
	user := domain.TimelineItem{ID: "00000000-0000-7000-8000-000000000081", SealedAt: now, Content: domain.UserMessage{Text: "partition the disk"}}
	assistant := domain.TimelineItem{ID: "00000000-0000-7000-8000-000000000082", SealedAt: now, Content: domain.AssistantMessage{Text: "Used the available fallback.", Tools: []domain.ToolCall{
		{Tool: domain.ToolKindExecCommand, Args: map[string]string{"action": "run"}, Error: &domain.ToolError{Message: "fdisk unavailable"}},
		{Tool: domain.ToolKindExecCommand, Args: map[string]string{"action": "run"}, Result: &domain.ToolResult{Text: "sfdisk succeeded"}},
	}}}
	turn := chatpkg.CompletedTurn{Session: domain.Session{ID: "00000000-0000-7000-8000-000000000091"}, User: user, Assistant: assistant, Items: []domain.TimelineItem{user, assistant}}
	first := engine.curationSignalsForCompletedTurn(turn)
	if slices.ContainsFunc(first, func(signal memory.CurationSignal) bool {
		return signal.Kind == memory.CurationSignalKindRepeatedWorkaround
	}) {
		t.Fatalf("first-session signals = %#v", first)
	}
	secondSameSession := engine.curationSignalsForCompletedTurn(turn)
	if slices.ContainsFunc(secondSameSession, func(signal memory.CurationSignal) bool {
		return signal.Kind == memory.CurationSignalKindRepeatedWorkaround
	}) {
		t.Fatalf("same-session signals = %#v", secondSameSession)
	}
	turn.Session.ID = "00000000-0000-7000-8000-000000000092"
	secondSession := engine.curationSignalsForCompletedTurn(turn)
	if !slices.ContainsFunc(secondSession, func(signal memory.CurationSignal) bool {
		return signal.Kind == memory.CurationSignalKindRepeatedWorkaround
	}) {
		t.Fatalf("cross-session signals = %#v", secondSession)
	}
}
