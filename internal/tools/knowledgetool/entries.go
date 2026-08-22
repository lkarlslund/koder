package knowledgetool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
)

type entryPatch struct {
	Kind           *knowledge.EntryKind      `json:"kind,omitempty"`
	Title          *string                   `json:"title,omitempty"`
	Summary        *string                   `json:"summary,omitempty"`
	Body           *string                   `json:"body,omitempty"`
	Aliases        *[]string                 `json:"aliases,omitempty"`
	Tags           *[]string                 `json:"tags,omitempty"`
	Scope          *knowledge.Scope          `json:"scope,omitempty"`
	Applicability  *knowledge.Applicability  `json:"applicability,omitempty"`
	Risk           *[]knowledge.RiskClass    `json:"risk,omitempty"`
	Confidence     *float32                  `json:"confidence,omitempty"`
	ValidFrom      *time.Time                `json:"valid_from,omitempty"`
	ValidUntil     *time.Time                `json:"valid_until,omitempty"`
	ObservedAt     *time.Time                `json:"observed_at,omitempty"`
	ReviewAfter    *time.Time                `json:"review_after,omitempty"`
	EvidenceIDs    *[]knowledge.EvidenceID   `json:"evidence_ids,omitempty"`
	PersonalOrigin *knowledge.PersonalOrigin `json:"personal_origin,omitempty"`
}

type verificationInput struct {
	Status      knowledge.VerificationStatus `json:"status"`
	Method      string                       `json:"method,omitempty"`
	EvidenceIDs []knowledge.EvidenceID       `json:"evidence_ids,omitempty"`
}

type entryMutationResult struct {
	Entry          knowledge.Entry                 `json:"entry"`
	Replacement    *knowledge.Entry                `json:"replacement,omitempty"`
	Created        bool                            `json:"created,omitempty"`
	Updated        bool                            `json:"updated,omitempty"`
	Classification *knowledge.ClassificationResult `json:"classification,omitempty"`
}

type entryDeleteResult struct {
	ID      knowledge.EntryID `json:"id"`
	Deleted bool              `json:"deleted"`
}

func normalizeEntryCreateArgs(args map[string]string) (map[string]string, error) {
	chunkID, err := normalizeChunkID(args["chunk_id"])
	if err != nil {
		return nil, fmt.Errorf("chunk_id: %w", err)
	}
	patch, encoded, err := normalizeEntryPatch(args["entry"])
	if err != nil {
		return nil, err
	}
	if _, err := patch.createCandidate(); err != nil {
		return nil, err
	}
	out := map[string]string{"action": "entry_create", "chunk_id": string(chunkID), "entry": encoded}
	if err := normalizeBool(args, out, "review_approved"); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeEntryUpdateArgs(args map[string]string) (map[string]string, error) {
	entryID, err := normalizeEntryID(args["id"])
	if err != nil {
		return nil, err
	}
	revision, err := normalizeExpectedRevision(args)
	if err != nil {
		return nil, err
	}
	patch, encoded, err := normalizeEntryPatch(args["entry"])
	if err != nil {
		return nil, err
	}
	if !patch.hasChanges() {
		return nil, errors.New("entry_update requires at least one editable entry field")
	}
	out := map[string]string{
		"action": "entry_update", "id": string(entryID),
		"expected_revision": strconv.FormatUint(revision, 10), "entry": encoded,
	}
	if err := normalizeReason(args, out); err != nil {
		return nil, err
	}
	if err := normalizeBool(args, out, "review_approved"); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeEntrySupersedeArgs(args map[string]string) (map[string]string, error) {
	out, err := normalizeEntryLifecycleArgs(args, "entry_supersede")
	if err != nil {
		return nil, err
	}
	replacementID, err := normalizeEntryID(args["replacement_entry_id"])
	if err != nil {
		return nil, fmt.Errorf("replacement_entry_id: %w", err)
	}
	out["replacement_entry_id"] = string(replacementID)
	return out, nil
}

func normalizeEntryLifecycleArgs(args map[string]string, action string) (map[string]string, error) {
	entryID, err := normalizeEntryID(args["id"])
	if err != nil {
		return nil, err
	}
	revision, err := normalizeExpectedRevision(args)
	if err != nil {
		return nil, err
	}
	out := map[string]string{"action": action, "id": string(entryID), "expected_revision": strconv.FormatUint(revision, 10)}
	if err := normalizeReason(args, out); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeEntryDeleteArgs(args map[string]string) (map[string]string, error) {
	out, err := normalizeEntryLifecycleArgs(args, "entry_delete")
	if err != nil {
		return nil, err
	}
	if err := normalizeBool(args, out, "confirmed"); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeVerifyArgs(args map[string]string) (map[string]string, error) {
	out, err := normalizeEntryLifecycleArgs(args, "verify")
	if err != nil {
		return nil, err
	}
	verification, encoded, err := normalizeVerification(args["verification"])
	if err != nil {
		return nil, err
	}
	if verification.Status != knowledge.VerificationStatusUnverified && len(verification.EvidenceIDs) == 0 {
		return nil, errors.New("assessed verification requires verification.evidence_ids")
	}
	out["verification"] = encoded
	return out, nil
}

func normalizeEntryPatch(raw string) (entryPatch, string, error) {
	var patch entryPatch
	encoded, err := decodeStrictObject(raw, "entry", &patch)
	if err != nil {
		return entryPatch{}, "", err
	}
	if err := patch.validateBounds(); err != nil {
		return entryPatch{}, "", err
	}
	return patch, encoded, nil
}

func normalizeVerification(raw string) (verificationInput, string, error) {
	var input verificationInput
	encoded, err := decodeStrictObject(raw, "verification", &input)
	if err != nil {
		return verificationInput{}, "", err
	}
	if input.Status == knowledge.VerificationStatusUnspecified || !input.Status.IsAVerificationStatus() {
		return verificationInput{}, "", errors.New("verification.status is required")
	}
	if len(input.Method) > 4<<10 {
		return verificationInput{}, "", errors.New("verification.method exceeds 4 KiB")
	}
	if len(input.EvidenceIDs) > 100 {
		return verificationInput{}, "", errors.New("verification.evidence_ids exceeds 100 items")
	}
	if err := validateEvidenceIDs(input.EvidenceIDs); err != nil {
		return verificationInput{}, "", err
	}
	if input.Status == knowledge.VerificationStatusUnverified {
		input.Method = ""
		input.EvidenceIDs = nil
		data, err := json.Marshal(input)
		if err != nil {
			return verificationInput{}, "", fmt.Errorf("encode verification object: %w", err)
		}
		encoded = string(data)
	}
	return input, encoded, nil
}

func decodeStrictObject(raw, name string, destination any) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%s object is required", name)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", fmt.Errorf("decode %s object: %w", name, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("%s object contains multiple JSON values", name)
		}
		return "", fmt.Errorf("decode %s object: %w", name, err)
	}
	data, err := json.Marshal(destination)
	if err != nil {
		return "", fmt.Errorf("encode %s object: %w", name, err)
	}
	return string(data), nil
}

func (p entryPatch) createCandidate() (knowledge.Entry, error) {
	if p.Kind == nil || p.Title == nil {
		return knowledge.Entry{}, errors.New("entry_create requires entry.kind and entry.title")
	}
	return entryFromContent(p.apply(knowledgeService.EntryContent{})), nil
}

func (p entryPatch) apply(content knowledgeService.EntryContent) knowledgeService.EntryContent {
	if p.Kind != nil {
		content.Kind = *p.Kind
	}
	if p.Title != nil {
		content.Title = *p.Title
	}
	if p.Summary != nil {
		content.Summary = *p.Summary
	}
	if p.Body != nil {
		content.Body = *p.Body
	}
	if p.Aliases != nil {
		content.Aliases = slices.Clone(*p.Aliases)
	}
	if p.Tags != nil {
		content.Tags = slices.Clone(*p.Tags)
	}
	if p.Scope != nil {
		content.Scope = *p.Scope
	}
	if p.Applicability != nil {
		content.Applicability = cloneApplicability(*p.Applicability)
	}
	if p.Risk != nil {
		content.Risk = slices.Clone(*p.Risk)
	}
	if p.Confidence != nil {
		content.Confidence = *p.Confidence
	}
	if p.ValidFrom != nil {
		content.ValidFrom = p.ValidFrom.UTC().Round(0)
	}
	if p.ValidUntil != nil {
		content.ValidUntil = p.ValidUntil.UTC().Round(0)
	}
	if p.ObservedAt != nil {
		content.ObservedAt = p.ObservedAt.UTC().Round(0)
	}
	if p.ReviewAfter != nil {
		content.ReviewAfter = p.ReviewAfter.UTC().Round(0)
	}
	if p.EvidenceIDs != nil {
		content.EvidenceIDs = slices.Clone(*p.EvidenceIDs)
	}
	if p.PersonalOrigin != nil {
		content.PersonalOrigin = *p.PersonalOrigin
	}
	return content
}

func (p entryPatch) hasChanges() bool {
	return p.Kind != nil || p.Title != nil || p.Summary != nil || p.Body != nil || p.Aliases != nil || p.Tags != nil ||
		p.Scope != nil || p.Applicability != nil || p.Risk != nil || p.Confidence != nil || p.ValidFrom != nil ||
		p.ValidUntil != nil || p.ObservedAt != nil || p.ReviewAfter != nil || p.EvidenceIDs != nil || p.PersonalOrigin != nil
}

func (p entryPatch) validateBounds() error {
	for _, field := range []struct {
		name    string
		values  *[]string
		maximum int
	}{
		{name: "entry.aliases", values: p.Aliases, maximum: 100},
		{name: "entry.tags", values: p.Tags, maximum: 100},
	} {
		if field.values != nil && len(*field.values) > field.maximum {
			return fmt.Errorf("%s exceeds %d items", field.name, field.maximum)
		}
	}
	if p.Risk != nil && len(*p.Risk) > 6 {
		return errors.New("entry.risk exceeds 6 items")
	}
	if p.EvidenceIDs != nil {
		if len(*p.EvidenceIDs) > 100 {
			return errors.New("entry.evidence_ids exceeds 100 items")
		}
		if err := validateEvidenceIDs(*p.EvidenceIDs); err != nil {
			return err
		}
	}
	if p.Applicability != nil {
		for _, field := range []struct {
			name    string
			length  int
			maximum int
		}{
			{name: "entry.applicability.operating_systems", length: len(p.Applicability.OperatingSystems), maximum: 50},
			{name: "entry.applicability.architectures", length: len(p.Applicability.Architectures), maximum: 50},
			{name: "entry.applicability.software", length: len(p.Applicability.Software), maximum: 50},
			{name: "entry.applicability.locales", length: len(p.Applicability.Locales), maximum: 50},
			{name: "entry.applicability.conditions", length: len(p.Applicability.Conditions), maximum: 100},
		} {
			if field.length > field.maximum {
				return fmt.Errorf("%s exceeds %d items", field.name, field.maximum)
			}
		}
	}
	return nil
}

func entryFromContent(content knowledgeService.EntryContent) knowledge.Entry {
	return knowledge.Entry{
		Kind: content.Kind, Title: content.Title, Summary: content.Summary, Body: content.Body,
		Aliases: content.Aliases, Tags: content.Tags, Scope: content.Scope, Applicability: content.Applicability,
		Risk: content.Risk, Confidence: content.Confidence, ValidFrom: content.ValidFrom, ValidUntil: content.ValidUntil,
		ObservedAt: content.ObservedAt, ReviewAfter: content.ReviewAfter, EvidenceIDs: content.EvidenceIDs,
		PersonalOrigin: content.PersonalOrigin,
	}
}

func cloneApplicability(value knowledge.Applicability) knowledge.Applicability {
	value.OperatingSystems = slices.Clone(value.OperatingSystems)
	value.Architectures = slices.Clone(value.Architectures)
	value.Software = slices.Clone(value.Software)
	value.Locales = slices.Clone(value.Locales)
	value.Conditions = slices.Clone(value.Conditions)
	return value
}

func normalizeEntryID(raw string) (knowledge.EntryID, error) {
	object := knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: strings.TrimSpace(raw)}
	if err := object.Validate(); err != nil {
		return "", err
	}
	return knowledge.EntryID(object.ID), nil
}

func validateEvidenceIDs(values []knowledge.EvidenceID) error {
	seen := make(map[knowledge.EvidenceID]struct{}, len(values))
	for _, value := range values {
		if !isCanonicalUUIDv7(string(value)) {
			return fmt.Errorf("invalid evidence ID %q", value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate evidence ID %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func isCanonicalUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '7' || !strings.ContainsRune("89ab", rune(value[19])) {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func callEntryCreate(ctx context.Context, service *knowledgeService.Service, args map[string]string) (entryMutationResult, error) {
	if _, err := service.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: args["chunk_id"]}); err != nil {
		return entryMutationResult{}, err
	}
	patch, _, err := normalizeEntryPatch(args["entry"])
	if err != nil {
		return entryMutationResult{}, err
	}
	candidate, err := patch.createCandidate()
	if err != nil {
		return entryMutationResult{}, err
	}
	result, err := service.CreateEntry(ctx, knowledgeService.CreateEntryRequest{
		ChunkID: knowledge.ChunkID(args["chunk_id"]), Entry: candidate,
		ReviewApproved: boolArg(args, "review_approved"),
	})
	if err != nil {
		return entryMutationResult{}, err
	}
	classification := result.Classification
	return entryMutationResult{Entry: result.Entry, Created: true, Classification: &classification}, nil
}

func callEntryUpdate(ctx context.Context, service *knowledgeService.Service, args map[string]string) (entryMutationResult, error) {
	currentRecord, err := service.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: args["id"]})
	if err != nil {
		return entryMutationResult{}, err
	}
	if currentRecord.Entry == nil {
		return entryMutationResult{}, errors.New("knowledge entry projection is unavailable")
	}
	patch, _, err := normalizeEntryPatch(args["entry"])
	if err != nil {
		return entryMutationResult{}, err
	}
	result, err := service.UpdateEntry(ctx, knowledgeService.UpdateEntryRequest{
		EntryID: knowledge.EntryID(args["id"]), ExpectedRevision: uintArg(args, "expected_revision"),
		Content: patch.apply(knowledgeService.EntryContentFrom(*currentRecord.Entry)), Reason: args["reason"],
		ReviewApproved: boolArg(args, "review_approved"),
	})
	if err != nil {
		return entryMutationResult{}, err
	}
	classification := result.Classification
	return entryMutationResult{Entry: result.Entry, Updated: result.Updated, Classification: &classification}, nil
}

func callEntrySupersede(ctx context.Context, service *knowledgeService.Service, args map[string]string) (entryMutationResult, error) {
	for _, entryID := range []string{args["id"], args["replacement_entry_id"]} {
		if _, err := service.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: entryID}); err != nil {
			return entryMutationResult{}, err
		}
	}
	result, err := service.SupersedeEntry(ctx, knowledgeService.SupersedeEntryRequest{
		EntryID: knowledge.EntryID(args["id"]), ExpectedRevision: uintArg(args, "expected_revision"),
		ReplacementEntryID: knowledge.EntryID(args["replacement_entry_id"]), Reason: args["reason"],
	})
	if err != nil {
		return entryMutationResult{}, err
	}
	replacement := result.Replacement
	return entryMutationResult{Entry: result.Entry, Replacement: &replacement, Updated: result.Updated}, nil
}

func callEntryLifecycle(ctx context.Context, service *knowledgeService.Service, args map[string]string) (entryMutationResult, error) {
	if _, err := service.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: args["id"]}); err != nil {
		return entryMutationResult{}, err
	}
	request := knowledgeService.EntryLifecycleRequest{
		EntryID: knowledge.EntryID(args["id"]), ExpectedRevision: uintArg(args, "expected_revision"), Reason: args["reason"],
	}
	var result knowledgeService.EntryLifecycleResult
	var err error
	if args["action"] == "entry_archive" {
		result, err = service.ArchiveEntry(ctx, request)
	} else {
		result, err = service.RestoreEntry(ctx, request)
	}
	if err != nil {
		return entryMutationResult{}, err
	}
	return entryMutationResult{Entry: result.Entry, Updated: result.Updated}, nil
}

func callEntryDelete(ctx context.Context, service *knowledgeService.Service, args map[string]string) (entryDeleteResult, error) {
	if _, err := service.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: args["id"]}); err != nil {
		return entryDeleteResult{}, err
	}
	request := knowledgeService.DeleteEntryRequest{
		EntryID: knowledge.EntryID(args["id"]), ExpectedRevision: uintArg(args, "expected_revision"),
		Confirmed: boolArg(args, "confirmed"),
	}
	if err := service.DeleteEntry(ctx, request); err != nil {
		return entryDeleteResult{}, err
	}
	return entryDeleteResult{ID: request.EntryID, Deleted: true}, nil
}

func callVerify(ctx context.Context, service *knowledgeService.Service, args map[string]string) (entryMutationResult, error) {
	if _, err := service.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: args["id"]}); err != nil {
		return entryMutationResult{}, err
	}
	verification, _, err := normalizeVerification(args["verification"])
	if err != nil {
		return entryMutationResult{}, err
	}
	result, err := service.VerifyEntry(ctx, knowledgeService.VerifyEntryRequest{
		EntryID: knowledge.EntryID(args["id"]), ExpectedRevision: uintArg(args, "expected_revision"),
		Status: verification.Status, Method: verification.Method, EvidenceIDs: verification.EvidenceIDs,
		Reason: args["reason"],
	})
	if err != nil {
		return entryMutationResult{}, err
	}
	return entryMutationResult{Entry: result.Entry, Updated: result.Updated}, nil
}
