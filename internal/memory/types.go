// Package memory defines Koder's canonical durable-memory domain.
//
// The package contains domain values only. Persistence, transport, tools, and browser
// representations adapt these types at their respective boundaries.
package memory

import "time"

// Distinct ID types prevent accidentally joining unrelated memory records in Go code.
// They all use Koder's canonical UUIDv7 string representation on the wire.
type (
	ChunkID    string
	EntryID    string
	LinkID     string
	EvidenceID string
	RevisionID string
)

// Scope identifies where memory applies. Selector is empty only for global scope.
type Scope struct {
	Kind     ScopeKind `json:"kind"`
	Selector string    `json:"selector,omitempty"`
}

// PrincipalRef identifies an actor permitted by shared visibility policy.
// Kind is intentionally a string until Koder has a general multi-user principal model.
type PrincipalRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Actor identifies who or what committed a canonical revision.
type Actor struct {
	Kind ActorKind `json:"kind"`
	ID   string    `json:"id"`
}

// Publisher identifies the originator of a portable or locally curated chunk.
type Publisher struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// SoftwareConstraint limits an entry to named software and an optional version range.
// VersionRange is an opaque human-readable constraint until a version grammar is adopted.
type SoftwareConstraint struct {
	Name         string `json:"name"`
	VersionRange string `json:"version_range,omitempty"`
}

// Applicability contains structured conditions beyond an entry's primary scope.
type Applicability struct {
	OperatingSystems []string             `json:"operating_systems,omitempty"`
	Architectures    []string             `json:"architectures,omitempty"`
	Software         []SoftwareConstraint `json:"software,omitempty"`
	Locales          []string             `json:"locales,omitempty"`
	Conditions       []string             `json:"conditions,omitempty"`
}

// Verification records how and when a claim's current support was assessed.
type Verification struct {
	Status      VerificationStatus `json:"status"`
	Method      string             `json:"method,omitempty"`
	EvidenceIDs []EvidenceID       `json:"evidence_ids,omitempty"`
	Actor       Actor              `json:"actor"`
	VerifiedAt  time.Time          `json:"verified_at,omitzero"`
}

// Revision identifies the current immutable revision of a mutable canonical object.
type Revision struct {
	Number    uint64     `json:"number"`
	ID        RevisionID `json:"id"`
	Actor     Actor      `json:"actor"`
	Reason    string     `json:"reason,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// ChunkCounts is derived metadata and does not advance a chunk's content revision.
type ChunkCounts struct {
	Entries  uint64 `json:"entries"`
	Links    uint64 `json:"links"`
	Evidence uint64 `json:"evidence"`
}

// Chunk is a coherent, portable collection of memory entries.
type Chunk struct {
	ID              ChunkID        `json:"id"`
	Title           string         `json:"title"`
	Description     string         `json:"description,omitempty"`
	Aliases         []string       `json:"aliases,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	Kind            ChunkKind      `json:"kind"`
	Scope           Scope          `json:"scope"`
	Visibility      Visibility     `json:"visibility"`
	SharedWith      []PrincipalRef `json:"shared_with,omitempty"`
	Language        string         `json:"language,omitempty"`
	Locale          string         `json:"locale,omitempty"`
	Domain          string         `json:"domain,omitempty"`
	Risk            []RiskClass    `json:"risk,omitempty"`
	State           ChunkState     `json:"state"`
	SchemaVersion   uint32         `json:"schema_version"`
	Revision        Revision       `json:"revision"`
	Publisher       Publisher      `json:"publisher,omitzero"`
	License         string         `json:"license,omitempty"`
	SourcePolicy    string         `json:"source_policy,omitempty"`
	DependencyIDs   []ChunkID      `json:"dependency_ids,omitempty"`
	MinKoderVersion string         `json:"min_koder_version,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	LastUsedAt      time.Time      `json:"last_used_at,omitzero"`
	LastVerifiedAt  time.Time      `json:"last_verified_at,omitzero"`
	ReviewAfter     time.Time      `json:"review_after,omitzero"`
	Counts          ChunkCounts    `json:"counts"`
}

// Entry is one reusable claim or procedure within a memory chunk.
type Entry struct {
	ID             EntryID        `json:"id"`
	ChunkID        ChunkID        `json:"chunk_id"`
	Kind           EntryKind      `json:"kind"`
	Title          string         `json:"title"`
	Summary        string         `json:"summary,omitempty"`
	Body           string         `json:"body,omitempty"`
	Aliases        []string       `json:"aliases,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
	Scope          Scope          `json:"scope"`
	Applicability  Applicability  `json:"applicability,omitzero"`
	Risk           []RiskClass    `json:"risk,omitempty"`
	Confidence     float32        `json:"confidence,omitempty"`
	Verification   Verification   `json:"verification"`
	ValidFrom      time.Time      `json:"valid_from,omitzero"`
	ValidUntil     time.Time      `json:"valid_until,omitzero"`
	ObservedAt     time.Time      `json:"observed_at,omitzero"`
	ReviewAfter    time.Time      `json:"review_after,omitzero"`
	State          EntryState     `json:"state"`
	SupersededByID EntryID        `json:"superseded_by_id,omitempty"`
	EvidenceIDs    []EvidenceID   `json:"evidence_ids,omitempty"`
	PersonalOrigin PersonalOrigin `json:"personal_origin,omitempty"`
	Revision       Revision       `json:"revision"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	LastUsedAt     time.Time      `json:"last_used_at,omitzero"`
}

// IsSensitiveInference reports whether an unconfirmed personal inference carries a
// risk class that requires it to remain review-only rather than active memory.
func (e Entry) IsSensitiveInference() bool {
	if e.Scope.Kind != ScopeKindPersonal || e.PersonalOrigin != PersonalOriginInferred {
		return false
	}
	for _, risk := range e.Risk {
		switch risk {
		case RiskClassPersonalSensitive, RiskClassMedical, RiskClassLegal, RiskClassFinancial,
			RiskClassPhysicalSafety, RiskClassSecuritySensitive:
			return true
		}
	}
	return false
}

// ObjectRef identifies a memory graph endpoint or revision owner.
type ObjectRef struct {
	Kind ObjectKind `json:"kind"`
	ID   string     `json:"id"`
}

// Link is a typed, directed relationship between memory chunks or entries.
type Link struct {
	ID          LinkID       `json:"id"`
	Source      ObjectRef    `json:"source"`
	Target      ObjectRef    `json:"target"`
	Kind        LinkKind     `json:"kind"`
	Label       string       `json:"label,omitempty"`
	Notes       string       `json:"notes,omitempty"`
	EvidenceIDs []EvidenceID `json:"evidence_ids,omitempty"`
	State       LinkState    `json:"state"`
	Revision    Revision     `json:"revision"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// Source identifies inspectable evidence without embedding the entire source content.
type Source struct {
	ID          string    `json:"id"`
	URI         string    `json:"uri,omitempty"`
	Title       string    `json:"title,omitempty"`
	ContentHash string    `json:"content_hash,omitempty"`
	Excerpt     string    `json:"excerpt,omitempty"`
	AccessedAt  time.Time `json:"accessed_at,omitzero"`
}

// Evidence is immutable support for a memory entry revision or link.
type Evidence struct {
	ID         EvidenceID      `json:"id"`
	Type       EvidenceType    `json:"type"`
	Quality    EvidenceQuality `json:"quality"`
	Source     Source          `json:"source"`
	ObservedAt time.Time       `json:"observed_at,omitzero"`
	Actor      Actor           `json:"actor"`
	CreatedAt  time.Time       `json:"created_at"`
}
