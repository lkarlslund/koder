package kpackage

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestValidateAcceptsCanonicalPackageAndOptionalFeatures(t *testing.T) {
	t.Parallel()
	request := canonicalExampleRequest(t)
	request.Features.Optional = []string{"future-preview"}
	parsed := exportAndParse(t, request)
	validated, err := Validate(parsed, ValidationOptions{CurrentKoderVersion: "r1847-local-dirty"})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if validated.SignatureState != SignatureStateUnsigned || len(validated.Entries) != 2 || len(validated.Links) != 1 || len(validated.Evidence) != 1 || len(validated.Assets) != 0 {
		t.Fatalf("validated package = %#v", validated)
	}
	validated.Entries[0].Title = "mutated"
	validated.Manifest.Chunk.Tags[0] = "mutated"
	again, err := Validate(parsed, ValidationOptions{CurrentKoderVersion: "r1847"})
	if err != nil || again.Entries[0].Title == "mutated" || again.Manifest.Chunk.Tags[0] == "mutated" {
		t.Fatal("validated records alias a previous result")
	}
}

func TestValidateSignaturePolicy(t *testing.T) {
	t.Parallel()
	publicKey, privateKey := deterministicSigningKey(t, 0x71)
	request := canonicalExampleRequest(t)
	request.Signing = &SigningConfig{KeyID: "publisher:example:validation", PrivateKey: privateKey}
	parsed := exportAndParse(t, request)

	unknown, err := Validate(parsed, ValidationOptions{CurrentKoderVersion: "r1847"})
	if err != nil || unknown.SignatureState != SignatureStateUnknown {
		t.Fatalf("unknown signature result=%s error=%v", unknown.SignatureState, err)
	}
	verified, err := Validate(parsed, ValidationOptions{
		CurrentKoderVersion: "r1847", RequireVerifiedSignature: true,
		VerificationKeys: map[string]ed25519.PublicKey{"publisher:example:validation": publicKey},
	})
	if err != nil || verified.SignatureState != SignatureStateVerified {
		t.Fatalf("verified signature result=%s error=%v", verified.SignatureState, err)
	}
	if _, err := Validate(parsed, ValidationOptions{CurrentKoderVersion: "r1847", RequireVerifiedSignature: true}); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("required unknown signature error = %v", err)
	}
	wrongKey := deterministicPublicKey(t, 0x72)
	if _, err := Validate(parsed, ValidationOptions{CurrentKoderVersion: "r1847", VerificationKeys: map[string]ed25519.PublicKey{"publisher:example:validation": wrongKey}}); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("wrong signature key error = %v", err)
	}
	unsigned := exportAndParse(t, canonicalExampleRequest(t))
	if _, err := Validate(unsigned, ValidationOptions{CurrentKoderVersion: "r1847", RequireVerifiedSignature: true}); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("required unsigned package error = %v", err)
	}
}

func TestValidateManifestPolicyAndCompatibility(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		mutate    func(*Manifest)
		options   ValidationOptions
		wantError error
		wantText  string
	}{
		{name: "future Koder", mutate: func(value *Manifest) { value.MinKoderVersion = "r9999" }, wantError: ErrIncompatiblePackage, wantText: "requires Koder"},
		{name: "required feature", mutate: func(value *Manifest) { value.Features.Required = []string{"semantic-v2"} }, wantError: ErrIncompatiblePackage, wantText: "unsupported"},
		{name: "supported feature", mutate: func(value *Manifest) { value.Features.Required = []string{"semantic-v2"} }, options: ValidationOptions{SupportedFeatures: []string{"semantic-v2"}}},
		{name: "unsorted features", mutate: func(value *Manifest) { value.Features.Required = []string{"zeta", "alpha"} }, options: ValidationOptions{SupportedFeatures: []string{"alpha", "zeta"}}, wantError: ErrInvalidPackage, wantText: "not sorted"},
		{name: "overlapping features", mutate: func(value *Manifest) {
			value.Features.Required = []string{"same"}
			value.Features.Optional = []string{"same"}
		}, options: ValidationOptions{SupportedFeatures: []string{"same"}}, wantError: ErrInvalidPackage, wantText: "both required and optional"},
		{name: "invalid locale", mutate: func(value *Manifest) { value.Chunk.Locale = "en-0" }, wantError: ErrInvalidPackage, wantText: "BCP 47"},
		{name: "invalid license", mutate: func(value *Manifest) { value.License.Name = "MIT && Apache-2.0" }, wantError: ErrInvalidPackage, wantText: "license"},
		{name: "self package dependency", mutate: func(value *Manifest) {
			value.Dependencies = []Dependency{{PackageID: value.Package.ID, ChunkID: "01a02b00-0000-7000-8000-000000000010", Version: "1.0.0", Title: "Self", Required: true}}
		}, wantError: ErrInvalidPackage, wantText: "dependency"},
		{name: "self chunk dependency", mutate: func(value *Manifest) {
			value.Dependencies = []Dependency{{PackageID: "01a02b00-0000-7000-8000-000000000011", ChunkID: value.Chunk.ID, Version: "1.0.0", Title: "Self", Required: true}}
		}, wantError: ErrInvalidPackage, wantText: "dependency"},
		{name: "swapped record paths", mutate: func(value *Manifest) {
			value.Content.Edges.Path, value.Content.Sources.Path = value.Content.Sources.Path, value.Content.Edges.Path
		}, wantError: ErrInvalidPackage, wantText: "canonical paths"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest, payload := exportedParts(t, canonicalExampleRequest(t))
			test.mutate(&manifest)
			parsed := parseParts(t, manifest, payload, nil)
			options := test.options
			options.CurrentKoderVersion = "r1847"
			_, err := Validate(parsed, options)
			if test.wantError == nil {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantError) || !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("Validate() error = %v, want %v containing %q", err, test.wantError, test.wantText)
			}
		})
	}
}

func TestValidateInventoryAndRecordIntegrity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		mutate    func(*Manifest, map[string][]byte) []testZIPEntry
		wantError error
		wantText  string
	}{
		{name: "payload hash", mutate: func(_ *Manifest, files map[string][]byte) []testZIPEntry {
			files[entryPath(2)][len(files[entryPath(2)])-2] ^= 1
			return nil
		}, wantError: ErrIntegrityMismatch, wantText: "SHA-256"},
		{name: "missing payload", mutate: func(_ *Manifest, files map[string][]byte) []testZIPEntry { delete(files, entryPath(2)); return nil }, wantError: ErrIntegrityMismatch, wantText: "missing"},
		{name: "extra payload", mutate: func(_ *Manifest, _ map[string][]byte) []testZIPEntry {
			return []testZIPEntry{{name: "assets/extra.txt", data: []byte("extra")}}
		}, wantError: ErrIntegrityMismatch, wantText: "do not equal"},
		{name: "count mismatch", mutate: func(manifest *Manifest, _ map[string][]byte) []testZIPEntry {
			manifest.Content.Entries.Count++
			return nil
		}, wantError: ErrIntegrityMismatch, wantText: "counts"},
		{name: "unknown entry field", mutate: func(manifest *Manifest, files map[string][]byte) []testZIPEntry {
			mutateEntryMetadata(t, manifest, files, entryPath(2), func(value map[string]any) { value["unknown"] = true })
			return nil
		}, wantError: ErrInvalidPackage, wantText: "unknown field"},
		{name: "entry ID path mismatch", mutate: func(manifest *Manifest, files map[string][]byte) []testZIPEntry {
			mutateEntryMetadata(t, manifest, files, entryPath(2), func(value map[string]any) { value["id"] = "01a02b00-0000-7000-8000-000000000099" })
			return nil
		}, wantError: ErrInvalidPackage, wantText: "does not match path"},
		{name: "missing evidence reference", mutate: func(manifest *Manifest, files map[string][]byte) []testZIPEntry {
			mutateEntryMetadata(t, manifest, files, entryPath(2), func(value map[string]any) { value["evidence_ids"] = []any{"01a02b00-0000-7000-8000-000000000099"} })
			return nil
		}, wantError: ErrInvalidPackage, wantText: "missing evidence"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest, payload := exportedParts(t, canonicalExampleRequest(t))
			extra := test.mutate(&manifest, payload)
			parsed := parseParts(t, manifest, payload, extra)
			_, err := Validate(parsed, ValidationOptions{CurrentKoderVersion: "r1847"})
			if !errors.Is(err, test.wantError) || !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("Validate() error = %v, want %v containing %q", err, test.wantError, test.wantText)
			}
		})
	}

	t.Run("nested archive asset", func(t *testing.T) {
		request := canonicalExampleRequest(t)
		request.Assets = []Asset{{Path: "assets/archive.zip", MediaType: "application/zip", Data: []byte{'P', 'K', 3, 4}}}
		parsed := exportAndParse(t, request)
		if _, err := Validate(parsed, ValidationOptions{CurrentKoderVersion: "r1847"}); !errors.Is(err, ErrInvalidPackage) || !strings.Contains(err.Error(), "nested archive") {
			t.Fatalf("Validate() error = %v", err)
		}
	})
}

func TestValidateRejectsSchemaExtensionsAndNoncanonicalManifest(t *testing.T) {
	t.Parallel()
	manifest, payload := exportedParts(t, canonicalExampleRequest(t))
	canonical, err := canonicalJSON(manifest, true)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(canonical, []byte("{\n"), []byte("{\n  \"unknown\": true,\n"), 1)
	parsed := parseRawParts(t, unknown, manifest, payload, nil)
	if _, err := Validate(parsed, ValidationOptions{CurrentKoderVersion: "r1847"}); !errors.Is(err, ErrInvalidPackage) || !strings.Contains(err.Error(), "additional properties") {
		t.Fatalf("unknown manifest member error = %v", err)
	}

	compact, err := canonicalJSON(manifest, false)
	if err != nil {
		t.Fatal(err)
	}
	parsed = parseRawParts(t, compact, manifest, payload, nil)
	if _, err := Validate(parsed, ValidationOptions{CurrentKoderVersion: "r1847"}); !errors.Is(err, ErrInvalidPackage) || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("noncanonical manifest error = %v", err)
	}
}

func TestLicenseExpressionValidation(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"MIT", "CC-BY-4.0", "MIT OR Apache-2.0", "(MIT OR Apache-2.0) AND BSD-3-Clause", "GPL-2.0-only WITH Classpath-exception-2.0", "LicenseRef-Proprietary", "NONE", "NOASSERTION"} {
		if !validLicenseExpression(value) {
			t.Errorf("validLicenseExpression(%q) = false", value)
		}
	}
	for _, value := range []string{"", "MIT && Apache-2.0", "MIT OR", "OR MIT", "(MIT", "MIT)", "MIT WITH", "MIT WITH Exception OR", "MIT/Apache"} {
		if validLicenseExpression(value) {
			t.Errorf("validLicenseExpression(%q) = true", value)
		}
	}
}

func exportAndParse(t *testing.T, request ExportRequest) ParsedPackage {
	t.Helper()
	var output bytes.Buffer
	if _, err := Export(&output, request); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseBytes(output.Bytes(), ParseLimits{})
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func exportedParts(t *testing.T, request ExportRequest) (Manifest, map[string][]byte) {
	t.Helper()
	var output bytes.Buffer
	result, err := Export(&output, request)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte, len(reader.File)-1)
	for _, file := range reader.File[1:] {
		files[file.Name] = readZIPFile(t, file)
	}
	return result.Manifest, files
}

func parseParts(t *testing.T, manifest Manifest, payload map[string][]byte, extra []testZIPEntry) ParsedPackage {
	t.Helper()
	raw, err := canonicalJSON(manifest, true)
	if err != nil {
		t.Fatal(err)
	}
	return parseRawParts(t, raw, manifest, payload, extra)
}

func parseRawParts(t *testing.T, raw []byte, manifest Manifest, payload map[string][]byte, extra []testZIPEntry) ParsedPackage {
	t.Helper()
	date, clock := zipTime(manifest.CreatedAt)
	entries := []testZIPEntry{{name: "manifest.json", data: raw, date: date, clock: clock}}
	for _, file := range manifest.Files {
		if data, exists := payload[file.Path]; exists {
			entries = append(entries, testZIPEntry{name: file.Path, data: data, date: date, clock: clock})
		}
	}
	for index := range extra {
		extra[index].date = date
		extra[index].clock = clock
	}
	entries = append(entries, extra...)
	parsed, err := ParseBytes(makeTestZIP(t, entries), ParseLimits{})
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func mutateEntryMetadata(t *testing.T, manifest *Manifest, files map[string][]byte, name string, mutate func(map[string]any)) {
	t.Helper()
	data := files[name]
	parts := bytes.SplitN(bytes.TrimPrefix(data, []byte("---\n")), []byte("\n---\n"), 2)
	if len(parts) != 2 {
		t.Fatal("invalid entry fixture")
	}
	var metadata map[string]any
	decoder := json.NewDecoder(bytes.NewReader(parts[0]))
	decoder.UseNumber()
	if err := decoder.Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	mutate(metadata)
	frontMatter, err := canonicalJSON(metadata, false)
	if err != nil {
		t.Fatal(err)
	}
	updated := append([]byte("---\n"), frontMatter...)
	updated = append(updated, []byte("\n---\n")...)
	updated = append(updated, parts[1]...)
	files[name] = updated
	updateManifestFile(t, manifest, name, updated)
}

func updateManifestFile(t *testing.T, manifest *Manifest, name string, data []byte) {
	t.Helper()
	index := slices.IndexFunc(manifest.Files, func(file File) bool { return file.Path == name })
	if index < 0 {
		t.Fatalf("manifest has no %s", name)
	}
	digest := sha256.Sum256(data)
	manifest.Files[index].SHA256 = hex.EncodeToString(digest[:])
	manifest.Files[index].Size = int64(len(data))
}

func entryPath(suffix byte) string {
	return fmt.Sprintf("entries/01a02b00-0000-7000-8000-00000000000%d.md", suffix)
}
