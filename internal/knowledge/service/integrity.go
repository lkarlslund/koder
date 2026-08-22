package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
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
	Scanned          knowledgeStore.ScanStats `json:"scanned"`
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
	maintenance, ok := s.store.(knowledgeStore.MaintenanceStore)
	if !ok {
		return IntegrityReport{}, knowledgeStore.ErrUnsupported
	}
	checkpoint := s.MutationCheckpoint()
	chunks := make(map[knowledge.ChunkID]knowledge.Chunk)
	entries := make(map[knowledge.EntryID]knowledge.Entry)
	links := make(map[knowledge.LinkID]knowledge.Link)
	evidence := make(map[knowledge.EvidenceID]knowledge.Evidence)
	issues := make([]IntegrityIssue, 0)
	stats, err := maintenance.ScanCanonical(ctx, func(record knowledgeStore.CanonicalRecord) error {
		if recordErr := record.Validate(); recordErr != nil {
			issues = append(issues, IntegrityIssue{Kind: IntegrityInvalidRecord, ObjectKind: string(record.Kind), ObjectID: record.ID()})
			return nil
		}
		switch record.Kind {
		case knowledgeStore.RecordKindChunk:
			chunks[record.Chunk.ID] = *record.Chunk
		case knowledgeStore.RecordKindEntry:
			entries[record.Entry.ID] = *record.Entry
		case knowledgeStore.RecordKindLink:
			links[record.Link.ID] = *record.Link
		case knowledgeStore.RecordKindEvidence:
			evidence[record.Evidence.ID] = *record.Evidence
		}
		return nil
	})
	if err != nil {
		return IntegrityReport{}, fmt.Errorf("scan canonical knowledge integrity: %w", err)
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
		return IntegrityReport{}, fmt.Errorf("%w: knowledge changed during integrity scan", knowledgeStore.ErrConflict)
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

func deriveIntegrityIssues(chunks map[knowledge.ChunkID]knowledge.Chunk, entries map[knowledge.EntryID]knowledge.Entry, links map[knowledge.LinkID]knowledge.Link, evidence map[knowledge.EvidenceID]knowledge.Evidence) []IntegrityIssue {
	issues := make([]IntegrityIssue, 0)
	referencedEvidence := make(map[knowledge.EvidenceID]struct{})
	addMissingEvidence := func(kind, id string, ids ...[]knowledge.EvidenceID) {
		seen := make(map[knowledge.EvidenceID]struct{})
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
	seenLinks := make(map[string]knowledge.LinkID)
	linkIDs := make([]knowledge.LinkID, 0, len(links))
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
		normalized := knowledge.NormalizeLink(link)
		key := fmt.Sprintf("%d\x00%s\x00%d\x00%s\x00%d", normalized.Source.Kind, normalized.Source.ID, normalized.Target.Kind, normalized.Target.ID, normalized.Kind)
		if prior, exists := seenLinks[key]; exists {
			issues = append(issues, IntegrityIssue{Kind: IntegrityDuplicateLink, ObjectKind: "link", ObjectID: string(link.ID), RelatedID: string(prior)})
		} else {
			seenLinks[key] = link.ID
		}
	}
	chunkValues := make([]knowledge.Chunk, 0, len(chunks))
	entryValues := make([]knowledge.Entry, 0, len(entries))
	linkValues := make([]knowledge.Link, 0, len(links))
	evidenceValues := make([]knowledge.Evidence, 0, len(evidence))
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
	wantCounts := knowledgeStore.DeriveChunkCounts(chunkValues, entryValues, linkValues, evidenceValues)
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

func (s *Service) scanRevisionIntegrity(ctx context.Context, chunks map[knowledge.ChunkID]knowledge.Chunk, entries map[knowledge.EntryID]knowledge.Entry, links map[knowledge.LinkID]knowledge.Link) ([]IntegrityIssue, uint64, map[knowledge.ObjectRef]knowledge.Actor, error) {
	type revisionOwner struct {
		ref      knowledge.ObjectRef
		revision knowledge.Revision
	}
	owners := make([]revisionOwner, 0, len(chunks)+len(entries)+len(links))
	for _, chunk := range chunks {
		owners = append(owners, revisionOwner{ref: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(chunk.ID)}, revision: chunk.Revision})
	}
	for _, entry := range entries {
		owners = append(owners, revisionOwner{ref: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.ID)}, revision: entry.Revision})
	}
	for _, link := range links {
		owners = append(owners, revisionOwner{ref: knowledge.ObjectRef{Kind: knowledge.ObjectKindLink, ID: string(link.ID)}, revision: link.Revision})
	}
	slices.SortFunc(owners, func(left, right revisionOwner) int {
		if left.ref.Kind != right.ref.Kind {
			return int(left.ref.Kind) - int(right.ref.Kind)
		}
		return strings.Compare(left.ref.ID, right.ref.ID)
	})
	issues := make([]IntegrityIssue, 0)
	initialActors := make(map[knowledge.ObjectRef]knowledge.Actor, len(owners))
	var scanned uint64
	for _, owner := range owners {
		revisions, err := s.allRevisions(ctx, owner.ref)
		if errors.Is(err, knowledgeStore.ErrNotFound) {
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
		seenIDs := make(map[knowledge.RevisionID]struct{}, len(revisions))
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

func (s *Service) allRevisions(ctx context.Context, object knowledge.ObjectRef) ([]knowledgeStore.CanonicalRecord, error) {
	result := make([]knowledgeStore.CanonicalRecord, 0)
	cursor := ""
	for {
		page, err := s.store.ListRevisions(ctx, knowledgeStore.RevisionListRequest{Object: object, Limit: 200, Cursor: cursor})
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

func (s *Service) scanIndexIntegrity(ctx context.Context, chunks map[knowledge.ChunkID]knowledge.Chunk, entries map[knowledge.EntryID]knowledge.Entry, links map[knowledge.LinkID]knowledge.Link, evidence map[knowledge.EvidenceID]knowledge.Evidence) ([]IntegrityIssue, uint64, error) {
	issues := make([]IntegrityIssue, 0)
	var checked uint64
	for _, sortKind := range []knowledgeStore.ChunkSort{
		knowledgeStore.ChunkSortTitle, knowledgeStore.ChunkSortCreatedAt, knowledgeStore.ChunkSortUpdatedAt, knowledgeStore.ChunkSortLastUsedAt,
	} {
		indexed, duplicates, err := s.indexedChunkIDs(ctx, sortKind)
		if err != nil {
			return nil, checked, fmt.Errorf("scan chunk index %s: %w", sortKind, err)
		}
		checked += uint64(len(indexed))
		issues = append(issues, compareIndexedIDs("chunk", string(sortKind), chunkStringIDs(chunks), indexed, duplicates)...)
	}
	for _, sortKind := range []knowledgeStore.EntrySort{
		knowledgeStore.EntrySortTitle, knowledgeStore.EntrySortCreatedAt, knowledgeStore.EntrySortUpdatedAt,
		knowledgeStore.EntrySortLastUsedAt, knowledgeStore.EntrySortReviewAfter,
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

func chunkStringIDs(values map[knowledge.ChunkID]knowledge.Chunk) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for id := range values {
		result[string(id)] = struct{}{}
	}
	return result
}

func entryStringIDs(values map[knowledge.EntryID]knowledge.Entry) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for id := range values {
		result[string(id)] = struct{}{}
	}
	return result
}

func (s *Service) indexedChunkIDs(ctx context.Context, sortKind knowledgeStore.ChunkSort) (map[string]struct{}, []string, error) {
	ids := make(map[string]struct{})
	duplicates := make([]string, 0)
	cursor := ""
	for {
		page, err := s.store.ListChunks(ctx, knowledgeStore.ChunkListRequest{Sort: sortKind, Limit: 200, Cursor: cursor})
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

func (s *Service) indexedEntryIDs(ctx context.Context, sortKind knowledgeStore.EntrySort) (map[string]struct{}, []string, error) {
	ids := make(map[string]struct{})
	duplicates := make([]string, 0)
	cursor := ""
	for {
		page, err := s.store.ListEntries(ctx, knowledgeStore.EntryListRequest{Sort: sortKind, Limit: 200, Cursor: cursor})
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

func (s *Service) scanAdjacencyIndexes(ctx context.Context, chunks map[knowledge.ChunkID]knowledge.Chunk, entries map[knowledge.EntryID]knowledge.Entry, links map[knowledge.LinkID]knowledge.Link) ([]IntegrityIssue, uint64, error) {
	expected := make(map[knowledge.ObjectRef]map[knowledge.LinkID]struct{}, len(chunks)+len(entries))
	for id := range chunks {
		expected[knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(id)}] = make(map[knowledge.LinkID]struct{})
	}
	for id := range entries {
		expected[knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(id)}] = make(map[knowledge.LinkID]struct{})
	}
	for id, link := range links {
		if values, exists := expected[link.Source]; exists {
			values[id] = struct{}{}
		}
		if values, exists := expected[link.Target]; exists {
			values[id] = struct{}{}
		}
	}
	endpoints := make([]knowledge.ObjectRef, 0, len(expected))
	for endpoint := range expected {
		endpoints = append(endpoints, endpoint)
	}
	slices.SortFunc(endpoints, func(left, right knowledge.ObjectRef) int {
		if left.Kind != right.Kind {
			return int(left.Kind) - int(right.Kind)
		}
		return strings.Compare(left.ID, right.ID)
	})
	issues := make([]IntegrityIssue, 0)
	var checked uint64
	for _, endpoint := range endpoints {
		actual := make(map[knowledge.LinkID]struct{})
		cursor := ""
		for {
			page, err := s.store.ListAdjacentLinks(ctx, knowledgeStore.AdjacentLinkListRequest{
				Filter: knowledgeStore.AdjacentLinkFilter{Endpoint: endpoint, Direction: knowledgeStore.LinkDirectionBoth},
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

func (s *Service) scanSearchIndexes(ctx context.Context, chunks map[knowledge.ChunkID]knowledge.Chunk, entries map[knowledge.EntryID]knowledge.Entry, links map[knowledge.LinkID]knowledge.Link, evidence map[knowledge.EvidenceID]knowledge.Evidence) ([]IntegrityIssue, uint64, error) {
	records := make([]knowledgeStore.CanonicalRecord, 0, len(chunks)+len(entries)+len(links)+len(evidence))
	for _, value := range chunks {
		value := value
		records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindChunk, Chunk: &value})
	}
	for _, value := range entries {
		value := value
		records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindEntry, Entry: &value})
	}
	for _, value := range links {
		value := value
		records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindLink, Link: &value})
	}
	for _, value := range evidence {
		value := value
		records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindEvidence, Evidence: &value})
	}
	slices.SortFunc(records, func(left, right knowledgeStore.CanonicalRecord) int {
		if left.Kind != right.Kind {
			return strings.Compare(string(left.Kind), string(right.Kind))
		}
		return strings.Compare(left.ID(), right.ID())
	})
	issues := make([]IntegrityIssue, 0)
	var checked uint64
	for _, record := range records {
		page, err := s.store.SearchExact(ctx, knowledgeStore.ExactSearchRequest{Query: record.ID(), Kinds: []knowledgeStore.RecordKind{record.Kind}, Limit: 100})
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
	err = s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		ids := make([]knowledge.EvidenceID, 0, len(evidence))
		for id := range evidence {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			item := evidence[id]
			indexed, err := tx.EvidenceBySource(ctx, item.Source.ID, item.Source.ContentHash)
			checked++
			if errors.Is(err, knowledgeStore.ErrNotFound) {
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

func (s *Service) scanLexicalIndex(ctx context.Context, entries map[knowledge.EntryID]knowledge.Entry) ([]IntegrityIssue, uint64, error) {
	expected := make(map[string]knowledgeStore.LexicalPosting)
	termSet := make(map[string]struct{})
	for _, entry := range entries {
		for _, posting := range knowledgeStore.EntryLexicalPostings(entry) {
			expected[lexicalIntegrityKey(posting.Term, posting.EntryID)] = posting
			termSet[posting.Term] = struct{}{}
		}
	}
	terms := make([]string, 0, len(termSet))
	for term := range termSet {
		terms = append(terms, term)
	}
	slices.Sort(terms)
	actual := make(map[string]knowledgeStore.LexicalPosting, len(expected))
	issues := make([]IntegrityIssue, 0)
	var checked uint64
	for start := 0; start < len(terms); start += 64 {
		batch := terms[start:min(start+64, len(terms))]
		cursor := ""
		documentCountChecked := false
		for {
			page, err := s.store.LookupLexicalPostings(ctx, knowledgeStore.LexicalPostingRequest{Terms: batch, Limit: 1000, Cursor: cursor})
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

func lexicalIntegrityKey(term string, entryID knowledge.EntryID) string {
	return term + "\x00" + string(entryID)
}

func derivePackageProvenanceIssues(chunks map[knowledge.ChunkID]knowledge.Chunk, entries map[knowledge.EntryID]knowledge.Entry, links map[knowledge.LinkID]knowledge.Link, evidence map[knowledge.EvidenceID]knowledge.Evidence, initialActors map[knowledge.ObjectRef]knowledge.Actor) ([]IntegrityIssue, uint64) {
	issues := make([]IntegrityIssue, 0)
	packageByChunk := make(map[knowledge.ChunkID]string)
	var checked uint64
	for id, chunk := range chunks {
		ref := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(id)}
		initial := initialActors[ref]
		packaged := initial.Kind == knowledge.ActorKindImport || chunk.Publisher.ID != "" || chunk.Publisher.Name != "" || chunk.License != ""
		if !packaged {
			continue
		}
		checked++
		packageByChunk[id] = initial.ID
		validPackageID := (knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: initial.ID}).Validate() == nil
		if initial.Kind != knowledge.ActorKindImport || !validPackageID || strings.TrimSpace(chunk.Publisher.ID) == "" || strings.TrimSpace(chunk.Publisher.Name) == "" || strings.TrimSpace(chunk.License) == "" {
			issues = append(issues, IntegrityIssue{Kind: IntegrityPackageProvenance, ObjectKind: "chunk", ObjectID: string(id), RelatedID: initial.ID})
		}
	}
	for id, entry := range entries {
		initial := initialActors[knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(id)}]
		if initial.Kind != knowledge.ActorKindImport {
			continue
		}
		if packageByChunk[entry.ChunkID] == "" || packageByChunk[entry.ChunkID] != initial.ID {
			issues = append(issues, IntegrityIssue{Kind: IntegrityPackageProvenance, ObjectKind: "entry", ObjectID: string(id), RelatedID: initial.ID})
		}
	}
	for id, link := range links {
		initial := initialActors[knowledge.ObjectRef{Kind: knowledge.ObjectKindLink, ID: string(id)}]
		if initial.Kind != knowledge.ActorKindImport {
			continue
		}
		matches := false
		for _, endpoint := range []knowledge.ObjectRef{link.Source, link.Target} {
			chunkID := endpointChunkID(endpoint, entries)
			matches = matches || packageByChunk[chunkID] == initial.ID
		}
		if !matches {
			issues = append(issues, IntegrityIssue{Kind: IntegrityPackageProvenance, ObjectKind: "link", ObjectID: string(id), RelatedID: initial.ID})
		}
	}
	referencedPackages := make(map[knowledge.EvidenceID]map[string]struct{})
	addEvidencePackages := func(ids []knowledge.EvidenceID, packageID string) {
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
		for _, endpoint := range []knowledge.ObjectRef{link.Source, link.Target} {
			addEvidencePackages(link.EvidenceIDs, packageByChunk[endpointChunkID(endpoint, entries)])
		}
	}
	for id, item := range evidence {
		if item.Actor.Kind != knowledge.ActorKindImport {
			continue
		}
		if _, matches := referencedPackages[id][item.Actor.ID]; !matches {
			issues = append(issues, IntegrityIssue{Kind: IntegrityPackageProvenance, ObjectKind: "evidence", ObjectID: string(id), RelatedID: item.Actor.ID})
		}
	}
	return issues, checked
}

func endpointChunkID(endpoint knowledge.ObjectRef, entries map[knowledge.EntryID]knowledge.Entry) knowledge.ChunkID {
	switch endpoint.Kind {
	case knowledge.ObjectKindChunk:
		return knowledge.ChunkID(endpoint.ID)
	case knowledge.ObjectKindEntry:
		return entries[knowledge.EntryID(endpoint.ID)].ChunkID
	default:
		return ""
	}
}

func integrityEndpointExists(endpoint knowledge.ObjectRef, chunks map[knowledge.ChunkID]knowledge.Chunk, entries map[knowledge.EntryID]knowledge.Entry) bool {
	switch endpoint.Kind {
	case knowledge.ObjectKindChunk:
		_, exists := chunks[knowledge.ChunkID(endpoint.ID)]
		return exists
	case knowledge.ObjectKindEntry:
		_, exists := entries[knowledge.EntryID(endpoint.ID)]
		return exists
	default:
		return false
	}
}
