package memorytool

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/lkarlslund/koder/internal/memory"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func authorizeToolScopes(ctx context.Context, service *memoryService.Service, offer memoryService.ToolOffer, args map[string]string) error {
	if allScopeKindsAllowed(offer.ScopeKinds) {
		return nil
	}
	action := args["action"]
	switch action {
	case "search", "chunk_list":
		return nil // Query adapters inject the policy scopes into their store filters.
	case "get", "history":
		kind, err := memory.ObjectKindString(args["object_kind"])
		if err != nil {
			return err
		}
		return authorizeObjectScope(ctx, service, offer, memory.ObjectRef{Kind: kind, ID: args["id"]})
	case "neighbors":
		kind, err := memory.ObjectKindString(args["object_kind"])
		if err != nil {
			return err
		}
		return authorizeObjectScope(ctx, service, offer, memory.ObjectRef{Kind: kind, ID: args["id"]})
	case "chunk_create":
		patch, _, err := normalizeChunkPatch(args["chunk"])
		if err != nil {
			return err
		}
		candidate, err := patch.createCandidate()
		if err != nil {
			return err
		}
		return requireAllowedScope(offer, candidate.Scope)
	case "chunk_update":
		record, err := authorizedRecord(ctx, service, offer, memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: args["id"]})
		if err != nil {
			return err
		}
		if record.Chunk == nil {
			return errors.New("memory chunk projection is unavailable")
		}
		patch, _, err := normalizeChunkPatch(args["chunk"])
		if err != nil {
			return err
		}
		return requireAllowedScope(offer, patch.apply(memoryService.ChunkContentFrom(*record.Chunk)).Scope)
	case "chunk_get", "chunk_archive", "chunk_restore", "chunk_delete":
		return authorizeObjectScope(ctx, service, offer, memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: args["id"]})
	case "package_export":
		return authorizeObjectScope(ctx, service, offer, memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: args["id"]})
	case "entry_create":
		parent, err := authorizedRecord(ctx, service, offer, memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: args["chunk_id"]})
		if err != nil {
			return err
		}
		if parent.Chunk == nil {
			return errors.New("memory chunk projection is unavailable")
		}
		patch, _, err := normalizeEntryPatch(args["entry"])
		if err != nil {
			return err
		}
		scope := parent.Chunk.Scope
		if patch.Scope != nil {
			scope = *patch.Scope
		}
		return requireAllowedScope(offer, scope)
	case "entry_update":
		record, err := authorizedRecord(ctx, service, offer, memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: args["id"]})
		if err != nil {
			return err
		}
		if record.Entry == nil {
			return errors.New("memory entry projection is unavailable")
		}
		patch, _, err := normalizeEntryPatch(args["entry"])
		if err != nil {
			return err
		}
		return requireAllowedScope(offer, patch.apply(memoryService.EntryContentFrom(*record.Entry)).Scope)
	case "entry_supersede":
		for _, entryID := range []string{args["id"], args["replacement_entry_id"]} {
			if err := authorizeObjectScope(ctx, service, offer, memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: entryID}); err != nil {
				return err
			}
		}
		return nil
	case "entry_archive", "entry_restore", "entry_delete", "verify":
		return authorizeObjectScope(ctx, service, offer, memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: args["id"]})
	case "link":
		if args["id"] != "" {
			return authorizeObjectScope(ctx, service, offer, memory.ObjectRef{Kind: memory.ObjectKindLink, ID: args["id"]})
		}
		relationship, _, err := normalizeRelationship(args["relationship"])
		if err != nil {
			return err
		}
		for _, endpoint := range []memory.ObjectRef{relationship.Source, relationship.Target} {
			if err := authorizeObjectScope(ctx, service, offer, endpoint); err != nil {
				return err
			}
		}
		return nil
	case "unlink":
		return authorizeObjectScope(ctx, service, offer, memory.ObjectRef{Kind: memory.ObjectKindLink, ID: args["id"]})
	default:
		return nil
	}
}

func authorizeObjectScope(ctx context.Context, service *memoryService.Service, offer memoryService.ToolOffer, object memory.ObjectRef) error {
	_, err := authorizedRecord(ctx, service, offer, object)
	return err
}

func authorizedRecord(ctx context.Context, service *memoryService.Service, offer memoryService.ToolOffer, object memory.ObjectRef) (memoryStoreAPI.CanonicalRecord, error) {
	record, err := service.Get(ctx, object)
	if err != nil {
		return memoryStoreAPI.CanonicalRecord{}, err
	}
	if err := requireRecordScope(ctx, service, offer, record); err != nil {
		return memoryStoreAPI.CanonicalRecord{}, err
	}
	return record, nil
}

func requireRecordScope(ctx context.Context, service *memoryService.Service, offer memoryService.ToolOffer, record memoryStoreAPI.CanonicalRecord) error {
	switch record.Kind {
	case memoryStoreAPI.RecordKindChunk:
		if record.Chunk != nil {
			if err := requireAllowedScope(offer, record.Chunk.Scope); err != nil {
				return err
			}
		}
	case memoryStoreAPI.RecordKindEntry:
		if record.Entry != nil {
			if err := requireAllowedScope(offer, record.Entry.Scope); err != nil {
				return err
			}
		}
	case memoryStoreAPI.RecordKindLink:
		if record.Link != nil {
			for _, endpoint := range []memory.ObjectRef{record.Link.Source, record.Link.Target} {
				if err := authorizeObjectScope(ctx, service, offer, endpoint); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func requireAllowedScope(offer memoryService.ToolOffer, scope memory.Scope) error {
	if slices.Contains(offer.ScopeKinds, scope.Kind) {
		return nil
	}
	return fmt.Errorf("%w: scope %s", memoryService.ErrToolOfferDenied, scope.Kind)
}

func allScopeKindsAllowed(values []memory.ScopeKind) bool {
	for _, supported := range supportedScopeKinds {
		if !slices.Contains(values, supported) {
			return false
		}
	}
	return true
}
