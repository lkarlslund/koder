package service

import (
	"slices"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type MutationKind string

const (
	MutationCreated    MutationKind = "created"
	MutationUpdated    MutationKind = "updated"
	MutationArchived   MutationKind = "archived"
	MutationRestored   MutationKind = "restored"
	MutationSuperseded MutationKind = "superseded"
	MutationVerified   MutationKind = "verified"
	MutationUnlinked   MutationKind = "unlinked"
	MutationDeleted    MutationKind = "deleted"
)

// MutationEvent is a content-free invalidation patch. StreamID and Sequence
// establish total order for delivery/gap detection, while Revision establishes
// per-object optimistic order. Clients refetch through the authorized API.
type MutationEvent struct {
	StreamID string            `json:"stream_id"`
	Sequence uint64            `json:"sequence"`
	Kind     MutationKind      `json:"kind"`
	Object   MutationObject    `json:"object"`
	Revision *MutationRevision `json:"revision,omitempty"`
	Related  []MutationObject  `json:"related,omitempty"`
}

type MutationObject struct {
	Kind knowledgeStore.RecordKind `json:"kind"`
	ID   string                    `json:"id"`
}

type MutationRevision struct {
	Number uint64               `json:"number"`
	ID     knowledge.RevisionID `json:"id"`
}

func (s *Service) SubscribeMutations(buffer int) (<-chan MutationEvent, func()) {
	if buffer <= 0 {
		buffer = 64
	}
	if buffer > 1024 {
		buffer = 1024
	}
	channel := make(chan MutationEvent, buffer)
	s.mutationMu.Lock()
	subscriberID := s.mutationNextSub
	s.mutationNextSub++
	s.mutationSubs[subscriberID] = channel
	s.mutationMu.Unlock()
	return channel, func() {
		s.mutationMu.Lock()
		if existing, ok := s.mutationSubs[subscriberID]; ok {
			delete(s.mutationSubs, subscriberID)
			close(existing)
		}
		s.mutationMu.Unlock()
	}
}

func (s *Service) publishMutation(event MutationEvent) {
	s.publishMutations([]MutationEvent{event})
}

func (s *Service) publishMutations(events []MutationEvent) {
	if len(events) == 0 {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	for _, event := range events {
		s.mutationSequence++
		event.StreamID = s.mutationStreamID
		event.Sequence = s.mutationSequence
		event.Related = slices.Clone(event.Related)
		if event.Revision != nil {
			revision := *event.Revision
			event.Revision = &revision
		}
		for _, subscriber := range s.mutationSubs {
			select {
			case subscriber <- cloneMutationEvent(event):
			default:
			}
		}
	}
}

func cloneMutationEvent(event MutationEvent) MutationEvent {
	event.Related = slices.Clone(event.Related)
	if event.Revision != nil {
		revision := *event.Revision
		event.Revision = &revision
	}
	return event
}

func chunkMutation(kind MutationKind, chunk knowledge.Chunk) MutationEvent {
	revision := MutationRevision{Number: chunk.Revision.Number, ID: chunk.Revision.ID}
	return MutationEvent{Kind: kind, Object: MutationObject{Kind: knowledgeStore.RecordKindChunk, ID: string(chunk.ID)}, Revision: &revision}
}

func entryMutation(kind MutationKind, entry knowledge.Entry) MutationEvent {
	revision := MutationRevision{Number: entry.Revision.Number, ID: entry.Revision.ID}
	return MutationEvent{Kind: kind, Object: MutationObject{Kind: knowledgeStore.RecordKindEntry, ID: string(entry.ID)}, Revision: &revision}
}

func linkMutation(kind MutationKind, link knowledge.Link) MutationEvent {
	revision := MutationRevision{Number: link.Revision.Number, ID: link.Revision.ID}
	return MutationEvent{Kind: kind, Object: MutationObject{Kind: knowledgeStore.RecordKindLink, ID: string(link.ID)}, Revision: &revision}
}

func evidenceMutation(kind MutationKind, evidence knowledge.Evidence) MutationEvent {
	return MutationEvent{Kind: kind, Object: MutationObject{Kind: knowledgeStore.RecordKindEvidence, ID: string(evidence.ID)}}
}
