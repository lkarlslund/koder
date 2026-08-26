package service

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/kpackage"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var ErrImportConflictPolicy = errors.New("memory package conflict policy is invalid")

// ImportConflictPolicy is an explicit, package-wide decision for conflicting
// canonical identities. The empty policy never resolves a conflict implicitly.
type ImportConflictPolicy string

const (
	ImportConflictPolicyUnspecified ImportConflictPolicy = ""
	ImportConflictPolicyReplace     ImportConflictPolicy = "replace"
	ImportConflictPolicyMerge       ImportConflictPolicy = "merge"
	ImportConflictPolicyKeepBoth    ImportConflictPolicy = "keep_both"
)

func (p ImportConflictPolicy) Validate() error {
	switch p {
	case ImportConflictPolicyUnspecified, ImportConflictPolicyReplace, ImportConflictPolicyMerge, ImportConflictPolicyKeepBoth:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrImportConflictPolicy, p)
	}
}

// ImportIDRemap explains an identity decision made by keep-both, or a reused
// semantic identity required by a store uniqueness constraint.
type ImportIDRemap struct {
	Kind   memoryStoreAPI.RecordKind `json:"kind"`
	FromID string                    `json:"from_id"`
	ToID   string                    `json:"to_id"`
	Reason string                    `json:"reason"`
	Reused bool                      `json:"reused,omitempty"`
}

func resolveImportConflictPreview(preview ImportPreview, policy ImportConflictPolicy) (ImportPreview, error) {
	if err := policy.Validate(); err != nil {
		return preview, err
	}
	if preview.Summary.Conflicts == 0 {
		return preview, nil
	}
	if policy == ImportConflictPolicyUnspecified || policy == ImportConflictPolicyKeepBoth {
		return preview, &ImportBlockedError{Preview: preview}
	}
	for index := range preview.Impacts {
		impact := &preview.Impacts[index]
		if impact.Action == ImportImpactConflict && impact.Blocking && impact.ConflictResolvable {
			impact.Blocking = false
			preview.Summary.Blockers--
		}
	}
	preview.ReadyToStage = preview.Summary.Blockers == 0 && !preview.ReviewRequired
	return preview, nil
}

// keepBothImportPackage detaches every conflicting identity while preserving all
// internal references. Store-level semantic duplicates (same evidence source or
// equivalent relationship) reuse the authorized local identity because the
// canonical model intentionally permits only one such object.
func keepBothImportPackage(pkg kpackage.ValidatedPackage, preview ImportPreview, newID func() string) (kpackage.ValidatedPackage, []ImportIDRemap, error) {
	result := pkg.Clone()
	conflicts := make(map[string]ImportImpact)
	for _, impact := range preview.Impacts {
		if impact.Action == ImportImpactConflict {
			conflicts[importImpactKey(impact.Kind, impact.ID)] = impact
		}
	}
	if len(conflicts) == 0 {
		return result, nil, nil
	}

	var remaps []ImportIDRemap
	chunkIDs := make(map[string]string)
	entryIDs := make(map[string]string)
	evidenceIDs := make(map[string]string)
	linkIDs := make(map[string]string)
	assetPaths := make(map[string]string)

	originalChunkID := result.Manifest.Chunk.ID
	_, chunkConflict := conflicts[importImpactKey(memoryStoreAPI.RecordKindChunk, originalChunkID)]
	if chunkConflict {
		id, err := nextImportObjectID(newID)
		if err != nil {
			return kpackage.ValidatedPackage{}, nil, err
		}
		chunkIDs[originalChunkID] = id
		remaps = append(remaps, ImportIDRemap{Kind: memoryStoreAPI.RecordKindChunk, FromID: originalChunkID, ToID: id, Reason: "keep_both"})
	}

	for _, entry := range result.Entries {
		_, conflict := conflicts[importImpactKey(memoryStoreAPI.RecordKindEntry, string(entry.ID))]
		if !chunkConflict && !conflict {
			continue
		}
		id, err := nextImportObjectID(newID)
		if err != nil {
			return kpackage.ValidatedPackage{}, nil, err
		}
		entryIDs[string(entry.ID)] = id
		remaps = append(remaps, ImportIDRemap{Kind: memoryStoreAPI.RecordKindEntry, FromID: string(entry.ID), ToID: id, Reason: "keep_both"})
	}

	droppedEvidence := make(map[string]struct{})
	for _, evidence := range result.Evidence {
		impact, conflict := conflicts[importImpactKey(memoryStoreAPI.RecordKindEvidence, string(evidence.ID))]
		if !conflict {
			continue
		}
		if impact.Reason == "evidence_source_exists" && impact.ExistingID != "" {
			evidenceIDs[string(evidence.ID)] = impact.ExistingID
			droppedEvidence[string(evidence.ID)] = struct{}{}
			remaps = append(remaps, ImportIDRemap{
				Kind: memoryStoreAPI.RecordKindEvidence, FromID: string(evidence.ID), ToID: impact.ExistingID,
				Reason: "same_source_reused", Reused: true,
			})
			continue
		}
		id, err := nextImportObjectID(newID)
		if err != nil {
			return kpackage.ValidatedPackage{}, nil, err
		}
		evidenceIDs[string(evidence.ID)] = id
		remaps = append(remaps, ImportIDRemap{Kind: memoryStoreAPI.RecordKindEvidence, FromID: string(evidence.ID), ToID: id, Reason: "keep_both"})
	}

	droppedLinks := make(map[string]struct{})
	for _, link := range result.Links {
		impact, conflict := conflicts[importImpactKey(memoryStoreAPI.RecordKindLink, string(link.ID))]
		if conflict && impact.Reason == "equivalent_link_exists" && impact.ExistingID != "" && !linkTouchesRemappedEndpoint(link, chunkIDs, entryIDs) {
			linkIDs[string(link.ID)] = impact.ExistingID
			droppedLinks[string(link.ID)] = struct{}{}
			remaps = append(remaps, ImportIDRemap{
				Kind: memoryStoreAPI.RecordKindLink, FromID: string(link.ID), ToID: impact.ExistingID,
				Reason: "equivalent_link_reused", Reused: true,
			})
			continue
		}
		if !chunkConflict && !conflict && !linkTouchesRemappedEndpoint(link, chunkIDs, entryIDs) {
			continue
		}
		id, err := nextImportObjectID(newID)
		if err != nil {
			return kpackage.ValidatedPackage{}, nil, err
		}
		linkIDs[string(link.ID)] = id
		remaps = append(remaps, ImportIDRemap{Kind: memoryStoreAPI.RecordKindLink, FromID: string(link.ID), ToID: id, Reason: "keep_both"})
	}

	for _, file := range result.Manifest.Files {
		if _, conflict := conflicts[importImpactKey(importImpactAssetKind, file.Path)]; !conflict || !strings.HasPrefix(file.Path, "assets/") {
			continue
		}
		newPath := keepBothAssetPath(file.Path, newID())
		assetPaths[file.Path] = newPath
		remaps = append(remaps, ImportIDRemap{Kind: importImpactAssetKind, FromID: file.Path, ToID: newPath, Reason: "keep_both"})
	}

	if replacement := chunkIDs[originalChunkID]; replacement != "" {
		result.Manifest.Chunk.ID = replacement
	}
	for index := range result.Entries {
		entry := &result.Entries[index]
		if replacement := entryIDs[string(entry.ID)]; replacement != "" {
			entry.ID = memory.EntryID(replacement)
		}
		if replacement := chunkIDs[string(entry.ChunkID)]; replacement != "" {
			entry.ChunkID = memory.ChunkID(replacement)
		}
		if replacement := entryIDs[string(entry.SupersededByID)]; replacement != "" {
			entry.SupersededByID = memory.EntryID(replacement)
		}
		entry.EvidenceIDs = remapEvidenceIDs(entry.EvidenceIDs, evidenceIDs)
		entry.Verification.EvidenceIDs = remapEvidenceIDs(entry.Verification.EvidenceIDs, evidenceIDs)
	}
	filteredEvidence := result.Evidence[:0]
	for _, evidence := range result.Evidence {
		originalID := string(evidence.ID)
		if _, drop := droppedEvidence[originalID]; drop {
			continue
		}
		if replacement := evidenceIDs[originalID]; replacement != "" {
			evidence.ID = memory.EvidenceID(replacement)
		}
		filteredEvidence = append(filteredEvidence, evidence)
	}
	result.Evidence = filteredEvidence
	filteredLinks := result.Links[:0]
	for _, link := range result.Links {
		originalID := string(link.ID)
		if _, drop := droppedLinks[originalID]; drop {
			continue
		}
		if replacement := linkIDs[originalID]; replacement != "" {
			link.ID = memory.LinkID(replacement)
		}
		link.Source = remapImportObjectRef(link.Source, chunkIDs, entryIDs)
		link.Target = remapImportObjectRef(link.Target, chunkIDs, entryIDs)
		link.EvidenceIDs = remapEvidenceIDs(link.EvidenceIDs, evidenceIDs)
		filteredLinks = append(filteredLinks, link)
	}
	result.Links = filteredLinks
	if len(assetPaths) != 0 {
		assets := make(map[string][]byte, len(result.Assets))
		for current, data := range result.Assets {
			if replacement := assetPaths[current]; replacement != "" {
				current = replacement
			}
			assets[current] = data
		}
		result.Assets = assets
		for index := range result.Manifest.Files {
			if replacement := assetPaths[result.Manifest.Files[index].Path]; replacement != "" {
				result.Manifest.Files[index].Path = replacement
			}
		}
	}
	slices.SortFunc(remaps, func(left, right ImportIDRemap) int {
		if order := strings.Compare(string(left.Kind), string(right.Kind)); order != 0 {
			return order
		}
		return strings.Compare(left.FromID, right.FromID)
	})
	return result, remaps, nil
}

func importImpactKey(kind memoryStoreAPI.RecordKind, id string) string {
	return string(kind) + "\x00" + id
}

func nextImportObjectID(newID func() string) (string, error) {
	id := strings.TrimSpace(newID())
	if err := (memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: id}).Validate(); err != nil {
		return "", fmt.Errorf("allocate keep-both import ID: %w", err)
	}
	return id, nil
}

func linkTouchesRemappedEndpoint(link memory.Link, chunks, entries map[string]string) bool {
	for _, endpoint := range []memory.ObjectRef{link.Source, link.Target} {
		if endpoint.Kind == memory.ObjectKindChunk && chunks[endpoint.ID] != "" {
			return true
		}
		if endpoint.Kind == memory.ObjectKindEntry && entries[endpoint.ID] != "" {
			return true
		}
	}
	return false
}

func remapImportObjectRef(reference memory.ObjectRef, chunks, entries map[string]string) memory.ObjectRef {
	if reference.Kind == memory.ObjectKindChunk && chunks[reference.ID] != "" {
		reference.ID = chunks[reference.ID]
	}
	if reference.Kind == memory.ObjectKindEntry && entries[reference.ID] != "" {
		reference.ID = entries[reference.ID]
	}
	return reference
}

func remapEvidenceIDs(ids []memory.EvidenceID, remaps map[string]string) []memory.EvidenceID {
	result := slices.Clone(ids)
	for index, id := range result {
		if replacement := remaps[string(id)]; replacement != "" {
			result[index] = memory.EvidenceID(replacement)
		}
	}
	return result
}

func keepBothAssetPath(current, suffix string) string {
	extension := path.Ext(current)
	base := strings.TrimSuffix(current, extension)
	compact := strings.ReplaceAll(suffix, "-", "")
	if len(compact) > 12 {
		compact = compact[len(compact)-12:]
	}
	return base + ".import-" + compact + extension
}
