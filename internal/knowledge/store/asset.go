package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lkarlslund/koder/internal/knowledge"
)

// MaxPackageAssetBytes matches the hard per-file package parser ceiling.
const MaxPackageAssetBytes = 64 << 20

// PackageAsset is immutable package support data owned by one canonical chunk.
// Assets are never executable and are not graph objects or search-index content.
type PackageAsset struct {
	ChunkID   knowledge.ChunkID `json:"chunk_id"`
	Path      string            `json:"path"`
	MediaType string            `json:"media_type"`
	SHA256    string            `json:"sha256"`
	Data      []byte            `json:"data"`
}

func (a PackageAsset) Validate() error {
	if err := (knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(a.ChunkID)}).Validate(); err != nil {
		return err
	}
	if !strings.HasPrefix(a.Path, "assets/") || path.Clean(a.Path) != a.Path || strings.Contains(a.Path, "\\") || !utf8.ValidString(a.Path) {
		return fmt.Errorf("%w: package asset path is invalid", knowledge.ErrInvalidRecord)
	}
	for _, component := range strings.Split(strings.TrimPrefix(a.Path, "assets/"), "/") {
		if component == "" || strings.HasPrefix(component, ".") {
			return fmt.Errorf("%w: package asset path is invalid", knowledge.ErrInvalidRecord)
		}
	}
	if _, _, err := mime.ParseMediaType(a.MediaType); err != nil || strings.ContainsAny(a.MediaType, "\r\n") {
		return fmt.Errorf("%w: package asset media type is invalid", knowledge.ErrInvalidRecord)
	}
	if len(a.Data) > MaxPackageAssetBytes {
		return fmt.Errorf("%w: package asset exceeds %d bytes", knowledge.ErrInvalidRecord, MaxPackageAssetBytes)
	}
	if len(a.SHA256) != sha256.Size*2 {
		return fmt.Errorf("%w: package asset SHA-256 is invalid", knowledge.ErrInvalidRecord)
	}
	decoded, err := hex.DecodeString(a.SHA256)
	if err != nil || a.SHA256 != strings.ToLower(a.SHA256) {
		return fmt.Errorf("%w: package asset SHA-256 is invalid", knowledge.ErrInvalidRecord)
	}
	digest := sha256.Sum256(a.Data)
	if !slices.Equal(decoded, digest[:]) {
		return fmt.Errorf("%w: package asset SHA-256 does not match data", knowledge.ErrInvalidRecord)
	}
	return nil
}

func ClonePackageAsset(value PackageAsset) PackageAsset {
	value.Data = slices.Clone(value.Data)
	return value
}
