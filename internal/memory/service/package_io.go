package service

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/kpackage"
	"github.com/lkarlslund/koder/internal/memory/observability"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	"github.com/lkarlslund/koder/internal/version"
)

const defaultLocalPackagePublisherID = "koder:local"

var ErrPersonalExportConsent = errors.New("personal memory export requires explicit consent")

type ExportPackageRequest struct {
	ChunkID         memory.ChunkID
	Package         kpackage.Identity
	Publisher       kpackage.Publisher
	License         kpackage.License
	IncludePersonal bool
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
func (s *Service) ValidateImportArchive(ctx context.Context, data []byte) (pkg kpackage.ValidatedPackage, err error) {
	operation := s.operationRecorder.Start(observability.OperationImportValidate, AuditIDFromContext(ctx))
	defer func() {
		operation.Finish(operationOutcome(err, pkg.Manifest.Chunk.ID == ""), uint64(len(data)), importPackageObjectCount(pkg))
	}()
	if err := ctx.Err(); err != nil {
		return kpackage.ValidatedPackage{}, err
	}
	if int64(len(data)) > kpackage.HardMaxArchiveBytes {
		return kpackage.ValidatedPackage{}, fmt.Errorf("%w: compressed archive exceeds %d bytes", kpackage.ErrLimitExceeded, kpackage.HardMaxArchiveBytes)
	}
	parsed, err := kpackage.ParseBytes(data, kpackage.DefaultParseLimits())
	if err != nil {
		return kpackage.ValidatedPackage{}, fmt.Errorf("parse memory package: %w", err)
	}
	validated, err := kpackage.Validate(parsed, cloneImportValidationOptions(s.importValidation))
	if err != nil {
		return kpackage.ValidatedPackage{}, fmt.Errorf("validate memory package: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return kpackage.ValidatedPackage{}, err
	}
	return validated, nil
}

func importPackageObjectCount(pkg kpackage.ValidatedPackage) uint64 {
	count := len(pkg.Entries) + len(pkg.Links) + len(pkg.Evidence) + len(pkg.Assets)
	if pkg.Manifest.Chunk.ID != "" {
		count++
	}
	return uint64(count)
}

// ExportPackage writes one authorized canonical chunk as a portable package. Export
// includes every lifecycle state owned by the chunk, authorized relationships that the
// v1 package can represent, cited evidence, assets, and explicit chunk dependencies.
func (s *Service) ExportPackage(ctx context.Context, writer io.Writer, request ExportPackageRequest) (ExportPackageResult, error) {
	if err := ctx.Err(); err != nil {
		return ExportPackageResult{}, err
	}
	if writer == nil || request.ChunkID == "" {
		return ExportPackageResult{}, fmt.Errorf("%w: export writer and chunk ID are required", memory.ErrInvalidRecord)
	}
	record, err := s.Get(ctx, memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(request.ChunkID)})
	if err != nil {
		return ExportPackageResult{}, fmt.Errorf("authorize memory package export: %w", err)
	}
	if record.Chunk == nil {
		return ExportPackageResult{}, fmt.Errorf("%w: chunk projection is unavailable", memory.ErrInvalidRecord)
	}
	chunk := *record.Chunk
	if personalExportRequiresConsent(chunk) && !request.IncludePersonal {
		return ExportPackageResult{}, ErrPersonalExportConsent
	}
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
		return ExportPackageResult{}, fmt.Errorf("export memory package: %w", err)
	}
	return ExportPackageResult{
		ExportResult: result, Filename: packageFilename(chunk), Entries: len(entries), Links: len(links),
		Evidence: len(evidence), Assets: len(assets),
	}, nil
}

func personalExportRequiresConsent(chunk memory.Chunk) bool {
	return chunk.ID == PersonalMeChunkID || chunk.Kind == memory.ChunkKindPersonal || chunk.Scope.Kind == memory.ScopeKindPersonal
}

func (s *Service) exportPackageEntries(ctx context.Context, chunkID memory.ChunkID) ([]memory.Entry, error) {
	request := memoryStoreAPI.EntryListRequest{
		Filter: memoryStoreAPI.EntryFilter{
			ChunkIDs: []memory.ChunkID{chunkID},
			States: []memory.EntryState{
				memory.EntryStateDraft, memory.EntryStateActive, memory.EntryStateSuperseded, memory.EntryStateArchived,
			},
		},
		Sort: memoryStoreAPI.EntrySortCreatedAt, Limit: 200,
	}
	var result []memory.Entry
	for {
		page, err := s.ListEntries(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("list memory package entries: %w", err)
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
	slices.SortFunc(result, func(left, right memory.Entry) int { return strings.Compare(string(left.ID), string(right.ID)) })
	return result, nil
}

func (s *Service) exportPackageLinks(ctx context.Context, chunk memory.Chunk, entries []memory.Entry) ([]memory.Link, []memory.ChunkID, error) {
	ownedEntries := make(map[string]struct{}, len(entries))
	endpoints := []memory.ObjectRef{{Kind: memory.ObjectKindChunk, ID: string(chunk.ID)}}
	for _, entry := range entries {
		ownedEntries[string(entry.ID)] = struct{}{}
		endpoints = append(endpoints, memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entry.ID)})
	}
	linksByID := make(map[memory.LinkID]memory.Link)
	for _, endpoint := range endpoints {
		request := memoryStoreAPI.AdjacentLinkListRequest{Filter: memoryStoreAPI.AdjacentLinkFilter{
			Endpoint: endpoint, Direction: memoryStoreAPI.LinkDirectionBoth,
			States: []memory.LinkState{memory.LinkStateActive, memory.LinkStateArchived},
		}, Limit: 100}
		for {
			page, err := s.store.ListAdjacentLinks(ctx, request)
			if err != nil {
				return nil, nil, fmt.Errorf("list memory package relationships: %w", err)
			}
			for _, link := range page.Links {
				if _, exists := linksByID[link.ID]; exists {
					continue
				}
				if _, err := s.Get(ctx, memory.ObjectRef{Kind: memory.ObjectKindLink, ID: string(link.ID)}); err != nil {
					if errors.Is(err, ErrChunkPolicyDenied) {
						continue
					}
					return nil, nil, fmt.Errorf("authorize memory package relationship: %w", err)
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

	dependencySet := make(map[memory.ChunkID]struct{}, len(chunk.DependencyIDs))
	for _, id := range chunk.DependencyIDs {
		if id != chunk.ID {
			dependencySet[id] = struct{}{}
		}
	}
	links := make([]memory.Link, 0, len(linksByID))
	for _, link := range linksByID {
		exportable := true
		for _, endpoint := range []memory.ObjectRef{link.Source, link.Target} {
			switch endpoint.Kind {
			case memory.ObjectKindChunk:
				if endpoint.ID != string(chunk.ID) {
					dependencySet[memory.ChunkID(endpoint.ID)] = struct{}{}
				}
			case memory.ObjectKindEntry:
				if _, owned := ownedEntries[endpoint.ID]; !owned {
					exportable = false
				}
			}
		}
		if exportable {
			links = append(links, link)
		}
	}
	slices.SortFunc(links, func(left, right memory.Link) int { return strings.Compare(string(left.ID), string(right.ID)) })
	dependencyIDs := make([]memory.ChunkID, 0, len(dependencySet))
	for id := range dependencySet {
		dependencyIDs = append(dependencyIDs, id)
	}
	slices.Sort(dependencyIDs)
	return links, dependencyIDs, nil
}

func (s *Service) exportPackageDependencies(ctx context.Context, chunk memory.Chunk, ids []memory.ChunkID) ([]kpackage.Dependency, error) {
	result := make([]kpackage.Dependency, 0, len(ids))
	for _, id := range ids {
		record, err := s.Get(ctx, memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(id)})
		if err != nil {
			return nil, fmt.Errorf("read memory package dependency: %w", err)
		}
		if record.Chunk == nil {
			return nil, fmt.Errorf("%w: dependency chunk projection is unavailable", memory.ErrInvalidRecord)
		}
		dependency := *record.Chunk
		result = append(result, kpackage.Dependency{
			PackageID: string(dependency.ID), ChunkID: string(dependency.ID), Version: packageRevisionVersion(dependency.Revision.Number),
			Title: dependency.Title, Required: slices.Contains(chunk.DependencyIDs, dependency.ID),
		})
	}
	return result, nil
}

func (s *Service) exportPackageSupport(ctx context.Context, chunkID memory.ChunkID, entries []memory.Entry, links []memory.Link) ([]memory.Evidence, []kpackage.Asset, error) {
	evidenceIDs := make(map[memory.EvidenceID]struct{})
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
	ids := make([]memory.EvidenceID, 0, len(evidenceIDs))
	for id := range evidenceIDs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var evidence []memory.Evidence
	var storedAssets []memoryStoreAPI.PackageAsset
	err := s.store.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
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
		return nil, nil, fmt.Errorf("read memory package support data: %w", err)
	}
	assets := make([]kpackage.Asset, 0, len(storedAssets))
	for _, asset := range storedAssets {
		assets = append(assets, kpackage.Asset{Path: asset.Path, MediaType: asset.MediaType, Data: slices.Clone(asset.Data)})
	}
	return evidence, assets, nil
}

func (s *Service) defaultExportPackageRequest(request ExportPackageRequest, chunk memory.Chunk) ExportPackageRequest {
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

func packageFilename(chunk memory.Chunk) string {
	name := strings.ToLower(memory.NormalizeTitle(chunk.Title))
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
		name = "memory"
	}
	return name + ".kmemory"
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
