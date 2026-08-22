package service

import (
	"context"
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
	Scanned    knowledgeStore.ScanStats `json:"scanned"`
	Issues     []IntegrityIssue         `json:"issues"`
	IssueCount int                      `json:"issue_count"`
	Truncated  bool                     `json:"truncated"`
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
	maintenance, ok := s.store.(knowledgeStore.MaintenanceStore)
	if !ok {
		return IntegrityReport{}, knowledgeStore.ErrUnsupported
	}
	chunks := make(map[knowledge.ChunkID]knowledge.Chunk)
	entries := make(map[knowledge.EntryID]knowledge.Entry)
	links := make(map[knowledge.LinkID]knowledge.Link)
	evidence := make(map[knowledge.EvidenceID]knowledge.Evidence)
	stats, err := maintenance.ScanCanonical(ctx, func(record knowledgeStore.CanonicalRecord) error {
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
	issues := deriveIntegrityIssues(chunks, entries, links, evidence)
	report := IntegrityReport{Scanned: stats, IssueCount: len(issues)}
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
	slices.SortFunc(issues, func(left, right IntegrityIssue) int {
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
	})
	return issues
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
