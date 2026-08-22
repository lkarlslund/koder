package service

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/kpackage"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/version"
)

const defaultLocalPackagePublisherID = "koder:local"

type ExportPackageRequest struct {
	ChunkID   knowledge.ChunkID
	Package   kpackage.Identity
	Publisher kpackage.Publisher
	License   kpackage.License
}

type ExportPackageResult struct {
	kpackage.ExportResult
	Filename string `json:"filename"`
	Entries  int    `json:"entries"`
	Links    int    `json:"links"`
	Evidence int    `json:"evidence"`
	Assets   int    `json:"assets"`
}

// ValidateImportArchive applies structural, integrity, compatibility, signature,
// and record validation to bounded archive bytes. It does not preview, stage, or write.
func (s *Service) ValidateImportArchive(ctx context.Context, data []byte) (kpackage.ValidatedPackage, error) {
	if err := ctx.Err(); err != nil {
		return kpackage.ValidatedPackage{}, err
	}
	if int64(len(data)) > kpackage.HardMaxArchiveBytes {
		return kpackage.ValidatedPackage{}, fmt.Errorf("%w: compressed archive exceeds %d bytes", kpackage.ErrLimitExceeded, kpackage.HardMaxArchiveBytes)
	}
	parsed, err := kpackage.ParseBytes(data, kpackage.DefaultParseLimits())
	if err != nil {
		return kpackage.ValidatedPackage{}, fmt.Errorf("parse knowledge package: %w", err)
	}
	validated, err := kpackage.Validate(parsed, cloneImportValidationOptions(s.importValidation))
	if err != nil {
		return kpackage.ValidatedPackage{}, fmt.Errorf("validate knowledge package: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return kpackage.ValidatedPackage{}, err
	}
	return validated, nil
}

// ExportPackage writes one authorized canonical chunk as a portable package. Export
// includes every lifecycle state owned by the chunk, authorized relationships that the
// v1 package can represent, cited evidence, assets, and explicit chunk dependencies.
func (s *Service) ExportPackage(ctx context.Context, writer io.Writer, request ExportPackageRequest) (ExportPackageResult, error) {
	if err := ctx.Err(); err != nil {
		return ExportPackageResult{}, err
	}
	if writer == nil || request.ChunkID == "" {
		return ExportPackageResult{}, fmt.Errorf("%w: export writer and chunk ID are required", knowledge.ErrInvalidRecord)
	}
	record, err := s.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(request.ChunkID)})
	if err != nil {
		return ExportPackageResult{}, fmt.Errorf("authorize knowledge package export: %w", err)
	}
	if record.Chunk == nil {
		return ExportPackageResult{}, fmt.Errorf("%w: chunk projection is unavailable", knowledge.ErrInvalidRecord)
	}
	chunk := *record.Chunk
	entries, err := s.exportPackageEntries(ctx, chunk.ID)
	if err != nil {
		return ExportPackageResult{}, err
	}
	links, dependencyIDs, err := s.exportPackageLinks(ctx, chunk, entries)
	if err != nil {
		return ExportPackageResult{}, err
	}
	dependencies, err := s.exportPackageDependencies(ctx, chunk, dependencyIDs)
	if err != nil {
		return ExportPackageResult{}, err
	}
	chunk.DependencyIDs = slices.Clone(dependencyIDs)
	evidence, assets, err := s.exportPackageSupport(ctx, chunk.ID, entries, links)
	if err != nil {
		return ExportPackageResult{}, err
	}
	request = s.defaultExportPackageRequest(request, chunk)
	if chunk.MinKoderVersion == "" {
		chunk.MinKoderVersion = s.importValidation.CurrentKoderVersion
	}
	exportRequest := kpackage.ExportRequest{
		Package: request.Package, Publisher: request.Publisher, License: request.License,
		CreatedAt: chunk.UpdatedAt.UTC(), Dependencies: dependencies, Chunk: chunk,
		Entries: entries, Links: links, Evidence: evidence, Assets: assets,
	}
	result, err := kpackage.Export(writer, exportRequest)
	if err != nil {
		return ExportPackageResult{}, fmt.Errorf("export knowledge package: %w", err)
	}
	return ExportPackageResult{
		ExportResult: result, Filename: packageFilename(chunk), Entries: len(entries), Links: len(links),
		Evidence: len(evidence), Assets: len(assets),
	}, nil
}

func (s *Service) exportPackageEntries(ctx context.Context, chunkID knowledge.ChunkID) ([]knowledge.Entry, error) {
	request := knowledgeStore.EntryListRequest{
		Filter: knowledgeStore.EntryFilter{
			ChunkIDs: []knowledge.ChunkID{chunkID},
			States: []knowledge.EntryState{
				knowledge.EntryStateDraft, knowledge.EntryStateActive, knowledge.EntryStateSuperseded, knowledge.EntryStateArchived,
			},
		},
		Sort: knowledgeStore.EntrySortCreatedAt, Limit: 200,
	}
	var result []knowledge.Entry
	for {
		page, err := s.ListEntries(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("list knowledge package entries: %w", err)
		}
		result = append(result, page.Entries...)
		if len(result) > kpackage.HardMaxFiles || page.NextCursor == "" {
			if len(result) > kpackage.HardMaxFiles {
				return nil, fmt.Errorf("%w: package entry count exceeds %d", kpackage.ErrLimitExceeded, kpackage.HardMaxFiles)
			}
			break
		}
		request.Cursor = page.NextCursor
	}
	slices.SortFunc(result, func(left, right knowledge.Entry) int { return strings.Compare(string(left.ID), string(right.ID)) })
	return result, nil
}

func (s *Service) exportPackageLinks(ctx context.Context, chunk knowledge.Chunk, entries []knowledge.Entry) ([]knowledge.Link, []knowledge.ChunkID, error) {
	ownedEntries := make(map[string]struct{}, len(entries))
	endpoints := []knowledge.ObjectRef{{Kind: knowledge.ObjectKindChunk, ID: string(chunk.ID)}}
	for _, entry := range entries {
		ownedEntries[string(entry.ID)] = struct{}{}
		endpoints = append(endpoints, knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.ID)})
	}
	linksByID := make(map[knowledge.LinkID]knowledge.Link)
	for _, endpoint := range endpoints {
		request := knowledgeStore.AdjacentLinkListRequest{Filter: knowledgeStore.AdjacentLinkFilter{
			Endpoint: endpoint, Direction: knowledgeStore.LinkDirectionBoth,
			States: []knowledge.LinkState{knowledge.LinkStateActive, knowledge.LinkStateArchived},
		}, Limit: 100}
		for {
			page, err := s.store.ListAdjacentLinks(ctx, request)
			if err != nil {
				return nil, nil, fmt.Errorf("list knowledge package relationships: %w", err)
			}
			for _, link := range page.Links {
				if _, exists := linksByID[link.ID]; exists {
					continue
				}
				if _, err := s.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindLink, ID: string(link.ID)}); err != nil {
					if errors.Is(err, ErrChunkPolicyDenied) {
						continue
					}
					return nil, nil, fmt.Errorf("authorize knowledge package relationship: %w", err)
				}
				linksByID[link.ID] = link
				if len(linksByID) > kpackage.HardMaxFiles {
					return nil, nil, fmt.Errorf("%w: package relationship count exceeds %d", kpackage.ErrLimitExceeded, kpackage.HardMaxFiles)
				}
			}
			if page.NextCursor == "" {
				break
			}
			request.Cursor = page.NextCursor
		}
	}

	dependencySet := make(map[knowledge.ChunkID]struct{}, len(chunk.DependencyIDs))
	for _, id := range chunk.DependencyIDs {
		if id != chunk.ID {
			dependencySet[id] = struct{}{}
		}
	}
	links := make([]knowledge.Link, 0, len(linksByID))
	for _, link := range linksByID {
		exportable := true
		for _, endpoint := range []knowledge.ObjectRef{link.Source, link.Target} {
			switch endpoint.Kind {
			case knowledge.ObjectKindChunk:
				if endpoint.ID != string(chunk.ID) {
					dependencySet[knowledge.ChunkID(endpoint.ID)] = struct{}{}
				}
			case knowledge.ObjectKindEntry:
				if _, owned := ownedEntries[endpoint.ID]; !owned {
					exportable = false
				}
			}
		}
		if exportable {
			links = append(links, link)
		}
	}
	slices.SortFunc(links, func(left, right knowledge.Link) int { return strings.Compare(string(left.ID), string(right.ID)) })
	dependencyIDs := make([]knowledge.ChunkID, 0, len(dependencySet))
	for id := range dependencySet {
		dependencyIDs = append(dependencyIDs, id)
	}
	slices.Sort(dependencyIDs)
	return links, dependencyIDs, nil
}

func (s *Service) exportPackageDependencies(ctx context.Context, chunk knowledge.Chunk, ids []knowledge.ChunkID) ([]kpackage.Dependency, error) {
	result := make([]kpackage.Dependency, 0, len(ids))
	for _, id := range ids {
		record, err := s.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(id)})
		if err != nil {
			return nil, fmt.Errorf("read knowledge package dependency: %w", err)
		}
		if record.Chunk == nil {
			return nil, fmt.Errorf("%w: dependency chunk projection is unavailable", knowledge.ErrInvalidRecord)
		}
		dependency := *record.Chunk
		result = append(result, kpackage.Dependency{
			PackageID: string(dependency.ID), ChunkID: string(dependency.ID), Version: packageRevisionVersion(dependency.Revision.Number),
			Title: dependency.Title, Required: slices.Contains(chunk.DependencyIDs, dependency.ID),
		})
	}
	return result, nil
}

func (s *Service) exportPackageSupport(ctx context.Context, chunkID knowledge.ChunkID, entries []knowledge.Entry, links []knowledge.Link) ([]knowledge.Evidence, []kpackage.Asset, error) {
	evidenceIDs := make(map[knowledge.EvidenceID]struct{})
	for _, entry := range entries {
		for _, id := range append(slices.Clone(entry.EvidenceIDs), entry.Verification.EvidenceIDs...) {
			evidenceIDs[id] = struct{}{}
		}
	}
	for _, link := range links {
		for _, id := range link.EvidenceIDs {
			evidenceIDs[id] = struct{}{}
		}
	}
	ids := make([]knowledge.EvidenceID, 0, len(evidenceIDs))
	for id := range evidenceIDs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var evidence []knowledge.Evidence
	var storedAssets []knowledgeStore.PackageAsset
	err := s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		for _, id := range ids {
			item, err := tx.Evidence(ctx, id)
			if err != nil {
				return err
			}
			evidence = append(evidence, item)
		}
		var err error
		storedAssets, err = tx.ListAssets(ctx, chunkID)
		return err
	})
	if err != nil {
		return nil, nil, fmt.Errorf("read knowledge package support data: %w", err)
	}
	assets := make([]kpackage.Asset, 0, len(storedAssets))
	for _, asset := range storedAssets {
		assets = append(assets, kpackage.Asset{Path: asset.Path, MediaType: asset.MediaType, Data: slices.Clone(asset.Data)})
	}
	return evidence, assets, nil
}

func (s *Service) defaultExportPackageRequest(request ExportPackageRequest, chunk knowledge.Chunk) ExportPackageRequest {
	if request.Package.ID == "" {
		request.Package.ID = string(chunk.ID)
	}
	if request.Package.Version == "" {
		request.Package.Version = packageRevisionVersion(chunk.Revision.Number)
	}
	if request.Publisher.ID == "" {
		request.Publisher.ID = chunk.Publisher.ID
	}
	if request.Publisher.Name == "" {
		request.Publisher.Name = chunk.Publisher.Name
	}
	if request.Publisher.ID == "" {
		request.Publisher.ID = defaultLocalPackagePublisherID
	}
	if request.Publisher.Name == "" {
		request.Publisher.Name = "Koder local export"
	}
	if request.License.Name == "" {
		request.License.Name = chunk.License
	}
	if request.License.Name == "" {
		request.License.Name = "NOASSERTION"
	}
	return request
}

func packageRevisionVersion(revision uint64) string {
	return fmt.Sprintf("0.0.%d", max(uint64(1), revision))
}

func packageFilename(chunk knowledge.Chunk) string {
	name := strings.ToLower(knowledge.NormalizeTitle(chunk.Title))
	var builder strings.Builder
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			builder.WriteRune(character)
		case builder.Len() != 0 && !strings.HasSuffix(builder.String(), "-"):
			builder.WriteByte('-')
		}
	}
	name = strings.Trim(builder.String(), "-")
	if name == "" {
		name = "knowledge"
	}
	return name + ".kknowledge"
}

func normalizeImportValidationOptions(options kpackage.ValidationOptions) kpackage.ValidationOptions {
	options.CurrentKoderVersion = canonicalRuntimeBuild(options.CurrentKoderVersion)
	if options.CurrentKoderVersion == "" {
		options.CurrentKoderVersion = canonicalRuntimeBuild(version.Version)
	}
	if options.CurrentKoderVersion == "" {
		options.CurrentKoderVersion = "r0000"
	}
	return cloneImportValidationOptions(options)
}

func cloneImportValidationOptions(options kpackage.ValidationOptions) kpackage.ValidationOptions {
	options.SupportedFeatures = slices.Clone(options.SupportedFeatures)
	if options.VerificationKeys != nil {
		keys := make(map[string]ed25519.PublicKey, len(options.VerificationKeys))
		for id, key := range options.VerificationKeys {
			keys[id] = slices.Clone(key)
		}
		options.VerificationKeys = keys
	}
	return options
}

func canonicalRuntimeBuild(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "r") {
		return ""
	}
	digits := strings.TrimLeft(value[1:], "0123456789")
	length := len(value[1:]) - len(digits)
	if length == 0 {
		return ""
	}
	return value[:length+1]
}
