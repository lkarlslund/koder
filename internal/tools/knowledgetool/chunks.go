package knowledgetool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type chunkPatch struct {
	Title           *string                   `json:"title,omitempty"`
	Description     *string                   `json:"description,omitempty"`
	Aliases         *[]string                 `json:"aliases,omitempty"`
	Tags            *[]string                 `json:"tags,omitempty"`
	Kind            *knowledge.ChunkKind      `json:"kind,omitempty"`
	Scope           *knowledge.Scope          `json:"scope,omitempty"`
	Visibility      *knowledge.Visibility     `json:"visibility,omitempty"`
	SharedWith      *[]knowledge.PrincipalRef `json:"shared_with,omitempty"`
	Language        *string                   `json:"language,omitempty"`
	Locale          *string                   `json:"locale,omitempty"`
	Domain          *string                   `json:"domain,omitempty"`
	Risk            *[]knowledge.RiskClass    `json:"risk,omitempty"`
	Publisher       *knowledge.Publisher      `json:"publisher,omitempty"`
	License         *string                   `json:"license,omitempty"`
	SourcePolicy    *string                   `json:"source_policy,omitempty"`
	DependencyIDs   *[]knowledge.ChunkID      `json:"dependency_ids,omitempty"`
	MinKoderVersion *string                   `json:"min_koder_version,omitempty"`
	ReviewAfter     *time.Time                `json:"review_after,omitempty"`
}

type chunkPageResult struct {
	Chunks     []knowledge.Chunk `json:"chunks"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type chunkMutationResult struct {
	Chunk          knowledge.Chunk                 `json:"chunk"`
	Created        bool                            `json:"created,omitempty"`
	Updated        bool                            `json:"updated,omitempty"`
	Classification *knowledge.ClassificationResult `json:"classification,omitempty"`
}

type chunkDeleteResult struct {
	ID                       knowledge.ChunkID      `json:"id"`
	Deleted                  bool                   `json:"deleted"`
	Cascade                  bool                   `json:"cascade"`
	DeletedEntryIDs          []knowledge.EntryID    `json:"deleted_entry_ids,omitempty"`
	DeletedLinkIDs           []knowledge.LinkID     `json:"deleted_link_ids,omitempty"`
	DeletedEvidenceIDs       []knowledge.EvidenceID `json:"deleted_evidence_ids,omitempty"`
	UpdatedDependentChunkIDs []knowledge.ChunkID    `json:"updated_dependent_chunk_ids,omitempty"`
}

func normalizeChunkListArgs(args map[string]string) (map[string]string, error) {
	out := map[string]string{"action": "chunk_list"}
	for _, filter := range []struct {
		name    string
		maximum int
		valid   func(string) bool
	}{
		{name: "kinds", maximum: 5, valid: validChunkKind},
		{name: "states", maximum: 3, valid: validChunkState},
		{name: "scope_kinds", maximum: 5, valid: validScopeKind},
	} {
		if err := normalizeNamedList(args, out, filter.name, filter.maximum, filter.valid); err != nil {
			return nil, err
		}
	}
	if raw := strings.TrimSpace(args["tags"]); raw != "" {
		values, err := decodeStringList(raw, "tags", 25)
		if err != nil {
			return nil, err
		}
		encoded, err := encodeStringList(values)
		if err != nil {
			return nil, err
		}
		out["tags"] = encoded
	}
	if locale := strings.TrimSpace(args["locale"]); locale != "" {
		out["locale"] = locale
	}
	sortField := strings.TrimSpace(args["sort"])
	switch knowledgeStore.ChunkSort(sortField) {
	case "":
	case knowledgeStore.ChunkSortTitle, knowledgeStore.ChunkSortCreatedAt, knowledgeStore.ChunkSortUpdatedAt, knowledgeStore.ChunkSortLastUsedAt:
		out["sort"] = sortField
	default:
		return nil, fmt.Errorf("invalid chunk sort %q", sortField)
	}
	if err := normalizeBool(args, out, "descending"); err != nil {
		return nil, err
	}
	if _, explicit := out["descending"]; explicit && sortField == "" {
		out["sort"] = string(knowledgeStore.ChunkSortUpdatedAt)
	}
	if err := normalizeLimit(args, out, 200); err != nil {
		return nil, err
	}
	if err := normalizeCursor(args, out); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeChunkGetArgs(args map[string]string) (map[string]string, error) {
	chunkID, err := normalizeChunkID(args["id"])
	if err != nil {
		return nil, err
	}
	return map[string]string{"action": "chunk_get", "id": string(chunkID)}, nil
}

func normalizeChunkCreateArgs(args map[string]string) (map[string]string, error) {
	patch, encoded, err := normalizeChunkPatch(args["chunk"])
	if err != nil {
		return nil, err
	}
	if _, err := patch.createCandidate(); err != nil {
		return nil, err
	}
	out := map[string]string{"action": "chunk_create", "chunk": encoded}
	if err := normalizeBool(args, out, "review_approved"); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeChunkUpdateArgs(args map[string]string) (map[string]string, error) {
	chunkID, err := normalizeChunkID(args["id"])
	if err != nil {
		return nil, err
	}
	revision, err := normalizeExpectedRevision(args)
	if err != nil {
		return nil, err
	}
	patch, encoded, err := normalizeChunkPatch(args["chunk"])
	if err != nil {
		return nil, err
	}
	if !patch.hasChanges() {
		return nil, errors.New("chunk_update requires at least one editable chunk field")
	}
	out := map[string]string{
		"action": "chunk_update", "id": string(chunkID),
		"expected_revision": strconv.FormatUint(revision, 10), "chunk": encoded,
	}
	if err := normalizeReason(args, out); err != nil {
		return nil, err
	}
	if err := normalizeBool(args, out, "review_approved"); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeChunkLifecycleArgs(args map[string]string, action string) (map[string]string, error) {
	chunkID, err := normalizeChunkID(args["id"])
	if err != nil {
		return nil, err
	}
	revision, err := normalizeExpectedRevision(args)
	if err != nil {
		return nil, err
	}
	out := map[string]string{"action": action, "id": string(chunkID), "expected_revision": strconv.FormatUint(revision, 10)}
	if err := normalizeReason(args, out); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeChunkDeleteArgs(args map[string]string) (map[string]string, error) {
	out, err := normalizeChunkLifecycleArgs(args, "chunk_delete")
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"confirmed", "cascade"} {
		if err := normalizeBool(args, out, name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func normalizeChunkPatch(raw string) (chunkPatch, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return chunkPatch{}, "", errors.New("chunk object is required")
	}
	var patch chunkPatch
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil {
		return chunkPatch{}, "", fmt.Errorf("decode chunk object: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return chunkPatch{}, "", errors.New("chunk object contains multiple JSON values")
		}
		return chunkPatch{}, "", fmt.Errorf("decode chunk object: %w", err)
	}
	data, err := json.Marshal(patch)
	if err != nil {
		return chunkPatch{}, "", fmt.Errorf("encode chunk object: %w", err)
	}
	return patch, string(data), nil
}

func (p chunkPatch) createCandidate() (knowledge.Chunk, error) {
	if p.Title == nil || p.Kind == nil || p.Scope == nil {
		return knowledge.Chunk{}, errors.New("chunk_create requires chunk.title, chunk.kind, and chunk.scope")
	}
	content := p.apply(knowledgeService.ChunkContent{})
	return chunkFromContent(content), nil
}

func (p chunkPatch) apply(content knowledgeService.ChunkContent) knowledgeService.ChunkContent {
	if p.Title != nil {
		content.Title = *p.Title
	}
	if p.Description != nil {
		content.Description = *p.Description
	}
	if p.Aliases != nil {
		content.Aliases = slices.Clone(*p.Aliases)
	}
	if p.Tags != nil {
		content.Tags = slices.Clone(*p.Tags)
	}
	if p.Kind != nil {
		content.Kind = *p.Kind
	}
	if p.Scope != nil {
		content.Scope = *p.Scope
	}
	if p.Visibility != nil {
		content.Visibility = *p.Visibility
	}
	if p.SharedWith != nil {
		content.SharedWith = slices.Clone(*p.SharedWith)
	}
	if p.Language != nil {
		content.Language = *p.Language
	}
	if p.Locale != nil {
		content.Locale = *p.Locale
	}
	if p.Domain != nil {
		content.Domain = *p.Domain
	}
	if p.Risk != nil {
		content.Risk = slices.Clone(*p.Risk)
	}
	if p.Publisher != nil {
		content.Publisher = *p.Publisher
	}
	if p.License != nil {
		content.License = *p.License
	}
	if p.SourcePolicy != nil {
		content.SourcePolicy = *p.SourcePolicy
	}
	if p.DependencyIDs != nil {
		content.DependencyIDs = slices.Clone(*p.DependencyIDs)
	}
	if p.MinKoderVersion != nil {
		content.MinKoderVersion = *p.MinKoderVersion
	}
	if p.ReviewAfter != nil {
		content.ReviewAfter = *p.ReviewAfter
	}
	return content
}

func (p chunkPatch) hasChanges() bool {
	return p.Title != nil || p.Description != nil || p.Aliases != nil || p.Tags != nil || p.Kind != nil || p.Scope != nil ||
		p.Visibility != nil || p.SharedWith != nil || p.Language != nil || p.Locale != nil || p.Domain != nil || p.Risk != nil ||
		p.Publisher != nil || p.License != nil || p.SourcePolicy != nil || p.DependencyIDs != nil || p.MinKoderVersion != nil || p.ReviewAfter != nil
}

func chunkFromContent(content knowledgeService.ChunkContent) knowledge.Chunk {
	return knowledge.Chunk{
		Title: content.Title, Description: content.Description, Aliases: content.Aliases, Tags: content.Tags,
		Kind: content.Kind, Scope: content.Scope, Visibility: content.Visibility, SharedWith: content.SharedWith,
		Language: content.Language, Locale: content.Locale, Domain: content.Domain, Risk: content.Risk,
		Publisher: content.Publisher, License: content.License, SourcePolicy: content.SourcePolicy,
		DependencyIDs: content.DependencyIDs, MinKoderVersion: content.MinKoderVersion, ReviewAfter: content.ReviewAfter,
	}
}

func normalizeChunkID(raw string) (knowledge.ChunkID, error) {
	object := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: strings.TrimSpace(raw)}
	if err := object.Validate(); err != nil {
		return "", err
	}
	return knowledge.ChunkID(object.ID), nil
}

func normalizeExpectedRevision(args map[string]string) (uint64, error) {
	revision, err := strconv.ParseUint(strings.TrimSpace(args["expected_revision"]), 10, 64)
	if err != nil || revision == 0 {
		return 0, errors.New("expected_revision must be a positive integer")
	}
	return revision, nil
}

func normalizeReason(args, out map[string]string) error {
	reason := strings.TrimSpace(args["reason"])
	if len(reason) > 4<<10 {
		return errors.New("reason exceeds 4 KiB")
	}
	if reason != "" {
		out["reason"] = reason
	}
	return nil
}

func normalizeNamedList(args, out map[string]string, name string, maximum int, valid func(string) bool) error {
	raw := strings.TrimSpace(args[name])
	if raw == "" {
		return nil
	}
	values, err := decodeStringList(raw, name, maximum)
	if err != nil {
		return err
	}
	for _, value := range values {
		if !valid(value) {
			return fmt.Errorf("invalid %s value %q", name, value)
		}
	}
	encoded, err := encodeStringList(values)
	if err != nil {
		return err
	}
	out[name] = encoded
	return nil
}

func validChunkKind(value string) bool {
	kind, err := knowledge.ChunkKindString(value)
	return err == nil && kind != knowledge.ChunkKindUnspecified
}

func validChunkState(value string) bool {
	state, err := knowledge.ChunkStateString(value)
	return err == nil && state != knowledge.ChunkStateUnspecified
}

func validScopeKind(value string) bool {
	scope, err := knowledge.ScopeKindString(value)
	return err == nil && scope != knowledge.ScopeKindUnspecified
}

func callChunkList(ctx context.Context, service *knowledgeService.Service, offer knowledgeService.ToolOffer, args map[string]string) (chunkPageResult, error) {
	request := knowledgeStore.ChunkListRequest{
		Sort: knowledgeStore.ChunkSort(args["sort"]), Descending: boolArg(args, "descending"),
		Limit: intArg(args, "limit", 50), Cursor: args["cursor"],
	}
	if err := decodeChunkListFilters(args, &request.Filter); err != nil {
		return chunkPageResult{}, err
	}
	if len(request.Filter.ScopeKinds) == 0 {
		request.Filter.ScopeKinds = slices.Clone(offer.ScopeKinds)
	} else {
		for _, scopeKind := range request.Filter.ScopeKinds {
			if !slices.Contains(offer.ScopeKinds, scopeKind) {
				return chunkPageResult{}, fmt.Errorf("%w: scope %s", knowledgeService.ErrToolOfferDenied, scopeKind)
			}
		}
	}
	page, err := service.ListChunks(ctx, request)
	if err != nil {
		return chunkPageResult{}, err
	}
	return chunkPageResult{Chunks: page.Chunks, NextCursor: page.NextCursor}, nil
}

func decodeChunkListFilters(args map[string]string, filter *knowledgeStore.ChunkFilter) error {
	if err := decodeEnumList(args["kinds"], func(value string) error {
		kind, err := knowledge.ChunkKindString(value)
		if err == nil {
			filter.Kinds = append(filter.Kinds, kind)
		}
		return err
	}); err != nil {
		return err
	}
	if err := decodeEnumList(args["states"], func(value string) error {
		state, err := knowledge.ChunkStateString(value)
		if err == nil {
			filter.States = append(filter.States, state)
		}
		return err
	}); err != nil {
		return err
	}
	if err := decodeEnumList(args["scope_kinds"], func(value string) error {
		scope, err := knowledge.ScopeKindString(value)
		if err == nil {
			filter.ScopeKinds = append(filter.ScopeKinds, scope)
		}
		return err
	}); err != nil {
		return err
	}
	if args["tags"] != "" {
		if err := json.Unmarshal([]byte(args["tags"]), &filter.Tags); err != nil {
			return fmt.Errorf("decode normalized tags: %w", err)
		}
	}
	filter.Locale = args["locale"]
	return nil
}

func decodeEnumList(raw string, appendValue func(string) error) error {
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return err
	}
	for _, value := range values {
		if err := appendValue(value); err != nil {
			return err
		}
	}
	return nil
}

func callChunkGet(ctx context.Context, service *knowledgeService.Service, args map[string]string) (recordResult, error) {
	return callGet(ctx, service, map[string]string{"object_kind": knowledge.ObjectKindChunk.String(), "id": args["id"]})
}

func callChunkCreate(ctx context.Context, service *knowledgeService.Service, args map[string]string) (chunkMutationResult, error) {
	patch, _, err := normalizeChunkPatch(args["chunk"])
	if err != nil {
		return chunkMutationResult{}, err
	}
	candidate, err := patch.createCandidate()
	if err != nil {
		return chunkMutationResult{}, err
	}
	result, err := service.CreateChunk(ctx, knowledgeService.CreateChunkRequest{
		Chunk: candidate, ReviewApproved: boolArg(args, "review_approved"),
	})
	if err != nil {
		return chunkMutationResult{}, err
	}
	classification := result.Classification
	return chunkMutationResult{Chunk: result.Chunk, Created: true, Classification: &classification}, nil
}

func callChunkUpdate(ctx context.Context, service *knowledgeService.Service, args map[string]string) (chunkMutationResult, error) {
	currentRecord, err := service.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: args["id"]})
	if err != nil {
		return chunkMutationResult{}, err
	}
	if currentRecord.Chunk == nil {
		return chunkMutationResult{}, errors.New("knowledge chunk projection is unavailable")
	}
	patch, _, err := normalizeChunkPatch(args["chunk"])
	if err != nil {
		return chunkMutationResult{}, err
	}
	result, err := service.UpdateChunk(ctx, knowledgeService.UpdateChunkRequest{
		ChunkID: knowledge.ChunkID(args["id"]), ExpectedRevision: uintArg(args, "expected_revision"),
		Content: patch.apply(knowledgeService.ChunkContentFrom(*currentRecord.Chunk)), Reason: args["reason"],
		ReviewApproved: boolArg(args, "review_approved"),
	})
	if err != nil {
		return chunkMutationResult{}, err
	}
	classification := result.Classification
	return chunkMutationResult{Chunk: result.Chunk, Updated: result.Updated, Classification: &classification}, nil
}

func callChunkLifecycle(ctx context.Context, service *knowledgeService.Service, args map[string]string) (chunkMutationResult, error) {
	if _, err := service.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: args["id"]}); err != nil {
		return chunkMutationResult{}, err
	}
	request := knowledgeService.ChunkLifecycleRequest{
		ChunkID: knowledge.ChunkID(args["id"]), ExpectedRevision: uintArg(args, "expected_revision"), Reason: args["reason"],
	}
	var result knowledgeService.ChunkLifecycleResult
	var err error
	if args["action"] == "chunk_archive" {
		result, err = service.ArchiveChunk(ctx, request)
	} else {
		result, err = service.RestoreChunk(ctx, request)
	}
	if err != nil {
		return chunkMutationResult{}, err
	}
	return chunkMutationResult{Chunk: result.Chunk, Updated: result.Updated}, nil
}

func callChunkDelete(ctx context.Context, service *knowledgeService.Service, args map[string]string) (chunkDeleteResult, error) {
	if _, err := service.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: args["id"]}); err != nil {
		return chunkDeleteResult{}, err
	}
	request := knowledgeService.DeleteChunkRequest{
		ChunkID: knowledge.ChunkID(args["id"]), ExpectedRevision: uintArg(args, "expected_revision"),
		Confirmed: boolArg(args, "confirmed"),
	}
	result := chunkDeleteResult{ID: request.ChunkID, Cascade: boolArg(args, "cascade")}
	if !result.Cascade {
		if err := service.DeleteChunk(ctx, request); err != nil {
			return chunkDeleteResult{}, err
		}
		result.Deleted = true
		return result, nil
	}
	cascade, err := service.CascadeDeleteChunk(ctx, request)
	if err != nil {
		return chunkDeleteResult{}, err
	}
	result.Deleted = true
	result.DeletedEntryIDs = cascade.DeletedEntryIDs
	result.DeletedLinkIDs = cascade.DeletedLinkIDs
	result.DeletedEvidenceIDs = cascade.DeletedEvidenceIDs
	result.UpdatedDependentChunkIDs = cascade.UpdatedDependentChunkIDs
	return result, nil
}

func uintArg(args map[string]string, name string) uint64 {
	value, err := strconv.ParseUint(args[name], 10, 64)
	if err != nil {
		return 0
	}
	return value
}
