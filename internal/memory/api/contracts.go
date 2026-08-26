// Package memoryapi defines the stable JSON boundary used by Memory HTTP
// clients. It deliberately separates editable request content from canonical
// domain records containing server-owned identity, lifecycle, and revision data.
package memoryapi

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/curation"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

const (
	PackageMediaType        = "application/vnd.koder.memory+zip"
	Version                 = "memory.v1"
	RoutePrefix             = "/api/memory/v1"
	ChunkCollectionPath     = RoutePrefix + "/chunks"
	EntryCollectionPath     = RoutePrefix + "/entries"
	LinkCollectionPath      = RoutePrefix + "/links"
	SearchPath              = RoutePrefix + "/search"
	GraphSnapshotPath       = RoutePrefix + "/graph/snapshot"
	NeighborPath            = RoutePrefix + "/neighbors"
	OperationalStatusPath   = RoutePrefix + "/status"
	IndexRebuildPath        = RoutePrefix + "/indexes/rebuild"
	ChatContextPath         = RoutePrefix + "/chat-context"
	GraphViewCollectionPath = RoutePrefix + "/views"
	PackageCollectionPath   = RoutePrefix + "/packages"
	PackagePreviewPath      = PackageCollectionPath + "/preview"
	PackageStagePath        = PackageCollectionPath + "/stages"
	PackageExportPrefix     = PackageCollectionPath + "/export"
	CurationCandidatePath   = RoutePrefix + "/curation/candidates"
	ExplorerURL             = "/memory"
)

func ChunkPath(chunkID memory.ChunkID) string {
	return ChunkCollectionPath + "/" + url.PathEscape(string(chunkID))
}

func ChunkLifecyclePath(chunkID memory.ChunkID, action string) string {
	return ChunkPath(chunkID) + "/" + url.PathEscape(strings.TrimSpace(action))
}

func ChunkHistoryPath(chunkID memory.ChunkID) string {
	return ChunkPath(chunkID) + "/history"
}

func EntryPath(entryID memory.EntryID) string {
	return EntryCollectionPath + "/" + url.PathEscape(string(entryID))
}

func EntryEvidencePath(entryID memory.EntryID) string {
	return EntryPath(entryID) + "/evidence"
}

func EntryHistoryPath(entryID memory.EntryID) string {
	return EntryPath(entryID) + "/history"
}

func EntryLifecyclePath(entryID memory.EntryID, action string) string {
	return EntryPath(entryID) + "/" + url.PathEscape(strings.TrimSpace(action))
}

func LinkPath(linkID memory.LinkID) string {
	return LinkCollectionPath + "/" + url.PathEscape(string(linkID))
}

func LinkLifecyclePath(linkID memory.LinkID, action string) string {
	return LinkPath(linkID) + "/" + url.PathEscape(strings.TrimSpace(action))
}

func LinkHistoryPath(linkID memory.LinkID) string {
	return LinkPath(linkID) + "/history"
}

func GraphViewPath(viewID string) string {
	return GraphViewCollectionPath + "/" + url.PathEscape(strings.TrimSpace(viewID))
}

func PackageStageItemPath(stageID string) string {
	return PackageStagePath + "/" + url.PathEscape(strings.TrimSpace(stageID))
}

func PackageActivatePath(stageID string) string {
	return PackageStageItemPath(stageID) + "/activate"
}

func PackageExportPath(chunkID memory.ChunkID) string {
	return PackageExportPrefix + "/" + url.PathEscape(string(chunkID))
}

func CurationCandidateActionPath(candidateID curation.CandidateID, action string) string {
	return CurationCandidatePath + "/" + url.PathEscape(string(candidateID)) + "/" + url.PathEscape(strings.TrimSpace(action))
}

type ResponseMetadata struct {
	APIVersion string `json:"api_version"`
	RequestID  string `json:"request_id,omitempty"`
}

func Metadata(requestID string) ResponseMetadata {
	return ResponseMetadata{APIVersion: Version, RequestID: strings.TrimSpace(requestID)}
}

type Page struct {
	Limit             int      `json:"limit"`
	Returned          int      `json:"returned"`
	NextCursor        string   `json:"next_cursor,omitempty"`
	Truncated         bool     `json:"truncated,omitempty"`
	TruncationReasons []string `json:"truncation_reasons,omitempty"`
}

type ResourceMetadata struct {
	ETag        string `json:"etag,omitempty"`
	ExplorerURL string `json:"explorer_url,omitempty"`
}

func Resource(ref memory.ObjectRef, revision memory.Revision) ResourceMetadata {
	return ResourceMetadata{ETag: ETag(revision), ExplorerURL: ObjectExplorerURL(ref)}
}

func ETag(revision memory.Revision) string {
	if revision.ID == "" {
		return ""
	}
	return fmt.Sprintf(`"memory-%s"`, revision.ID)
}

func ObjectExplorerURL(ref memory.ObjectRef) string {
	if ref.Kind == memory.ObjectKindUnspecified || strings.TrimSpace(ref.ID) == "" {
		return ExplorerURL
	}
	return ExplorerURL + "?object_kind=" + url.QueryEscape(ref.Kind.String()) + "&id=" + url.QueryEscape(ref.ID)
}

// ChunkContent is the complete client-editable portion of a chunk.
type ChunkContent struct {
	Title           string                `json:"title"`
	Description     string                `json:"description,omitempty"`
	Aliases         []string              `json:"aliases,omitempty"`
	Tags            []string              `json:"tags,omitempty"`
	Kind            memory.ChunkKind      `json:"kind"`
	Scope           memory.Scope          `json:"scope"`
	Visibility      memory.Visibility     `json:"visibility,omitempty"`
	SharedWith      []memory.PrincipalRef `json:"shared_with,omitempty"`
	Language        string                `json:"language,omitempty"`
	Locale          string                `json:"locale,omitempty"`
	Domain          string                `json:"domain,omitempty"`
	Risk            []memory.RiskClass    `json:"risk,omitempty"`
	Publisher       memory.Publisher      `json:"publisher,omitzero"`
	License         string                `json:"license,omitempty"`
	SourcePolicy    string                `json:"source_policy,omitempty"`
	DependencyIDs   []memory.ChunkID      `json:"dependency_ids,omitempty"`
	MinKoderVersion string                `json:"min_koder_version,omitempty"`
	ReviewAfter     time.Time             `json:"review_after,omitzero"`
}

type ChunkListRequest struct {
	Kinds      []memory.ChunkKind  `json:"kinds,omitempty"`
	States     []memory.ChunkState `json:"states,omitempty"`
	Scopes     []memory.Scope      `json:"scopes,omitempty"`
	ScopeKinds []memory.ScopeKind  `json:"scope_kinds,omitempty"`
	Tags       []string            `json:"tags,omitempty"`
	Locale     string              `json:"locale,omitempty"`
	Sort       string              `json:"sort,omitempty"`
	Descending bool                `json:"descending,omitempty"`
	Limit      int                 `json:"limit,omitempty"`
	Cursor     string              `json:"cursor,omitempty"`
}

type ChunkCreateRequest struct {
	Chunk          ChunkContent `json:"chunk"`
	ReviewApproved bool         `json:"review_approved,omitempty"`
}

type ChunkUpdateRequest struct {
	Chunk            ChunkContent `json:"chunk"`
	ExpectedRevision uint64       `json:"expected_revision"`
	Reason           string       `json:"reason,omitempty"`
	ReviewApproved   bool         `json:"review_approved,omitempty"`
}

type LifecycleRequest struct {
	ExpectedRevision uint64 `json:"expected_revision"`
	Reason           string `json:"reason,omitempty"`
}

type DeleteRequest struct {
	ExpectedRevision uint64 `json:"expected_revision"`
	Confirmed        bool   `json:"confirmed"`
	Cascade          bool   `json:"cascade,omitempty"`
}

type ChunkResponse struct {
	ResponseMetadata
	ResourceMetadata
	Chunk          memory.Chunk                 `json:"chunk"`
	Classification *memory.ClassificationResult `json:"classification,omitempty"`
}

type ChunkListResponse struct {
	ResponseMetadata
	Chunks []memory.Chunk `json:"chunks"`
	Page   Page           `json:"page"`
}

type DeleteResponse struct {
	ResponseMetadata
	Object          memory.ObjectRef    `json:"object"`
	Deleted         bool                `json:"deleted"`
	Cascade         bool                `json:"cascade,omitempty"`
	DeletedEntryIDs []memory.EntryID    `json:"deleted_entry_ids,omitempty"`
	DeletedLinkIDs  []memory.LinkID     `json:"deleted_link_ids,omitempty"`
	DeletedEvidence []memory.EvidenceID `json:"deleted_evidence_ids,omitempty"`
	UpdatedChunkIDs []memory.ChunkID    `json:"updated_chunk_ids,omitempty"`
}

type GraphViewSaveRequest struct {
	Name             string                        `json:"name"`
	State            memoryStoreAPI.GraphViewState `json:"state"`
	ExpectedRevision uint64                        `json:"expected_revision,omitempty"`
}

type GraphViewDeleteRequest struct {
	ExpectedRevision uint64 `json:"expected_revision"`
}

type GraphViewResponse struct {
	ResponseMetadata
	View memoryStoreAPI.SavedGraphView `json:"view"`
}

type GraphViewListResponse struct {
	ResponseMetadata
	Views []memoryStoreAPI.SavedGraphView `json:"views"`
}

type GraphViewDeleteResponse struct {
	ResponseMetadata
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type PackagePreviewResponse struct {
	ResponseMetadata
	Preview memoryService.ImportPreview `json:"preview"`
}

type PackageStageResponse struct {
	ResponseMetadata
	Stage   *memoryService.ImportStage  `json:"stage,omitempty"`
	Preview memoryService.ImportPreview `json:"preview"`
	Error   *memoryService.ServiceError `json:"error,omitempty"`
}

type PackageActivationResponse struct {
	ResponseMetadata
	Result memoryService.ActivateImportResult `json:"result"`
}

type PackageDiscardResponse struct {
	ResponseMetadata
	StageID   string `json:"stage_id"`
	Discarded bool   `json:"discarded"`
}

type CurationCandidateListResponse struct {
	ResponseMetadata
	Candidates []curation.StoredCandidate `json:"candidates"`
	Page       Page                       `json:"page"`
}

type CurationCandidateDecisionRequest struct {
	ExpectedVersion uint64 `json:"expected_version"`
	Reason          string `json:"reason,omitempty"`
}

type CurationCandidateResponse struct {
	ResponseMetadata
	Candidate curation.StoredCandidate `json:"candidate"`
}

// EntryContent is the complete client-editable portion of an entry.
type EntryContent struct {
	Kind           memory.EntryKind      `json:"kind"`
	Title          string                `json:"title"`
	Summary        string                `json:"summary,omitempty"`
	Body           string                `json:"body,omitempty"`
	Aliases        []string              `json:"aliases,omitempty"`
	Tags           []string              `json:"tags,omitempty"`
	Scope          memory.Scope          `json:"scope"`
	Applicability  memory.Applicability  `json:"applicability,omitzero"`
	Risk           []memory.RiskClass    `json:"risk,omitempty"`
	Confidence     float32               `json:"confidence,omitempty"`
	ValidFrom      time.Time             `json:"valid_from,omitzero"`
	ValidUntil     time.Time             `json:"valid_until,omitzero"`
	ObservedAt     time.Time             `json:"observed_at,omitzero"`
	ReviewAfter    time.Time             `json:"review_after,omitzero"`
	EvidenceIDs    []memory.EvidenceID   `json:"evidence_ids,omitempty"`
	PersonalOrigin memory.PersonalOrigin `json:"personal_origin,omitempty"`
}

type EntryListRequest struct {
	ChunkIDs   []memory.ChunkID    `json:"chunk_ids,omitempty"`
	Kinds      []memory.EntryKind  `json:"kinds,omitempty"`
	States     []memory.EntryState `json:"states,omitempty"`
	Scopes     []memory.Scope      `json:"scopes,omitempty"`
	ScopeKinds []memory.ScopeKind  `json:"scope_kinds,omitempty"`
	Tags       []string            `json:"tags,omitempty"`
	Locales    []string            `json:"locales,omitempty"`
	Sort       string              `json:"sort,omitempty"`
	Descending bool                `json:"descending,omitempty"`
	Limit      int                 `json:"limit,omitempty"`
	Cursor     string              `json:"cursor,omitempty"`
}

type EntryCreateRequest struct {
	ChunkID        memory.ChunkID `json:"chunk_id"`
	Entry          EntryContent   `json:"entry"`
	ReviewApproved bool           `json:"review_approved,omitempty"`
}

type EntryUpdateRequest struct {
	Entry            EntryContent `json:"entry"`
	ExpectedRevision uint64       `json:"expected_revision"`
	Reason           string       `json:"reason,omitempty"`
	ReviewApproved   bool         `json:"review_approved,omitempty"`
}

type EntrySupersedeRequest struct {
	ReplacementEntryID memory.EntryID `json:"replacement_entry_id"`
	ExpectedRevision   uint64         `json:"expected_revision"`
	Reason             string         `json:"reason,omitempty"`
}

type EntryVerifyRequest struct {
	ExpectedRevision uint64                    `json:"expected_revision"`
	Status           memory.VerificationStatus `json:"status"`
	Method           string                    `json:"method,omitempty"`
	EvidenceIDs      []memory.EvidenceID       `json:"evidence_ids,omitempty"`
	Reason           string                    `json:"reason,omitempty"`
}

type EntryDeleteRequest struct {
	ExpectedRevision uint64 `json:"expected_revision"`
	Confirmed        bool   `json:"confirmed"`
}

type EntryResponse struct {
	ResponseMetadata
	ResourceMetadata
	Entry          memory.Entry                 `json:"entry"`
	Replacement    *memory.Entry                `json:"replacement,omitempty"`
	Classification *memory.ClassificationResult `json:"classification,omitempty"`
}

type EntryListResponse struct {
	ResponseMetadata
	Entries []memory.Entry `json:"entries"`
	Page    Page           `json:"page"`
}

type EvidenceResponse struct {
	ResponseMetadata
	Evidence memory.Evidence `json:"evidence"`
}

type EvidenceListResponse struct {
	ResponseMetadata
	Evidence []memory.Evidence `json:"evidence"`
	Page     Page              `json:"page"`
}

type LinkContent struct {
	Source      memory.ObjectRef    `json:"source"`
	Target      memory.ObjectRef    `json:"target"`
	Kind        memory.LinkKind     `json:"kind"`
	Label       string              `json:"label,omitempty"`
	Notes       string              `json:"notes,omitempty"`
	EvidenceIDs []memory.EvidenceID `json:"evidence_ids,omitempty"`
}

type LinkCreateRequest struct {
	Link           LinkContent `json:"link"`
	ReviewApproved bool        `json:"review_approved,omitempty"`
}

type LinkResponse struct {
	ResponseMetadata
	ResourceMetadata
	Link           memory.Link                  `json:"link"`
	Classification *memory.ClassificationResult `json:"classification,omitempty"`
}

// Record is a tagged canonical record used by get, history, and graph APIs.
// Exactly one typed payload must be populated and match Kind.
type Record struct {
	Kind     memoryStoreAPI.RecordKind `json:"kind"`
	Chunk    *memory.Chunk             `json:"chunk,omitempty"`
	Entry    *memory.Entry             `json:"entry,omitempty"`
	Link     *memory.Link              `json:"link,omitempty"`
	Evidence *memory.Evidence          `json:"evidence,omitempty"`
}

type RecordResponse struct {
	ResponseMetadata
	ResourceMetadata
	Record Record `json:"record"`
}

type HistoryResponse struct {
	ResponseMetadata
	Object    memory.ObjectRef `json:"object"`
	Revisions []Record         `json:"revisions"`
	Page      Page             `json:"page"`
}

type SearchRequest struct {
	Query             string             `json:"query"`
	ChunkIDs          []memory.ChunkID   `json:"chunk_ids,omitempty"`
	Scopes            []memory.Scope     `json:"scopes,omitempty"`
	ScopeKinds        []memory.ScopeKind `json:"scope_kinds,omitempty"`
	IncludeInvalid    bool               `json:"include_invalid,omitempty"`
	IncludeSuperseded bool               `json:"include_superseded,omitempty"`
	ExpandGraph       bool               `json:"expand_graph,omitempty"`
	Limit             int                `json:"limit,omitempty"`
	Cursor            string             `json:"cursor,omitempty"`
}

type SearchResponse struct {
	ResponseMetadata
	OperationID          string                             `json:"operation_id"`
	Terms                []string                           `json:"terms"`
	Matches              []memoryService.LexicalSearchMatch `json:"matches"`
	Warnings             []memoryService.SearchWarning      `json:"warnings,omitempty"`
	Contradictions       []memoryService.Contradiction      `json:"contradictions,omitempty"`
	AsOf                 time.Time                          `json:"as_of"`
	CorpusDocumentCount  uint64                             `json:"corpus_document_count"`
	MatchedDocumentCount uint64                             `json:"matched_document_count"`
	GraphExpansion       *memoryService.GraphExpansionStats `json:"graph_expansion,omitempty"`
	Page                 Page                               `json:"page"`
}

type GraphSnapshotRequest struct {
	Root        memory.ObjectRef             `json:"root"`
	Direction   memoryStoreAPI.LinkDirection `json:"direction,omitempty"`
	Kinds       []memory.LinkKind            `json:"kinds,omitempty"`
	MaxDepth    int                          `json:"max_depth,omitempty"`
	MaxNodes    int                          `json:"max_nodes,omitempty"`
	MaxEdges    int                          `json:"max_edges,omitempty"`
	TimeLimitMS int                          `json:"time_limit_ms,omitempty"`
}

type NeighborRequest struct {
	Object    memory.ObjectRef             `json:"object"`
	Direction memoryStoreAPI.LinkDirection `json:"direction,omitempty"`
	Kinds     []memory.LinkKind            `json:"kinds,omitempty"`
	Limit     int                          `json:"limit,omitempty"`
	Cursor    string                       `json:"cursor,omitempty"`
}

type NeighborResponse struct {
	ResponseMetadata
	Object    memory.ObjectRef         `json:"object"`
	Neighbors []memoryService.Neighbor `json:"neighbors"`
	Page      Page                     `json:"page"`
}

type GraphNode struct {
	ID           string                    `json:"id"`
	Object       memory.ObjectRef          `json:"object"`
	SemanticKind string                    `json:"semantic_kind"`
	Title        string                    `json:"title"`
	Summary      string                    `json:"summary,omitempty"`
	Scope        memory.Scope              `json:"scope"`
	State        string                    `json:"state"`
	Revision     memory.Revision           `json:"revision"`
	Verification memory.VerificationStatus `json:"verification,omitempty"`
	Risk         []memory.RiskClass        `json:"risk,omitempty"`
}

type GraphEdge struct {
	ID       string           `json:"id"`
	Source   memory.ObjectRef `json:"source"`
	Target   memory.ObjectRef `json:"target"`
	Kind     memory.LinkKind  `json:"kind"`
	Label    string           `json:"label,omitempty"`
	State    memory.LinkState `json:"state"`
	Revision memory.Revision  `json:"revision"`
}

type GraphSnapshotResponse struct {
	ResponseMetadata
	Generation uint64                           `json:"generation"`
	Checkpoint memoryService.MutationCheckpoint `json:"checkpoint"`
	Nodes      []GraphNode                      `json:"nodes"`
	Edges      []GraphEdge                      `json:"edges"`
	Page       Page                             `json:"page"`
}

type OperationalStatusResponse struct {
	ResponseMetadata
	Status memoryService.OperationalStatus `json:"status"`
}

type IndexRebuildRequest struct {
	Index string `json:"index"`
}

type IndexRebuildResponse struct {
	ResponseMetadata
	Result memoryService.StartIndexRebuildResult `json:"result"`
}

type IndexRebuildCancelResponse struct {
	ResponseMetadata
	Result memoryService.CancelIndexRebuildResult `json:"result"`
}

type ChatContextRequest struct {
	SessionID string           `json:"session_id"`
	ChatID    string           `json:"chat_id"`
	Object    memory.ObjectRef `json:"object"`
}

type ChatContextResponse struct {
	ResponseMetadata
	Object      memory.ObjectRef `json:"object"`
	ExplorerURL string           `json:"explorer_url"`
	Queued      bool             `json:"queued"`
}

type ErrorResponse struct {
	ResponseMetadata
	Error *memoryService.ServiceError `json:"error"`
}
