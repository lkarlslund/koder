package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
)

func TestPackageAssetValidationAndClone(t *testing.T) {
	t.Parallel()
	data := []byte("asset data\n")
	digest := sha256.Sum256(data)
	valid := PackageAsset{
		ChunkID: "01a02b00-0000-7000-8000-000000000001", Path: "assets/note.txt",
		MediaType: "text/plain; charset=utf-8", SHA256: hex.EncodeToString(digest[:]), Data: data,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	clone := ClonePackageAsset(valid)
	clone.Data[0] = 'X'
	if valid.Data[0] == clone.Data[0] {
		t.Fatal("ClonePackageAsset() aliases data")
	}
	for name, mutate := range map[string]func(*PackageAsset){
		"chunk":      func(value *PackageAsset) { value.ChunkID = "bad" },
		"path":       func(value *PackageAsset) { value.Path = "assets/../secret" },
		"media type": func(value *PackageAsset) { value.MediaType = "" },
		"digest":     func(value *PackageAsset) { value.SHA256 = "00" + value.SHA256[2:] },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, knowledge.ErrInvalidRecord) {
				t.Fatalf("Validate() error = %v, want ErrInvalidRecord", err)
			}
		})
	}
}
