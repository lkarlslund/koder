package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

const (
	defaultIntegrityIssueLimit = 1000
	maxIntegrityIssueLimit     = 10000
)

type IntegrityIssueKind string

const (
	IntegrityOrphanEntry          IntegrityIssueKind = "orphan_entry"
	IntegrityUnreferencedEvidence IntegrityIssueKind = "unreferenced_evidence"
	IntegrityMissingDependency    IntegrityIssueKind = "missing_dependency"
	IntegrityMissingEvidence      IntegrityIssueKind = "missing_evidence"
	IntegrityBrokenSupersession   IntegrityIssueKind = "broken_supersession"
	IntegrityBrokenLinkSource     IntegrityIssueKind = "broken_link_source"
	IntegrityBrokenLinkTarget     IntegrityIssueKind = "broken_link_target"
	IntegrityDuplicateLink        IntegrityIssueKind = "duplicate_link"
	IntegrityChunkCountMismatch   IntegrityIssueKind = "chunk_count_mismatch"
	IntegrityInvalidRecord        IntegrityIssueKind = "invalid_record"
	IntegrityIndexMissing         IntegrityIssueKind = "index_missing_record"
	IntegrityIndexUnexpected      IntegrityIssueKind = "index_unexpected_record"
	IntegrityIndexDuplicate       IntegrityIssueKind = "index_duplicate_record"
	IntegrityIndexValueMismatch   IntegrityIssueKind = "index_value_mismatch"
	IntegrityRevisionMissing      IntegrityIssueKind = "revision_history_missing"
	IntegrityRevisionCount        IntegrityIssueKind = "revision_count_mismatch"
	IntegrityRevisionCurrent      IntegrityIssueKind = "revision_current_mismatch"
	IntegrityRevisionSequence     IntegrityIssueKind = "revision_sequence_gap"
	IntegrityRevisionDuplicateID  IntegrityIssueKind = "revision_duplicate_id"
	IntegrityPackageProvenance    IntegrityIssueKind = "package_provenance_incomplete"
)

type IntegrityScanRequest struct {
	IssueLimit int
}

type IntegrityIssue struct {
	Kind       IntegrityIssueKind `json:"kind"`
	ObjectKind string             `json:"object_kind"`
	ObjectID   string             `json:"object_id"`
	RelatedID  string             `json:"related_id,omitempty"`
}

type IntegrityReport struct {
	Scanned          memoryStoreAPI.ScanStats `json:"scanned"`
	RevisionsScanned uint64                   `json:"revisions_scanned"`
	IndexesChecked   uint64                   `json:"indexes_checked"`
	PackagesChecked  uint64                   `json:"packages_checked"`
	Issues           []IntegrityIssue         `json:"issues"`
	IssueCount       int                      `json:"issue_count"`
	Truncated        bool                     `json:"truncated"`
}

func (s *Service) ScanIntegrity(ctx context.Context, request IntegrityScanRequest) (IntegrityReport, error) {
	if err := ctx.Err(); err != nil {
		return IntegrityReport{}, err
	}
	if request.IssueLimit <= 0 {
		request.IssueLimit = defaultIntegrityIssueLimit
	}
	if request.IssueLimit > maxIntegrityIssueLimit {
		return IntegrityReport{}, fmt.Errorf("integrity issue limit must not exceed %d", maxIntegrityIssueLimit)
	}
	if err := s.authorizeOperational(ctx, OperationalIntegrityScan); err != nil {
		return IntegrityReport{}, err
	}
	maintenance, ok := s.store.(memoryStoreAPI.MaintenanceStore)
	if !ok {
		return IntegrityReport{}, memoryStoreAPI.ErrUnsupported
	}
	checkpoint := s.MutationCheckpoint()
	chunks := make(map[memory.ChunkID]memory.Chunk)
	entries := make(map[memory.EntryID]memory.Entry)
	links := make(map[memory.LinkID]memory.Link)
	evidence := make(map[memory.EvidenceID]memory.Evidence)
	issues := make([]IntegrityIssue, 0)
	stats, err := maintenance.ScanCanonical(ctx, func(record memoryStoreAPI.CanonicalRecord) error {
		if recordErr := record.Validate(); recordErr != nil {
			issues = append(issues, IntegrityIssue{Kind: IntegrityInvalidRecord, ObjectKind: string(record.Kind), ObjectID: record.ID()})
			return nil
		}
		switch record.Kind {
		case memoryStoreAPI.RecordKindChunk:
			chunks[record.Chunk.ID] = *record.Chunk
		case memoryStoreAPI.RecordKindEntry:
			entries[record.Entry.ID] = *record.Entry
		case memoryStoreAPI.RecordKindLink:
			links[record.Link.ID] = *record.Link
		case memoryStoreAPI.RecordKindEvidence:
			evidence[record.Evidence.ID] = *record.Evidence
		}
		return nil
	})
	if err != nil {
		return IntegrityReport{}, fmt.Errorf("scan canonical memory integrity: %w", err)
	}
	issues = append(issues, deriveIntegrityIssues(chunks, entries, links, evidence)...)
	revisionIssues, revisionsScanned, initialActors, err := s.scanRevisionIntegrity(ctx, chunks, entries, links)
	if err != nil {
		return IntegrityReport{}, err
	}
	issues = append(issues, revisionIssues...)
	indexIssues, indexesChecked, err := s.scanIndexIntegrity(ctx, chunks, entries, links, evidence)
	if err != nil {
		return IntegrityReport{}, err
	}
	issues = append(issues, indexIssues...)
	packageIssues, packagesChecked := derivePackageProvenanceIssues(chunks, entries, links, evidence, initialActors)
	issues = append(issues, packageIssues...)
	if current := s.MutationCheckpoint(); current != checkpoint {
		return IntegrityReport{}, fmt.Errorf("%w: memory changed during integrity scan", memoryStoreAPI.ErrConflict)
	}
	slices.SortFunc(issues, compareIntegrityIssues)
	report := IntegrityReport{
		Scanned: stats, RevisionsScanned: revisionsScanned, IndexesChecked: indexesChecked,
		PackagesChecked: packagesChecked, IssueCount: len(issues),
	}
	report.Truncated = len(issues) > request.IssueLimit
	report.Issues = slices.Clone(issues[:min(len(issues), request.IssueLimit)])
	return report, nil
}

func deriveIntegrityIssues(chunks map[memory.ChunkID]memory.Chunk, entries map[memory.EntryID]memory.Entry, links map[memory.LinkID]memory.Link, evidence map[memory.EvidenceID]memory.Evidence) []IntegrityIssue {
	issues := make([]IntegrityIssue, 0)
	referencedEvidence := make(map[memory.EvidenceID]struct{})
	addMissingEvidence := func(kind, id string, ids ...[]memory.EvidenceID) {
		seen := make(map[memory.EvidenceID]struct{})
		for _, group := range ids {
			for _, evidenceID := range group {
				if _, duplicate := seen[evidenceID]; duplicate {
					continue
				}
				seen[evidenceID] = struct{}{}
				referencedEvidence[evidenceID] = struct{}{}
				if _, exists := evidence[evidenceID]; !exists {
					issues = append(issues, IntegrityIssue{Kind: IntegrityMissingEvidence, ObjectKind: kind, ObjectID: id, RelatedID: string(evidenceID)})
				}
			}
		}
	}
	for _, chunk := range chunks {
		for _, dependencyID := range chunk.DependencyIDs {
			if _, exists := chunks[dependencyID]; !exists {
				issues = append(issues, IntegrityIssue{Kind: IntegrityMissingDependency, ObjectKind: "chunk", ObjectID: string(chunk.ID), RelatedID: string(dependencyID)})
			}
		}
	}
	for _, entry := range entries {
		if _, exists := chunks[entry.ChunkID]; !exists {
			issues = append(issues, IntegrityIssue{Kind: IntegrityOrphanEntry, ObjectKind: "entry", ObjectID: string(entry.ID), RelatedID: string(entry.ChunkID)})
		}
		if entry.SupersededByID != "" {
			if _, exists := entries[entry.SupersededByID]; !exists {
				issues = append(issues, IntegrityIssue{Kind: IntegrityBrokenSupersession, ObjectKind: "entry", ObjectID: string(entry.ID), RelatedID: string(entry.SupersededByID)})
			}
		}
		addMissingEvidence("entry", string(entry.ID), entry.EvidenceIDs, entry.Verification.EvidenceIDs)
	}
	seenLinks := make(map[string]memory.LinkID)
	linkIDs := make([]memory.LinkID, 0, len(links))
	for id := range links {
		linkIDs = append(linkIDs, id)
	}
	slices.Sort(linkIDs)
	for _, linkID := range linkIDs {
		link := links[linkID]
		if !integrityEndpointExists(link.Source, chunks, entries) {
			issues = append(issues, IntegrityIssue{Kind: IntegrityBrokenLinkSource, ObjectKind: "link", ObjectID: string(link.ID), RelatedID: link.Source.ID})
		}
		if !integrityEndpointExists(link.Target, chunks, entries) {
			issues = append(issues, IntegrityIssue{Kind: IntegrityBrokenLinkTarget, ObjectKind: "link", ObjectID: string(link.ID), RelatedID: link.Target.ID})
		}
		addMissingEvidence("link", string(link.ID), link.EvidenceIDs)
		normalized := memory.NormalizeLink(link)
		key := fmt.Sprintf("%d\x00%s\x00%d\x00%s\x00%d", normalized.Source.Kind, normalized.Source.ID, normalized.Target.Kind, normalized.Target.ID, normalized.Kind)
		if prior, exists := seenLinks[key]; exists {
			issues = append(issues, IntegrityIssue{Kind: IntegrityDuplicateLink, ObjectKind: "link", ObjectID: string(link.ID), RelatedID: string(prior)})
		} else {
			seenLinks[key] = link.ID
		}
	}
	chunkValues := make([]memory.Chunk, 0, len(chunks))
	entryValues := make([]memory.Entry, 0, len(entries))
	linkValues := make([]memory.Link, 0, len(links))
	evidenceValues := make([]memory.Evidence, 0, len(evidence))
	for _, value := range chunks {
		chunkValues = append(chunkValues, value)
	}
	for _, value := range entries {
		entryValues = append(entryValues, value)
	}
	for _, value := range links {
		linkValues = append(linkValues, value)
	}
	for _, value := range evidence {
		evidenceValues = append(evidenceValues, value)
	}
	wantCounts := memoryStoreAPI.DeriveChunkCounts(chunkValues, entryValues, linkValues, evidenceValues)
	for id, chunk := range chunks {
		if chunk.Counts != wantCounts[id] {
			issues = append(issues, IntegrityIssue{Kind: IntegrityChunkCountMismatch, ObjectKind: "chunk", ObjectID: string(id)})
		}
	}
	for id := range evidence {
		if _, referenced := referencedEvidence[id]; !referenced {
			issues = append(issues, IntegrityIssue{Kind: IntegrityUnreferencedEvidence, ObjectKind: "evidence", ObjectID: string(id)})
		}
	}
	slices.SortFunc(issues, compareIntegrityIssues)
	return issues
}

func compareIntegrityIssues(left, right IntegrityIssue) int {
	if order := strings.Compare(string(left.Kind), string(right.Kind)); order != 0 {
		return order
	}
	if order := strings.Compare(left.ObjectKind, right.ObjectKind); order != 0 {
		return order
	}
	if order := strings.Compare(left.ObjectID, right.ObjectID); order != 0 {
		return order
	}
	return strings.Compare(left.RelatedID, right.RelatedID)
}

func (s *Service) scanRevisionIntegrity(ctx context.Context, chunks map[memory.ChunkID]memory.Chunk, entries map[memory.EntryID]memory.Entry, links map[memory.LinkID]memory.Link) ([]IntegrityIssue, uint64, map[memory.ObjectRef]memory.Actor, error) {
	type revisionOwner struct {
		ref      memory.ObjectRef
		revision memory.Revision
	}
	owners := make([]revisionOwner, 0, len(chunks)+len(entries)+len(links))
	for _, chunk := range chunks {
		owners = append(owners, revisionOwner{ref: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunk.ID)}, revision: chunk.Revision})
	}
	for _, entry := range entries {
		owners = append(owners, revisionOwner{ref: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entry.ID)}, revision: entry.Revision})
	}
	for _, link := range links {
		owners = append(owners, revisionOwner{ref: memory.ObjectRef{Kind: memory.ObjectKindLink, ID: string(link.ID)}, revision: link.Revision})
	}
	slices.SortFunc(owners, func(left, right revisionOwner) int {
		if left.ref.Kind != right.ref.Kind {
			return int(left.ref.Kind) - int(right.ref.Kind)
		}
		return strings.Compare(left.ref.ID, right.ref.ID)
	})
	issues := make([]IntegrityIssue, 0)
	initialActors := make(map[memory.ObjectRef]memory.Actor, len(owners))
	var scanned uint64
	for _, owner := range owners {
		revisions, err := s.allRevisions(ctx, owner.ref)
		if errors.Is(err, memoryStoreAPI.ErrNotFound) {
			issues = append(issues, IntegrityIssue{Kind: IntegrityRevisionMissing, ObjectKind: owner.ref.Kind.String(), ObjectID: owner.ref.ID})
			continue
		}
		if err != nil {
			return nil, scanned, nil, fmt.Errorf("scan %s %s revision integrity: %w", owner.ref.Kind, owner.ref.ID, err)
		}
		scanned += uint64(len(revisions))
		if len(revisions) == 0 {
			issues = append(issues, IntegrityIssue{Kind: IntegrityRevisionMissing, ObjectKind: owner.ref.Kind.String(), ObjectID: owner.ref.ID})
			continue
		}
		initialActors[owner.ref] = revisions[len(revisions)-1].RevisionMetadata().Actor
		if uint64(len(revisions)) != owner.revision.Number {
			issues = append(issues, IntegrityIssue{Kind: IntegrityRevisionCount, ObjectKind: owner.ref.Kind.String(), ObjectID: owner.ref.ID})
		}
		if revisions[0].RevisionMetadata() != owner.revision {
			issues = append(issues, IntegrityIssue{Kind: IntegrityRevisionCurrent, ObjectKind: owner.ref.Kind.String(), ObjectID: owner.ref.ID})
		}
		seenIDs := make(map[memory.RevisionID]struct{}, len(revisions))
		for index, record := range revisions {
			metadata := record.RevisionMetadata()
			wantNumber := uint64(0)
			if uint64(index) < owner.revision.Number {
				wantNumber = owner.revision.Number - uint64(index)
			}
			if metadata.Number != wantNumber {
				issues = append(issues, IntegrityIssue{Kind: IntegrityRevisionSequence, ObjectKind: owner.ref.Kind.String(), ObjectID: owner.ref.ID, RelatedID: string(metadata.ID)})
			}
			if _, duplicate := seenIDs[metadata.ID]; duplicate {
				issues = append(issues, IntegrityIssue{Kind: IntegrityRevisionDuplicateID, ObjectKind: owner.ref.Kind.String(), ObjectID: owner.ref.ID, RelatedID: string(metadata.ID)})
			}
			seenIDs[metadata.ID] = struct{}{}
		}
	}
	return issues, scanned, initialActors, nil
}

func (s *Service) allRevisions(ctx context.Context, object memory.ObjectRef) ([]memoryStoreAPI.CanonicalRecord, error) {
	result := make([]memoryStoreAPI.CanonicalRecord, 0)
	cursor := ""
	for {
		page, err := s.store.ListRevisions(ctx, memoryStoreAPI.RevisionListRequest{Object: object, Limit: 200, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		result = append(result, page.Revisions...)
		if page.NextCursor == "" {
			return result, nil
		}
		cursor = page.NextCursor
	}
}

func (s *Service) scanIndexIntegrity(ctx context.Context, chunks map[memory.ChunkID]memory.Chunk, entries map[memory.EntryID]memory.Entry, links map[memory.LinkID]memory.Link, evidence map[memory.EvidenceID]memory.Evidence) ([]IntegrityIssue, uint64, error) {
	issues := make([]IntegrityIssue, 0)
	var checked uint64
	for _, sortKind := range []memoryStoreAPI.ChunkSort{
		memoryStoreAPI.ChunkSortTitle, memoryStoreAPI.ChunkSortCreatedAt, memoryStoreAPI.ChunkSortUpdatedAt, memoryStoreAPI.ChunkSortLastUsedAt,
	} {
		indexed, duplicates, err := s.indexedChunkIDs(ctx, sortKind)
		if err != nil {
			return nil, checked, fmt.Errorf("scan chunk index %s: %w", sortKind, err)
		}
		checked += uint64(len(indexed))
		issues = append(issues, compareIndexedIDs("chunk", string(sortKind), chunkStringIDs(chunks), indexed, duplicates)...)
	}
	for _, sortKind := range []memoryStoreAPI.EntrySort{
		memoryStoreAPI.EntrySortTitle, memoryStoreAPI.EntrySortCreatedAt, memoryStoreAPI.EntrySortUpdatedAt,
		memoryStoreAPI.EntrySortLastUsedAt, memoryStoreAPI.EntrySortReviewAfter,
	} {
		indexed, duplicates, err := s.indexedEntryIDs(ctx, sortKind)
		if err != nil {
			return nil, checked, fmt.Errorf("scan entry index %s: %w", sortKind, err)
		}
		checked += uint64(len(indexed))
		issues = append(issues, compareIndexedIDs("entry", string(sortKind), entryStringIDs(entries), indexed, duplicates)...)
	}
	adjacencyIssues, adjacencyChecked, err := s.scanAdjacencyIndexes(ctx, chunks, entries, links)
	if err != nil {
		return nil, checked, err
	}
	issues = append(issues, adjacencyIssues...)
	checked += adjacencyChecked
	searchIssues, searchChecked, err := s.scanSearchIndexes(ctx, chunks, entries, links, evidence)
	if err != nil {
		return nil, checked, err
	}
	issues = append(issues, searchIssues...)
	return issues, checked + searchChecked, nil
}

func compareIndexedIDs(objectKind, indexName string, canonical, indexed map[string]struct{}, duplicates []string) []IntegrityIssue {
	issues := make([]IntegrityIssue, 0)
	for _, id := range duplicates {
		issues = append(issues, IntegrityIssue{Kind: IntegrityIndexDuplicate, ObjectKind: objectKind, ObjectID: id, RelatedID: indexName})
	}
	for id := range canonical {
		if _, exists := indexed[id]; !exists {
			issues = append(issues, IntegrityIssue{Kind: IntegrityIndexMissing, ObjectKind: objectKind, ObjectID: id, RelatedID: indexName})
		}
	}
	for id := range indexed {
		if _, exists := canonical[id]; !exists {
			issues = append(issues, IntegrityIssue{Kind: IntegrityIndexUnexpected, ObjectKind: objectKind, ObjectID: id, RelatedID: indexName})
		}
	}
	return issues
}

func chunkStringIDs(values map[memory.ChunkID]memory.Chunk) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for id := range values {
		result[string(id)] = struct{}{}
	}
	return result
}

func entryStringIDs(values map[memory.EntryID]memory.Entry) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for id := range values {
		result[string(id)] = struct{}{}
	}
	return result
}

func (s *Service) indexedChunkIDs(ctx context.Context, sortKind memoryStoreAPI.ChunkSort) (map[string]struct{}, []string, error) {
	ids := make(map[string]struct{})
	duplicates := make([]string, 0)
	cursor := ""
	for {
		page, err := s.store.ListChunks(ctx, memoryStoreAPI.ChunkListRequest{Sort: sortKind, Limit: 200, Cursor: cursor})
		if err != nil {
			return nil, nil, err
		}
		for _, chunk := range page.Chunks {
			id := string(chunk.ID)
			if _, exists := ids[id]; exists {
				duplicates = append(duplicates, id)
			}
			ids[id] = struct{}{}
		}
		if page.NextCursor == "" {
			return ids, duplicates, nil
		}
		cursor = page.NextCursor
	}
}

func (s *Service) indexedEntryIDs(ctx context.Context, sortKind memoryStoreAPI.EntrySort) (map[string]struct{}, []string, error) {
	ids := make(map[string]struct{})
	duplicates := make([]string, 0)
	cursor := ""
	for {
		page, err := s.store.ListEntries(ctx, memoryStoreAPI.EntryListRequest{Sort: sortKind, Limit: 200, Cursor: cursor})
		if err != nil {
			return nil, nil, err
		}
		for _, entry := range page.Entries {
			id := string(entry.ID)
			if _, exists := ids[id]; exists {
				duplicates = append(duplicates, id)
			}
			ids[id] = struct{}{}
		}
		if page.NextCursor == "" {
			return ids, duplicates, nil
		}
		cursor = page.NextCursor
	}
}

func (s *Service) scanAdjacencyIndexes(ctx context.Context, chunks map[memory.ChunkID]memory.Chunk, entries map[memory.EntryID]memory.Entry, links map[memory.LinkID]memory.Link) ([]IntegrityIssue, uint64, error) {
	expected := make(map[memory.ObjectRef]map[memory.LinkID]struct{}, len(chunks)+len(entries))
	for id := range chunks {
		expected[memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(id)}] = make(map[memory.LinkID]struct{})
	}
	for id := range entries {
		expected[memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(id)}] = make(map[memory.LinkID]struct{})
	}
	for id, link := range links {
		if values, exists := expected[link.Source]; exists {
			values[id] = struct{}{}
		}
		if values, exists := expected[link.Target]; exists {
			values[id] = struct{}{}
		}
	}
	endpoints := make([]memory.ObjectRef, 0, len(expected))
	for endpoint := range expected {
		endpoints = append(endpoints, endpoint)
	}
	slices.SortFunc(endpoints, func(left, right memory.ObjectRef) int {
		if left.Kind != right.Kind {
			return int(left.Kind) - int(right.Kind)
		}
		return strings.Compare(left.ID, right.ID)
	})
	issues := make([]IntegrityIssue, 0)
	var checked uint64
	for _, endpoint := range endpoints {
		actual := make(map[memory.LinkID]struct{})
		cursor := ""
		for {
			page, err := s.store.ListAdjacentLinks(ctx, memoryStoreAPI.AdjacentLinkListRequest{
				Filter: memoryStoreAPI.AdjacentLinkFilter{Endpoint: endpoint, Direction: memoryStoreAPI.LinkDirectionBoth},
				Limit:  100, Cursor: cursor,
			})
			if err != nil {
				return nil, checked, fmt.Errorf("scan adjacency index for %s %s: %w", endpoint.Kind, endpoint.ID, err)
			}
			for _, link := range page.Links {
				checked++
				if _, duplicate := actual[link.ID]; duplicate {
					issues = append(issues, IntegrityIssue{Kind: IntegrityIndexDuplicate, ObjectKind: "link", ObjectID: string(link.ID), RelatedID: endpoint.ID})
				}
				actual[link.ID] = struct{}{}
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		for linkID := range expected[endpoint] {
			if _, exists := actual[linkID]; !exists {
				issues = append(issues, IntegrityIssue{Kind: IntegrityIndexMissing, ObjectKind: "link", ObjectID: string(linkID), RelatedID: endpoint.ID})
			}
		}
		for linkID := range actual {
			if _, exists := expected[endpoint][linkID]; !exists {
				issues = append(issues, IntegrityIssue{Kind: IntegrityIndexUnexpected, ObjectKind: "link", ObjectID: string(linkID), RelatedID: endpoint.ID})
			}
		}
	}
	return issues, checked, nil
}

func (s *Service) scanSearchIndexes(ctx context.Context, chunks map[memory.ChunkID]memory.Chunk, entries map[memory.EntryID]memory.Entry, links map[memory.LinkID]memory.Link, evidence map[memory.EvidenceID]memory.Evidence) ([]IntegrityIssue, uint64, error) {
	records := make([]memoryStoreAPI.CanonicalRecord, 0, len(chunks)+len(entries)+len(links)+len(evidence))
	for _, value := range chunks {
		value := value
		records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindChunk, Chunk: &value})
	}
	for _, value := range entries {
		value := value
		records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindEntry, Entry: &value})
	}
	for _, value := range links {
		value := value
		records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindLink, Link: &value})
	}
	for _, value := range evidence {
		value := value
		records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindEvidence, Evidence: &value})
	}
	slices.SortFunc(records, func(left, right memoryStoreAPI.CanonicalRecord) int {
		if left.Kind != right.Kind {
			return strings.Compare(string(left.Kind), string(right.Kind))
		}
		return strings.Compare(left.ID(), right.ID())
	})
	issues := make([]IntegrityIssue, 0)
	var checked uint64
	for _, record := range records {
		page, err := s.store.SearchExact(ctx, memoryStoreAPI.ExactSearchRequest{Query: record.ID(), Kinds: []memoryStoreAPI.RecordKind{record.Kind}, Limit: 100})
		if err != nil {
			return nil, checked, fmt.Errorf("scan exact index for %s %s: %w", record.Kind, record.ID(), err)
		}
		checked++
		matches := 0
		for _, hit := range page.Hits {
			if hit.Record.Kind == record.Kind && hit.Record.ID() == record.ID() {
				matches++
			} else {
				issues = append(issues, IntegrityIssue{Kind: IntegrityIndexUnexpected, ObjectKind: string(hit.Record.Kind), ObjectID: hit.Record.ID(), RelatedID: "exact:" + record.ID()})
			}
		}
		switch {
		case matches == 0:
			issues = append(issues, IntegrityIssue{Kind: IntegrityIndexMissing, ObjectKind: string(record.Kind), ObjectID: record.ID(), RelatedID: "exact:id"})
		case matches > 1:
			issues = append(issues, IntegrityIssue{Kind: IntegrityIndexDuplicate, ObjectKind: string(record.Kind), ObjectID: record.ID(), RelatedID: "exact:id"})
		}
	}
	lexicalIssues, lexicalChecked, err := s.scanLexicalIndex(ctx, entries)
	if err != nil {
		return nil, checked, err
	}
	issues = append(issues, lexicalIssues...)
	checked += lexicalChecked
	err = s.store.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		ids := make([]memory.EvidenceID, 0, len(evidence))
		for id := range evidence {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			item := evidence[id]
			indexed, err := tx.EvidenceBySource(ctx, item.Source.ID, item.Source.ContentHash)
			checked++
			if errors.Is(err, memoryStoreAPI.ErrNotFound) {
				issues = append(issues, IntegrityIssue{Kind: IntegrityIndexMissing, ObjectKind: "evidence", ObjectID: string(id), RelatedID: "source"})
				continue
			}
			if err != nil {
				return err
			}
			if indexed.ID != id {
				issues = append(issues, IntegrityIssue{Kind: IntegrityIndexUnexpected, ObjectKind: "evidence", ObjectID: string(indexed.ID), RelatedID: string(id)})
			}
		}
		return nil
	})
	if err != nil {
		return nil, checked, fmt.Errorf("scan evidence source index: %w", err)
	}
	return issues, checked, nil
}

func (s *Service) scanLexicalIndex(ctx context.Context, entries map[memory.EntryID]memory.Entry) ([]IntegrityIssue, uint64, error) {
	expected := make(map[string]memoryStoreAPI.LexicalPosting)
	termSet := make(map[string]struct{})
	for _, entry := range entries {
		for _, posting := range memoryStoreAPI.EntryLexicalPostings(entry) {
			expected[lexicalIntegrityKey(posting.Term, posting.EntryID)] = posting
			termSet[posting.Term] = struct{}{}
		}
	}
	terms := make([]string, 0, len(termSet))
	for term := range termSet {
		terms = append(terms, term)
	}
	slices.Sort(terms)
	actual := make(map[string]memoryStoreAPI.LexicalPosting, len(expected))
	issues := make([]IntegrityIssue, 0)
	var checked uint64
	for start := 0; start < len(terms); start += 64 {
		batch := terms[start:min(start+64, len(terms))]
		cursor := ""
		documentCountChecked := false
		for {
			page, err := s.store.LookupLexicalPostings(ctx, memoryStoreAPI.LexicalPostingRequest{Terms: batch, Limit: 1000, Cursor: cursor})
			if err != nil {
				return nil, checked, fmt.Errorf("scan lexical index: %w", err)
			}
			if !documentCountChecked && page.DocumentCount != uint64(len(entries)) {
				issues = append(issues, IntegrityIssue{Kind: IntegrityIndexValueMismatch, ObjectKind: "lexical", ObjectID: "document_count"})
			}
			documentCountChecked = true
			for _, posting := range page.Postings {
				checked++
				key := lexicalIntegrityKey(posting.Term, posting.EntryID)
				if _, duplicate := actual[key]; duplicate {
					issues = append(issues, IntegrityIssue{Kind: IntegrityIndexDuplicate, ObjectKind: "entry", ObjectID: string(posting.EntryID), RelatedID: posting.Term})
				}
				actual[key] = posting
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
	}
	for key, posting := range expected {
		indexed, exists := actual[key]
		switch {
		case !exists:
			issues = append(issues, IntegrityIssue{Kind: IntegrityIndexMissing, ObjectKind: "entry", ObjectID: string(posting.EntryID), RelatedID: posting.Term})
		case indexed.Frequencies != posting.Frequencies:
			issues = append(issues, IntegrityIssue{Kind: IntegrityIndexValueMismatch, ObjectKind: "entry", ObjectID: string(posting.EntryID), RelatedID: posting.Term})
		}
	}
	for key, posting := range actual {
		if _, exists := expected[key]; !exists {
			issues = append(issues, IntegrityIssue{Kind: IntegrityIndexUnexpected, ObjectKind: "entry", ObjectID: string(posting.EntryID), RelatedID: posting.Term})
		}
	}
	return issues, checked, nil
}

func lexicalIntegrityKey(term string, entryID memory.EntryID) string {
	return term + "\x00" + string(entryID)
}

func derivePackageProvenanceIssues(chunks map[memory.ChunkID]memory.Chunk, entries map[memory.EntryID]memory.Entry, links map[memory.LinkID]memory.Link, evidence map[memory.EvidenceID]memory.Evidence, initialActors map[memory.ObjectRef]memory.Actor) ([]IntegrityIssue, uint64) {
	issues := make([]IntegrityIssue, 0)
	packageByChunk := make(map[memory.ChunkID]string)
	var checked uint64
	for id, chunk := range chunks {
		ref := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(id)}
		initial := initialActors[ref]
		packaged := initial.Kind == memory.ActorKindImport || chunk.Publisher.ID != "" || chunk.Publisher.Name != "" || chunk.License != ""
		if !packaged {
			continue
		}
		checked++
		packageByChunk[id] = initial.ID
		validPackageID := (memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: initial.ID}).Validate() == nil
		if initial.Kind != memory.ActorKindImport || !validPackageID || strings.TrimSpace(chunk.Publisher.ID) == "" || strings.TrimSpace(chunk.Publisher.Name) == "" || strings.TrimSpace(chunk.License) == "" {
			issues = append(issues, IntegrityIssue{Kind: IntegrityPackageProvenance, ObjectKind: "chunk", ObjectID: string(id), RelatedID: initial.ID})
		}
	}
	for id, entry := range entries {
		initial := initialActors[memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(id)}]
		if initial.Kind != memory.ActorKindImport {
			continue
		}
		if packageByChunk[entry.ChunkID] == "" || packageByChunk[entry.ChunkID] != initial.ID {
			issues = append(issues, IntegrityIssue{Kind: IntegrityPackageProvenance, ObjectKind: "entry", ObjectID: string(id), RelatedID: initial.ID})
		}
	}
	for id, link := range links {
		initial := initialActors[memory.ObjectRef{Kind: memory.ObjectKindLink, ID: string(id)}]
		if initial.Kind != memory.ActorKindImport {
			continue
		}
		matches := false
		for _, endpoint := range []memory.ObjectRef{link.Source, link.Target} {
			chunkID := endpointChunkID(endpoint, entries)
			matches = matches || packageByChunk[chunkID] == initial.ID
		}
		if !matches {
			issues = append(issues, IntegrityIssue{Kind: IntegrityPackageProvenance, ObjectKind: "link", ObjectID: string(id), RelatedID: initial.ID})
		}
	}
	referencedPackages := make(map[memory.EvidenceID]map[string]struct{})
	addEvidencePackages := func(ids []memory.EvidenceID, packageID string) {
		if packageID == "" {
			return
		}
		for _, evidenceID := range ids {
			if referencedPackages[evidenceID] == nil {
				referencedPackages[evidenceID] = make(map[string]struct{})
			}
			referencedPackages[evidenceID][packageID] = struct{}{}
		}
	}
	for _, entry := range entries {
		addEvidencePackages(entry.EvidenceIDs, packageByChunk[entry.ChunkID])
		addEvidencePackages(entry.Verification.EvidenceIDs, packageByChunk[entry.ChunkID])
	}
	for _, link := range links {
		for _, endpoint := range []memory.ObjectRef{link.Source, link.Target} {
			addEvidencePackages(link.EvidenceIDs, packageByChunk[endpointChunkID(endpoint, entries)])
		}
	}
	for id, item := range evidence {
		if item.Actor.Kind != memory.ActorKindImport {
			continue
		}
		if _, matches := referencedPackages[id][item.Actor.ID]; !matches {
			issues = append(issues, IntegrityIssue{Kind: IntegrityPackageProvenance, ObjectKind: "evidence", ObjectID: string(id), RelatedID: item.Actor.ID})
		}
	}
	return issues, checked
}

func endpointChunkID(endpoint memory.ObjectRef, entries map[memory.EntryID]memory.Entry) memory.ChunkID {
	switch endpoint.Kind {
	case memory.ObjectKindChunk:
		return memory.ChunkID(endpoint.ID)
	case memory.ObjectKindEntry:
		return entries[memory.EntryID(endpoint.ID)].ChunkID
	default:
		return ""
	}
}

func integrityEndpointExists(endpoint memory.ObjectRef, chunks map[memory.ChunkID]memory.Chunk, entries map[memory.EntryID]memory.Entry) bool {
	switch endpoint.Kind {
	case memory.ObjectKindChunk:
		_, exists := chunks[memory.ChunkID(endpoint.ID)]
		return exists
	case memory.ObjectKindEntry:
		_, exists := entries[memory.EntryID(endpoint.ID)]
		return exists
	default:
		return false
	}
}
