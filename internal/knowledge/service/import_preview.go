package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/kpackage"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

// ImportImpactAction describes one read-only conclusion about a package object.
type ImportImpactAction string

const (
	ImportImpactAdd               ImportImpactAction = "add"
	ImportImpactUnchanged         ImportImpactAction = "unchanged"
	ImportImpactConflict          ImportImpactAction = "conflict"
	ImportImpactReference         ImportImpactAction = "reference"
	ImportImpactMissingDependency ImportImpactAction = "missing_dependency"
)

const importImpactAssetKind knowledgeStore.RecordKind = "asset"

// ImportImpact is one stable, content-free preview row. ExistingID is omitted when
// disclosing it could reveal an object the caller did not name.
type ImportImpact struct {
	Kind                knowledgeStore.RecordKind `json:"kind"`
	ID                  string                    `json:"id"`
	Action              ImportImpactAction        `json:"action"`
	Reason              string                    `json:"reason"`
	ExistingID          string                    `json:"existing_id,omitempty"`
	Required            bool                      `json:"required,omitempty"`
	Blocking            bool                      `json:"blocking,omitempty"`
	ConflictResolvable  bool                      `json:"conflict_resolvable,omitempty"`
	CrossChunk          bool                      `json:"cross_chunk,omitempty"`
	ExistingRevision    uint64                    `json:"-"`
	ExistingFingerprint string                    `json:"-"`
}

// ImportImpactSummary gives UI and tool callers bounded aggregate counts without
// requiring them to reinterpret individual reasons.
type ImportImpactSummary struct {
	Additions           int `json:"additions"`
	Unchanged           int `json:"unchanged"`
	Conflicts           int `json:"conflicts"`
	References          int `json:"references"`
	MissingDependencies int `json:"missing_dependencies"`
	CrossChunkLinks     int `json:"cross_chunk_links"`
	Blockers            int `json:"blockers"`
}

// ImportPreview is a deterministic read-only analysis of one validated package.
type ImportPreview struct {
	Package        kpackage.Identity              `json:"package"`
	Publisher      kpackage.Publisher             `json:"publisher"`
	License        kpackage.License               `json:"license"`
	ChunkID        knowledge.ChunkID              `json:"chunk_id"`
	ChunkTitle     string                         `json:"chunk_title"`
	SignatureState kpackage.SignatureState        `json:"signature_state"`
	PublisherTrust PublisherTrust                 `json:"publisher_trust"`
	Classification knowledge.ClassificationResult `json:"classification"`
	Impacts        []ImportImpact                 `json:"impacts"`
	Summary        ImportImpactSummary            `json:"summary"`
	ReviewRequired bool                           `json:"review_required"`
	ReadyToStage   bool                           `json:"ready_to_stage"`
}

// PreviewImport scans and compares a validated package against one consistent store
// snapshot. It never opens a write transaction and never reserves IDs or revisions.
func (s *Service) PreviewImport(ctx context.Context, pkg kpackage.ValidatedPackage) (ImportPreview, error) {
	if err := ctx.Err(); err != nil {
		return ImportPreview{}, err
	}
	report, err := kpackage.Scan(ctx, pkg, s.classifier)
	preview := ImportPreview{
		Package: pkg.Manifest.Package, Publisher: pkg.Manifest.Publisher, License: pkg.Manifest.License,
		ChunkID:    knowledge.ChunkID(pkg.Manifest.Chunk.ID),
		ChunkTitle: pkg.Manifest.Chunk.Title, SignatureState: pkg.SignatureState,
		PublisherTrust: s.publishers.Assess(pkg.Manifest, pkg.SignatureState),
		Classification: knowledge.ClassificationResult{Decision: report.Decision, Findings: slices.Clone(report.Findings)},
		ReviewRequired: report.Decision == knowledge.ClassificationDecisionReview,
	}
	if err != nil {
		return preview, fmt.Errorf("scan knowledge package before preview: %w", err)
	}

	actor, err := s.actor(ctx)
	if err != nil {
		return ImportPreview{}, fmt.Errorf("resolve knowledge import actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return ImportPreview{}, err
	}
	incomingChunk := packageChunk(pkg.Manifest)
	importedEntries := make(map[knowledge.EntryID]struct{}, len(pkg.Entries))
	for _, entry := range pkg.Entries {
		importedEntries[entry.ID] = struct{}{}
	}
	err = s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		if err := s.previewChunkImpact(ctx, tx, actor, incomingChunk, &preview); err != nil {
			return err
		}
		if err := s.previewDependencyImpacts(ctx, tx, actor, pkg.Manifest.Dependencies, &preview); err != nil {
			return err
		}
		for _, entry := range sortedEntries(pkg.Entries) {
			existing, readErr := tx.Entry(ctx, entry.ID)
			if readErr == nil {
				if _, authErr := s.authorizeEntryChunk(ctx, tx, actor, ChunkPolicyRead, existing.ChunkID); authErr != nil {
					return authErr
				}
			}
			if impactErr := previewRecordImpact(existing, readErr, entry, knowledgeStore.RecordKindEntry, string(entry.ID), packageEntryContentEqual, &preview); impactErr != nil {
				return impactErr
			}
			if readErr == nil && (existing.State == knowledge.EntryStateArchived || existing.State == knowledge.EntryStateSuperseded) {
				protectLatestImportConflict(&preview, knowledgeStore.RecordKindEntry, string(entry.ID), "existing_entry_lifecycle_protected")
			}
		}
		for _, asset := range packageAssets(pkg) {
			existing, readErr := tx.Asset(ctx, asset.ChunkID, asset.Path)
			if impactErr := previewRecordImpact(existing, readErr, asset, importImpactAssetKind, asset.Path, packageAssetContentEqual, &preview); impactErr != nil {
				return impactErr
			}
		}
		for _, link := range sortedLinks(pkg.Links) {
			crossChunk := linkTouchesExternalChunk(link, incomingChunk.ID)
			if crossChunk {
				preview.Summary.CrossChunkLinks++
			}
			if impactErr := s.previewLinkImpact(ctx, tx, actor, link, crossChunk, &preview); impactErr != nil {
				return impactErr
			}
			impact := preview.Impacts[len(preview.Impacts)-1]
			if impact.Action == ImportImpactAdd {
				if authErr := s.authorizeIncomingLink(ctx, tx, actor, incomingChunk, importedEntries, link); authErr != nil {
					return authErr
				}
			}
		}
		for _, evidence := range sortedEvidence(pkg.Evidence) {
			if impactErr := previewEvidenceImpact(ctx, tx, evidence, &preview); impactErr != nil {
				return impactErr
			}
		}
		return nil
	})
	if err != nil {
		return ImportPreview{}, fmt.Errorf("preview knowledge package impact: %w", err)
	}
	preview.ReadyToStage = preview.Summary.Blockers == 0 && !preview.ReviewRequired
	return preview, nil
}

func (s *Service) authorizeIncomingLink(
	ctx context.Context,
	tx knowledgeStore.ReadTx,
	actor knowledge.Actor,
	incomingChunk knowledge.Chunk,
	importedEntries map[knowledge.EntryID]struct{},
	link knowledge.Link,
) error {
	chunks := make([]knowledge.Chunk, 0, 2)
	for _, endpoint := range []knowledge.ObjectRef{link.Source, link.Target} {
		switch endpoint.Kind {
		case knowledge.ObjectKindChunk:
			if endpoint.ID == string(incomingChunk.ID) {
				chunks = append(chunks, incomingChunk)
				continue
			}
		case knowledge.ObjectKindEntry:
			if _, exists := importedEntries[knowledge.EntryID(endpoint.ID)]; exists {
				chunks = append(chunks, incomingChunk)
				continue
			}
		}
		chunk, err := resolveLinkEndpoint(ctx, tx, endpoint)
		if errors.Is(err, knowledgeStore.ErrNotFound) {
			// The dependency impact is the safe, actionable blocker for a missing
			// external endpoint. There is no object to authorize yet.
			continue
		}
		if err != nil {
			return err
		}
		chunks = append(chunks, chunk)
	}
	return s.authorizeLinkChunks(ctx, actor, ChunkPolicyLinkCreate, true, chunks...)
}

func (s *Service) previewChunkImpact(ctx context.Context, tx knowledgeStore.ReadTx, actor knowledge.Actor, incoming knowledge.Chunk, preview *ImportPreview) error {
	existing, err := tx.Chunk(ctx, incoming.ID)
	switch {
	case errors.Is(err, knowledgeStore.ErrNotFound):
		if err := s.authorizeChunk(ctx, actor, ChunkPolicyCreate, incoming); err != nil {
			return err
		}
		appendImportImpact(preview, ImportImpact{Kind: knowledgeStore.RecordKindChunk, ID: string(incoming.ID), Action: ImportImpactAdd, Reason: "new_object"})
		return nil
	case err != nil:
		return err
	}
	if err := s.authorizeChunk(ctx, actor, ChunkPolicyRead, existing); err != nil {
		return err
	}
	if s.packageChunkContentEqual(existing, incoming) {
		appendImportImpact(preview, existingImportImpact(knowledgeStore.RecordKindChunk, string(incoming.ID), ImportImpactUnchanged, "same_content_already_present", existing))
	} else {
		impact := existingImportImpact(knowledgeStore.RecordKindChunk, string(incoming.ID), ImportImpactConflict, "id_exists_with_different_content", existing)
		impact.Blocking, impact.ConflictResolvable = true, true
		appendImportImpact(preview, impact)
	}
	return nil
}

func (s *Service) previewDependencyImpacts(ctx context.Context, tx knowledgeStore.ReadTx, actor knowledge.Actor, dependencies []kpackage.Dependency, preview *ImportPreview) error {
	dependencies = slices.Clone(dependencies)
	slices.SortFunc(dependencies, func(left, right kpackage.Dependency) int { return strings.Compare(left.ChunkID, right.ChunkID) })
	for _, dependency := range dependencies {
		existing, err := tx.Chunk(ctx, knowledge.ChunkID(dependency.ChunkID))
		if errors.Is(err, knowledgeStore.ErrNotFound) {
			appendImportImpact(preview, ImportImpact{
				Kind: knowledgeStore.RecordKindChunk, ID: dependency.ChunkID, Action: ImportImpactMissingDependency,
				Reason: dependencyReason(dependency.Required), Required: dependency.Required, Blocking: dependency.Required,
			})
			continue
		}
		if err != nil {
			return err
		}
		if err := s.authorizeChunk(ctx, actor, ChunkPolicyRead, existing); err != nil {
			return err
		}
		impact := existingImportImpact(knowledgeStore.RecordKindChunk, dependency.ChunkID, ImportImpactReference, "dependency_available", existing)
		impact.Required = dependency.Required
		if existing.State != knowledge.ChunkStateActive {
			impact.Reason = "dependency_inactive"
			impact.Blocking = true
		}
		appendImportImpact(preview, impact)
	}
	return nil
}

func (s *Service) previewLinkImpact(ctx context.Context, tx knowledgeStore.ReadTx, actor knowledge.Actor, incoming knowledge.Link, crossChunk bool, preview *ImportPreview) error {
	existing, err := tx.Link(ctx, incoming.ID)
	if err == nil {
		if err := s.authorizeExistingLink(ctx, tx, actor, existing); err != nil {
			return err
		}
		if packageLinkContentEqual(existing, incoming) {
			impact := existingImportImpact(knowledgeStore.RecordKindLink, string(incoming.ID), ImportImpactUnchanged, "same_content_already_present", existing)
			impact.CrossChunk = crossChunk
			appendImportImpact(preview, impact)
		} else {
			if equivalent, equivalentErr := tx.EquivalentLink(ctx, incoming); equivalentErr == nil && equivalent.ID != existing.ID {
				impact := existingImportImpact(knowledgeStore.RecordKindLink, string(incoming.ID), ImportImpactConflict, "link_identity_and_equivalence_conflict", existing)
				impact.Blocking, impact.CrossChunk = true, crossChunk
				appendImportImpact(preview, impact)
				return nil
			} else if equivalentErr != nil && !errors.Is(equivalentErr, knowledgeStore.ErrNotFound) {
				return equivalentErr
			}
			impact := existingImportImpact(knowledgeStore.RecordKindLink, string(incoming.ID), ImportImpactConflict, "id_exists_with_different_content", existing)
			impact.Blocking, impact.ConflictResolvable, impact.CrossChunk = true, true, crossChunk
			if existing.State == knowledge.LinkStateArchived {
				impact.Reason, impact.ConflictResolvable = "existing_link_lifecycle_protected", false
			}
			appendImportImpact(preview, impact)
		}
		return nil
	}
	if !errors.Is(err, knowledgeStore.ErrNotFound) {
		return err
	}
	equivalent, err := tx.EquivalentLink(ctx, incoming)
	if err == nil {
		if err := s.authorizeExistingLink(ctx, tx, actor, equivalent); err != nil {
			return err
		}
		impact := existingImportImpact(knowledgeStore.RecordKindLink, string(incoming.ID), ImportImpactConflict, "equivalent_link_exists", equivalent)
		impact.ExistingID = string(equivalent.ID)
		impact.Blocking, impact.ConflictResolvable, impact.CrossChunk = true, true, crossChunk
		if equivalent.State == knowledge.LinkStateArchived {
			impact.Reason, impact.ConflictResolvable = "existing_link_lifecycle_protected", false
		}
		appendImportImpact(preview, impact)
		return nil
	}
	if !errors.Is(err, knowledgeStore.ErrNotFound) {
		return err
	}
	appendImportImpact(preview, ImportImpact{Kind: knowledgeStore.RecordKindLink, ID: string(incoming.ID), Action: ImportImpactAdd, Reason: "new_object", CrossChunk: crossChunk})
	return ctx.Err()
}

func (s *Service) authorizeExistingLink(ctx context.Context, tx knowledgeStore.ReadTx, actor knowledge.Actor, link knowledge.Link) error {
	source, err := resolveLinkEndpoint(ctx, tx, link.Source)
	if err != nil {
		return err
	}
	target, err := resolveLinkEndpoint(ctx, tx, link.Target)
	if err != nil {
		return err
	}
	return s.authorizeLinkChunks(ctx, actor, ChunkPolicyRead, false, source, target)
}

func previewEvidenceImpact(ctx context.Context, tx knowledgeStore.ReadTx, incoming knowledge.Evidence, preview *ImportPreview) error {
	existing, err := tx.Evidence(ctx, incoming.ID)
	if err == nil {
		if packageEvidenceContentEqual(existing, incoming) {
			appendImportImpact(preview, existingImportImpact(knowledgeStore.RecordKindEvidence, string(incoming.ID), ImportImpactUnchanged, "same_content_already_present", existing))
		} else {
			conflict := existing
			reason := "id_exists_with_different_content"
			if sourceMatch, sourceErr := tx.EvidenceBySource(ctx, incoming.Source.ID, incoming.Source.ContentHash); sourceErr == nil {
				if sourceMatch.ID != existing.ID {
					impact := existingImportImpact(knowledgeStore.RecordKindEvidence, string(incoming.ID), ImportImpactConflict, "evidence_identity_and_source_conflict", existing)
					impact.Blocking = true
					appendImportImpact(preview, impact)
					return nil
				}
				conflict, reason = sourceMatch, "evidence_source_exists"
			} else if !errors.Is(sourceErr, knowledgeStore.ErrNotFound) {
				return sourceErr
			}
			impact := existingImportImpact(knowledgeStore.RecordKindEvidence, string(incoming.ID), ImportImpactConflict, reason, conflict)
			impact.ExistingID = string(conflict.ID)
			impact.Blocking, impact.ConflictResolvable = true, true
			appendImportImpact(preview, impact)
		}
		return nil
	}
	if !errors.Is(err, knowledgeStore.ErrNotFound) {
		return err
	}
	existing, err = tx.EvidenceBySource(ctx, incoming.Source.ID, incoming.Source.ContentHash)
	if err == nil {
		impact := existingImportImpact(knowledgeStore.RecordKindEvidence, string(incoming.ID), ImportImpactConflict, "evidence_source_exists", existing)
		impact.ExistingID = string(existing.ID)
		impact.Blocking, impact.ConflictResolvable = true, true
		appendImportImpact(preview, impact)
		return nil
	}
	if !errors.Is(err, knowledgeStore.ErrNotFound) {
		return err
	}
	appendImportImpact(preview, ImportImpact{Kind: knowledgeStore.RecordKindEvidence, ID: string(incoming.ID), Action: ImportImpactAdd, Reason: "new_object"})
	return ctx.Err()
}

func previewRecordImpact[T any](existing T, err error, incoming T, kind knowledgeStore.RecordKind, id string, equal func(T, T) bool, preview *ImportPreview) error {
	switch {
	case errors.Is(err, knowledgeStore.ErrNotFound):
		appendImportImpact(preview, ImportImpact{Kind: kind, ID: id, Action: ImportImpactAdd, Reason: "new_object"})
	case err != nil:
		return err
	case equal(existing, incoming):
		appendImportImpact(preview, existingImportImpact(kind, id, ImportImpactUnchanged, "same_content_already_present", existing))
	default:
		impact := existingImportImpact(kind, id, ImportImpactConflict, "id_exists_with_different_content", existing)
		impact.Blocking, impact.ConflictResolvable = true, true
		appendImportImpact(preview, impact)
	}
	return nil
}

func protectLatestImportConflict(preview *ImportPreview, kind knowledgeStore.RecordKind, id, reason string) {
	if len(preview.Impacts) == 0 {
		return
	}
	impact := &preview.Impacts[len(preview.Impacts)-1]
	if impact.Kind == kind && impact.ID == id && impact.Action == ImportImpactConflict {
		impact.Reason, impact.ConflictResolvable = reason, false
	}
}

func existingImportImpact(kind knowledgeStore.RecordKind, id string, action ImportImpactAction, reason string, existing any) ImportImpact {
	return ImportImpact{
		Kind: kind, ID: id, Action: action, Reason: reason, ExistingID: id,
		ExistingRevision: importRecordRevision(existing), ExistingFingerprint: importRecordFingerprint(existing),
	}
}

func importRecordRevision(value any) uint64 {
	switch record := value.(type) {
	case knowledge.Chunk:
		return record.Revision.Number
	case knowledge.Entry:
		return record.Revision.Number
	case knowledge.Link:
		return record.Revision.Number
	default:
		return 0
	}
}

func importRecordFingerprint(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func appendImportImpact(preview *ImportPreview, impact ImportImpact) {
	preview.Impacts = append(preview.Impacts, impact)
	switch impact.Action {
	case ImportImpactAdd:
		preview.Summary.Additions++
	case ImportImpactUnchanged:
		preview.Summary.Unchanged++
	case ImportImpactConflict:
		preview.Summary.Conflicts++
	case ImportImpactReference:
		preview.Summary.References++
	case ImportImpactMissingDependency:
		preview.Summary.MissingDependencies++
	}
	if impact.Blocking {
		preview.Summary.Blockers++
	}
}

func packageChunk(manifest kpackage.Manifest) knowledge.Chunk {
	dependencies := make([]knowledge.ChunkID, len(manifest.Dependencies))
	for index, dependency := range manifest.Dependencies {
		dependencies[index] = knowledge.ChunkID(dependency.ChunkID)
	}
	slices.Sort(dependencies)
	return knowledge.Chunk{
		ID: knowledge.ChunkID(manifest.Chunk.ID), Title: manifest.Chunk.Title, Description: manifest.Chunk.Description,
		Aliases: slices.Clone(manifest.Chunk.Aliases), Tags: slices.Clone(manifest.Chunk.Tags), Kind: manifest.Chunk.Kind,
		Scope: manifest.Chunk.Scope, Visibility: manifest.Chunk.Visibility, SharedWith: slices.Clone(manifest.Chunk.SharedWith),
		Language: manifest.Chunk.Language, Locale: manifest.Chunk.Locale, Domain: manifest.Chunk.Domain,
		Risk: slices.Clone(manifest.Chunk.Risk), State: manifest.Chunk.State, SchemaVersion: 1,
		Publisher: knowledge.Publisher{ID: manifest.Publisher.ID, Name: manifest.Publisher.Name}, License: manifest.License.Name,
		SourcePolicy: manifest.Chunk.SourcePolicy, DependencyIDs: dependencies, MinKoderVersion: manifest.MinKoderVersion,
		ReviewAfter: manifest.Chunk.ReviewAfter,
	}
}

func (s *Service) packageChunkContentEqual(existing, incoming knowledge.Chunk) bool {
	// Portable packages require publisher, license, and minimum-version metadata.
	// Local chunks may legitimately predate or omit those fields, so compare them
	// against the exact defaults our exporter adds instead of turning an immediate
	// export/reimport into a false content conflict.
	if existing.Publisher.ID == "" && existing.Publisher.Name == "" &&
		incoming.Publisher == (knowledge.Publisher{ID: defaultLocalPackagePublisherID, Name: "Koder local export"}) {
		existing.Publisher = incoming.Publisher
	}
	if existing.License == "" && incoming.License == "NOASSERTION" {
		existing.License = incoming.License
	}
	if existing.MinKoderVersion == "" && incoming.MinKoderVersion == s.importValidation.CurrentKoderVersion {
		existing.MinKoderVersion = incoming.MinKoderVersion
	}
	return chunkContentEqual(existing, incoming) && existing.State == incoming.State
}

func packageEntryContentEqual(existing, incoming knowledge.Entry) bool {
	existing.Revision, incoming.Revision = knowledge.Revision{}, knowledge.Revision{}
	existing.CreatedAt, incoming.CreatedAt = time.Time{}, time.Time{}
	existing.UpdatedAt, incoming.UpdatedAt = time.Time{}, time.Time{}
	existing.LastUsedAt, incoming.LastUsedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(existing, incoming)
}

func packageLinkContentEqual(existing, incoming knowledge.Link) bool {
	existing.Revision, incoming.Revision = knowledge.Revision{}, knowledge.Revision{}
	existing.CreatedAt, incoming.CreatedAt = time.Time{}, time.Time{}
	existing.UpdatedAt, incoming.UpdatedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(existing, incoming)
}

func packageEvidenceContentEqual(existing, incoming knowledge.Evidence) bool {
	existing.Actor, incoming.Actor = knowledge.Actor{}, knowledge.Actor{}
	existing.CreatedAt, incoming.CreatedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(existing, incoming)
}

func packageAssetContentEqual(existing, incoming knowledgeStore.PackageAsset) bool {
	return reflect.DeepEqual(existing, incoming)
}

func dependencyReason(required bool) string {
	if required {
		return "required_dependency_missing"
	}
	return "optional_dependency_missing"
}

func linkTouchesExternalChunk(link knowledge.Link, chunkID knowledge.ChunkID) bool {
	for _, endpoint := range []knowledge.ObjectRef{link.Source, link.Target} {
		if endpoint.Kind == knowledge.ObjectKindChunk && endpoint.ID != string(chunkID) {
			return true
		}
	}
	return false
}

func sortedEntries(values []knowledge.Entry) []knowledge.Entry {
	values = slices.Clone(values)
	slices.SortFunc(values, func(left, right knowledge.Entry) int { return strings.Compare(string(left.ID), string(right.ID)) })
	return values
}

func sortedLinks(values []knowledge.Link) []knowledge.Link {
	values = slices.Clone(values)
	slices.SortFunc(values, func(left, right knowledge.Link) int { return strings.Compare(string(left.ID), string(right.ID)) })
	return values
}

func sortedEvidence(values []knowledge.Evidence) []knowledge.Evidence {
	values = slices.Clone(values)
	slices.SortFunc(values, func(left, right knowledge.Evidence) int { return strings.Compare(string(left.ID), string(right.ID)) })
	return values
}

func packageAssets(pkg kpackage.ValidatedPackage) []knowledgeStore.PackageAsset {
	result := make([]knowledgeStore.PackageAsset, 0, len(pkg.Assets))
	chunkID := knowledge.ChunkID(pkg.Manifest.Chunk.ID)
	for path, data := range pkg.Assets {
		asset := knowledgeStore.PackageAsset{ChunkID: chunkID, Path: path, Data: slices.Clone(data)}
		for _, file := range pkg.Manifest.Files {
			if file.Path == path {
				asset.MediaType, asset.SHA256 = file.MediaType, file.SHA256
				break
			}
		}
		result = append(result, asset)
	}
	slices.SortFunc(result, func(left, right knowledgeStore.PackageAsset) int { return strings.Compare(left.Path, right.Path) })
	return result
}
