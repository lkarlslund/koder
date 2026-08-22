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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgepackagev1 "github.com/lkarlslund/koder/protocol/knowledge/package/v1"
)

var (
	ErrInvalidPackage      = errors.New("invalid knowledge package")
	ErrIntegrityMismatch   = errors.New("knowledge package integrity mismatch")
	ErrIncompatiblePackage = errors.New("incompatible knowledge package")
)

type SignatureState string

const (
	SignatureStateUnsigned SignatureState = "unsigned"
	SignatureStateUnknown  SignatureState = "unknown_key"
	SignatureStateVerified SignatureState = "verified"
)

type ValidationOptions struct {
	CurrentKoderVersion      string
	SupportedFeatures        []string
	VerificationKeys         map[string]ed25519.PublicKey
	RequireVerifiedSignature bool
}

type ValidatedPackage struct {
	Manifest       Manifest
	Entries        []knowledge.Entry
	Links          []knowledge.Link
	Evidence       []knowledge.Evidence
	Assets         map[string][]byte
	SignatureState SignatureState
}

var manifestSchemaCache struct {
	sync.Once
	schema *jsonschema.Schema
	err    error
}

// Validate applies the complete v1 content contract to a structurally parsed package.
// It does not authorize or persist the package.
func Validate(parsed ParsedPackage, options ValidationOptions) (ValidatedPackage, error) {
	if !runtimeBuildPattern.MatchString(options.CurrentKoderVersion) {
		return ValidatedPackage{}, fmt.Errorf("%w: current Koder version must use rNNNN syntax", ErrInvalidPackage)
	}
	if err := validateManifestSchema(parsed.ManifestBytes()); err != nil {
		return ValidatedPackage{}, err
	}
	canonicalManifest, err := canonicalJSON(parsed.Manifest, true)
	if err != nil {
		return ValidatedPackage{}, fmt.Errorf("%w: encode canonical manifest: %v", ErrInvalidPackage, err)
	}
	if !bytes.Equal(canonicalManifest, parsed.ManifestBytes()) {
		return ValidatedPackage{}, fmt.Errorf("%w: manifest.json is not canonical JSON", ErrInvalidPackage)
	}
	if err := validateManifestMetadata(parsed.Manifest, options); err != nil {
		return ValidatedPackage{}, err
	}
	if err := validateManifestChunk(parsed.Manifest.Chunk); err != nil {
		return ValidatedPackage{}, err
	}
	if err := validateInventory(parsed); err != nil {
		return ValidatedPackage{}, err
	}

	entries, links, evidence, assets, err := decodePackagePayload(parsed)
	if err != nil {
		return ValidatedPackage{}, err
	}
	if err := validateRecordGraph(parsed.Manifest, entries, links, evidence); err != nil {
		return ValidatedPackage{}, err
	}
	signatureState, err := validatePackageSignature(parsed.Manifest, options)
	if err != nil {
		return ValidatedPackage{}, err
	}
	return ValidatedPackage{
		Manifest: cloneValidatedManifest(parsed.Manifest), Entries: entries, Links: links, Evidence: evidence,
		Assets: assets, SignatureState: signatureState,
	}, nil
}

func cloneValidatedManifest(value Manifest) Manifest {
	value.Chunk.Aliases = slices.Clone(value.Chunk.Aliases)
	value.Chunk.Risk = slices.Clone(value.Chunk.Risk)
	value.Chunk.SharedWith = slices.Clone(value.Chunk.SharedWith)
	value.Chunk.Tags = slices.Clone(value.Chunk.Tags)
	value.Dependencies = slices.Clone(value.Dependencies)
	value.Features.Required = slices.Clone(value.Features.Required)
	value.Features.Optional = slices.Clone(value.Features.Optional)
	value.Files = slices.Clone(value.Files)
	if value.Signature != nil {
		signature := *value.Signature
		value.Signature = &signature
	}
	return value
}

func validateManifestSchema(data []byte) error {
	manifestSchemaCache.Do(func() {
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(knowledgepackagev1.ManifestSchema()))
		if err != nil {
			manifestSchemaCache.err = err
			return
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("manifest.schema.json", document); err != nil {
			manifestSchemaCache.err = err
			return
		}
		manifestSchemaCache.schema, manifestSchemaCache.err = compiler.Compile("manifest.schema.json")
	})
	if manifestSchemaCache.err != nil {
		return fmt.Errorf("%w: compile manifest schema: %v", ErrInvalidPackage, manifestSchemaCache.err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: parse manifest schema input: %v", ErrInvalidPackage, err)
	}
	if err := manifestSchemaCache.schema.Validate(document); err != nil {
		return fmt.Errorf("%w: manifest schema: %v", ErrInvalidPackage, err)
	}
	return nil
}

func validateManifestMetadata(manifest Manifest, options ValidationOptions) error {
	if manifest.Format != Format || manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported format or schema version", ErrIncompatiblePackage)
	}
	if !uuidV7Pattern.MatchString(manifest.Package.ID) || !versionPattern.MatchString(manifest.Package.Version) {
		return fmt.Errorf("%w: package identity is invalid", ErrInvalidPackage)
	}
	if !tokenPattern.MatchString(manifest.Publisher.ID) || strings.TrimSpace(manifest.Publisher.Name) == "" {
		return fmt.Errorf("%w: publisher identity is invalid", ErrInvalidPackage)
	}
	if err := validateOptionalURL("publisher URL", manifest.Publisher.URL); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	if !validLicenseExpression(manifest.License.Name) {
		return fmt.Errorf("%w: license expression %q is invalid", ErrInvalidPackage, manifest.License.Name)
	}
	if err := validateOptionalURL("license URL", manifest.License.URL); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	if manifest.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", ErrInvalidPackage)
	}
	_, offset := manifest.CreatedAt.Zone()
	if offset != 0 || manifest.CreatedAt.Year() < 1980 || manifest.CreatedAt.Year() > 2107 {
		return fmt.Errorf("%w: created_at must be UTC in the ZIP timestamp range", ErrInvalidPackage)
	}
	minimum, err := buildNumber(manifest.MinKoderVersion)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	current, err := buildNumber(options.CurrentKoderVersion)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	if minimum > current {
		return fmt.Errorf("%w: requires Koder %s, current is %s", ErrIncompatiblePackage, manifest.MinKoderVersion, options.CurrentKoderVersion)
	}
	supported := make(map[string]struct{}, len(options.SupportedFeatures))
	for _, feature := range options.SupportedFeatures {
		if !tokenPattern.MatchString(feature) {
			return fmt.Errorf("%w: supported feature %q is invalid", ErrInvalidPackage, feature)
		}
		supported[feature] = struct{}{}
	}
	seenFeatures := make(map[string]string, len(manifest.Features.Required)+len(manifest.Features.Optional))
	for _, feature := range manifest.Features.Required {
		seenFeatures[feature] = "required"
		if _, exists := supported[feature]; !exists {
			return fmt.Errorf("%w: required feature %q is unsupported", ErrIncompatiblePackage, feature)
		}
	}
	for _, feature := range manifest.Features.Optional {
		if previous, exists := seenFeatures[feature]; exists {
			return fmt.Errorf("%w: feature %q is both %s and optional", ErrInvalidPackage, feature, previous)
		}
		seenFeatures[feature] = "optional"
	}
	if !slices.IsSorted(manifest.Features.Required) || !slices.IsSorted(manifest.Features.Optional) {
		return fmt.Errorf("%w: feature names are not sorted", ErrInvalidPackage)
	}
	if err := validateDependenciesManifest(manifest); err != nil {
		return err
	}
	return nil
}

func validateManifestChunk(chunk ManifestChunk) error {
	if !uuidV7Pattern.MatchString(chunk.ID) || knowledge.NormalizeTitle(chunk.Title) != chunk.Title || chunk.Title == "" {
		return fmt.Errorf("%w: chunk identity or title is invalid", ErrInvalidPackage)
	}
	if chunk.Kind == knowledge.ChunkKindUnspecified || !chunk.Kind.IsAChunkKind() || chunk.State == knowledge.ChunkStateUnspecified || !chunk.State.IsAChunkState() || chunk.Visibility == knowledge.VisibilityUnspecified || !chunk.Visibility.IsAVisibility() {
		return fmt.Errorf("%w: chunk kind, state, or visibility is invalid", ErrInvalidPackage)
	}
	if err := chunk.Scope.Validate(); err != nil {
		return fmt.Errorf("%w: chunk scope: %v", ErrInvalidPackage, err)
	}
	if chunk.Visibility == knowledge.VisibilityShared && len(chunk.SharedWith) == 0 {
		return fmt.Errorf("%w: shared chunk has no principals", ErrInvalidPackage)
	}
	if chunk.Visibility != knowledge.VisibilityShared && len(chunk.SharedWith) != 0 {
		return fmt.Errorf("%w: shared_with requires shared visibility", ErrInvalidPackage)
	}
	for _, principal := range chunk.SharedWith {
		if err := principal.Validate(); err != nil {
			return fmt.Errorf("%w: shared principal: %v", ErrInvalidPackage, err)
		}
	}
	if !slices.Equal(chunk.Aliases, knowledge.NormalizeAliases(chunk.Title, chunk.Aliases)) || !slices.Equal(chunk.Tags, knowledge.NormalizeTags(chunk.Tags)) {
		return fmt.Errorf("%w: chunk aliases or tags are not canonical", ErrInvalidPackage)
	}
	for name, value := range map[string]string{"language": chunk.Language, "locale": chunk.Locale} {
		normalized, err := knowledge.NormalizeLocale(value)
		if err != nil || normalized != value {
			return fmt.Errorf("%w: chunk %s %q is not canonical BCP 47", ErrInvalidPackage, name, value)
		}
	}
	seenRisk := make(map[knowledge.RiskClass]struct{}, len(chunk.Risk))
	for _, risk := range chunk.Risk {
		if risk == knowledge.RiskClassUnspecified || risk == knowledge.RiskClassProhibitedSecret || !risk.IsARiskClass() {
			return fmt.Errorf("%w: chunk risk %q is invalid", ErrInvalidPackage, risk)
		}
		if _, exists := seenRisk[risk]; exists {
			return fmt.Errorf("%w: duplicate chunk risk %q", ErrInvalidPackage, risk)
		}
		seenRisk[risk] = struct{}{}
	}
	if !chunk.ReviewAfter.IsZero() {
		_, offset := chunk.ReviewAfter.Zone()
		if offset != 0 {
			return fmt.Errorf("%w: chunk review_after must be UTC", ErrInvalidPackage)
		}
	}
	return nil
}

func validateDependenciesManifest(manifest Manifest) error {
	if !slices.IsSortedFunc(manifest.Dependencies, compareDependency) {
		return fmt.Errorf("%w: dependencies are not sorted", ErrInvalidPackage)
	}
	seenPackages := make(map[string]struct{}, len(manifest.Dependencies))
	seenChunks := make(map[string]struct{}, len(manifest.Dependencies))
	for _, dependency := range manifest.Dependencies {
		if dependency.PackageID == manifest.Package.ID || dependency.ChunkID == manifest.Chunk.ID || !uuidV7Pattern.MatchString(dependency.PackageID) || !uuidV7Pattern.MatchString(dependency.ChunkID) || !versionPattern.MatchString(dependency.Version) || knowledge.NormalizeTitle(dependency.Title) != dependency.Title || dependency.Title == "" {
			return fmt.Errorf("%w: dependency identity, version, or title is invalid", ErrInvalidPackage)
		}
		if _, exists := seenPackages[dependency.PackageID]; exists {
			return fmt.Errorf("%w: duplicate dependency package %s", ErrInvalidPackage, dependency.PackageID)
		}
		if _, exists := seenChunks[dependency.ChunkID]; exists {
			return fmt.Errorf("%w: duplicate dependency chunk %s", ErrInvalidPackage, dependency.ChunkID)
		}
		seenPackages[dependency.PackageID] = struct{}{}
		seenChunks[dependency.ChunkID] = struct{}{}
	}
	return nil
}

func compareDependency(left, right Dependency) int {
	if result := strings.Compare(left.PackageID, right.PackageID); result != 0 {
		return result
	}
	return strings.Compare(left.ChunkID, right.ChunkID)
}

func validateInventory(parsed ParsedPackage) error {
	manifest := parsed.Manifest
	if manifest.Content.Entries.Directory != "entries/" || manifest.Content.Assets.Directory != "assets/" || manifest.Content.Edges.Path != "edges.jsonl" || manifest.Content.Sources.Path != "sources.jsonl" {
		return fmt.Errorf("%w: content locations are not the v1 canonical paths", ErrInvalidPackage)
	}
	paths := parsed.Paths()
	wantDate, wantTime := zipTime(manifest.CreatedAt)
	for _, name := range paths {
		metadata, exists := parsed.metadata[name]
		if !exists || metadata.method != zip.Store || metadata.hasExtra || metadata.modifiedDate != wantDate || metadata.modifiedTime != wantTime {
			return fmt.Errorf("%w: ZIP metadata for %q is not canonical", ErrInvalidPackage, name)
		}
	}
	wantArchivePaths := make([]string, 1, len(manifest.Files)+1)
	wantArchivePaths[0] = "manifest.json"
	seen := make(map[string]struct{}, len(manifest.Files))
	for index, file := range manifest.Files {
		if index > 0 && strings.Compare(manifest.Files[index-1].Path, file.Path) >= 0 {
			return fmt.Errorf("%w: manifest file inventory is not uniquely path-sorted", ErrInvalidPackage)
		}
		if _, exists := seen[file.Path]; exists {
			return fmt.Errorf("%w: duplicate manifest file %q", ErrInvalidPackage, file.Path)
		}
		seen[file.Path] = struct{}{}
		wantArchivePaths = append(wantArchivePaths, file.Path)
		data, exists := parsed.ReadFile(file.Path)
		if !exists {
			return fmt.Errorf("%w: declared file %q is missing", ErrIntegrityMismatch, file.Path)
		}
		digest := sha256.Sum256(data)
		if file.Size != int64(len(data)) || file.SHA256 != hex.EncodeToString(digest[:]) {
			return fmt.Errorf("%w: file %q size or SHA-256 differs", ErrIntegrityMismatch, file.Path)
		}
	}
	if !slices.Equal(paths, wantArchivePaths) {
		return fmt.Errorf("%w: ZIP entries %v do not equal manifest inventory %v", ErrIntegrityMismatch, paths, wantArchivePaths)
	}
	entries, edges, sources, assets := 0, 0, 0, 0
	for _, file := range manifest.Files {
		switch {
		case strings.HasPrefix(file.Path, "entries/"):
			entries++
			if file.MediaType != "text/markdown; charset=utf-8" {
				return fmt.Errorf("%w: entry %q has media type %q", ErrInvalidPackage, file.Path, file.MediaType)
			}
		case file.Path == "edges.jsonl":
			edges++
			if file.MediaType != "application/x-ndjson" {
				return fmt.Errorf("%w: edges.jsonl has media type %q", ErrInvalidPackage, file.MediaType)
			}
		case file.Path == "sources.jsonl":
			sources++
			if file.MediaType != "application/x-ndjson" {
				return fmt.Errorf("%w: sources.jsonl has media type %q", ErrInvalidPackage, file.MediaType)
			}
		case strings.HasPrefix(file.Path, "assets/"):
			assets++
			data, _ := parsed.ReadFile(file.Path)
			if nestedArchive(file.Path, file.MediaType, data) {
				return fmt.Errorf("%w: nested archive asset %q is not allowed", ErrInvalidPackage, file.Path)
			}
		default:
			return fmt.Errorf("%w: unexpected payload path %q", ErrInvalidPackage, file.Path)
		}
	}
	if entries != manifest.Content.Entries.Count || edges != 1 || sources != 1 || assets != manifest.Content.Assets.Count {
		return fmt.Errorf("%w: content inventory counts do not match files", ErrIntegrityMismatch)
	}
	return nil
}

func decodePackagePayload(parsed ParsedPackage) ([]knowledge.Entry, []knowledge.Link, []knowledge.Evidence, map[string][]byte, error) {
	manifest := parsed.Manifest
	entries := make([]knowledge.Entry, 0, manifest.Content.Entries.Count)
	assets := make(map[string][]byte, manifest.Content.Assets.Count)
	for _, file := range manifest.Files {
		data, _ := parsed.ReadFile(file.Path)
		switch {
		case strings.HasPrefix(file.Path, "entries/"):
			entry, err := decodeEntryMarkdown(file.Path, data)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			entries = append(entries, entry)
		case strings.HasPrefix(file.Path, "assets/"):
			assets[file.Path] = data
		}
	}
	edgesData, _ := parsed.ReadFile(manifest.Content.Edges.Path)
	links, err := decodeJSONLines[knowledge.Link]("edges.jsonl", edgesData, func(value knowledge.Link) error { return value.Validate() })
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sourcesData, _ := parsed.ReadFile(manifest.Content.Sources.Path)
	evidence, err := decodeJSONLines[knowledge.Evidence]("sources.jsonl", sourcesData, func(value knowledge.Evidence) error { return value.Validate() })
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if len(entries) != manifest.Content.Entries.Count || len(links) != manifest.Content.Edges.Count || len(evidence) != manifest.Content.Sources.Count {
		return nil, nil, nil, nil, fmt.Errorf("%w: decoded record counts do not match manifest", ErrIntegrityMismatch)
	}
	return entries, links, evidence, assets, nil
}

func decodeEntryMarkdown(name string, data []byte) (knowledge.Entry, error) {
	if !utf8.Valid(data) || bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) || bytes.Contains(data, []byte{'\r'}) || !bytes.HasSuffix(data, []byte{'\n'}) || !bytes.HasPrefix(data, []byte("---\n")) {
		return knowledge.Entry{}, fmt.Errorf("%w: entry %q is not canonical UTF-8 Markdown", ErrInvalidPackage, name)
	}
	parts := bytes.SplitN(data[len("---\n"):], []byte("\n---\n"), 2)
	if len(parts) != 2 {
		return knowledge.Entry{}, fmt.Errorf("%w: entry %q has invalid JSON front matter", ErrInvalidPackage, name)
	}
	if err := requireCanonicalJSON(parts[0], false); err != nil {
		return knowledge.Entry{}, fmt.Errorf("%w: entry %q front matter: %v", ErrInvalidPackage, name, err)
	}
	var entry knowledge.Entry
	if err := decodeStrictJSON(parts[0], &entry); err != nil {
		return knowledge.Entry{}, fmt.Errorf("%w: entry %q front matter: %v", ErrInvalidPackage, name, err)
	}
	entry.Body = string(parts[1])
	if err := entry.Validate(); err != nil {
		return knowledge.Entry{}, fmt.Errorf("%w: entry %q: %v", ErrInvalidPackage, name, err)
	}
	wantName := "entries/" + string(entry.ID) + ".md"
	if name != wantName {
		return knowledge.Entry{}, fmt.Errorf("%w: entry ID does not match path %q", ErrInvalidPackage, name)
	}
	return entry, nil
}

func decodeJSONLines[T any](name string, data []byte, validate func(T) error) ([]T, error) {
	if !utf8.Valid(data) || bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) || bytes.Contains(data, []byte{'\r'}) || (len(data) > 0 && !bytes.HasSuffix(data, []byte{'\n'})) {
		return nil, fmt.Errorf("%w: %s is not canonical UTF-8 JSON Lines", ErrInvalidPackage, name)
	}
	trimmed := bytes.TrimSuffix(data, []byte{'\n'})
	if len(trimmed) == 0 {
		return []T{}, nil
	}
	lines := bytes.Split(trimmed, []byte{'\n'})
	values := make([]T, 0, len(lines))
	var priorID string
	for index, line := range lines {
		if len(line) == 0 {
			return nil, fmt.Errorf("%w: %s line %d is empty", ErrInvalidPackage, name, index+1)
		}
		if err := requireCanonicalJSON(line, false); err != nil {
			return nil, fmt.Errorf("%w: %s line %d: %v", ErrInvalidPackage, name, index+1, err)
		}
		var value T
		if err := decodeStrictJSON(line, &value); err != nil {
			return nil, fmt.Errorf("%w: %s line %d: %v", ErrInvalidPackage, name, index+1, err)
		}
		if err := validate(value); err != nil {
			return nil, fmt.Errorf("%w: %s line %d: %v", ErrInvalidPackage, name, index+1, err)
		}
		identity, err := recordID(value)
		if err != nil {
			return nil, err
		}
		if strings.Compare(priorID, identity) >= 0 {
			return nil, fmt.Errorf("%w: %s records are not uniquely ID-sorted", ErrInvalidPackage, name)
		}
		priorID = identity
		values = append(values, value)
	}
	return values, nil
}

func recordID(value any) (string, error) {
	switch record := value.(type) {
	case knowledge.Link:
		return string(record.ID), nil
	case knowledge.Evidence:
		return string(record.ID), nil
	default:
		return "", fmt.Errorf("%w: unsupported packaged record type %T", ErrInvalidPackage, value)
	}
}

func validateRecordGraph(manifest Manifest, entries []knowledge.Entry, links []knowledge.Link, evidenceRecords []knowledge.Evidence) error {
	entryIDs := make(map[string]struct{}, len(entries))
	evidenceIDs := make(map[knowledge.EvidenceID]struct{}, len(evidenceRecords))
	dependencyChunks := make(map[string]struct{}, len(manifest.Dependencies))
	for _, dependency := range manifest.Dependencies {
		dependencyChunks[dependency.ChunkID] = struct{}{}
	}
	for _, evidence := range evidenceRecords {
		if _, exists := evidenceIDs[evidence.ID]; exists {
			return fmt.Errorf("%w: duplicate evidence ID %s", ErrInvalidPackage, evidence.ID)
		}
		evidenceIDs[evidence.ID] = struct{}{}
	}
	for _, entry := range entries {
		if string(entry.ChunkID) != manifest.Chunk.ID {
			return fmt.Errorf("%w: entry %s belongs to chunk %s", ErrInvalidPackage, entry.ID, entry.ChunkID)
		}
		if _, exists := entryIDs[string(entry.ID)]; exists {
			return fmt.Errorf("%w: duplicate entry ID %s", ErrInvalidPackage, entry.ID)
		}
		entryIDs[string(entry.ID)] = struct{}{}
		if err := requireEvidence(entry.EvidenceIDs, evidenceIDs, "entry "+string(entry.ID)); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidPackage, err)
		}
		if err := requireEvidence(entry.Verification.EvidenceIDs, evidenceIDs, "entry verification "+string(entry.ID)); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidPackage, err)
		}
	}
	for _, entry := range entries {
		if entry.SupersededByID != "" {
			if _, exists := entryIDs[string(entry.SupersededByID)]; !exists {
				return fmt.Errorf("%w: entry %s supersedes to missing entry %s", ErrInvalidPackage, entry.ID, entry.SupersededByID)
			}
		}
	}
	seenLinks := make(map[knowledge.LinkID]struct{}, len(links))
	for _, link := range links {
		if _, exists := seenLinks[link.ID]; exists {
			return fmt.Errorf("%w: duplicate link ID %s", ErrInvalidPackage, link.ID)
		}
		seenLinks[link.ID] = struct{}{}
		if err := requireEndpoint(link.Source, knowledge.ChunkID(manifest.Chunk.ID), entryIDs, dependencyChunks); err != nil {
			return fmt.Errorf("%w: link %s source: %v", ErrInvalidPackage, link.ID, err)
		}
		if err := requireEndpoint(link.Target, knowledge.ChunkID(manifest.Chunk.ID), entryIDs, dependencyChunks); err != nil {
			return fmt.Errorf("%w: link %s target: %v", ErrInvalidPackage, link.ID, err)
		}
		if err := requireEvidence(link.EvidenceIDs, evidenceIDs, "link "+string(link.ID)); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidPackage, err)
		}
	}
	return nil
}

func validatePackageSignature(manifest Manifest, options ValidationOptions) (SignatureState, error) {
	if manifest.Signature == nil {
		if options.RequireVerifiedSignature {
			return "", fmt.Errorf("%w: a verified signature is required", ErrInvalidPackage)
		}
		return SignatureStateUnsigned, nil
	}
	key, exists := options.VerificationKeys[manifest.Signature.KeyID]
	if !exists {
		if options.RequireVerifiedSignature {
			return "", fmt.Errorf("%w: no verification key for %s", ErrInvalidPackage, manifest.Signature.KeyID)
		}
		return SignatureStateUnknown, nil
	}
	if err := VerifyManifest(manifest, key); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	return SignatureStateVerified, nil
}

func requireCanonicalJSON(data []byte, indent bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	canonical, err := canonicalJSON(document, indent)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, canonical) {
		return errors.New("JSON is not canonical")
	}
	return nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func nestedArchive(name, mediaType string, data []byte) bool {
	extension := strings.ToLower(filepath.Ext(name))
	mediaType = strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	if slices.Contains([]string{".zip", ".kknowledge", ".jar", ".apk", ".docx", ".xlsx", ".pptx"}, extension) ||
		slices.Contains([]string{"application/zip", "application/java-archive", "application/vnd.android.package-archive"}, mediaType) {
		return true
	}
	return bytes.HasPrefix(data, []byte{'P', 'K', 3, 4}) || bytes.HasPrefix(data, []byte{'P', 'K', 5, 6}) || bytes.HasPrefix(data, []byte{'P', 'K', 7, 8})
}

func buildNumber(value string) (int64, error) {
	if !runtimeBuildPattern.MatchString(value) {
		return 0, fmt.Errorf("invalid build number %q", value)
	}
	digits := strings.TrimPrefix(value, "r")
	if separator := strings.IndexByte(digits, '-'); separator >= 0 {
		digits = digits[:separator]
	}
	return strconv.ParseInt(digits, 10, 64)
}

func validLicenseExpression(value string) bool {
	tokens := licenseTokens(value)
	if len(tokens) == 0 {
		return false
	}
	position := 0
	var parseExpression func() bool
	var parseTerm func() bool
	parseAtom := func() bool {
		if position >= len(tokens) {
			return false
		}
		if tokens[position] == "(" {
			position++
			if !parseExpression() || position >= len(tokens) || tokens[position] != ")" {
				return false
			}
			position++
			return true
		}
		if !licenseIdentifier(tokens[position]) {
			return false
		}
		position++
		if position < len(tokens) && tokens[position] == "WITH" {
			position++
			if position >= len(tokens) || !licenseIdentifier(tokens[position]) {
				return false
			}
			position++
		}
		return true
	}
	parseTerm = func() bool {
		if !parseAtom() {
			return false
		}
		for position < len(tokens) && tokens[position] == "AND" {
			position++
			if !parseAtom() {
				return false
			}
		}
		return true
	}
	parseExpression = func() bool {
		if !parseTerm() {
			return false
		}
		for position < len(tokens) && tokens[position] == "OR" {
			position++
			if !parseTerm() {
				return false
			}
		}
		return true
	}
	return parseExpression() && position == len(tokens)
}

func licenseTokens(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n\t") {
		return nil
	}
	value = strings.ReplaceAll(value, "(", " ( ")
	value = strings.ReplaceAll(value, ")", " ) ")
	return strings.Fields(value)
}

func licenseIdentifier(value string) bool {
	if value == "" || value == "AND" || value == "OR" || value == "WITH" || value == "(" || value == ")" {
		return false
	}
	for index, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == ':' || (r == '+' && index == len(value)-1) {
			continue
		}
		return false
	}
	return true
}
