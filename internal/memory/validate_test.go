package memory

import (
	"errors"
	"testing"
	"time"
)

const (
	testChunkID     ChunkID    = "019f132e-4f3a-739a-9ab2-5198dcd19e67"
	testOtherChunk  ChunkID    = "019f132e-4f3a-739a-9ab2-5198dcd19e68"
	testEntryID     EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd33"
	testOtherEntry  EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd34"
	testLinkID      LinkID     = "01a020a6-84d5-7b03-a995-bb2cfb4528b0"
	testEvidenceID  EvidenceID = "01a01688-fc6b-7a53-a907-4f903461820e"
	testRevisionID  RevisionID = "01a01688-fc5d-7f7d-8bb8-de244977f8a1"
	testRevisionID2 RevisionID = "01a01688-fc5d-7f7d-8bb8-de244977f8a2"
)

var testTime = time.Date(2026, 8, 22, 12, 30, 45, 123, time.UTC)

func validActor() Actor {
	return Actor{Kind: ActorKindSystem, ID: "koder"}
}

func validRevision(id RevisionID) Revision {
	return Revision{Number: 1, ID: id, Actor: validActor(), CreatedAt: testTime}
}

func validChunk() Chunk {
	return Chunk{
		ID:            testChunkID,
		Title:         "Linux partition tools",
		Kind:          ChunkKindEnvironment,
		Scope:         Scope{Kind: ScopeKindEnvironment, Selector: "host:workstation"},
		Visibility:    VisibilityPrivate,
		State:         ChunkStateActive,
		SchemaVersion: 1,
		Revision:      validRevision(testRevisionID),
		CreatedAt:     testTime,
		UpdatedAt:     testTime,
	}
}

func validVerification() Verification {
	return Verification{Status: VerificationStatusUnverified}
}

func assessedVerification(status VerificationStatus) Verification {
	return Verification{
		Status:      status,
		EvidenceIDs: []EvidenceID{testEvidenceID},
		Actor:       validActor(),
		VerifiedAt:  testTime,
	}
}

func validEntry() Entry {
	return Entry{
		ID:           testEntryID,
		ChunkID:      testChunkID,
		Kind:         EntryKindProcedure,
		Title:        "Use sfdisk for scripted changes",
		Scope:        Scope{Kind: ScopeKindEnvironment, Selector: "host:workstation"},
		Verification: validVerification(),
		State:        EntryStateActive,
		Revision:     validRevision(testRevisionID),
		CreatedAt:    testTime,
		UpdatedAt:    testTime,
	}
}

func validLink() Link {
	return Link{
		ID:        testLinkID,
		Source:    ObjectRef{Kind: ObjectKindEntry, ID: string(testEntryID)},
		Target:    ObjectRef{Kind: ObjectKindEntry, ID: string(testOtherEntry)},
		Kind:      LinkKindAlternativeTo,
		State:     LinkStateActive,
		Revision:  validRevision(testRevisionID),
		CreatedAt: testTime,
		UpdatedAt: testTime,
	}
}

func validEvidence() Evidence {
	return Evidence{
		ID:        testEvidenceID,
		Type:      EvidenceTypeObservation,
		Quality:   EvidenceQualityPrimary,
		Source:    Source{ID: "observation:partition-success"},
		Actor:     validActor(),
		CreatedAt: testTime,
	}
}

func expectInvalid(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("expected ErrInvalidRecord, got %v", err)
	}
}

func TestCanonicalRecordsValidate(t *testing.T) {
	t.Parallel()
	for name, validate := range map[string]func() error{
		"chunk":    validChunk().Validate,
		"entry":    validEntry().Validate,
		"link":     validLink().Validate,
		"evidence": validEvidence().Validate,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestChunkValidateRequiredFields(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*Chunk){
		"id":             func(value *Chunk) { value.ID = "" },
		"title":          func(value *Chunk) { value.Title = "" },
		"kind":           func(value *Chunk) { value.Kind = ChunkKindUnspecified },
		"scope":          func(value *Chunk) { value.Scope = Scope{} },
		"visibility":     func(value *Chunk) { value.Visibility = VisibilityUnspecified },
		"state":          func(value *Chunk) { value.State = ChunkStateUnspecified },
		"schema_version": func(value *Chunk) { value.SchemaVersion = 0 },
		"revision":       func(value *Chunk) { value.Revision = Revision{} },
		"created_at":     func(value *Chunk) { value.CreatedAt = time.Time{} },
		"updated_at":     func(value *Chunk) { value.UpdatedAt = time.Time{} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			value := validChunk()
			mutate(&value)
			expectInvalid(t, value.Validate())
		})
	}
}

func TestEntryValidateRequiredFields(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*Entry){
		"id":           func(value *Entry) { value.ID = "" },
		"chunk_id":     func(value *Entry) { value.ChunkID = "" },
		"kind":         func(value *Entry) { value.Kind = EntryKindUnspecified },
		"title":        func(value *Entry) { value.Title = "" },
		"scope":        func(value *Entry) { value.Scope = Scope{} },
		"verification": func(value *Entry) { value.Verification = Verification{} },
		"state":        func(value *Entry) { value.State = EntryStateUnspecified },
		"revision":     func(value *Entry) { value.Revision = Revision{} },
		"created_at":   func(value *Entry) { value.CreatedAt = time.Time{} },
		"updated_at":   func(value *Entry) { value.UpdatedAt = time.Time{} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			value := validEntry()
			mutate(&value)
			expectInvalid(t, value.Validate())
		})
	}
}

func TestLinkValidateRequiredFields(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*Link){
		"id":         func(value *Link) { value.ID = "" },
		"source":     func(value *Link) { value.Source = ObjectRef{} },
		"target":     func(value *Link) { value.Target = ObjectRef{} },
		"kind":       func(value *Link) { value.Kind = LinkKindUnspecified },
		"state":      func(value *Link) { value.State = LinkStateUnspecified },
		"revision":   func(value *Link) { value.Revision = Revision{} },
		"created_at": func(value *Link) { value.CreatedAt = time.Time{} },
		"updated_at": func(value *Link) { value.UpdatedAt = time.Time{} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			value := validLink()
			mutate(&value)
			expectInvalid(t, value.Validate())
		})
	}
}

func TestEvidenceValidateRequiredFields(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*Evidence){
		"id":         func(value *Evidence) { value.ID = "" },
		"type":       func(value *Evidence) { value.Type = EvidenceTypeUnspecified },
		"quality":    func(value *Evidence) { value.Quality = EvidenceQualityUnspecified },
		"source":     func(value *Evidence) { value.Source = Source{} },
		"actor":      func(value *Evidence) { value.Actor = Actor{} },
		"created_at": func(value *Evidence) { value.CreatedAt = time.Time{} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			value := validEvidence()
			mutate(&value)
			expectInvalid(t, value.Validate())
		})
	}
}

func TestNestedRequiredFields(t *testing.T) {
	t.Parallel()
	tests := map[string]func() error{
		"global scope selector": func() error {
			return (Scope{Kind: ScopeKindGlobal, Selector: "unexpected"}).Validate()
		},
		"non-global scope selector": func() error {
			return (Scope{Kind: ScopeKindProject}).Validate()
		},
		"principal kind": func() error {
			return (PrincipalRef{ID: "user"}).Validate()
		},
		"principal id": func() error {
			return (PrincipalRef{Kind: "user"}).Validate()
		},
		"actor kind": func() error {
			return (Actor{ID: "koder"}).Validate()
		},
		"actor id": func() error {
			return (Actor{Kind: ActorKindSystem}).Validate()
		},
		"revision number": func() error {
			value := validRevision(testRevisionID)
			value.Number = 0
			return value.Validate()
		},
		"revision id": func() error {
			value := validRevision("")
			return value.Validate()
		},
		"revision actor": func() error {
			value := validRevision(testRevisionID)
			value.Actor = Actor{}
			return value.Validate()
		},
		"revision created_at": func() error {
			value := validRevision(testRevisionID)
			value.CreatedAt = time.Time{}
			return value.Validate()
		},
		"shared principals": func() error {
			value := validChunk()
			value.Visibility = VisibilityShared
			return value.Validate()
		},
		"personal origin": func() error {
			value := validEntry()
			value.Scope = Scope{Kind: ScopeKindPersonal, Selector: "user:local"}
			return value.Validate()
		},
		"superseding entry": func() error {
			value := validEntry()
			value.State = EntryStateSuperseded
			return value.Validate()
		},
		"assessed verification evidence": func() error {
			return (Verification{Status: VerificationStatusVerified, Actor: validActor(), VerifiedAt: testTime}).Validate()
		},
		"web uri": func() error {
			value := validEvidence()
			value.Type = EvidenceTypeWeb
			value.Source = Source{ID: "web:source", Title: "Source", Excerpt: "bounded", AccessedAt: testTime}
			return value.Validate()
		},
		"file hash": func() error {
			value := validEvidence()
			value.Type = EvidenceTypeFile
			value.Source = Source{ID: "file:source", URI: "docs/file.md"}
			return value.Validate()
		},
	}
	for name, validate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			expectInvalid(t, validate())
		})
	}
}

func TestEveryEnumValueIsValidated(t *testing.T) {
	t.Parallel()

	for _, value := range []ChunkKind{ChunkKindReference, ChunkKindPersonal, ChunkKindProject, ChunkKindEnvironment} {
		chunk := validChunk()
		chunk.Kind = value
		if err := chunk.Validate(); err != nil {
			t.Errorf("ChunkKind %v rejected: %v", value, err)
		}
	}
	for _, value := range []ScopeKind{ScopeKindGlobal, ScopeKindPersonal, ScopeKindProject, ScopeKindSession, ScopeKindEnvironment} {
		scope := Scope{Kind: value, Selector: "scope:id"}
		if value == ScopeKindGlobal {
			scope.Selector = ""
		}
		if err := scope.Validate(); err != nil {
			t.Errorf("ScopeKind %v rejected: %v", value, err)
		}
	}
	for _, value := range []Visibility{VisibilityPrivate, VisibilityInstallation, VisibilityShared, VisibilityPublic} {
		chunk := validChunk()
		chunk.Visibility = value
		if value == VisibilityShared {
			chunk.SharedWith = []PrincipalRef{{Kind: "user", ID: "local"}}
		}
		if err := chunk.Validate(); err != nil {
			t.Errorf("Visibility %v rejected: %v", value, err)
		}
	}
	for _, value := range []ChunkState{ChunkStateDraft, ChunkStateActive, ChunkStateArchived} {
		chunk := validChunk()
		chunk.State = value
		if err := chunk.Validate(); err != nil {
			t.Errorf("ChunkState %v rejected: %v", value, err)
		}
	}
	for _, value := range []EntryKind{EntryKindFact, EntryKindProcedure, EntryKindConcept, EntryKindWarning, EntryKindPreference, EntryKindDecision, EntryKindReference} {
		entry := validEntry()
		entry.Kind = value
		if err := entry.Validate(); err != nil {
			t.Errorf("EntryKind %v rejected: %v", value, err)
		}
	}
	for _, value := range []EntryState{EntryStateDraft, EntryStateActive, EntryStateSuperseded, EntryStateArchived} {
		entry := validEntry()
		entry.State = value
		if value == EntryStateSuperseded {
			entry.SupersededByID = testOtherEntry
		}
		if err := entry.Validate(); err != nil {
			t.Errorf("EntryState %v rejected: %v", value, err)
		}
	}
	for _, value := range []LinkKind{LinkKindRelatedTo, LinkKindPartOf, LinkKindRequires, LinkKindAlternativeTo, LinkKindAppliesTo, LinkKindSupersedes, LinkKindContradicts, LinkKindCausedBy, LinkKindSupportedBy, LinkKindDerivedFrom} {
		link := validLink()
		link.Kind = value
		if value == LinkKindPartOf {
			link.Target = ObjectRef{Kind: ObjectKindChunk, ID: string(testOtherChunk)}
		}
		if err := link.Validate(); err != nil {
			t.Errorf("LinkKind %v rejected: %v", value, err)
		}
	}
	for _, value := range []LinkState{LinkStateActive, LinkStateArchived} {
		link := validLink()
		link.State = value
		if err := link.Validate(); err != nil {
			t.Errorf("LinkState %v rejected: %v", value, err)
		}
	}
	for _, value := range []RiskClass{RiskClassPersonalSensitive, RiskClassMedical, RiskClassLegal, RiskClassFinancial, RiskClassPhysicalSafety, RiskClassSecuritySensitive} {
		chunk := validChunk()
		chunk.Risk = []RiskClass{value}
		if err := chunk.Validate(); err != nil {
			t.Errorf("RiskClass %v rejected: %v", value, err)
		}
	}
	for _, value := range []VerificationStatus{VerificationStatusUnverified, VerificationStatusPartiallyVerified, VerificationStatusVerified, VerificationStatusDisputed} {
		verification := validVerification()
		if value != VerificationStatusUnverified {
			verification = assessedVerification(value)
		}
		if err := verification.Validate(); err != nil {
			t.Errorf("VerificationStatus %v rejected: %v", value, err)
		}
	}
	for _, value := range []EvidenceType{EvidenceTypeUserStatement, EvidenceTypeChatTurn, EvidenceTypeToolResult, EvidenceTypeFile, EvidenceTypeWeb, EvidenceTypePackage, EvidenceTypeObservation} {
		evidence := validEvidence()
		evidence.Type = value
		switch value {
		case EvidenceTypeFile:
			evidence.Source.URI = "docs/memory.md"
			evidence.Source.ContentHash = "sha256:abc"
		case EvidenceTypeWeb:
			evidence.Source.URI = "https://example.invalid/source"
			evidence.Source.Title = "Source"
			evidence.Source.Excerpt = "bounded excerpt"
			evidence.Source.AccessedAt = testTime
		}
		if err := evidence.Validate(); err != nil {
			t.Errorf("EvidenceType %v rejected: %v", value, err)
		}
	}
	for _, value := range []EvidenceQuality{EvidenceQualityPrimary, EvidenceQualityAuthoritative, EvidenceQualitySecondary, EvidenceQualityAnecdotal, EvidenceQualityGenerated} {
		evidence := validEvidence()
		evidence.Quality = value
		if err := evidence.Validate(); err != nil {
			t.Errorf("EvidenceQuality %v rejected: %v", value, err)
		}
	}
	for _, value := range []PersonalOrigin{PersonalOriginExplicit, PersonalOriginObserved, PersonalOriginInferred} {
		entry := validEntry()
		entry.Scope = Scope{Kind: ScopeKindPersonal, Selector: "user:local"}
		entry.PersonalOrigin = value
		if value == PersonalOriginObserved {
			entry.ObservedAt = testTime
			entry.EvidenceIDs = []EvidenceID{testEvidenceID}
		}
		if value == PersonalOriginInferred {
			entry.Confidence = 0.6
		}
		if err := entry.Validate(); err != nil {
			t.Errorf("PersonalOrigin %v rejected: %v", value, err)
		}
	}
	for _, value := range []ObjectKind{ObjectKindChunk, ObjectKindEntry, ObjectKindLink} {
		if err := (ObjectRef{Kind: value, ID: string(testEntryID)}).Validate(); err != nil {
			t.Errorf("ObjectKind %v rejected: %v", value, err)
		}
	}
	for _, value := range []ActorKind{ActorKindUser, ActorKindChat, ActorKindSystem, ActorKindImport} {
		if err := (Actor{Kind: value, ID: "actor"}).Validate(); err != nil {
			t.Errorf("ActorKind %v rejected: %v", value, err)
		}
	}
}

func TestPersonalOriginStructuralPolicy(t *testing.T) {
	t.Parallel()
	tests := map[string]func() error{
		"origin on non-personal entry": func() error {
			entry := validEntry()
			entry.PersonalOrigin = PersonalOriginExplicit
			return entry.Validate()
		},
		"missing personal origin": func() error {
			entry := validEntry()
			entry.Scope = Scope{Kind: ScopeKindPersonal, Selector: "me"}
			return entry.Validate()
		},
		"observed without timestamp": func() error {
			entry := validEntry()
			entry.Scope = Scope{Kind: ScopeKindPersonal, Selector: "me"}
			entry.PersonalOrigin = PersonalOriginObserved
			entry.EvidenceIDs = []EvidenceID{testEvidenceID}
			return entry.Validate()
		},
		"observed without evidence": func() error {
			entry := validEntry()
			entry.Scope = Scope{Kind: ScopeKindPersonal, Selector: "me"}
			entry.PersonalOrigin = PersonalOriginObserved
			entry.ObservedAt = testTime
			return entry.Validate()
		},
		"inferred without uncertainty": func() error {
			entry := validEntry()
			entry.Scope = Scope{Kind: ScopeKindPersonal, Selector: "me"}
			entry.PersonalOrigin = PersonalOriginInferred
			entry.Confidence = 1
			return entry.Validate()
		},
	}
	for name, validate := range tests {
		t.Run(name, func(t *testing.T) { expectInvalid(t, validate()) })
	}
}

func TestSensitiveInferredPersonalMemoryCannotBeActive(t *testing.T) {
	t.Parallel()
	for _, risk := range []RiskClass{
		RiskClassPersonalSensitive, RiskClassMedical, RiskClassLegal, RiskClassFinancial,
		RiskClassPhysicalSafety, RiskClassSecuritySensitive,
	} {
		entry := validEntry()
		entry.Scope = Scope{Kind: ScopeKindPersonal, Selector: "me"}
		entry.PersonalOrigin = PersonalOriginInferred
		entry.Confidence = 0.6
		entry.Risk = []RiskClass{risk}
		if !entry.IsSensitiveInference() {
			t.Fatalf("IsSensitiveInference(%s) = false", risk)
		}
		expectInvalid(t, entry.Validate())
		entry.State = EntryStateDraft
		if err := entry.Validate(); err != nil {
			t.Fatalf("draft sensitive inference with risk %s rejected: %v", risk, err)
		}
	}
	ordinary := validEntry()
	ordinary.Scope = Scope{Kind: ScopeKindPersonal, Selector: "me"}
	ordinary.PersonalOrigin = PersonalOriginInferred
	ordinary.Confidence = 0.6
	if ordinary.IsSensitiveInference() {
		t.Fatal("risk-free personal inference marked sensitive")
	}
}

func TestUnknownEnumValuesAreRejected(t *testing.T) {
	t.Parallel()
	tests := map[string]func() error{
		"chunk kind":  func() error { value := validChunk(); value.Kind = ChunkKind(255); return value.Validate() },
		"scope kind":  func() error { return (Scope{Kind: ScopeKind(255), Selector: "x"}).Validate() },
		"visibility":  func() error { value := validChunk(); value.Visibility = Visibility(255); return value.Validate() },
		"chunk state": func() error { value := validChunk(); value.State = ChunkState(255); return value.Validate() },
		"entry kind":  func() error { value := validEntry(); value.Kind = EntryKind(255); return value.Validate() },
		"entry state": func() error { value := validEntry(); value.State = EntryState(255); return value.Validate() },
		"link kind":   func() error { value := validLink(); value.Kind = LinkKind(255); return value.Validate() },
		"link state":  func() error { value := validLink(); value.State = LinkState(255); return value.Validate() },
		"risk":        func() error { value := validChunk(); value.Risk = []RiskClass{RiskClass(255)}; return value.Validate() },
		"verification": func() error {
			value := validEntry()
			value.Verification.Status = VerificationStatus(255)
			return value.Validate()
		},
		"evidence type":    func() error { value := validEvidence(); value.Type = EvidenceType(255); return value.Validate() },
		"evidence quality": func() error { value := validEvidence(); value.Quality = EvidenceQuality(255); return value.Validate() },
		"personal origin": func() error {
			value := validEntry()
			value.PersonalOrigin = PersonalOrigin(255)
			return value.Validate()
		},
		"object kind": func() error { return (ObjectRef{Kind: ObjectKind(255), ID: string(testEntryID)}).Validate() },
		"actor kind":  func() error { return (Actor{Kind: ActorKind(255), ID: "actor"}).Validate() },
	}
	for name, validate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			expectInvalid(t, validate())
		})
	}
}

func TestCanonicalConstraints(t *testing.T) {
	t.Parallel()
	t.Run("UUID must be version 7", func(t *testing.T) {
		entry := validEntry()
		entry.ID = "01a01f76-1ff6-4c1d-967a-66ad5703dd33"
		expectInvalid(t, entry.Validate())
	})
	t.Run("timestamps must be UTC", func(t *testing.T) {
		chunk := validChunk()
		chunk.UpdatedAt = testTime.In(time.FixedZone("offset", 3600))
		chunk.Revision.CreatedAt = chunk.UpdatedAt
		expectInvalid(t, chunk.Validate())
	})
	t.Run("validity interval must increase", func(t *testing.T) {
		entry := validEntry()
		entry.ValidFrom = testTime
		entry.ValidUntil = testTime
		expectInvalid(t, entry.Validate())
	})
	t.Run("supersession cannot reference itself", func(t *testing.T) {
		entry := validEntry()
		entry.State = EntryStateSuperseded
		entry.SupersededByID = entry.ID
		expectInvalid(t, entry.Validate())
	})
	t.Run("link endpoints must differ", func(t *testing.T) {
		link := validLink()
		link.Target = link.Source
		expectInvalid(t, link.Validate())
	})
	t.Run("links cannot target links", func(t *testing.T) {
		link := validLink()
		link.Target = ObjectRef{Kind: ObjectKindLink, ID: string(testLinkID)}
		expectInvalid(t, link.Validate())
	})
	t.Run("prohibited secrets cannot be canonical", func(t *testing.T) {
		chunk := validChunk()
		chunk.Risk = []RiskClass{RiskClassProhibitedSecret}
		expectInvalid(t, chunk.Validate())
	})
	t.Run("active Markdown cannot be canonical", func(t *testing.T) {
		entry := validEntry()
		entry.Body = "<script>alert(1)</script>"
		expectInvalid(t, entry.Validate())
	})
	t.Run("revision time equals projection update", func(t *testing.T) {
		chunk := validChunk()
		chunk.Revision = validRevision(testRevisionID2)
		chunk.Revision.CreatedAt = testTime.Add(time.Second)
		expectInvalid(t, chunk.Validate())
	})
	t.Run("assessed verification requires UTC time", func(t *testing.T) {
		verification := assessedVerification(VerificationStatusVerified)
		verification.VerifiedAt = testTime.In(time.FixedZone("offset", -3600))
		expectInvalid(t, verification.Validate())
	})
}
