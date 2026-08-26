package service

import (
	"context"
	"slices"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
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
	AuditID  string            `json:"audit_id,omitempty"`
}

type MutationObject struct {
	Kind memoryStoreAPI.RecordKind `json:"kind"`
	ID   string                    `json:"id"`
}

type MutationRevision struct {
	Number uint64            `json:"number"`
	ID     memory.RevisionID `json:"id"`
}

type MutationCheckpoint struct {
	StreamID string `json:"stream_id"`
	Sequence uint64 `json:"sequence"`
}

// MutationCheckpoint returns the point clients can use as their live-update
// baseline. Call it before reading a snapshot: concurrent mutations may then
// appear both in the snapshot and as an event, but can never be missed.
func (s *Service) MutationCheckpoint() MutationCheckpoint {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	return MutationCheckpoint{StreamID: s.mutationStreamID, Sequence: s.mutationSequence}
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

func (s *Service) publishMutation(ctx context.Context, event MutationEvent) {
	s.publishMutations(ctx, []MutationEvent{event})
}

func (s *Service) publishMutations(ctx context.Context, events []MutationEvent) {
	if len(events) == 0 {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	for _, event := range events {
		event.AuditID = AuditIDFromContext(ctx)
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

func chunkMutation(kind MutationKind, chunk memory.Chunk) MutationEvent {
	revision := MutationRevision{Number: chunk.Revision.Number, ID: chunk.Revision.ID}
	return MutationEvent{Kind: kind, Object: MutationObject{Kind: memoryStoreAPI.RecordKindChunk, ID: string(chunk.ID)}, Revision: &revision}
}

func entryMutation(kind MutationKind, entry memory.Entry) MutationEvent {
	revision := MutationRevision{Number: entry.Revision.Number, ID: entry.Revision.ID}
	return MutationEvent{Kind: kind, Object: MutationObject{Kind: memoryStoreAPI.RecordKindEntry, ID: string(entry.ID)}, Revision: &revision}
}

func linkMutation(kind MutationKind, link memory.Link) MutationEvent {
	revision := MutationRevision{Number: link.Revision.Number, ID: link.Revision.ID}
	return MutationEvent{Kind: kind, Object: MutationObject{Kind: memoryStoreAPI.RecordKindLink, ID: string(link.ID)}, Revision: &revision}
}

func evidenceMutation(kind MutationKind, evidence memory.Evidence) MutationEvent {
	return MutationEvent{Kind: kind, Object: MutationObject{Kind: memoryStoreAPI.RecordKindEvidence, ID: string(evidence.ID)}}
}
