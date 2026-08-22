package knowledge

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidRecord identifies a canonical knowledge record that violates domain invariants.
var ErrInvalidRecord = errors.New("invalid knowledge record")

func invalid(field, message string) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalidRecord, field, message)
}

func required(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return invalid(field, "is required")
	}
	return nil
}

func validateUUIDv7(field, value string) error {
	if err := required(field, value); err != nil {
		return err
	}
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return invalid(field, "must be a canonical UUIDv7")
	}
	for i := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if (value[i] < '0' || value[i] > '9') && (value[i] < 'a' || value[i] > 'f') {
			return invalid(field, "must be a lowercase canonical UUIDv7")
		}
	}
	if value[14] != '7' || !strings.ContainsRune("89ab", rune(value[19])) {
		return invalid(field, "must be a UUIDv7")
	}
	return nil
}

func validateTime(field string, value time.Time, requiredValue bool) error {
	if value.IsZero() {
		if requiredValue {
			return invalid(field, "is required")
		}
		return nil
	}
	_, offset := value.Zone()
	if offset != 0 {
		return invalid(field, "must be normalized to UTC")
	}
	return nil
}

func validateTimes(createdAt, updatedAt time.Time) error {
	if err := validateTime("created_at", createdAt, true); err != nil {
		return err
	}
	if err := validateTime("updated_at", updatedAt, true); err != nil {
		return err
	}
	if updatedAt.Before(createdAt) {
		return invalid("updated_at", "must not precede created_at")
	}
	return nil
}

func validateEvidenceIDs(field string, values []EvidenceID) error {
	seen := make(map[EvidenceID]struct{}, len(values))
	for i, value := range values {
		if err := validateUUIDv7(fmt.Sprintf("%s[%d]", field, i), string(value)); err != nil {
			return err
		}
		if _, exists := seen[value]; exists {
			return invalid(field, "contains a duplicate evidence ID")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateRisk(values []RiskClass) error {
	seen := make(map[RiskClass]struct{}, len(values))
	for i, value := range values {
		if value == RiskClassUnspecified || !value.IsARiskClass() {
			return invalid(fmt.Sprintf("risk[%d]", i), "is not a known risk class")
		}
		if value == RiskClassProhibitedSecret {
			return invalid(fmt.Sprintf("risk[%d]", i), "prohibited secrets cannot be stored")
		}
		if _, exists := seen[value]; exists {
			return invalid("risk", "contains a duplicate risk class")
		}
		seen[value] = struct{}{}
	}
	return nil
}

// Validate checks a scope's canonical invariants.
func (s Scope) Validate() error {
	if s.Kind == ScopeKindUnspecified || !s.Kind.IsAScopeKind() {
		return invalid("scope.kind", "is not a known scope kind")
	}
	if s.Kind == ScopeKindGlobal {
		if strings.TrimSpace(s.Selector) != "" {
			return invalid("scope.selector", "must be empty for global scope")
		}
		return nil
	}
	return required("scope.selector", s.Selector)
}

// Validate checks a shared-visibility principal reference.
func (p PrincipalRef) Validate() error {
	if err := required("principal.kind", p.Kind); err != nil {
		return err
	}
	return required("principal.id", p.ID)
}

// Validate checks a revision actor.
func (a Actor) Validate() error {
	if a.Kind == ActorKindUnspecified || !a.Kind.IsAActorKind() {
		return invalid("actor.kind", "is not a known actor kind")
	}
	return required("actor.id", a.ID)
}

// Validate checks immutable revision metadata.
func (r Revision) Validate() error {
	if r.Number == 0 {
		return invalid("revision.number", "must be at least 1")
	}
	if err := validateUUIDv7("revision.id", string(r.ID)); err != nil {
		return err
	}
	if err := r.Actor.Validate(); err != nil {
		return err
	}
	return validateTime("revision.created_at", r.CreatedAt, true)
}

// Validate checks verification state and its supporting evidence references.
func (v Verification) Validate() error {
	if v.Status == VerificationStatusUnspecified || !v.Status.IsAVerificationStatus() {
		return invalid("verification.status", "is not a known verification status")
	}
	if err := validateEvidenceIDs("verification.evidence_ids", v.EvidenceIDs); err != nil {
		return err
	}
	if v.Status == VerificationStatusUnverified {
		if !v.VerifiedAt.IsZero() {
			return invalid("verification.verified_at", "must be empty while unverified")
		}
		return nil
	}
	if len(v.EvidenceIDs) == 0 {
		return invalid("verification.evidence_ids", "is required for assessed verification")
	}
	if err := v.Actor.Validate(); err != nil {
		return err
	}
	return validateTime("verification.verified_at", v.VerifiedAt, true)
}

func validateVisibility(value Visibility, principals []PrincipalRef) error {
	if value == VisibilityUnspecified || !value.IsAVisibility() {
		return invalid("visibility", "is not a known visibility")
	}
	if value == VisibilityShared && len(principals) == 0 {
		return invalid("shared_with", "is required for shared visibility")
	}
	for _, principal := range principals {
		if err := principal.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks a canonical chunk projection.
func (c Chunk) Validate() error {
	if err := validateUUIDv7("id", string(c.ID)); err != nil {
		return err
	}
	if err := required("title", c.Title); err != nil {
		return err
	}
	if c.Kind == ChunkKindUnspecified || !c.Kind.IsAChunkKind() {
		return invalid("kind", "is not a known chunk kind")
	}
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	if err := validateVisibility(c.Visibility, c.SharedWith); err != nil {
		return err
	}
	if err := validateRisk(c.Risk); err != nil {
		return err
	}
	if c.State == ChunkStateUnspecified || !c.State.IsAChunkState() {
		return invalid("state", "is not a known chunk state")
	}
	if c.SchemaVersion == 0 {
		return invalid("schema_version", "must be at least 1")
	}
	if err := c.Revision.Validate(); err != nil {
		return err
	}
	if err := validateTimes(c.CreatedAt, c.UpdatedAt); err != nil {
		return err
	}
	if !c.Revision.CreatedAt.Equal(c.UpdatedAt) {
		return invalid("revision.created_at", "must equal updated_at")
	}
	for _, field := range []struct {
		name  string
		value time.Time
	}{
		{name: "last_used_at", value: c.LastUsedAt},
		{name: "last_verified_at", value: c.LastVerifiedAt},
		{name: "review_after", value: c.ReviewAfter},
	} {
		if err := validateTime(field.name, field.value, false); err != nil {
			return err
		}
	}
	seenDependencies := make(map[ChunkID]struct{}, len(c.DependencyIDs))
	for i, dependencyID := range c.DependencyIDs {
		if err := validateUUIDv7(fmt.Sprintf("dependency_ids[%d]", i), string(dependencyID)); err != nil {
			return err
		}
		if dependencyID == c.ID {
			return invalid("dependency_ids", "cannot contain the owning chunk")
		}
		if _, exists := seenDependencies[dependencyID]; exists {
			return invalid("dependency_ids", "contains a duplicate chunk ID")
		}
		seenDependencies[dependencyID] = struct{}{}
	}
	return nil
}

// Validate checks a canonical entry projection.
func (e Entry) Validate() error {
	if err := validateUUIDv7("id", string(e.ID)); err != nil {
		return err
	}
	if err := validateUUIDv7("chunk_id", string(e.ChunkID)); err != nil {
		return err
	}
	if e.Kind == EntryKindUnspecified || !e.Kind.IsAEntryKind() {
		return invalid("kind", "is not a known entry kind")
	}
	if err := required("title", e.Title); err != nil {
		return err
	}
	if err := ValidateMarkdown("body", e.Body); err != nil {
		return err
	}
	if err := e.Scope.Validate(); err != nil {
		return err
	}
	if err := validateRisk(e.Risk); err != nil {
		return err
	}
	if e.Confidence < 0 || e.Confidence > 1 {
		return invalid("confidence", "must be between 0 and 1")
	}
	if err := e.Verification.Validate(); err != nil {
		return err
	}
	if e.State == EntryStateUnspecified || !e.State.IsAEntryState() {
		return invalid("state", "is not a known entry state")
	}
	if e.State == EntryStateSuperseded {
		if err := validateUUIDv7("superseded_by_id", string(e.SupersededByID)); err != nil {
			return err
		}
		if e.SupersededByID == e.ID {
			return invalid("superseded_by_id", "cannot identify the same entry")
		}
	} else if e.SupersededByID != "" {
		return invalid("superseded_by_id", "requires superseded state")
	}
	if err := validateEvidenceIDs("evidence_ids", e.EvidenceIDs); err != nil {
		return err
	}
	if e.PersonalOrigin != PersonalOriginUnspecified && !e.PersonalOrigin.IsAPersonalOrigin() {
		return invalid("personal_origin", "is not a known personal origin")
	}
	if e.Scope.Kind == ScopeKindPersonal {
		if e.PersonalOrigin == PersonalOriginUnspecified {
			return invalid("personal_origin", "is required for personal scope")
		}
	} else if e.PersonalOrigin != PersonalOriginUnspecified {
		return invalid("personal_origin", "is only valid for personal scope")
	}
	if e.PersonalOrigin == PersonalOriginObserved {
		if e.ObservedAt.IsZero() {
			return invalid("observed_at", "is required for observed personal knowledge")
		}
		if len(e.EvidenceIDs) == 0 {
			return invalid("evidence_ids", "observed personal knowledge requires evidence")
		}
	}
	if e.PersonalOrigin == PersonalOriginInferred && (e.Confidence <= 0 || e.Confidence >= 1) {
		return invalid("confidence", "inferred personal knowledge requires explicit uncertainty between 0 and 1")
	}
	if e.IsSensitiveInference() && e.State == EntryStateActive {
		return invalid("state", "sensitive inferred personal knowledge must remain a reviewable draft")
	}
	if err := e.Revision.Validate(); err != nil {
		return err
	}
	if err := validateTimes(e.CreatedAt, e.UpdatedAt); err != nil {
		return err
	}
	if !e.Revision.CreatedAt.Equal(e.UpdatedAt) {
		return invalid("revision.created_at", "must equal updated_at")
	}
	for _, field := range []struct {
		name  string
		value time.Time
	}{
		{name: "valid_from", value: e.ValidFrom},
		{name: "valid_until", value: e.ValidUntil},
		{name: "observed_at", value: e.ObservedAt},
		{name: "review_after", value: e.ReviewAfter},
		{name: "last_used_at", value: e.LastUsedAt},
	} {
		if err := validateTime(field.name, field.value, false); err != nil {
			return err
		}
	}
	if !e.ValidFrom.IsZero() && !e.ValidUntil.IsZero() && !e.ValidUntil.After(e.ValidFrom) {
		return invalid("valid_until", "must be later than valid_from")
	}
	return nil
}

// Validate checks a generic knowledge object reference.
func (r ObjectRef) Validate() error {
	if r.Kind == ObjectKindUnspecified || !r.Kind.IsAObjectKind() {
		return invalid("object.kind", "is not a known object kind")
	}
	return validateUUIDv7("object.id", r.ID)
}

func validateEndpoint(field string, endpoint ObjectRef) error {
	if err := endpoint.Validate(); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	if endpoint.Kind != ObjectKindChunk && endpoint.Kind != ObjectKindEntry {
		return invalid(field+".kind", "must identify a chunk or entry")
	}
	return nil
}

// Validate checks a canonical graph link projection.
func (l Link) Validate() error {
	if err := validateUUIDv7("id", string(l.ID)); err != nil {
		return err
	}
	if err := ValidateRelationshipShape(l.Kind, l.Source, l.Target); err != nil {
		return err
	}
	if err := validateEvidenceIDs("evidence_ids", l.EvidenceIDs); err != nil {
		return err
	}
	if l.State == LinkStateUnspecified || !l.State.IsALinkState() {
		return invalid("state", "is not a known link state")
	}
	if err := l.Revision.Validate(); err != nil {
		return err
	}
	if err := validateTimes(l.CreatedAt, l.UpdatedAt); err != nil {
		return err
	}
	if !l.Revision.CreatedAt.Equal(l.UpdatedAt) {
		return invalid("revision.created_at", "must equal updated_at")
	}
	return nil
}

// Validate checks an evidence source's common invariants.
func (s Source) Validate() error {
	if err := required("source.id", s.ID); err != nil {
		return err
	}
	return validateTime("source.accessed_at", s.AccessedAt, false)
}

// Validate checks a canonical immutable evidence record.
func (e Evidence) Validate() error {
	if err := validateUUIDv7("id", string(e.ID)); err != nil {
		return err
	}
	if e.Type == EvidenceTypeUnspecified || !e.Type.IsAEvidenceType() {
		return invalid("type", "is not a known evidence type")
	}
	if e.Quality == EvidenceQualityUnspecified || !e.Quality.IsAEvidenceQuality() {
		return invalid("quality", "is not a known evidence quality")
	}
	if err := e.Source.Validate(); err != nil {
		return err
	}
	if e.Type == EvidenceTypeFile {
		if err := required("source.uri", e.Source.URI); err != nil {
			return err
		}
		if err := required("source.content_hash", e.Source.ContentHash); err != nil {
			return err
		}
	}
	if e.Type == EvidenceTypeWeb {
		if err := required("source.uri", e.Source.URI); err != nil {
			return err
		}
		if err := required("source.title", e.Source.Title); err != nil {
			return err
		}
		if e.Source.ContentHash == "" && e.Source.Excerpt == "" {
			return invalid("source", "web evidence requires a content hash or excerpt")
		}
		if err := validateTime("source.accessed_at", e.Source.AccessedAt, true); err != nil {
			return err
		}
	}
	if err := e.Actor.Validate(); err != nil {
		return err
	}
	if err := validateTime("observed_at", e.ObservedAt, false); err != nil {
		return err
	}
	return validateTime("created_at", e.CreatedAt, true)
}
