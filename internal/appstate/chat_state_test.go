package appstate

import (
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

func TestChatStateMergeLoadedPreservesRecordIdentity(t *testing.T) {
	t.Helper()
	initialMessages := []domain.Message{{ID: 1, Role: domain.MessageRoleUser, Summary: "one"}}
	initialParts := map[int64][]domain.Part{
		1: {{ID: 10, MessageID: 1, Kind: domain.PartKindText, Payload: domain.TextPayload{Text: "one"}}},
	}
	state := NewChatState(initialMessages, initialParts, []store.Approval{{ID: 50}})
	msgRecord := state.Messages()[0]
	partRecord := msgRecord.Parts[0]

	updatedMessages := []domain.Message{{ID: 1, Role: domain.MessageRoleUser, Summary: "updated"}}
	updatedParts := map[int64][]domain.Part{
		1: {{ID: 10, MessageID: 1, Kind: domain.PartKindText, Payload: domain.TextPayload{Text: "updated"}}},
	}
	state.MergeLoaded(updatedMessages, updatedParts, []store.Approval{{ID: 51}})

	if got := state.Messages()[0]; got != msgRecord {
		t.Fatalf("message record pointer changed")
	}
	if got := state.Messages()[0].Parts[0]; got != partRecord {
		t.Fatalf("part record pointer changed")
	}
	if got := state.Messages()[0].Message.Summary; got != "updated" {
		t.Fatalf("message summary = %q", got)
	}
	if got := state.Messages()[0].Parts[0].Part.Text(); got != "updated" {
		t.Fatalf("part text = %q", got)
	}
	if approvals := state.Approvals(); len(approvals) != 1 || approvals[0].ID != 51 {
		t.Fatalf("approvals = %+v", approvals)
	}
}

func TestChatStateAppendMessage(t *testing.T) {
	t.Helper()
	state := NewChatState(nil, nil, nil)
	record := state.AppendMessage(
		domain.Message{ID: 7, Role: domain.MessageRoleAssistant, Summary: "hello"},
		[]domain.Part{{ID: 9, MessageID: 7, Kind: domain.PartKindText, Payload: domain.TextPayload{Text: "hello"}}},
	)
	if record == nil {
		t.Fatalf("append returned nil record")
	}
	if got := len(state.Messages()); got != 1 {
		t.Fatalf("message count = %d", got)
	}
	if got := state.Messages()[0]; got != record {
		t.Fatalf("state did not keep appended record")
	}
	if got := state.SnapshotMessages()[0].ID; got != 7 {
		t.Fatalf("snapshot message id = %d", got)
	}
	if got := state.SnapshotParts()[7][0].ID; got != 9 {
		t.Fatalf("snapshot part id = %d", got)
	}
}
