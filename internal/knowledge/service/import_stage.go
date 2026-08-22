package service

import (
	"context"
	"errors"
	"fmt"
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
	policy     ImportConflictPolicy
	remapped   []ImportIDRemap
	owner      knowledge.Actor
	createdAt  time.Time
	expiresAt  time.Time
	activating bool
}

// ImportBlockedError carries the content-free preview that must be resolved before staging.
type ImportBlockedError struct {
	Preview ImportPreview
}

func (e *ImportBlockedError) Error() string { return ErrImportBlocked.Error() }
func (e *ImportBlockedError) Unwrap() error { return ErrImportBlocked }

// StageImportRequest carries one validated package and explicit sensitive-data review.
type StageImportRequest struct {
	Package        kpackage.ValidatedPackage
	ReviewApproved bool
	ConflictPolicy ImportConflictPolicy
}

// ImportStage is an actor-owned, expiring activation handle plus its approved preview.
type ImportStage struct {
	ID             string               `json:"id"`
	Package        kpackage.Identity    `json:"package"`
	ChunkID        knowledge.ChunkID    `json:"chunk_id"`
	ConflictPolicy ImportConflictPolicy `json:"conflict_policy,omitempty"`
	Remapped       []ImportIDRemap      `json:"remapped,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
	ExpiresAt      time.Time            `json:"expires_at"`
	Preview        ImportPreview        `json:"preview"`
}

// ActivateImportResult summarizes the records made visible by one atomic activation.
type ActivateImportResult struct {
	Package        kpackage.Identity    `json:"package"`
	ChunkID        knowledge.ChunkID    `json:"chunk_id"`
	ConflictPolicy ImportConflictPolicy `json:"conflict_policy,omitempty"`
	Remapped       []ImportIDRemap      `json:"remapped,omitempty"`
	ActivatedAt    time.Time            `json:"activated_at"`
	Added          ImportImpactSummary  `json:"added"`
	Replaced       int                  `json:"replaced,omitempty"`
	KeptLocal      int                  `json:"kept_local,omitempty"`
}

type activatedRecords struct {
	chunk           *knowledge.Chunk
	assets          []knowledgeStore.PackageAsset
	entries         []knowledge.Entry
	links           []knowledge.Link
	evidence        []knowledge.Evidence
	updatedChunks   []knowledge.Chunk
	updatedEntries  []knowledge.Entry
	updatedLinks    []knowledge.Link
	updatedEvidence []knowledge.Evidence
	replaced        int
	keptLocal       int
}

// StageImport creates a short-lived in-memory stage after classification, review, policy,
// dependency, and conflict checks. Canonical storage is not modified.
func (s *Service) StageImport(ctx context.Context, request StageImportRequest) (ImportStage, error) {
	if err := request.ConflictPolicy.Validate(); err != nil {
		return ImportStage{}, err
	}
	pkg := request.Package.Clone()
	preview, err := s.PreviewImport(ctx, pkg)
	if err != nil {
		return ImportStage{Preview: preview}, err
	}
	if err := requireClassificationApproval(preview.Classification, request.ReviewApproved); err != nil {
		return ImportStage{Preview: preview}, err
	}
	var remapped []ImportIDRemap
	if preview.Summary.Conflicts != 0 && request.ConflictPolicy == ImportConflictPolicyKeepBoth {
		pkg, remapped, err = keepBothImportPackage(pkg, preview, s.newID)
		if err != nil {
			return ImportStage{Preview: preview}, err
		}
		preview, err = s.PreviewImport(ctx, pkg)
		if err != nil {
			return ImportStage{Preview: preview}, err
		}
		if err := requireClassificationApproval(preview.Classification, request.ReviewApproved); err != nil {
			return ImportStage{Preview: preview}, err
		}
	}
	preview, err = resolveImportConflictPreview(preview, request.ConflictPolicy)
	if err != nil {
		return ImportStage{Preview: preview}, err
	}
	if preview.Summary.Blockers != 0 {
		return ImportStage{Preview: preview}, &ImportBlockedError{Preview: preview}
	}
	preview.ReadyToStage = true
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
		pkg: pkg.Clone(), preview: cloneImportPreview(preview), policy: request.ConflictPolicy, remapped: slices.Clone(remapped),
		owner: owner, createdAt: now, expiresAt: now.Add(s.importStageTTL),
	}
	s.importMu.Lock()
	s.removeExpiredImportStagesLocked(now)
	if _, exists := s.importStages[stageID]; exists {
		s.importMu.Unlock()
		return ImportStage{}, fmt.Errorf("create knowledge import stage: duplicate stage ID")
	}
	s.importStages[stageID] = state
	s.importMu.Unlock()
	return ImportStage{
		ID: stageID, Package: pkg.Manifest.Package, ChunkID: knowledge.ChunkID(pkg.Manifest.Chunk.ID),
		ConflictPolicy: request.ConflictPolicy, Remapped: slices.Clone(remapped),
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
		added, err = s.activateImportTransaction(ctx, tx, owner, pkg, expected, state.policy, activatedAt)
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
		Package: pkg.Manifest.Package, ChunkID: knowledge.ChunkID(pkg.Manifest.Chunk.ID), ConflictPolicy: state.policy,
		Remapped: slices.Clone(state.remapped), ActivatedAt: activatedAt, Added: importAddedSummary(added),
		Replaced: added.replaced, KeptLocal: added.keptLocal,
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
	policy ImportConflictPolicy,
	now time.Time,
) (activatedRecords, error) {
	dependencies, err := s.availableImportDependencies(ctx, tx, owner, pkg.Manifest.Dependencies, expected)
	if err != nil {
		return activatedRecords{}, err
	}
	incomingChunk := packageChunk(pkg.Manifest)
	incomingChunk.DependencyIDs = dependencies
	chunkImpact, ok := expectedImportImpact(expected, knowledgeStore.RecordKindChunk, string(incomingChunk.ID))
	if !ok {
		return activatedRecords{}, fmt.Errorf("%w: chunk %s was not previewed", ErrImportStageStale, incomingChunk.ID)
	}
	existingChunk, err := verifyImportChunk(ctx, tx, incomingChunk, chunkImpact)
	if err != nil {
		return activatedRecords{}, err
	}
	action := ChunkPolicyCreate
	if existingChunk != nil {
		action = ChunkPolicyRead
		if policy == ImportConflictPolicyReplace && expected.Summary.Conflicts != 0 {
			action = ChunkPolicyUpdate
		}
	}
	if err := s.authorizeChunk(ctx, owner, action, incomingChunk); err != nil {
		return activatedRecords{}, err
	}

	entryPlans, err := verifyImportEntries(ctx, tx, pkg.Entries, expected)
	if err != nil {
		return activatedRecords{}, err
	}
	evidencePlans, err := verifyImportEvidence(ctx, tx, pkg.Evidence, expected)
	if err != nil {
		return activatedRecords{}, err
	}
	linkPlans, err := verifyImportLinks(ctx, tx, pkg.Links, expected)
	if err != nil {
		return activatedRecords{}, err
	}
	assetPlans, err := verifyImportAssets(ctx, tx, packageAssets(pkg), expected)
	if err != nil {
		return activatedRecords{}, err
	}
	importedEntries := make(map[knowledge.EntryID]struct{}, len(pkg.Entries))
	for _, entry := range pkg.Entries {
		importedEntries[entry.ID] = struct{}{}
	}
	for _, plan := range entryPlans {
		entryAction := ChunkPolicyEntryCreate
		if plan.existing != nil {
			if policy == ImportConflictPolicyMerge {
				continue
			}
			entryAction = ChunkPolicyEntryUpdate
			if plan.existing.State != knowledge.EntryStateActive && plan.existing.State != knowledge.EntryStateDraft {
				return activatedRecords{}, fmt.Errorf("%w: entry %s is %q", ErrEntryNotEditable, plan.existing.ID, plan.existing.State)
			}
		}
		if err := s.authorizeChunk(ctx, owner, entryAction, incomingChunk); err != nil {
			return activatedRecords{}, err
		}
	}
	for _, plan := range linkPlans {
		if plan.impact.Action == ImportImpactConflict && policy == ImportConflictPolicyMerge {
			continue
		}
		if err := s.authorizeIncomingLink(ctx, tx, owner, incomingChunk, importedEntries, plan.incoming); err != nil {
			return activatedRecords{}, err
		}
	}

	importActor := knowledge.Actor{Kind: knowledge.ActorKindImport, ID: pkg.Manifest.Package.ID}
	reason := "Imported package " + pkg.Manifest.Package.ID + "@" + pkg.Manifest.Package.Version
	added := activatedRecords{}
	evidenceRemaps := make(map[string]string)
	for _, plan := range evidencePlans {
		if plan.existing != nil && plan.impact.ExistingID != "" && plan.impact.ExistingID != string(plan.incoming.ID) {
			evidenceRemaps[string(plan.incoming.ID)] = plan.impact.ExistingID
		}
		if plan.impact.Action == ImportImpactConflict && policy == ImportConflictPolicyMerge {
			added.keptLocal++
			continue
		}
		candidate := prepareImportedEvidence(plan.incoming, importActor, now)
		if plan.existing != nil {
			candidate.ID = plan.existing.ID
		}
		if err := candidate.Validate(); err != nil {
			return activatedRecords{}, err
		}
		if plan.existing != nil {
			if err := tx.DeleteEvidence(ctx, plan.existing.ID); err != nil {
				return activatedRecords{}, err
			}
		}
		if err := tx.PutEvidence(ctx, candidate); err != nil {
			return activatedRecords{}, err
		}
		if plan.existing == nil {
			added.evidence = append(added.evidence, candidate)
		} else {
			added.updatedEvidence = append(added.updatedEvidence, candidate)
			added.replaced++
		}
	}
	switch chunkImpact.Action {
	case ImportImpactAdd:
		candidate := prepareImportedChunk(incomingChunk, importActor, knowledge.RevisionID(s.newID()), reason, now)
		if err := candidate.Validate(); err != nil {
			return activatedRecords{}, err
		}
		if err := tx.PutChunk(ctx, candidate, 0); err != nil {
			return activatedRecords{}, err
		}
		added.chunk = &candidate
	case ImportImpactConflict:
		if policy == ImportConflictPolicyMerge {
			added.keptLocal++
			break
		}
		candidate := prepareReplacedChunk(incomingChunk, *existingChunk, importActor, knowledge.RevisionID(s.newID()), reason, now)
		if err := candidate.Validate(); err != nil {
			return activatedRecords{}, err
		}
		if err := tx.PutChunk(ctx, candidate, existingChunk.Revision.Number); err != nil {
			return activatedRecords{}, err
		}
		added.updatedChunks = append(added.updatedChunks, candidate)
		added.replaced++
	}
	for _, plan := range assetPlans {
		if plan.impact.Action == ImportImpactConflict && policy == ImportConflictPolicyMerge {
			added.keptLocal++
			continue
		}
		if plan.existing != nil {
			if err := tx.DeleteAsset(ctx, plan.existing.ChunkID, plan.existing.Path); err != nil {
				return activatedRecords{}, err
			}
		}
		if err := tx.PutAsset(ctx, plan.incoming); err != nil {
			return activatedRecords{}, err
		}
		if plan.existing == nil {
			added.assets = append(added.assets, knowledgeStore.ClonePackageAsset(plan.incoming))
		} else {
			added.replaced++
		}
	}
	for _, plan := range entryPlans {
		if plan.impact.Action == ImportImpactConflict && policy == ImportConflictPolicyMerge {
			added.keptLocal++
			continue
		}
		candidate := plan.incoming
		candidate.EvidenceIDs = remapEvidenceIDs(candidate.EvidenceIDs, evidenceRemaps)
		candidate.Verification.EvidenceIDs = remapEvidenceIDs(candidate.Verification.EvidenceIDs, evidenceRemaps)
		if plan.existing == nil {
			candidate = prepareImportedEntry(candidate, importActor, knowledge.RevisionID(s.newID()), reason, now)
		} else {
			candidate = prepareReplacedEntry(candidate, *plan.existing, importActor, knowledge.RevisionID(s.newID()), reason, now)
		}
		if err := candidate.Validate(); err != nil {
			return activatedRecords{}, err
		}
		if err := validateEvidenceReferences(ctx, tx, candidate.EvidenceIDs, candidate.Verification.EvidenceIDs); err != nil {
			return activatedRecords{}, err
		}
		expectedRevision := uint64(0)
		if plan.existing != nil {
			expectedRevision = plan.existing.Revision.Number
		}
		if err := tx.PutEntry(ctx, candidate, expectedRevision); err != nil {
			return activatedRecords{}, err
		}
		if plan.existing == nil {
			added.entries = append(added.entries, candidate)
		} else {
			added.updatedEntries = append(added.updatedEntries, candidate)
			added.replaced++
		}
	}
	for _, plan := range linkPlans {
		if plan.impact.Action == ImportImpactConflict && policy == ImportConflictPolicyMerge {
			added.keptLocal++
			continue
		}
		candidate := plan.incoming
		candidate.EvidenceIDs = remapEvidenceIDs(candidate.EvidenceIDs, evidenceRemaps)
		if plan.existing == nil {
			candidate = prepareImportedLink(candidate, importActor, knowledge.RevisionID(s.newID()), reason, now)
		} else {
			candidate.ID = plan.existing.ID
			candidate = prepareReplacedLink(candidate, *plan.existing, importActor, knowledge.RevisionID(s.newID()), reason, now)
		}
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
		expectedRevision := uint64(0)
		if plan.existing != nil {
			expectedRevision = plan.existing.Revision.Number
		}
		if err := tx.PutLink(ctx, candidate, expectedRevision); err != nil {
			return activatedRecords{}, err
		}
		if plan.existing == nil {
			added.links = append(added.links, candidate)
		} else {
			added.updatedLinks = append(added.updatedLinks, candidate)
			added.replaced++
		}
	}
	return added, nil
}

func (s *Service) availableImportDependencies(ctx context.Context, tx knowledgeStore.ReadTx, owner knowledge.Actor, dependencies []kpackage.Dependency, expected ImportPreview) ([]knowledge.ChunkID, error) {
	result := make([]knowledge.ChunkID, 0, len(dependencies))
	for _, dependency := range dependencies {
		impact, ok := expectedImportImpact(expected, knowledgeStore.RecordKindChunk, dependency.ChunkID)
		expectedAction := impact.Action
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
		if !ok || expectedAction != ImportImpactReference || !importBaselineMatches(chunk, impact) {
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

func verifyImportChunk(ctx context.Context, tx knowledgeStore.ReadTx, incoming knowledge.Chunk, impact ImportImpact) (*knowledge.Chunk, error) {
	existing, err := tx.Chunk(ctx, incoming.ID)
	if errors.Is(err, knowledgeStore.ErrNotFound) && impact.Action == ImportImpactAdd {
		return nil, nil
	}
	if errors.Is(err, knowledgeStore.ErrNotFound) {
		return nil, fmt.Errorf("%w: chunk %s availability changed", ErrImportStageStale, incoming.ID)
	}
	if err != nil {
		return nil, err
	}
	if impact.Action != ImportImpactUnchanged && impact.Action != ImportImpactConflict || !importBaselineMatches(existing, impact) {
		return nil, fmt.Errorf("%w: chunk %s changed", ErrImportStageStale, incoming.ID)
	}
	return &existing, nil
}

type plannedImport[T any] struct {
	incoming T
	existing *T
	impact   ImportImpact
}

func verifyImportEntries(ctx context.Context, tx knowledgeStore.ReadTx, entries []knowledge.Entry, expected ImportPreview) ([]plannedImport[knowledge.Entry], error) {
	result := make([]plannedImport[knowledge.Entry], 0, len(entries))
	for _, incoming := range sortedEntries(entries) {
		impact, ok := expectedImportImpact(expected, knowledgeStore.RecordKindEntry, string(incoming.ID))
		if !ok {
			return nil, fmt.Errorf("%w: entry %s was not previewed", ErrImportStageStale, incoming.ID)
		}
		existing, err := tx.Entry(ctx, incoming.ID)
		if errors.Is(err, knowledgeStore.ErrNotFound) && impact.Action == ImportImpactAdd {
			result = append(result, plannedImport[knowledge.Entry]{incoming: incoming, impact: impact})
			continue
		}
		if errors.Is(err, knowledgeStore.ErrNotFound) {
			return nil, fmt.Errorf("%w: entry %s availability changed", ErrImportStageStale, incoming.ID)
		}
		if err != nil {
			return nil, err
		}
		if impact.Action != ImportImpactUnchanged && impact.Action != ImportImpactConflict || !importBaselineMatches(existing, impact) {
			return nil, fmt.Errorf("%w: entry %s changed", ErrImportStageStale, incoming.ID)
		}
		if impact.Action == ImportImpactConflict {
			current := existing
			result = append(result, plannedImport[knowledge.Entry]{incoming: incoming, existing: &current, impact: impact})
		}
	}
	return result, ctx.Err()
}

func verifyImportEvidence(ctx context.Context, tx knowledgeStore.ReadTx, evidence []knowledge.Evidence, expected ImportPreview) ([]plannedImport[knowledge.Evidence], error) {
	result := make([]plannedImport[knowledge.Evidence], 0, len(evidence))
	for _, incoming := range sortedEvidence(evidence) {
		impact, ok := expectedImportImpact(expected, knowledgeStore.RecordKindEvidence, string(incoming.ID))
		if !ok {
			return nil, fmt.Errorf("%w: evidence %s was not previewed", ErrImportStageStale, incoming.ID)
		}
		if impact.Action == ImportImpactAdd {
			if _, err := tx.Evidence(ctx, incoming.ID); err == nil {
				return nil, fmt.Errorf("%w: evidence %s availability changed", ErrImportStageStale, incoming.ID)
			} else if !errors.Is(err, knowledgeStore.ErrNotFound) {
				return nil, err
			}
			if _, err := tx.EvidenceBySource(ctx, incoming.Source.ID, incoming.Source.ContentHash); err == nil {
				return nil, fmt.Errorf("%w: evidence source now exists", ErrImportStageStale)
			} else if !errors.Is(err, knowledgeStore.ErrNotFound) {
				return nil, err
			}
			result = append(result, plannedImport[knowledge.Evidence]{incoming: incoming, impact: impact})
			continue
		}
		existingID := knowledge.EvidenceID(impact.ExistingID)
		if existingID == "" {
			existingID = incoming.ID
		}
		existing, err := tx.Evidence(ctx, existingID)
		if errors.Is(err, knowledgeStore.ErrNotFound) || err == nil && !importBaselineMatches(existing, impact) {
			return nil, fmt.Errorf("%w: evidence %s changed", ErrImportStageStale, incoming.ID)
		}
		if err != nil {
			return nil, err
		}
		if impact.Action == ImportImpactConflict {
			current := existing
			result = append(result, plannedImport[knowledge.Evidence]{incoming: incoming, existing: &current, impact: impact})
		} else if impact.Action != ImportImpactUnchanged {
			return nil, fmt.Errorf("%w: evidence %s action changed", ErrImportStageStale, incoming.ID)
		}
	}
	return result, ctx.Err()
}

func verifyImportLinks(ctx context.Context, tx knowledgeStore.ReadTx, links []knowledge.Link, expected ImportPreview) ([]plannedImport[knowledge.Link], error) {
	result := make([]plannedImport[knowledge.Link], 0, len(links))
	for _, incoming := range sortedLinks(links) {
		impact, ok := expectedImportImpact(expected, knowledgeStore.RecordKindLink, string(incoming.ID))
		if !ok {
			return nil, fmt.Errorf("%w: link %s was not previewed", ErrImportStageStale, incoming.ID)
		}
		if impact.Action == ImportImpactAdd {
			if _, err := tx.Link(ctx, incoming.ID); err == nil {
				return nil, fmt.Errorf("%w: link %s availability changed", ErrImportStageStale, incoming.ID)
			} else if !errors.Is(err, knowledgeStore.ErrNotFound) {
				return nil, err
			}
			if _, err := tx.EquivalentLink(ctx, incoming); err == nil {
				return nil, fmt.Errorf("%w: equivalent link now exists", ErrImportStageStale)
			} else if !errors.Is(err, knowledgeStore.ErrNotFound) {
				return nil, err
			}
			result = append(result, plannedImport[knowledge.Link]{incoming: incoming, impact: impact})
			continue
		}
		existingID := knowledge.LinkID(impact.ExistingID)
		if existingID == "" {
			existingID = incoming.ID
		}
		existing, err := tx.Link(ctx, existingID)
		if errors.Is(err, knowledgeStore.ErrNotFound) || err == nil && !importBaselineMatches(existing, impact) {
			return nil, fmt.Errorf("%w: link %s changed", ErrImportStageStale, incoming.ID)
		}
		if err != nil {
			return nil, err
		}
		if impact.Action == ImportImpactConflict {
			current := existing
			result = append(result, plannedImport[knowledge.Link]{incoming: incoming, existing: &current, impact: impact})
		} else if impact.Action != ImportImpactUnchanged {
			return nil, fmt.Errorf("%w: link %s action changed", ErrImportStageStale, incoming.ID)
		}
	}
	return result, ctx.Err()
}

func verifyImportAssets(ctx context.Context, tx knowledgeStore.ReadTx, assets []knowledgeStore.PackageAsset, expected ImportPreview) ([]plannedImport[knowledgeStore.PackageAsset], error) {
	result := make([]plannedImport[knowledgeStore.PackageAsset], 0, len(assets))
	for _, incoming := range assets {
		impact, ok := expectedImportImpact(expected, importImpactAssetKind, incoming.Path)
		if !ok {
			return nil, fmt.Errorf("%w: asset %s was not previewed", ErrImportStageStale, incoming.Path)
		}
		existing, err := tx.Asset(ctx, incoming.ChunkID, incoming.Path)
		if errors.Is(err, knowledgeStore.ErrNotFound) && impact.Action == ImportImpactAdd {
			result = append(result, plannedImport[knowledgeStore.PackageAsset]{incoming: knowledgeStore.ClonePackageAsset(incoming), impact: impact})
			continue
		}
		if errors.Is(err, knowledgeStore.ErrNotFound) {
			return nil, fmt.Errorf("%w: asset %s availability changed", ErrImportStageStale, incoming.Path)
		}
		if err != nil {
			return nil, err
		}
		if impact.Action != ImportImpactUnchanged && impact.Action != ImportImpactConflict || !importBaselineMatches(existing, impact) {
			return nil, fmt.Errorf("%w: asset %s changed", ErrImportStageStale, incoming.Path)
		}
		if impact.Action == ImportImpactConflict {
			current := knowledgeStore.ClonePackageAsset(existing)
			result = append(result, plannedImport[knowledgeStore.PackageAsset]{incoming: knowledgeStore.ClonePackageAsset(incoming), existing: &current, impact: impact})
		}
	}
	return result, ctx.Err()
}

func expectedImportImpact(preview ImportPreview, kind knowledgeStore.RecordKind, id string) (ImportImpact, bool) {
	for _, impact := range preview.Impacts {
		if impact.Kind == kind && impact.ID == id {
			return impact, true
		}
	}
	return ImportImpact{}, false
}

func importBaselineMatches(existing any, impact ImportImpact) bool {
	return impact.ExistingFingerprint != "" && importRecordFingerprint(existing) == impact.ExistingFingerprint &&
		importRecordRevision(existing) == impact.ExistingRevision
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

func prepareReplacedChunk(value, existing knowledge.Chunk, actor knowledge.Actor, revisionID knowledge.RevisionID, reason string, now time.Time) knowledge.Chunk {
	value.SchemaVersion = 1
	value.Revision = knowledge.Revision{Number: existing.Revision.Number + 1, ID: revisionID, Actor: actor, Reason: reason, CreatedAt: now}
	value.CreatedAt, value.UpdatedAt = existing.CreatedAt, now
	value.LastUsedAt, value.Counts = existing.LastUsedAt, existing.Counts
	return value
}

func prepareReplacedEntry(value, existing knowledge.Entry, actor knowledge.Actor, revisionID knowledge.RevisionID, reason string, now time.Time) knowledge.Entry {
	value.Revision = knowledge.Revision{Number: existing.Revision.Number + 1, ID: revisionID, Actor: actor, Reason: reason, CreatedAt: now}
	value.CreatedAt, value.UpdatedAt, value.LastUsedAt = existing.CreatedAt, now, existing.LastUsedAt
	return value
}

func prepareReplacedLink(value, existing knowledge.Link, actor knowledge.Actor, revisionID knowledge.RevisionID, reason string, now time.Time) knowledge.Link {
	value.Revision = knowledge.Revision{Number: existing.Revision.Number + 1, ID: revisionID, Actor: actor, Reason: reason, CreatedAt: now}
	value.CreatedAt, value.UpdatedAt = existing.CreatedAt, now
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
	for _, chunk := range added.updatedChunks {
		if current, err := s.Chunk(ctx, chunk.ID); err == nil {
			chunk = current
		}
		s.publishMutation(ctx, chunkMutation(MutationUpdated, chunk))
	}
	for _, evidence := range added.updatedEvidence {
		s.publishMutation(ctx, evidenceMutation(MutationUpdated, evidence))
	}
	for _, entry := range added.updatedEntries {
		s.publishMutation(ctx, entryMutation(MutationUpdated, entry))
	}
	for _, link := range added.updatedLinks {
		s.publishMutation(ctx, linkMutation(MutationUpdated, link))
	}
}
