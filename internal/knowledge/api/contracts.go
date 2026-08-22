// Package knowledgeapi defines the stable JSON boundary used by Knowledge HTTP
// clients. It deliberately separates editable request content from canonical
// domain records containing server-owned identity, lifecycle, and revision data.
package knowledgeapi

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/curation"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const (
	PackageMediaType        = "application/vnd.koder.knowledge+zip"
	Version                 = "knowledge.v1"
	RoutePrefix             = "/api/knowledge/v1"
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
	ExplorerURL             = "/knowledge"
)

func ChunkPath(chunkID knowledge.ChunkID) string {
	return ChunkCollectionPath + "/" + url.PathEscape(string(chunkID))
}

func ChunkLifecyclePath(chunkID knowledge.ChunkID, action string) string {
	return ChunkPath(chunkID) + "/" + url.PathEscape(strings.TrimSpace(action))
}

func ChunkHistoryPath(chunkID knowledge.ChunkID) string {
	return ChunkPath(chunkID) + "/history"
}

func EntryPath(entryID knowledge.EntryID) string {
	return EntryCollectionPath + "/" + url.PathEscape(string(entryID))
}

func EntryEvidencePath(entryID knowledge.EntryID) string {
	return EntryPath(entryID) + "/evidence"
}

func EntryHistoryPath(entryID knowledge.EntryID) string {
	return EntryPath(entryID) + "/history"
}

func EntryLifecyclePath(entryID knowledge.EntryID, action string) string {
	return EntryPath(entryID) + "/" + url.PathEscape(strings.TrimSpace(action))
}

func LinkPath(linkID knowledge.LinkID) string {
	return LinkCollectionPath + "/" + url.PathEscape(string(linkID))
}

func LinkLifecyclePath(linkID knowledge.LinkID, action string) string {
	return LinkPath(linkID) + "/" + url.PathEscape(strings.TrimSpace(action))
}

func LinkHistoryPath(linkID knowledge.LinkID) string {
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

func PackageExportPath(chunkID knowledge.ChunkID) string {
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

func Resource(ref knowledge.ObjectRef, revision knowledge.Revision) ResourceMetadata {
	return ResourceMetadata{ETag: ETag(revision), ExplorerURL: ObjectExplorerURL(ref)}
}

func ETag(revision knowledge.Revision) string {
	if revision.ID == "" {
		return ""
	}
	return fmt.Sprintf(`"knowledge-%s"`, revision.ID)
}

func ObjectExplorerURL(ref knowledge.ObjectRef) string {
	if ref.Kind == knowledge.ObjectKindUnspecified || strings.TrimSpace(ref.ID) == "" {
		return ExplorerURL
	}
	return ExplorerURL + "?object_kind=" + url.QueryEscape(ref.Kind.String()) + "&id=" + url.QueryEscape(ref.ID)
}

// ChunkContent is the complete client-editable portion of a chunk.
type ChunkContent struct {
	Title           string                   `json:"title"`
	Description     string                   `json:"description,omitempty"`
	Aliases         []string                 `json:"aliases,omitempty"`
	Tags            []string                 `json:"tags,omitempty"`
	Kind            knowledge.ChunkKind      `json:"kind"`
	Scope           knowledge.Scope          `json:"scope"`
	Visibility      knowledge.Visibility     `json:"visibility,omitempty"`
	SharedWith      []knowledge.PrincipalRef `json:"shared_with,omitempty"`
	Language        string                   `json:"language,omitempty"`
	Locale          string                   `json:"locale,omitempty"`
	Domain          string                   `json:"domain,omitempty"`
	Risk            []knowledge.RiskClass    `json:"risk,omitempty"`
	Publisher       knowledge.Publisher      `json:"publisher,omitzero"`
	License         string                   `json:"license,omitempty"`
	SourcePolicy    string                   `json:"source_policy,omitempty"`
	DependencyIDs   []knowledge.ChunkID      `json:"dependency_ids,omitempty"`
	MinKoderVersion string                   `json:"min_koder_version,omitempty"`
	ReviewAfter     time.Time                `json:"review_after,omitzero"`
}

type ChunkListRequest struct {
	Kinds      []knowledge.ChunkKind  `json:"kinds,omitempty"`
	States     []knowledge.ChunkState `json:"states,omitempty"`
	Scopes     []knowledge.Scope      `json:"scopes,omitempty"`
	ScopeKinds []knowledge.ScopeKind  `json:"scope_kinds,omitempty"`
	Tags       []string               `json:"tags,omitempty"`
	Locale     string                 `json:"locale,omitempty"`
	Sort       string                 `json:"sort,omitempty"`
	Descending bool                   `json:"descending,omitempty"`
	Limit      int                    `json:"limit,omitempty"`
	Cursor     string                 `json:"cursor,omitempty"`
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
	Chunk          knowledge.Chunk                 `json:"chunk"`
	Classification *knowledge.ClassificationResult `json:"classification,omitempty"`
}

type ChunkListResponse struct {
	ResponseMetadata
	Chunks []knowledge.Chunk `json:"chunks"`
	Page   Page              `json:"page"`
}

type DeleteResponse struct {
	ResponseMetadata
	Object          knowledge.ObjectRef    `json:"object"`
	Deleted         bool                   `json:"deleted"`
	Cascade         bool                   `json:"cascade,omitempty"`
	DeletedEntryIDs []knowledge.EntryID    `json:"deleted_entry_ids,omitempty"`
	DeletedLinkIDs  []knowledge.LinkID     `json:"deleted_link_ids,omitempty"`
	DeletedEvidence []knowledge.EvidenceID `json:"deleted_evidence_ids,omitempty"`
	UpdatedChunkIDs []knowledge.ChunkID    `json:"updated_chunk_ids,omitempty"`
}

type GraphViewSaveRequest struct {
	Name             string                        `json:"name"`
	State            knowledgeStore.GraphViewState `json:"state"`
	ExpectedRevision uint64                        `json:"expected_revision,omitempty"`
}

type GraphViewDeleteRequest struct {
	ExpectedRevision uint64 `json:"expected_revision"`
}

type GraphViewResponse struct {
	ResponseMetadata
	View knowledgeStore.SavedGraphView `json:"view"`
}

type GraphViewListResponse struct {
	ResponseMetadata
	Views []knowledgeStore.SavedGraphView `json:"views"`
}

type GraphViewDeleteResponse struct {
	ResponseMetadata
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type PackagePreviewResponse struct {
	ResponseMetadata
	Preview knowledgeService.ImportPreview `json:"preview"`
}

type PackageStageResponse struct {
	ResponseMetadata
	Stage   *knowledgeService.ImportStage  `json:"stage,omitempty"`
	Preview knowledgeService.ImportPreview `json:"preview"`
	Error   *knowledgeService.ServiceError `json:"error,omitempty"`
}

type PackageActivationResponse struct {
	ResponseMetadata
	Result knowledgeService.ActivateImportResult `json:"result"`
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
	Kind           knowledge.EntryKind      `json:"kind"`
	Title          string                   `json:"title"`
	Summary        string                   `json:"summary,omitempty"`
	Body           string                   `json:"body,omitempty"`
	Aliases        []string                 `json:"aliases,omitempty"`
	Tags           []string                 `json:"tags,omitempty"`
	Scope          knowledge.Scope          `json:"scope"`
	Applicability  knowledge.Applicability  `json:"applicability,omitzero"`
	Risk           []knowledge.RiskClass    `json:"risk,omitempty"`
	Confidence     float32                  `json:"confidence,omitempty"`
	ValidFrom      time.Time                `json:"valid_from,omitzero"`
	ValidUntil     time.Time                `json:"valid_until,omitzero"`
	ObservedAt     time.Time                `json:"observed_at,omitzero"`
	ReviewAfter    time.Time                `json:"review_after,omitzero"`
	EvidenceIDs    []knowledge.EvidenceID   `json:"evidence_ids,omitempty"`
	PersonalOrigin knowledge.PersonalOrigin `json:"personal_origin,omitempty"`
}

type EntryListRequest struct {
	ChunkIDs   []knowledge.ChunkID    `json:"chunk_ids,omitempty"`
	Kinds      []knowledge.EntryKind  `json:"kinds,omitempty"`
	States     []knowledge.EntryState `json:"states,omitempty"`
	Scopes     []knowledge.Scope      `json:"scopes,omitempty"`
	ScopeKinds []knowledge.ScopeKind  `json:"scope_kinds,omitempty"`
	Tags       []string               `json:"tags,omitempty"`
	Locales    []string               `json:"locales,omitempty"`
	Sort       string                 `json:"sort,omitempty"`
	Descending bool                   `json:"descending,omitempty"`
	Limit      int                    `json:"limit,omitempty"`
	Cursor     string                 `json:"cursor,omitempty"`
}

type EntryCreateRequest struct {
	ChunkID        knowledge.ChunkID `json:"chunk_id"`
	Entry          EntryContent      `json:"entry"`
	ReviewApproved bool              `json:"review_approved,omitempty"`
}

type EntryUpdateRequest struct {
	Entry            EntryContent `json:"entry"`
	ExpectedRevision uint64       `json:"expected_revision"`
	Reason           string       `json:"reason,omitempty"`
	ReviewApproved   bool         `json:"review_approved,omitempty"`
}

type EntrySupersedeRequest struct {
	ReplacementEntryID knowledge.EntryID `json:"replacement_entry_id"`
	ExpectedRevision   uint64            `json:"expected_revision"`
	Reason             string            `json:"reason,omitempty"`
}

type EntryVerifyRequest struct {
	ExpectedRevision uint64                       `json:"expected_revision"`
	Status           knowledge.VerificationStatus `json:"status"`
	Method           string                       `json:"method,omitempty"`
	EvidenceIDs      []knowledge.EvidenceID       `json:"evidence_ids,omitempty"`
	Reason           string                       `json:"reason,omitempty"`
}

type EntryDeleteRequest struct {
	ExpectedRevision uint64 `json:"expected_revision"`
	Confirmed        bool   `json:"confirmed"`
}

type EntryResponse struct {
	ResponseMetadata
	ResourceMetadata
	Entry          knowledge.Entry                 `json:"entry"`
	Replacement    *knowledge.Entry                `json:"replacement,omitempty"`
	Classification *knowledge.ClassificationResult `json:"classification,omitempty"`
}

type EntryListResponse struct {
	ResponseMetadata
	Entries []knowledge.Entry `json:"entries"`
	Page    Page              `json:"page"`
}

type EvidenceResponse struct {
	ResponseMetadata
	Evidence knowledge.Evidence `json:"evidence"`
}

type EvidenceListResponse struct {
	ResponseMetadata
	Evidence []knowledge.Evidence `json:"evidence"`
	Page     Page                 `json:"page"`
}

type LinkContent struct {
	Source      knowledge.ObjectRef    `json:"source"`
	Target      knowledge.ObjectRef    `json:"target"`
	Kind        knowledge.LinkKind     `json:"kind"`
	Label       string                 `json:"label,omitempty"`
	Notes       string                 `json:"notes,omitempty"`
	EvidenceIDs []knowledge.EvidenceID `json:"evidence_ids,omitempty"`
}

type LinkCreateRequest struct {
	Link           LinkContent `json:"link"`
	ReviewApproved bool        `json:"review_approved,omitempty"`
}

type LinkResponse struct {
	ResponseMetadata
	ResourceMetadata
	Link           knowledge.Link                  `json:"link"`
	Classification *knowledge.ClassificationResult `json:"classification,omitempty"`
}

// Record is a tagged canonical record used by get, history, and graph APIs.
// Exactly one typed payload must be populated and match Kind.
type Record struct {
	Kind     knowledgeStore.RecordKind `json:"kind"`
	Chunk    *knowledge.Chunk          `json:"chunk,omitempty"`
	Entry    *knowledge.Entry          `json:"entry,omitempty"`
	Link     *knowledge.Link           `json:"link,omitempty"`
	Evidence *knowledge.Evidence       `json:"evidence,omitempty"`
}

type RecordResponse struct {
	ResponseMetadata
	ResourceMetadata
	Record Record `json:"record"`
}

type HistoryResponse struct {
	ResponseMetadata
	Object    knowledge.ObjectRef `json:"object"`
	Revisions []Record            `json:"revisions"`
	Page      Page                `json:"page"`
}

type SearchRequest struct {
	Query             string                `json:"query"`
	ChunkIDs          []knowledge.ChunkID   `json:"chunk_ids,omitempty"`
	Scopes            []knowledge.Scope     `json:"scopes,omitempty"`
	ScopeKinds        []knowledge.ScopeKind `json:"scope_kinds,omitempty"`
	IncludeInvalid    bool                  `json:"include_invalid,omitempty"`
	IncludeSuperseded bool                  `json:"include_superseded,omitempty"`
	ExpandGraph       bool                  `json:"expand_graph,omitempty"`
	Limit             int                   `json:"limit,omitempty"`
	Cursor            string                `json:"cursor,omitempty"`
}

type SearchResponse struct {
	ResponseMetadata
	Terms                []string                              `json:"terms"`
	Matches              []knowledgeService.LexicalSearchMatch `json:"matches"`
	Warnings             []knowledgeService.SearchWarning      `json:"warnings,omitempty"`
	Contradictions       []knowledgeService.Contradiction      `json:"contradictions,omitempty"`
	AsOf                 time.Time                             `json:"as_of"`
	CorpusDocumentCount  uint64                                `json:"corpus_document_count"`
	MatchedDocumentCount uint64                                `json:"matched_document_count"`
	GraphExpansion       *knowledgeService.GraphExpansionStats `json:"graph_expansion,omitempty"`
	Page                 Page                                  `json:"page"`
}

type GraphSnapshotRequest struct {
	Root        knowledge.ObjectRef          `json:"root"`
	Direction   knowledgeStore.LinkDirection `json:"direction,omitempty"`
	Kinds       []knowledge.LinkKind         `json:"kinds,omitempty"`
	MaxDepth    int                          `json:"max_depth,omitempty"`
	MaxNodes    int                          `json:"max_nodes,omitempty"`
	MaxEdges    int                          `json:"max_edges,omitempty"`
	TimeLimitMS int                          `json:"time_limit_ms,omitempty"`
}

type NeighborRequest struct {
	Object    knowledge.ObjectRef          `json:"object"`
	Direction knowledgeStore.LinkDirection `json:"direction,omitempty"`
	Kinds     []knowledge.LinkKind         `json:"kinds,omitempty"`
	Limit     int                          `json:"limit,omitempty"`
	Cursor    string                       `json:"cursor,omitempty"`
}

type NeighborResponse struct {
	ResponseMetadata
	Object    knowledge.ObjectRef         `json:"object"`
	Neighbors []knowledgeService.Neighbor `json:"neighbors"`
	Page      Page                        `json:"page"`
}

type GraphNode struct {
	ID           string                       `json:"id"`
	Object       knowledge.ObjectRef          `json:"object"`
	SemanticKind string                       `json:"semantic_kind"`
	Title        string                       `json:"title"`
	Summary      string                       `json:"summary,omitempty"`
	Scope        knowledge.Scope              `json:"scope"`
	State        string                       `json:"state"`
	Revision     knowledge.Revision           `json:"revision"`
	Verification knowledge.VerificationStatus `json:"verification,omitempty"`
	Risk         []knowledge.RiskClass        `json:"risk,omitempty"`
}

type GraphEdge struct {
	ID       string              `json:"id"`
	Source   knowledge.ObjectRef `json:"source"`
	Target   knowledge.ObjectRef `json:"target"`
	Kind     knowledge.LinkKind  `json:"kind"`
	Label    string              `json:"label,omitempty"`
	State    knowledge.LinkState `json:"state"`
	Revision knowledge.Revision  `json:"revision"`
}

type GraphSnapshotResponse struct {
	ResponseMetadata
	Generation uint64                              `json:"generation"`
	Checkpoint knowledgeService.MutationCheckpoint `json:"checkpoint"`
	Nodes      []GraphNode                         `json:"nodes"`
	Edges      []GraphEdge                         `json:"edges"`
	Page       Page                                `json:"page"`
}

type OperationalStatusResponse struct {
	ResponseMetadata
	Status knowledgeService.OperationalStatus `json:"status"`
}

type IndexRebuildRequest struct {
	Index string `json:"index"`
}

type IndexRebuildResponse struct {
	ResponseMetadata
	Result knowledgeService.StartIndexRebuildResult `json:"result"`
}

type IndexRebuildCancelResponse struct {
	ResponseMetadata
	Result knowledgeService.CancelIndexRebuildResult `json:"result"`
}

type ChatContextRequest struct {
	SessionID string              `json:"session_id"`
	ChatID    string              `json:"chat_id"`
	Object    knowledge.ObjectRef `json:"object"`
}

type ChatContextResponse struct {
	ResponseMetadata
	Object      knowledge.ObjectRef `json:"object"`
	ExplorerURL string              `json:"explorer_url"`
	Queued      bool                `json:"queued"`
}

type ErrorResponse struct {
	ResponseMetadata
	Error *knowledgeService.ServiceError `json:"error"`
}
