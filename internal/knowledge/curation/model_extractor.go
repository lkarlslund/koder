package curation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lkarlslund/koder/internal/knowledge"
)

const (
	maxDraftResponseBytes = 256 << 10
	maxDraftCandidates    = 16
	maxCandidateSourceIDs = 64
	maxTurnItems          = 64
	maxTurnItemBytes      = 64 << 10
	maxTurnMaterialBytes  = 256 << 10
)

type CandidateAction string

const (
	CandidateActionCreateEntry     CandidateAction = "create_entry"
	CandidateActionUpdateEntry     CandidateAction = "update_entry"
	CandidateActionSupersedeEntry  CandidateAction = "supersede_entry"
	CandidateActionContradictEntry CandidateAction = "contradict_entry"
)

type CandidateRoute string

const (
	CandidateRouteAutomatic     CandidateRoute = "automatic"
	CandidateRoutePendingReview CandidateRoute = "pending_review"
)

// TurnItem is bounded material loaded from an authorized completed turn.
type TurnItem struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	Text string `json:"text"`
}

type TurnMaterial struct {
	Items []TurnItem `json:"items"`
}

// TurnLoader hydrates only the timeline items requested by the extractor.
type TurnLoader interface {
	Load(context.Context, knowledge.CompletedTurnRef, []string) (TurnMaterial, error)
}

// DraftModel is the only model-specific seam. Schema is supplied on every call so
// adapters can use native structured output when available.
type DraftModel interface {
	Draft(context.Context, TurnMaterial, json.RawMessage) ([]byte, error)
}

// CandidateSink atomically stores a completely validated set of drafts.
type CandidateSink interface {
	StoreCandidates(context.Context, knowledge.CurationRecordID, []CandidateDraft) (uint32, error)
}

// EntryDraft contains only client-owned entry fields; canonical IDs, evidence records,
// verification, lifecycle, revision, and timestamps are assigned by later policy stages.
type EntryDraft struct {
	Kind           knowledge.EntryKind      `json:"kind"`
	Title          string                   `json:"title"`
	Summary        string                   `json:"summary,omitempty"`
	Body           string                   `json:"body,omitempty"`
	Aliases        []string                 `json:"aliases,omitempty"`
	Tags           []string                 `json:"tags,omitempty"`
	Scope          knowledge.Scope          `json:"scope"`
	Applicability  knowledge.Applicability  `json:"applicability,omitzero"`
	Risk           []knowledge.RiskClass    `json:"risk,omitempty"`
	Confidence     float32                  `json:"confidence"`
	ValidFrom      time.Time                `json:"valid_from,omitzero"`
	ValidUntil     time.Time                `json:"valid_until,omitzero"`
	ObservedAt     time.Time                `json:"observed_at,omitzero"`
	ReviewAfter    time.Time                `json:"review_after,omitzero"`
	PersonalOrigin knowledge.PersonalOrigin `json:"personal_origin,omitempty"`
}

type CandidateDraft struct {
	Action         CandidateAction                `json:"action"`
	ChunkID        knowledge.ChunkID              `json:"chunk_id"`
	TargetEntryID  knowledge.EntryID              `json:"target_entry_id,omitempty"`
	TargetRevision uint64                         `json:"-"`
	Entry          EntryDraft                     `json:"entry"`
	Reason         string                         `json:"reason"`
	SourceItemIDs  []string                       `json:"source_item_ids"`
	Classification knowledge.ClassificationResult `json:"-"`
	Route          CandidateRoute                 `json:"-"`
	ReviewReasons  []string                       `json:"-"`
}

type modelDraftResponse struct {
	Candidates []CandidateDraft `json:"candidates"`
}

type ModelExtractorConfig struct {
	Loader     TurnLoader
	Model      DraftModel
	Sink       CandidateSink
	Classifier knowledge.Classifier
}

type ModelExtractor struct {
	loader     TurnLoader
	model      DraftModel
	sink       CandidateSink
	classifier knowledge.Classifier
}

func NewModelExtractor(config ModelExtractorConfig) (*ModelExtractor, error) {
	if config.Loader == nil || config.Model == nil || config.Sink == nil || config.Classifier == nil {
		return nil, fmt.Errorf("%w: model extraction requires loader, model, candidate sink, and classifier", ErrUnavailable)
	}
	return &ModelExtractor{loader: config.Loader, model: config.Model, sink: config.Sink, classifier: config.Classifier}, nil
}

func (e *ModelExtractor) Extract(ctx context.Context, record knowledge.CurationRecord) (ExtractionResult, error) {
	if record.State != knowledge.CurationStateProcessing {
		return ExtractionResult{}, fmt.Errorf("%w: model extraction requires a processing record", knowledge.ErrInvalidRecord)
	}
	itemIDs := curationSourceItemIDs(record)
	material, err := e.loader.Load(ctx, record.Source, itemIDs)
	if err != nil {
		return ExtractionResult{}, fmt.Errorf("load completed turn material: %w", err)
	}
	material, err = validateAndRedactMaterial(ctx, e.classifier, material, itemIDs)
	if err != nil {
		return ExtractionResult{}, err
	}
	materialIDs := make(map[string]struct{}, len(material.Items))
	for _, item := range material.Items {
		materialIDs[item.ID] = struct{}{}
	}
	raw, err := e.model.Draft(ctx, cloneTurnMaterial(material), append(json.RawMessage(nil), candidateDraftSchema...))
	if err != nil {
		return ExtractionResult{}, fmt.Errorf("draft knowledge candidates: %w", err)
	}
	drafts, err := decodeCandidateDrafts(raw)
	if err != nil {
		return ExtractionResult{}, err
	}
	for index := range drafts {
		drafts[index], err = normalizeAndValidateCandidate(ctx, e.classifier, drafts[index], materialIDs)
		if err != nil {
			return ExtractionResult{}, fmt.Errorf("candidate %d: %w", index, err)
		}
	}
	stored, err := e.sink.StoreCandidates(ctx, record.ID, drafts)
	if err != nil {
		return ExtractionResult{}, fmt.Errorf("store knowledge candidates: %w", err)
	}
	if stored > uint32(len(drafts)) {
		return ExtractionResult{}, fmt.Errorf("%w: candidate sink reported more drafts than it received", knowledge.ErrInvalidRecord)
	}
	return ExtractionResult{CandidateCount: stored}, nil
}

func cloneTurnMaterial(material TurnMaterial) TurnMaterial {
	material.Items = append([]TurnItem(nil), material.Items...)
	return material
}

func decodeCandidateDrafts(raw []byte) ([]CandidateDraft, error) {
	if len(raw) == 0 || len(raw) > maxDraftResponseBytes {
		return nil, fmt.Errorf("%w: candidate response must contain 1 to %d bytes", knowledge.ErrInvalidRecord, maxDraftResponseBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var response modelDraftResponse
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("%w: decode candidate response: %v", knowledge.ErrInvalidRecord, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if len(response.Candidates) > maxDraftCandidates {
		return nil, fmt.Errorf("%w: candidate response exceeds %d candidates", knowledge.ErrInvalidRecord, maxDraftCandidates)
	}
	return response.Candidates, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("%w: candidate response contains trailing JSON", knowledge.ErrInvalidRecord)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: candidate response has invalid trailing data", knowledge.ErrInvalidRecord)
	}
	return nil
}

func validateAndRedactMaterial(ctx context.Context, classifier knowledge.Classifier, material TurnMaterial, allowedIDs []string) (TurnMaterial, error) {
	if len(material.Items) == 0 || len(material.Items) > maxTurnItems {
		return TurnMaterial{}, fmt.Errorf("%w: turn material must contain 1 to %d items", knowledge.ErrInvalidRecord, maxTurnItems)
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(material.Items))
	total := 0
	for index := range material.Items {
		item := &material.Items[index]
		if _, ok := allowed[item.ID]; !ok {
			return TurnMaterial{}, fmt.Errorf("%w: turn material contains an unrequested item", knowledge.ErrInvalidRecord)
		}
		if _, exists := seen[item.ID]; exists {
			return TurnMaterial{}, fmt.Errorf("%w: turn material contains a duplicate item", knowledge.ErrInvalidRecord)
		}
		seen[item.ID] = struct{}{}
		if !slices.Contains([]string{"user", "assistant", "tool"}, item.Role) {
			return TurnMaterial{}, fmt.Errorf("%w: turn material role is invalid", knowledge.ErrInvalidRecord)
		}
		if !utf8.ValidString(item.Text) || len(item.Text) > maxTurnItemBytes {
			return TurnMaterial{}, fmt.Errorf("%w: turn material item exceeds text limits", knowledge.ErrInvalidRecord)
		}
		total += len(item.Text)
		if total > maxTurnMaterialBytes {
			return TurnMaterial{}, fmt.Errorf("%w: turn material exceeds %d bytes", knowledge.ErrInvalidRecord, maxTurnMaterialBytes)
		}
		classification, err := classifier.Classify(ctx, knowledge.ClassificationInput{Fields: []knowledge.ClassificationField{{Name: "turn_item", Value: item.Text}}})
		if err != nil {
			return TurnMaterial{}, fmt.Errorf("classify turn material: %w", err)
		}
		item.Text = redactSecretFindings(item.Text, classification.Findings)
	}
	if len(seen) != len(allowed) {
		return TurnMaterial{}, fmt.Errorf("%w: turn material omitted a requested item", knowledge.ErrInvalidRecord)
	}
	return material, nil
}

func redactSecretFindings(value string, findings []knowledge.ClassificationFinding) string {
	ranges := make([][2]int, 0, len(findings))
	for _, finding := range findings {
		if !slices.Contains([]knowledge.FindingKind{knowledge.FindingKindPrivateKey, knowledge.FindingKindCredential, knowledge.FindingKindAuthToken}, finding.Kind) {
			continue
		}
		if finding.Start < 0 || finding.End <= finding.Start || finding.End > len(value) {
			continue
		}
		ranges = append(ranges, [2]int{finding.Start, finding.End})
	}
	slices.SortFunc(ranges, func(left, right [2]int) int {
		if left[0] != right[0] {
			return left[0] - right[0]
		}
		return left[1] - right[1]
	})
	merged := ranges[:0]
	for _, span := range ranges {
		if len(merged) == 0 || span[0] > merged[len(merged)-1][1] {
			merged = append(merged, span)
			continue
		}
		if span[1] > merged[len(merged)-1][1] {
			merged[len(merged)-1][1] = span[1]
		}
	}
	for index := len(merged) - 1; index >= 0; index-- {
		span := merged[index]
		value = value[:span[0]] + "[REDACTED]" + value[span[1]:]
	}
	return value
}

func normalizeAndValidateCandidate(ctx context.Context, classifier knowledge.Classifier, draft CandidateDraft, materialIDs map[string]struct{}) (CandidateDraft, error) {
	switch draft.Action {
	case CandidateActionCreateEntry:
		if draft.TargetEntryID != "" {
			return CandidateDraft{}, fmt.Errorf("%w: create_entry cannot identify a target entry", knowledge.ErrInvalidRecord)
		}
	case CandidateActionUpdateEntry, CandidateActionSupersedeEntry, CandidateActionContradictEntry:
		if !isCanonicalUUIDv7(string(draft.TargetEntryID)) {
			return CandidateDraft{}, fmt.Errorf("%w: target_entry_id must be a canonical UUIDv7", knowledge.ErrInvalidRecord)
		}
	default:
		return CandidateDraft{}, fmt.Errorf("%w: candidate action is invalid", knowledge.ErrInvalidRecord)
	}
	if !isCanonicalUUIDv7(string(draft.ChunkID)) {
		return CandidateDraft{}, fmt.Errorf("%w: chunk_id must be a canonical UUIDv7", knowledge.ErrInvalidRecord)
	}
	draft.Reason = knowledge.NormalizeTitle(draft.Reason)
	if draft.Reason == "" || len(draft.Reason) > 1000 {
		return CandidateDraft{}, fmt.Errorf("%w: candidate reason must contain 1 to 1000 bytes", knowledge.ErrInvalidRecord)
	}
	if len(draft.SourceItemIDs) == 0 || len(draft.SourceItemIDs) > maxCandidateSourceIDs {
		return CandidateDraft{}, fmt.Errorf("%w: candidate source_item_ids are required and bounded", knowledge.ErrInvalidRecord)
	}
	seenSources := make(map[string]struct{}, len(draft.SourceItemIDs))
	for _, id := range draft.SourceItemIDs {
		if _, exists := materialIDs[id]; !exists {
			return CandidateDraft{}, fmt.Errorf("%w: candidate references material outside the completed turn", knowledge.ErrInvalidRecord)
		}
		if _, exists := seenSources[id]; exists {
			return CandidateDraft{}, fmt.Errorf("%w: candidate source_item_ids contain a duplicate", knowledge.ErrInvalidRecord)
		}
		seenSources[id] = struct{}{}
	}
	entry := knowledge.Entry{
		Kind: draft.Entry.Kind, Title: draft.Entry.Title, Summary: draft.Entry.Summary, Body: draft.Entry.Body,
		Aliases: draft.Entry.Aliases, Tags: draft.Entry.Tags, Scope: draft.Entry.Scope, Applicability: draft.Entry.Applicability,
		Risk: draft.Entry.Risk, Confidence: draft.Entry.Confidence, ValidFrom: draft.Entry.ValidFrom, ValidUntil: draft.Entry.ValidUntil,
		ObservedAt: draft.Entry.ObservedAt, ReviewAfter: draft.Entry.ReviewAfter, PersonalOrigin: draft.Entry.PersonalOrigin,
	}
	entry, err := knowledge.NormalizeEntry(entry)
	if err != nil {
		return CandidateDraft{}, err
	}
	if entry.Kind == knowledge.EntryKindUnspecified || !entry.Kind.IsAEntryKind() || entry.Title == "" || len(entry.Title) > 240 || len(entry.Summary) > 4000 {
		return CandidateDraft{}, fmt.Errorf("%w: candidate entry kind, title, or summary is invalid", knowledge.ErrInvalidRecord)
	}
	if err := knowledge.ValidateMarkdown("candidate.entry.body", entry.Body); err != nil {
		return CandidateDraft{}, err
	}
	if err := entry.Scope.Validate(); err != nil {
		return CandidateDraft{}, err
	}
	if entry.Confidence < 0 || entry.Confidence > 1 {
		return CandidateDraft{}, fmt.Errorf("%w: candidate entry confidence must be between 0 and 1", knowledge.ErrInvalidRecord)
	}
	if err := validateDraftRisk(entry.Risk); err != nil {
		return CandidateDraft{}, err
	}
	if entry.Scope.Kind == knowledge.ScopeKindPersonal {
		if entry.PersonalOrigin == knowledge.PersonalOriginUnspecified || !entry.PersonalOrigin.IsAPersonalOrigin() {
			return CandidateDraft{}, fmt.Errorf("%w: personal candidate origin is required", knowledge.ErrInvalidRecord)
		}
		if entry.PersonalOrigin == knowledge.PersonalOriginInferred && (entry.Confidence <= 0 || entry.Confidence >= 1) {
			return CandidateDraft{}, fmt.Errorf("%w: inferred personal candidate requires explicit uncertainty", knowledge.ErrInvalidRecord)
		}
		if entry.PersonalOrigin == knowledge.PersonalOriginObserved && entry.ObservedAt.IsZero() {
			return CandidateDraft{}, fmt.Errorf("%w: observed personal candidate requires observed_at", knowledge.ErrInvalidRecord)
		}
	} else if entry.PersonalOrigin != knowledge.PersonalOriginUnspecified {
		return CandidateDraft{}, fmt.Errorf("%w: personal origin requires personal scope", knowledge.ErrInvalidRecord)
	}
	if err := validateDraftTimes(entry); err != nil {
		return CandidateDraft{}, err
	}
	classification, err := classifier.Classify(ctx, knowledge.ClassificationInput{
		Fields: []knowledge.ClassificationField{{Name: "title", Value: entry.Title}, {Name: "summary", Value: entry.Summary}, {Name: "body", Value: entry.Body}}, Risk: entry.Risk,
	})
	if err != nil {
		return CandidateDraft{}, fmt.Errorf("classify candidate draft: %w", err)
	}
	if classification.Decision == knowledge.ClassificationDecisionReject {
		return CandidateDraft{}, fmt.Errorf("%w: candidate draft contains prohibited content", knowledge.ErrInvalidRecord)
	}
	draft.Entry = EntryDraft{
		Kind: entry.Kind, Title: entry.Title, Summary: entry.Summary, Body: entry.Body,
		Aliases: entry.Aliases, Tags: entry.Tags, Scope: entry.Scope, Applicability: entry.Applicability,
		Risk: entry.Risk, Confidence: entry.Confidence, ValidFrom: entry.ValidFrom, ValidUntil: entry.ValidUntil,
		ObservedAt: entry.ObservedAt, ReviewAfter: entry.ReviewAfter, PersonalOrigin: entry.PersonalOrigin,
	}
	draft.Classification = classification
	return draft, nil
}

func validateDraftRisk(values []knowledge.RiskClass) error {
	seen := make(map[knowledge.RiskClass]struct{}, len(values))
	for _, value := range values {
		if value == knowledge.RiskClassUnspecified || !value.IsARiskClass() || value == knowledge.RiskClassProhibitedSecret {
			return fmt.Errorf("%w: candidate entry risk is invalid", knowledge.ErrInvalidRecord)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%w: candidate entry risk contains a duplicate", knowledge.ErrInvalidRecord)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateDraftTimes(entry knowledge.Entry) error {
	for _, value := range []time.Time{entry.ValidFrom, entry.ValidUntil, entry.ObservedAt, entry.ReviewAfter} {
		if !value.IsZero() {
			_, offset := value.Zone()
			if offset != 0 {
				return fmt.Errorf("%w: candidate entry timestamps must use UTC", knowledge.ErrInvalidRecord)
			}
		}
	}
	if !entry.ValidFrom.IsZero() && !entry.ValidUntil.IsZero() && !entry.ValidUntil.After(entry.ValidFrom) {
		return fmt.Errorf("%w: candidate valid_until must follow valid_from", knowledge.ErrInvalidRecord)
	}
	return nil
}

func curationSourceItemIDs(record knowledge.CurationRecord) []string {
	seen := map[string]struct{}{record.Source.UserItemID: {}, record.Source.AssistantItemID: {}}
	values := []string{record.Source.UserItemID, record.Source.AssistantItemID}
	for _, signal := range record.Signals {
		for _, id := range signal.SourceItemIDs {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			values = append(values, id)
		}
	}
	return values
}

func isCanonicalUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '7' || !strings.ContainsRune("89ab", rune(value[19])) {
		return false
	}
	for index := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if (value[index] < '0' || value[index] > '9') && (value[index] < 'a' || value[index] > 'f') {
			return false
		}
	}
	return true
}

var candidateDraftSchema = json.RawMessage(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object","additionalProperties":false,"required":["candidates"],
  "properties":{"candidates":{"type":"array","maxItems":16,"items":{"$ref":"#/$defs/candidate"}}},
  "$defs":{
    "scope":{"type":"object","additionalProperties":false,"required":["kind"],"properties":{"kind":{"enum":["global","personal","project","session","environment"]},"selector":{"type":"string"}}},
    "software":{"type":"object","additionalProperties":false,"required":["name"],"properties":{"name":{"type":"string"},"version_range":{"type":"string"}}},
    "applicability":{"type":"object","additionalProperties":false,"properties":{"operating_systems":{"type":"array","items":{"type":"string"}},"architectures":{"type":"array","items":{"type":"string"}},"software":{"type":"array","items":{"$ref":"#/$defs/software"}},"locales":{"type":"array","items":{"type":"string"}},"conditions":{"type":"array","items":{"type":"string"}}}},
    "entry":{"type":"object","additionalProperties":false,"required":["kind","title","scope","confidence"],"properties":{"kind":{"enum":["fact","procedure","concept","warning","preference","decision","reference"]},"title":{"type":"string","maxLength":240},"summary":{"type":"string","maxLength":4000},"body":{"type":"string","maxLength":262144},"aliases":{"type":"array","maxItems":64,"items":{"type":"string"}},"tags":{"type":"array","maxItems":64,"items":{"type":"string"}},"scope":{"$ref":"#/$defs/scope"},"applicability":{"$ref":"#/$defs/applicability"},"risk":{"type":"array","maxItems":6,"uniqueItems":true,"items":{"enum":["personal_sensitive","medical","legal","financial","physical_safety","security_sensitive"]}},"confidence":{"type":"number","minimum":0,"maximum":1},"valid_from":{"type":"string","format":"date-time"},"valid_until":{"type":"string","format":"date-time"},"observed_at":{"type":"string","format":"date-time"},"review_after":{"type":"string","format":"date-time"},"personal_origin":{"enum":["explicit","observed","inferred"]}}},
    "candidate":{"type":"object","additionalProperties":false,"required":["action","chunk_id","entry","reason","source_item_ids"],"properties":{"action":{"enum":["create_entry","update_entry","supersede_entry","contradict_entry"]},"chunk_id":{"type":"string"},"target_entry_id":{"type":"string"},"entry":{"$ref":"#/$defs/entry"},"reason":{"type":"string","maxLength":1000},"source_item_ids":{"type":"array","minItems":1,"maxItems":64,"items":{"type":"string"}}}}
  }
}`)
