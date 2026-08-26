package memorytool

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

type relationshipInput struct {
	Source      memory.ObjectRef    `json:"source"`
	Target      memory.ObjectRef    `json:"target"`
	Kind        memory.LinkKind     `json:"kind"`
	Label       string              `json:"label,omitempty"`
	Notes       string              `json:"notes,omitempty"`
	EvidenceIDs []memory.EvidenceID `json:"evidence_ids,omitempty"`
}

type linkMutationResult struct {
	Link           memory.Link                  `json:"link"`
	Created        bool                         `json:"created,omitempty"`
	Updated        bool                         `json:"updated,omitempty"`
	Classification *memory.ClassificationResult `json:"classification,omitempty"`
}

type historyRevisionResult struct {
	Kind             memoryStoreAPI.RecordKind `json:"kind"`
	ID               string                    `json:"id"`
	Revision         memory.Revision           `json:"revision"`
	SemanticKind     string                    `json:"semantic_kind"`
	Title            string                    `json:"title,omitempty"`
	Summary          string                    `json:"summary,omitempty"`
	Scope            *memory.Scope             `json:"scope,omitempty"`
	State            string                    `json:"state"`
	Verification     *memory.Verification      `json:"verification,omitempty"`
	SupersededByID   memory.EntryID            `json:"superseded_by_id,omitempty"`
	Source           *memory.ObjectRef         `json:"source,omitempty"`
	Target           *memory.ObjectRef         `json:"target,omitempty"`
	RelationshipKind memory.LinkKind           `json:"relationship_kind,omitempty"`
	Label            string                    `json:"label,omitempty"`
}

type historyPageResult struct {
	Revisions  []historyRevisionResult `json:"revisions"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

func normalizeLinkArgs(args map[string]string) (map[string]string, error) {
	if strings.TrimSpace(args["id"]) != "" {
		if strings.TrimSpace(args["relationship"]) != "" {
			return nil, errors.New("link accepts either relationship for creation or id for restoration, not both")
		}
		return normalizeLinkLifecycleArgs(args, "link")
	}
	if strings.TrimSpace(args["expected_revision"]) != "" {
		return nil, errors.New("expected_revision is only valid when restoring an archived link")
	}
	_, encoded, err := normalizeRelationship(args["relationship"])
	if err != nil {
		return nil, err
	}
	out := map[string]string{"action": "link", "relationship": encoded}
	if err := normalizeBool(args, out, "review_approved"); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeUnlinkArgs(args map[string]string) (map[string]string, error) {
	return normalizeLinkLifecycleArgs(args, "unlink")
}

func normalizeLinkLifecycleArgs(args map[string]string, action string) (map[string]string, error) {
	linkID, err := normalizeLinkID(args["id"])
	if err != nil {
		return nil, err
	}
	revision, err := normalizeExpectedRevision(args)
	if err != nil {
		return nil, err
	}
	out := map[string]string{"action": action, "id": string(linkID), "expected_revision": strconv.FormatUint(revision, 10)}
	if err := normalizeReason(args, out); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeHistoryArgs(args map[string]string) (map[string]string, error) {
	kind, objectID, err := normalizeObject(args, true)
	if err != nil {
		return nil, err
	}
	out := map[string]string{"action": "history", "object_kind": kind.String(), "id": objectID}
	if err := normalizeLimit(args, out, 50); err != nil {
		return nil, err
	}
	if err := normalizeCursor(args, out); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeRelationship(raw string) (relationshipInput, string, error) {
	var input relationshipInput
	encoded, err := decodeStrictObject(raw, "relationship", &input)
	if err != nil {
		return relationshipInput{}, "", err
	}
	if err := memory.ValidateRelationshipShape(input.Kind, input.Source, input.Target); err != nil {
		return relationshipInput{}, "", err
	}
	if len(input.EvidenceIDs) > 100 {
		return relationshipInput{}, "", errors.New("relationship.evidence_ids exceeds 100 items")
	}
	if err := validateEvidenceIDs(input.EvidenceIDs); err != nil {
		return relationshipInput{}, "", err
	}
	return input, encoded, nil
}

func normalizeLinkID(raw string) (memory.LinkID, error) {
	object := memory.ObjectRef{Kind: memory.ObjectKindLink, ID: strings.TrimSpace(raw)}
	if err := object.Validate(); err != nil {
		return "", err
	}
	return memory.LinkID(object.ID), nil
}

func callLink(ctx context.Context, service *memoryService.Service, args map[string]string) (linkMutationResult, error) {
	if args["id"] != "" {
		if _, err := service.Get(ctx, memory.ObjectRef{Kind: memory.ObjectKindLink, ID: args["id"]}); err != nil {
			return linkMutationResult{}, err
		}
		result, err := service.RestoreLink(ctx, memoryService.LinkLifecycleRequest{
			LinkID: memory.LinkID(args["id"]), ExpectedRevision: uintArg(args, "expected_revision"), Reason: args["reason"],
		})
		if err != nil {
			return linkMutationResult{}, err
		}
		return linkMutationResult{Link: result.Link, Updated: result.Updated}, nil
	}
	input, _, err := normalizeRelationship(args["relationship"])
	if err != nil {
		return linkMutationResult{}, err
	}
	for _, endpoint := range []memory.ObjectRef{input.Source, input.Target} {
		if _, err := service.Get(ctx, endpoint); err != nil {
			return linkMutationResult{}, err
		}
	}
	result, err := service.CreateLink(ctx, memoryService.CreateLinkRequest{
		Link: memory.Link{
			Source: input.Source, Target: input.Target, Kind: input.Kind,
			Label: input.Label, Notes: input.Notes, EvidenceIDs: input.EvidenceIDs,
		},
		ReviewApproved: boolArg(args, "review_approved"),
	})
	if err != nil {
		return linkMutationResult{}, err
	}
	classification := result.Classification
	return linkMutationResult{Link: result.Link, Created: true, Classification: &classification}, nil
}

func callUnlink(ctx context.Context, service *memoryService.Service, args map[string]string) (linkMutationResult, error) {
	if _, err := service.Get(ctx, memory.ObjectRef{Kind: memory.ObjectKindLink, ID: args["id"]}); err != nil {
		return linkMutationResult{}, err
	}
	result, err := service.Unlink(ctx, memoryService.LinkLifecycleRequest{
		LinkID: memory.LinkID(args["id"]), ExpectedRevision: uintArg(args, "expected_revision"), Reason: args["reason"],
	})
	if err != nil {
		return linkMutationResult{}, err
	}
	return linkMutationResult{Link: result.Link, Updated: result.Updated}, nil
}

func callHistory(ctx context.Context, service *memoryService.Service, offer memoryService.ToolOffer, args map[string]string) (historyPageResult, error) {
	kind, err := memory.ObjectKindString(args["object_kind"])
	if err != nil {
		return historyPageResult{}, err
	}
	object := memory.ObjectRef{Kind: kind, ID: args["id"]}
	if _, err := service.Get(ctx, object); err != nil {
		return historyPageResult{}, err
	}
	page, err := service.History(ctx, memoryStoreAPI.RevisionListRequest{
		Object: object, Limit: intArg(args, "limit", 10), Cursor: args["cursor"],
	})
	if err != nil {
		return historyPageResult{}, err
	}
	result := historyPageResult{Revisions: make([]historyRevisionResult, 0, len(page.Revisions)), NextCursor: page.NextCursor}
	for _, revision := range page.Revisions {
		if err := requireRecordScope(ctx, service, offer, revision); err != nil {
			return historyPageResult{}, err
		}
		result.Revisions = append(result.Revisions, adaptHistoryRevision(revision))
	}
	return result, nil
}

func adaptHistoryRevision(record memoryStoreAPI.CanonicalRecord) historyRevisionResult {
	result := historyRevisionResult{Kind: record.Kind, ID: record.ID(), Revision: record.RevisionMetadata()}
	switch record.Kind {
	case memoryStoreAPI.RecordKindChunk:
		if record.Chunk != nil {
			result.SemanticKind = record.Chunk.Kind.String()
			result.Title = record.Chunk.Title
			result.Summary = record.Chunk.Description
			scope := record.Chunk.Scope
			result.Scope = &scope
			result.State = record.Chunk.State.String()
		}
	case memoryStoreAPI.RecordKindEntry:
		if record.Entry != nil {
			result.SemanticKind = record.Entry.Kind.String()
			result.Title = record.Entry.Title
			result.Summary = record.Entry.Summary
			scope := record.Entry.Scope
			result.Scope = &scope
			result.State = record.Entry.State.String()
			verification := record.Entry.Verification
			result.Verification = &verification
			result.SupersededByID = record.Entry.SupersededByID
		}
	case memoryStoreAPI.RecordKindLink:
		if record.Link != nil {
			result.SemanticKind = record.Link.Kind.String()
			result.State = record.Link.State.String()
			source, target := record.Link.Source, record.Link.Target
			result.Source, result.Target = &source, &target
			result.RelationshipKind = record.Link.Kind
			result.Label = record.Link.Label
		}
	}
	return result
}
