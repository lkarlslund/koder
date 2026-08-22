// Package kpackage implements Koder's portable .kknowledge container format.
package kpackage

import (
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
)

const (
	Format        = "koder.knowledge.package"
	SchemaVersion = 1
)

type Identity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type Publisher struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type License struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type Dependency struct {
	PackageID string `json:"package_id"`
	ChunkID   string `json:"chunk_id"`
	Version   string `json:"version"`
	Title     string `json:"title"`
	Required  bool   `json:"required"`
}

type Features struct {
	Required []string `json:"required"`
	Optional []string `json:"optional"`
}

type Asset struct {
	Path      string
	MediaType string
	Data      []byte
}

type ExportRequest struct {
	Package      Identity
	Publisher    Publisher
	License      License
	CreatedAt    time.Time
	Features     Features
	Dependencies []Dependency
	Chunk        knowledge.Chunk
	Entries      []knowledge.Entry
	Links        []knowledge.Link
	Evidence     []knowledge.Evidence
	Assets       []Asset
}

type ManifestChunk struct {
	Aliases      []string                 `json:"aliases,omitempty"`
	Description  string                   `json:"description,omitempty"`
	Domain       string                   `json:"domain,omitempty"`
	ID           string                   `json:"id"`
	Kind         knowledge.ChunkKind      `json:"kind"`
	Language     string                   `json:"language,omitempty"`
	Locale       string                   `json:"locale,omitempty"`
	ReviewAfter  time.Time                `json:"review_after,omitzero"`
	Risk         []knowledge.RiskClass    `json:"risk,omitempty"`
	Scope        knowledge.Scope          `json:"scope"`
	SharedWith   []knowledge.PrincipalRef `json:"shared_with,omitempty"`
	SourcePolicy string                   `json:"source_policy,omitempty"`
	State        knowledge.ChunkState     `json:"state"`
	Tags         []string                 `json:"tags,omitempty"`
	Title        string                   `json:"title"`
	Visibility   knowledge.Visibility     `json:"visibility"`
}

type Collection struct {
	Directory string `json:"directory"`
	Count     int    `json:"count"`
}

type RecordFile struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

type Content struct {
	Assets  Collection `json:"assets"`
	Edges   RecordFile `json:"edges"`
	Entries Collection `json:"entries"`
	Sources RecordFile `json:"sources"`
}

type File struct {
	MediaType string `json:"media_type"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
}

type Signature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	Value     string `json:"value"`
}

type Manifest struct {
	Chunk           ManifestChunk `json:"chunk"`
	Content         Content       `json:"content"`
	CreatedAt       time.Time     `json:"created_at"`
	Dependencies    []Dependency  `json:"dependencies"`
	Features        Features      `json:"features"`
	Files           []File        `json:"files"`
	Format          string        `json:"format"`
	License         License       `json:"license"`
	MinKoderVersion string        `json:"min_koder_version"`
	Package         Identity      `json:"package"`
	Publisher       Publisher     `json:"publisher"`
	SchemaVersion   int           `json:"schema_version"`
	Signature       *Signature    `json:"signature,omitempty"`
}

type ExportResult struct {
	Manifest Manifest
	SHA256   string
	Size     int64
}
