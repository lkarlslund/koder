package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/kpackage"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var (
	ErrImportBlocked       = errors.New("knowledge package import is blocked by preview findings")
	ErrImportStageNotFound = errors.New("knowledge package import stage not found")
	ErrImportStageExpired  = errors.New("knowledge package import stage expired")
	ErrImportStageBusy     = errors.New("knowledge package import stage is already activating")
	ErrImportStageStale    = errors.New("knowledge package import stage assumptions changed")
)

type stagedImport struct {
	pkg        kpackage.ValidatedPackage
	preview    ImportPreview
	owner      knowledge.Actor
	createdAt  time.Time
	expiresAt  time.Time
	activating bool
}

// ImportBlockedError carries the content-free preview that must be resolved before
// staging. Conflict policy selection is added by KG-1109.
type ImportBlockedError struct {
	Preview ImportPreview
}

func (e *ImportBlockedError) Error() string { return ErrImportBlocked.Error() }
func (e *ImportBlockedError) Unwrap() error { return ErrImportBlocked }

// StageImportRequest carries one validated package and explicit sensitive-data review.
type StageImportRequest struct {
	Package        kpackage.ValidatedPackage
	ReviewApproved bool
}

// ImportStage is an actor-owned, expiring activation handle plus its approved preview.
type ImportStage struct {
	ID        string            `json:"id"`
	Package   kpackage.Identity `json:"package"`
	ChunkID   knowledge.ChunkID `json:"chunk_id"`
	CreatedAt time.Time         `json:"created_at"`
	ExpiresAt time.Time         `json:"expires_at"`
	Preview   ImportPreview     `json:"preview"`
}

// ActivateImportResult summarizes the records made visible by one atomic activation.
type ActivateImportResult struct {
	Package     kpackage.Identity   `json:"package"`
	ChunkID     knowledge.ChunkID   `json:"chunk_id"`
	ActivatedAt time.Time           `json:"activated_at"`
	Added       ImportImpactSummary `json:"added"`
}

type activatedRecords struct {
	chunk    *knowledge.Chunk
	assets   []knowledgeStore.PackageAsset
	entries  []knowledge.Entry
	links    []knowledge.Link
	evidence []knowledge.Evidence
}

// StageImport creates a short-lived in-memory stage after classification, review, policy,
// dependency, and conflict checks. Canonical storage is not modified.
func (s *Service) StageImport(ctx context.Context, request StageImportRequest) (ImportStage, error) {
	preview, err := s.PreviewImport(ctx, request.Package)
	if err != nil {
		return ImportStage{Preview: preview}, err
	}
	if err := requireClassificationApproval(preview.Classification, request.ReviewApproved); err != nil {
		return ImportStage{Preview: preview}, err
	}
	if preview.Summary.Blockers != 0 {
		return ImportStage{Preview: preview}, &ImportBlockedError{Preview: preview}
	}
	owner, err := s.actor(ctx)
	if err != nil {
		return ImportStage{}, fmt.Errorf("resolve knowledge import stage owner: %w", err)
	}
	if err := owner.Validate(); err != nil {
		return ImportStage{}, err
	}
	now := s.now().UTC().Round(0)
	stageID := s.newID()
	if err := (knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: stageID}).Validate(); err != nil {
		return ImportStage{}, fmt.Errorf("create knowledge import stage ID: %w", err)
	}
	state := &stagedImport{
		pkg: request.Package.Clone(), preview: cloneImportPreview(preview),
		owner: owner, createdAt: now, expiresAt: now.Add(s.importStageTTL),
	}
	preview.ReadyToStage = true
	s.importMu.Lock()
	s.removeExpiredImportStagesLocked(now)
	if _, exists := s.importStages[stageID]; exists {
		s.importMu.Unlock()
		return ImportStage{}, fmt.Errorf("create knowledge import stage: duplicate stage ID")
	}
	s.importStages[stageID] = state
	s.importMu.Unlock()
	return ImportStage{
		ID: stageID, Package: request.Package.Manifest.Package, ChunkID: knowledge.ChunkID(request.Package.Manifest.Chunk.ID),
		CreatedAt: state.createdAt, ExpiresAt: state.expiresAt, Preview: cloneImportPreview(preview),
	}, nil
}

// ActivateImport atomically publishes every staged addition. All assumptions are checked
// again inside the write transaction; any error rolls the complete import back.
func (s *Service) ActivateImport(ctx context.Context, stageID string) (ActivateImportResult, error) {
	if err := ctx.Err(); err != nil {
		return ActivateImportResult{}, err
	}
	owner, err := s.actor(ctx)
	if err != nil {
		return ActivateImportResult{}, fmt.Errorf("resolve knowledge import actor: %w", err)
	}
	if err := owner.Validate(); err != nil {
		return ActivateImportResult{}, err
	}
	state, err := s.acquireImportStage(stageID, owner)
	if err != nil {
		return ActivateImportResult{}, err
	}
	succeeded := false
	stale := false
	defer func() { s.releaseImportStage(stageID, state, succeeded, stale) }()

	pkg := state.pkg.Clone()
	expected := cloneImportPreview(state.preview)
	activatedAt := s.now().UTC().Round(0)
	var added activatedRecords
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		var err error
		added, err = s.activateImportTransaction(ctx, tx, owner, pkg, expected, activatedAt)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrImportStageStale) || errors.Is(err, knowledgeStore.ErrConflict) || errors.Is(err, knowledgeStore.ErrNotFound) {
			stale = true
			return ActivateImportResult{}, fmt.Errorf("%w: package state changed before activation", ErrImportStageStale)
		}
		return ActivateImportResult{}, fmt.Errorf("activate staged knowledge package: %w", err)
	}
	succeeded = true
	s.publishImportedMutations(ctx, added)
	return ActivateImportResult{
		Package: pkg.Manifest.Package, ChunkID: knowledge.ChunkID(pkg.Manifest.Chunk.ID), ActivatedAt: activatedAt,
		Added: importAddedSummary(added),
	}, nil
}

// DiscardImportStage removes a stage owned by the current actor without touching
// canonical storage.
func (s *Service) DiscardImportStage(ctx context.Context, stageID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	owner, err := s.actor(ctx)
	if err != nil {
		return fmt.Errorf("resolve knowledge import stage owner: %w", err)
	}
	if err := owner.Validate(); err != nil {
		return err
	}
	now := s.now().UTC().Round(0)
	s.importMu.Lock()
	defer s.importMu.Unlock()
	state, exists := s.importStages[stageID]
	if !exists || state.owner != owner {
		return ErrImportStageNotFound
	}
	if !now.Before(state.expiresAt) {
		delete(s.importStages, stageID)
		return ErrImportStageExpired
	}
	if state.activating {
		return ErrImportStageBusy
	}
	delete(s.importStages, stageID)
	return nil
}

func (s *Service) acquireImportStage(stageID string, owner knowledge.Actor) (*stagedImport, error) {
	if stageID == "" {
		return nil, ErrImportStageNotFound
	}
	now := s.now().UTC().Round(0)
	s.importMu.Lock()
	defer s.importMu.Unlock()
	state, exists := s.importStages[stageID]
	if !exists || state.owner != owner {
		return nil, ErrImportStageNotFound
	}
	if !now.Before(state.expiresAt) {
		delete(s.importStages, stageID)
		return nil, ErrImportStageExpired
	}
	if state.activating {
		return nil, ErrImportStageBusy
	}
	state.activating = true
	return state, nil
}

func (s *Service) releaseImportStage(stageID string, state *stagedImport, succeeded, stale bool) {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	current, exists := s.importStages[stageID]
	if !exists || current != state {
		return
	}
	if succeeded || stale {
		delete(s.importStages, stageID)
		return
	}
	state.activating = false
}

func (s *Service) removeExpiredImportStagesLocked(now time.Time) {
	for id, state := range s.importStages {
		if !state.activating && !now.Before(state.expiresAt) {
			delete(s.importStages, id)
		}
	}
}

func (s *Service) activateImportTransaction(
	ctx context.Context,
	tx knowledgeStore.WriteTx,
	owner knowledge.Actor,
	pkg kpackage.ValidatedPackage,
	expected ImportPreview,
	now time.Time,
) (activatedRecords, error) {
	dependencies, err := s.availableImportDependencies(ctx, tx, owner, pkg.Manifest.Dependencies, expected)
	if err != nil {
		return activatedRecords{}, err
	}
	incomingChunk := packageChunk(pkg.Manifest)
	incomingChunk.DependencyIDs = dependencies
	chunkExists, err := verifyImportChunk(ctx, tx, incomingChunk, expectedImportAction(expected, knowledgeStore.RecordKindChunk, string(incomingChunk.ID)))
	if err != nil {
		return activatedRecords{}, err
	}
	action := ChunkPolicyCreate
	if chunkExists {
		action = ChunkPolicyUpdate
	}
	if err := s.authorizeChunk(ctx, owner, action, incomingChunk); err != nil {
		return activatedRecords{}, err
	}

	entriesToAdd, err := verifyImportEntries(ctx, tx, pkg.Entries, expected)
	if err != nil {
		return activatedRecords{}, err
	}
	evidenceToAdd, err := verifyImportEvidence(ctx, tx, pkg.Evidence, expected)
	if err != nil {
		return activatedRecords{}, err
	}
	linksToAdd, err := verifyImportLinks(ctx, tx, pkg.Links, expected)
	if err != nil {
		return activatedRecords{}, err
	}
	assetsToAdd, err := verifyImportAssets(ctx, tx, packageAssets(pkg), expected)
	if err != nil {
		return activatedRecords{}, err
	}
	importedEntries := make(map[knowledge.EntryID]struct{}, len(pkg.Entries))
	for _, entry := range pkg.Entries {
		importedEntries[entry.ID] = struct{}{}
	}
	for _, link := range linksToAdd {
		if err := s.authorizeIncomingLink(ctx, tx, owner, incomingChunk, importedEntries, link); err != nil {
			return activatedRecords{}, err
		}
	}

	importActor := knowledge.Actor{Kind: knowledge.ActorKindImport, ID: pkg.Manifest.Package.ID}
	reason := "Imported package " + pkg.Manifest.Package.ID + "@" + pkg.Manifest.Package.Version
	added := activatedRecords{}
	for _, evidence := range evidenceToAdd {
		candidate := prepareImportedEvidence(evidence, importActor, now)
		if err := candidate.Validate(); err != nil {
			return activatedRecords{}, err
		}
		if err := tx.PutEvidence(ctx, candidate); err != nil {
			return activatedRecords{}, err
		}
		added.evidence = append(added.evidence, candidate)
	}
	if !chunkExists {
		candidate := prepareImportedChunk(incomingChunk, importActor, knowledge.RevisionID(s.newID()), reason, now)
		if err := candidate.Validate(); err != nil {
			return activatedRecords{}, err
		}
		if err := tx.PutChunk(ctx, candidate, 0); err != nil {
			return activatedRecords{}, err
		}
		added.chunk = &candidate
	}
	for _, asset := range assetsToAdd {
		if err := tx.PutAsset(ctx, asset); err != nil {
			return activatedRecords{}, err
		}
		added.assets = append(added.assets, knowledgeStore.ClonePackageAsset(asset))
	}
	for _, entry := range entriesToAdd {
		candidate := prepareImportedEntry(entry, importActor, knowledge.RevisionID(s.newID()), reason, now)
		if err := candidate.Validate(); err != nil {
			return activatedRecords{}, err
		}
		if err := validateEvidenceReferences(ctx, tx, candidate.EvidenceIDs, candidate.Verification.EvidenceIDs); err != nil {
			return activatedRecords{}, err
		}
		if err := tx.PutEntry(ctx, candidate, 0); err != nil {
			return activatedRecords{}, err
		}
		added.entries = append(added.entries, candidate)
	}
	for _, link := range linksToAdd {
		candidate := prepareImportedLink(link, importActor, knowledge.RevisionID(s.newID()), reason, now)
		if _, err := resolveLinkEndpoint(ctx, tx, candidate.Source); err != nil {
			return activatedRecords{}, err
		}
		if _, err := resolveLinkEndpoint(ctx, tx, candidate.Target); err != nil {
			return activatedRecords{}, err
		}
		if err := validateEvidenceReferences(ctx, tx, candidate.EvidenceIDs); err != nil {
			return activatedRecords{}, err
		}
		if err := candidate.Validate(); err != nil {
			return activatedRecords{}, err
		}
		if err := tx.PutLink(ctx, candidate, 0); err != nil {
			return activatedRecords{}, err
		}
		added.links = append(added.links, candidate)
	}
	return added, nil
}

func (s *Service) availableImportDependencies(ctx context.Context, tx knowledgeStore.ReadTx, owner knowledge.Actor, dependencies []kpackage.Dependency, expected ImportPreview) ([]knowledge.ChunkID, error) {
	result := make([]knowledge.ChunkID, 0, len(dependencies))
	for _, dependency := range dependencies {
		expectedAction := expectedImportAction(expected, knowledgeStore.RecordKindChunk, dependency.ChunkID)
		chunk, err := tx.Chunk(ctx, knowledge.ChunkID(dependency.ChunkID))
		if errors.Is(err, knowledgeStore.ErrNotFound) && !dependency.Required && expectedAction == ImportImpactMissingDependency {
			continue
		}
		if errors.Is(err, knowledgeStore.ErrNotFound) {
			return nil, fmt.Errorf("%w: dependency %s is unavailable", ErrImportStageStale, dependency.ChunkID)
		}
		if err != nil {
			return nil, err
		}
		if expectedAction != ImportImpactReference {
			return nil, fmt.Errorf("%w: dependency %s availability changed", ErrImportStageStale, dependency.ChunkID)
		}
		if chunk.State != knowledge.ChunkStateActive {
			return nil, fmt.Errorf("%w: dependency %s is inactive", ErrImportStageStale, dependency.ChunkID)
		}
		if err := s.authorizeChunk(ctx, owner, ChunkPolicyRead, chunk); err != nil {
			return nil, err
		}
		result = append(result, chunk.ID)
	}
	slices.Sort(result)
	return result, nil
}

func verifyImportChunk(ctx context.Context, tx knowledgeStore.ReadTx, incoming knowledge.Chunk, expected ImportImpactAction) (bool, error) {
	existing, err := tx.Chunk(ctx, incoming.ID)
	if errors.Is(err, knowledgeStore.ErrNotFound) && expected == ImportImpactAdd {
		return false, nil
	}
	if errors.Is(err, knowledgeStore.ErrNotFound) {
		return false, fmt.Errorf("%w: chunk %s availability changed", ErrImportStageStale, incoming.ID)
	}
	if err != nil {
		return false, err
	}
	if expected != ImportImpactUnchanged || !packageChunkContentEqual(existing, incoming) {
		return false, fmt.Errorf("%w: chunk %s changed", ErrImportStageStale, incoming.ID)
	}
	return true, nil
}

func verifyImportEntries(ctx context.Context, tx knowledgeStore.ReadTx, entries []knowledge.Entry, expected ImportPreview) ([]knowledge.Entry, error) {
	result := make([]knowledge.Entry, 0, len(entries))
	for _, incoming := range sortedEntries(entries) {
		expectedAction := expectedImportAction(expected, knowledgeStore.RecordKindEntry, string(incoming.ID))
		existing, err := tx.Entry(ctx, incoming.ID)
		if errors.Is(err, knowledgeStore.ErrNotFound) && expectedAction == ImportImpactAdd {
			result = append(result, incoming)
			continue
		}
		if errors.Is(err, knowledgeStore.ErrNotFound) {
			return nil, fmt.Errorf("%w: entry %s availability changed", ErrImportStageStale, incoming.ID)
		}
		if err != nil {
			return nil, err
		}
		if expectedAction != ImportImpactUnchanged || !reflect.DeepEqual(existing, incoming) {
			return nil, fmt.Errorf("%w: entry %s changed", ErrImportStageStale, incoming.ID)
		}
	}
	return result, ctx.Err()
}

func verifyImportEvidence(ctx context.Context, tx knowledgeStore.ReadTx, evidence []knowledge.Evidence, expected ImportPreview) ([]knowledge.Evidence, error) {
	result := make([]knowledge.Evidence, 0, len(evidence))
	for _, incoming := range sortedEvidence(evidence) {
		expectedAction := expectedImportAction(expected, knowledgeStore.RecordKindEvidence, string(incoming.ID))
		existing, err := tx.Evidence(ctx, incoming.ID)
		if err == nil {
			if expectedAction != ImportImpactUnchanged || !reflect.DeepEqual(existing, incoming) {
				return nil, fmt.Errorf("%w: evidence %s changed", ErrImportStageStale, incoming.ID)
			}
			continue
		}
		if !errors.Is(err, knowledgeStore.ErrNotFound) {
			return nil, err
		}
		if expectedAction != ImportImpactAdd {
			return nil, fmt.Errorf("%w: evidence %s availability changed", ErrImportStageStale, incoming.ID)
		}
		if _, err := tx.EvidenceBySource(ctx, incoming.Source.ID, incoming.Source.ContentHash); err == nil {
			return nil, fmt.Errorf("%w: evidence source now exists", ErrImportStageStale)
		} else if !errors.Is(err, knowledgeStore.ErrNotFound) {
			return nil, err
		}
		result = append(result, incoming)
	}
	return result, ctx.Err()
}

func verifyImportLinks(ctx context.Context, tx knowledgeStore.ReadTx, links []knowledge.Link, expected ImportPreview) ([]knowledge.Link, error) {
	result := make([]knowledge.Link, 0, len(links))
	for _, incoming := range sortedLinks(links) {
		expectedAction := expectedImportAction(expected, knowledgeStore.RecordKindLink, string(incoming.ID))
		existing, err := tx.Link(ctx, incoming.ID)
		if err == nil {
			if expectedAction != ImportImpactUnchanged || !reflect.DeepEqual(existing, incoming) {
				return nil, fmt.Errorf("%w: link %s changed", ErrImportStageStale, incoming.ID)
			}
			continue
		}
		if !errors.Is(err, knowledgeStore.ErrNotFound) {
			return nil, err
		}
		if expectedAction != ImportImpactAdd {
			return nil, fmt.Errorf("%w: link %s availability changed", ErrImportStageStale, incoming.ID)
		}
		if _, err := tx.EquivalentLink(ctx, incoming); err == nil {
			return nil, fmt.Errorf("%w: equivalent link now exists", ErrImportStageStale)
		} else if !errors.Is(err, knowledgeStore.ErrNotFound) {
			return nil, err
		}
		result = append(result, incoming)
	}
	return result, ctx.Err()
}

func verifyImportAssets(ctx context.Context, tx knowledgeStore.ReadTx, assets []knowledgeStore.PackageAsset, expected ImportPreview) ([]knowledgeStore.PackageAsset, error) {
	result := make([]knowledgeStore.PackageAsset, 0, len(assets))
	for _, incoming := range assets {
		expectedAction := expectedImportAction(expected, importImpactAssetKind, incoming.Path)
		existing, err := tx.Asset(ctx, incoming.ChunkID, incoming.Path)
		if errors.Is(err, knowledgeStore.ErrNotFound) && expectedAction == ImportImpactAdd {
			result = append(result, knowledgeStore.ClonePackageAsset(incoming))
			continue
		}
		if errors.Is(err, knowledgeStore.ErrNotFound) {
			return nil, fmt.Errorf("%w: asset %s availability changed", ErrImportStageStale, incoming.Path)
		}
		if err != nil {
			return nil, err
		}
		if expectedAction != ImportImpactUnchanged || !reflect.DeepEqual(existing, incoming) {
			return nil, fmt.Errorf("%w: asset %s changed", ErrImportStageStale, incoming.Path)
		}
	}
	return result, ctx.Err()
}

func expectedImportAction(preview ImportPreview, kind knowledgeStore.RecordKind, id string) ImportImpactAction {
	for _, impact := range preview.Impacts {
		if impact.Kind == kind && impact.ID == id {
			return impact.Action
		}
	}
	return ""
}

func cloneImportPreview(value ImportPreview) ImportPreview {
	value.Classification.Findings = slices.Clone(value.Classification.Findings)
	value.Impacts = slices.Clone(value.Impacts)
	return value
}

func prepareImportedChunk(value knowledge.Chunk, actor knowledge.Actor, revisionID knowledge.RevisionID, reason string, now time.Time) knowledge.Chunk {
	value.SchemaVersion = 1
	value.Revision = knowledge.Revision{Number: 1, ID: revisionID, Actor: actor, Reason: reason, CreatedAt: now}
	value.CreatedAt, value.UpdatedAt, value.LastUsedAt = now, now, time.Time{}
	value.LastVerifiedAt = time.Time{}
	value.Counts = knowledge.ChunkCounts{}
	return value
}

func prepareImportedEntry(value knowledge.Entry, actor knowledge.Actor, revisionID knowledge.RevisionID, reason string, now time.Time) knowledge.Entry {
	value.Revision = knowledge.Revision{Number: 1, ID: revisionID, Actor: actor, Reason: reason, CreatedAt: now}
	value.CreatedAt, value.UpdatedAt, value.LastUsedAt = now, now, time.Time{}
	return value
}

func prepareImportedLink(value knowledge.Link, actor knowledge.Actor, revisionID knowledge.RevisionID, reason string, now time.Time) knowledge.Link {
	value.Revision = knowledge.Revision{Number: 1, ID: revisionID, Actor: actor, Reason: reason, CreatedAt: now}
	value.CreatedAt, value.UpdatedAt = now, now
	return value
}

func prepareImportedEvidence(value knowledge.Evidence, actor knowledge.Actor, now time.Time) knowledge.Evidence {
	value.Actor, value.CreatedAt = actor, now
	return value
}

func importAddedSummary(added activatedRecords) ImportImpactSummary {
	result := ImportImpactSummary{Additions: len(added.assets) + len(added.entries) + len(added.links) + len(added.evidence)}
	if added.chunk != nil {
		result.Additions++
	}
	return result
}

func (s *Service) publishImportedMutations(ctx context.Context, added activatedRecords) {
	if added.chunk != nil {
		chunk := *added.chunk
		if current, err := s.Chunk(ctx, chunk.ID); err == nil {
			chunk = current
		}
		s.publishMutation(ctx, chunkMutation(MutationCreated, chunk))
	}
	for _, evidence := range added.evidence {
		s.publishMutation(ctx, evidenceMutation(MutationCreated, evidence))
	}
	for _, entry := range added.entries {
		s.publishMutation(ctx, entryMutation(MutationCreated, entry))
	}
	for _, link := range added.links {
		s.publishMutation(ctx, linkMutation(MutationCreated, link))
	}
}
