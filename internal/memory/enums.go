package memory

//go:generate go tool enumer -type=ChunkKind,ScopeKind,Visibility,ChunkState,EntryKind,EntryState,LinkKind,LinkState,RiskClass,VerificationStatus,EvidenceType,EvidenceQuality,PersonalOrigin,ObjectKind,ActorKind,ClassificationDecision,FindingKind,CurationSignalKind,CurationState -trimprefix=ChunkKind,ScopeKind,Visibility,ChunkState,EntryKind,EntryState,LinkKind,LinkState,RiskClass,VerificationStatus,EvidenceType,EvidenceQuality,PersonalOrigin,ObjectKind,ActorKind,ClassificationDecision,FindingKind,CurationSignalKind,CurationState -transform=snake -json -text -values -output=enums_enumer.go

// ChunkKind identifies the purpose and default policy of a memory chunk.
type ChunkKind uint8

const (
	ChunkKindUnspecified ChunkKind = iota
	ChunkKindReference
	ChunkKindPersonal
	ChunkKindProject
	ChunkKindEnvironment
)

// ScopeKind identifies where a memory record applies. Scope does not grant access.
type ScopeKind uint8

const (
	ScopeKindUnspecified ScopeKind = iota
	ScopeKindGlobal
	ScopeKindPersonal
	ScopeKindProject
	ScopeKindSession
	ScopeKindEnvironment
)

// Visibility identifies who may discover or read a memory record.
type Visibility uint8

const (
	VisibilityUnspecified Visibility = iota
	VisibilityPrivate
	VisibilityInstallation
	VisibilityShared
	VisibilityPublic
)

// ChunkState is the reversible lifecycle state of a chunk. Permanent deletion is not a state.
type ChunkState uint8

const (
	ChunkStateUnspecified ChunkState = iota
	ChunkStateDraft
	ChunkStateActive
	ChunkStateArchived
)

// EntryKind identifies the semantic role of a memory entry.
type EntryKind uint8

const (
	EntryKindUnspecified EntryKind = iota
	EntryKindFact
	EntryKindProcedure
	EntryKindConcept
	EntryKindWarning
	EntryKindPreference
	EntryKindDecision
	EntryKindReference
)

// EntryState is the reversible lifecycle state of an entry. Permanent deletion is not a state.
type EntryState uint8

const (
	EntryStateUnspecified EntryState = iota
	EntryStateDraft
	EntryStateActive
	EntryStateSuperseded
	EntryStateArchived
)

// LinkKind identifies a typed relationship in the memory graph.
type LinkKind uint8

const (
	LinkKindUnspecified LinkKind = iota
	LinkKindRelatedTo
	LinkKindPartOf
	LinkKindRequires
	LinkKindAlternativeTo
	LinkKindAppliesTo
	LinkKindSupersedes
	LinkKindContradicts
	LinkKindCausedBy
	LinkKindSupportedBy
	LinkKindDerivedFrom
)

// LinkState is the reversible lifecycle state of a graph link.
type LinkState uint8

const (
	LinkStateUnspecified LinkState = iota
	LinkStateActive
	LinkStateArchived
)

// RiskClass identifies additional policy requirements. A record may have several classes.
type RiskClass uint8

const (
	RiskClassUnspecified RiskClass = iota
	RiskClassPersonalSensitive
	RiskClassMedical
	RiskClassLegal
	RiskClassFinancial
	RiskClassPhysicalSafety
	RiskClassSecuritySensitive
	RiskClassProhibitedSecret
)

// VerificationStatus describes the current evidentiary support for a claim.
type VerificationStatus uint8

const (
	VerificationStatusUnspecified VerificationStatus = iota
	VerificationStatusUnverified
	VerificationStatusPartiallyVerified
	VerificationStatusVerified
	VerificationStatusDisputed
)

// EvidenceType identifies the inspectable origin of evidence.
type EvidenceType uint8

const (
	EvidenceTypeUnspecified EvidenceType = iota
	EvidenceTypeUserStatement
	EvidenceTypeChatTurn
	EvidenceTypeToolResult
	EvidenceTypeFile
	EvidenceTypeWeb
	EvidenceTypePackage
	EvidenceTypeObservation
)

// EvidenceQuality classifies the authority of evidence independently from its type.
type EvidenceQuality uint8

const (
	EvidenceQualityUnspecified EvidenceQuality = iota
	EvidenceQualityPrimary
	EvidenceQualityAuthoritative
	EvidenceQualitySecondary
	EvidenceQualityAnecdotal
	EvidenceQualityGenerated
)

// PersonalOrigin identifies how personal memory arose.
type PersonalOrigin uint8

const (
	PersonalOriginUnspecified PersonalOrigin = iota
	PersonalOriginExplicit
	PersonalOriginObserved
	PersonalOriginInferred
)

// ObjectKind identifies a graph endpoint or revision owner.
type ObjectKind uint8

const (
	ObjectKindUnspecified ObjectKind = iota
	ObjectKindChunk
	ObjectKindEntry
	ObjectKindLink
)

// ActorKind identifies who or what committed a memory revision.
type ActorKind uint8

const (
	ActorKindUnspecified ActorKind = iota
	ActorKindUser
	ActorKindChat
	ActorKindSystem
	ActorKindImport
)

// ClassificationDecision is the ingestion policy implied by detected data classes.
type ClassificationDecision uint8

const (
	ClassificationDecisionUnspecified ClassificationDecision = iota
	ClassificationDecisionAllow
	ClassificationDecisionReview
	ClassificationDecisionReject
)

// FindingKind identifies a secret or sensitive-data classification without retaining the match.
type FindingKind uint8

const (
	FindingKindUnspecified FindingKind = iota
	FindingKindPrivateKey
	FindingKindCredential
	FindingKindAuthToken
	FindingKindPersonalIdentifier
	FindingKindContact
	FindingKindPreciseLocation
	FindingKindMedical
	FindingKindLegal
	FindingKindFinancial
	FindingKindPhysicalSafety
	FindingKindSecuritySensitive
	FindingKindBiometric
)

// CurationSignalKind identifies a durable-learning pattern observed in one sealed turn.
// A signal only schedules inspection; it is not itself canonical memory.
type CurationSignalKind uint8

const (
	CurationSignalKindUnspecified CurationSignalKind = iota
	CurationSignalKindFailedThenSucceeded
	CurationSignalKindResearchedThenSucceeded
	CurationSignalKindUserCorrection
	CurationSignalKindRepeatedWorkaround
	CurationSignalKindContradictingEvidence
	CurationSignalKindExplicitPersonalPreference
)

// CurationState tracks provider-independent processing of one completed turn.
type CurationState uint8

const (
	CurationStateUnspecified CurationState = iota
	CurationStateQueued
	CurationStateProcessing
	CurationStateNoCandidates
	CurationStateCandidatesReady
	CurationStateFailed
)
