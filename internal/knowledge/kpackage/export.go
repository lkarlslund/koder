package kpackage

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lkarlslund/koder/internal/knowledge"
)

var (
	uuidV7Pattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	buildPattern   = regexp.MustCompile(`^r[0-9]+$`)
	tokenPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/+~-]*$`)
)

type payload struct {
	path      string
	mediaType string
	data      []byte
}

type countingHashWriter struct {
	writer io.Writer
	hash   hash.Hash
	size   int64
}

func (w *countingHashWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	if n > 0 {
		_, _ = w.hash.Write(data[:n])
		w.size += int64(n)
	}
	return n, err
}

// Export writes one deterministic .kknowledge ZIP and returns its manifest and digest.
// The caller owns writer lifecycle. Validation finishes before the first byte is written.
func Export(writer io.Writer, request ExportRequest) (ExportResult, error) {
	if writer == nil {
		return ExportResult{}, errors.New("knowledge package export requires a writer")
	}
	manifest, payloads, err := buildExport(request)
	if err != nil {
		return ExportResult{}, err
	}
	manifestData, err := canonicalJSON(manifest, true)
	if err != nil {
		return ExportResult{}, fmt.Errorf("encode knowledge package manifest: %w", err)
	}

	digestWriter := &countingHashWriter{writer: writer, hash: sha256.New()}
	archive := zip.NewWriter(digestWriter)
	date, clock := zipTime(request.CreatedAt)
	if err := writeZIPFile(archive, "manifest.json", manifestData, date, clock); err != nil {
		_ = archive.Close()
		return ExportResult{}, err
	}
	for _, item := range payloads {
		if err := writeZIPFile(archive, item.path, item.data, date, clock); err != nil {
			_ = archive.Close()
			return ExportResult{}, err
		}
	}
	if err := archive.Close(); err != nil {
		return ExportResult{}, fmt.Errorf("close knowledge package archive: %w", err)
	}
	return ExportResult{
		Manifest: manifest,
		SHA256:   hex.EncodeToString(digestWriter.hash.Sum(nil)),
		Size:     digestWriter.size,
	}, nil
}

func buildExport(request ExportRequest) (Manifest, []payload, error) {
	if err := validateExportRequest(request); err != nil {
		return Manifest{}, nil, err
	}

	entries := slices.Clone(request.Entries)
	links := slices.Clone(request.Links)
	evidence := slices.Clone(request.Evidence)
	assets := slices.Clone(request.Assets)
	slices.SortFunc(entries, func(left, right knowledge.Entry) int { return strings.Compare(string(left.ID), string(right.ID)) })
	slices.SortFunc(links, func(left, right knowledge.Link) int { return strings.Compare(string(left.ID), string(right.ID)) })
	slices.SortFunc(evidence, func(left, right knowledge.Evidence) int { return strings.Compare(string(left.ID), string(right.ID)) })
	slices.SortFunc(assets, func(left, right Asset) int { return strings.Compare(left.Path, right.Path) })

	payloads := make([]payload, 0, len(entries)+len(assets)+2)
	edgesData, err := canonicalJSONLines(links)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("encode knowledge links: %w", err)
	}
	payloads = append(payloads, payload{path: "edges.jsonl", mediaType: "application/x-ndjson", data: edgesData})
	for _, entry := range entries {
		data, err := entryMarkdown(entry)
		if err != nil {
			return Manifest{}, nil, fmt.Errorf("encode knowledge entry %s: %w", entry.ID, err)
		}
		payloads = append(payloads, payload{
			path: "entries/" + string(entry.ID) + ".md", mediaType: "text/markdown; charset=utf-8", data: data,
		})
	}
	sourcesData, err := canonicalJSONLines(evidence)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("encode knowledge evidence: %w", err)
	}
	payloads = append(payloads, payload{path: "sources.jsonl", mediaType: "application/x-ndjson", data: sourcesData})
	for _, asset := range assets {
		payloads = append(payloads, payload{path: asset.Path, mediaType: asset.MediaType, data: slices.Clone(asset.Data)})
	}
	slices.SortFunc(payloads, func(left, right payload) int { return strings.Compare(left.path, right.path) })

	files := make([]File, len(payloads))
	for index, item := range payloads {
		digest := sha256.Sum256(item.data)
		files[index] = File{
			MediaType: item.mediaType,
			Path:      item.path,
			SHA256:    hex.EncodeToString(digest[:]),
			Size:      int64(len(item.data)),
		}
	}

	dependencies := append([]Dependency{}, request.Dependencies...)
	slices.SortFunc(dependencies, func(left, right Dependency) int {
		if result := strings.Compare(left.PackageID, right.PackageID); result != 0 {
			return result
		}
		return strings.Compare(left.ChunkID, right.ChunkID)
	})
	requiredFeatures := normalizedSet(request.Features.Required)
	optionalFeatures := normalizedSet(request.Features.Optional)
	manifest := Manifest{
		Chunk: ManifestChunk{
			Aliases: slices.Clone(request.Chunk.Aliases), Description: request.Chunk.Description, Domain: request.Chunk.Domain,
			ID: string(request.Chunk.ID), Kind: request.Chunk.Kind, Language: request.Chunk.Language, Locale: request.Chunk.Locale,
			ReviewAfter: request.Chunk.ReviewAfter, Risk: slices.Clone(request.Chunk.Risk), Scope: request.Chunk.Scope,
			SharedWith: slices.Clone(request.Chunk.SharedWith), SourcePolicy: request.Chunk.SourcePolicy, State: request.Chunk.State,
			Tags: slices.Clone(request.Chunk.Tags), Title: request.Chunk.Title, Visibility: request.Chunk.Visibility,
		},
		Content: Content{
			Assets:  Collection{Directory: "assets/", Count: len(assets)},
			Edges:   RecordFile{Path: "edges.jsonl", Count: len(links)},
			Entries: Collection{Directory: "entries/", Count: len(entries)},
			Sources: RecordFile{Path: "sources.jsonl", Count: len(evidence)},
		},
		CreatedAt: request.CreatedAt.UTC(), Dependencies: dependencies,
		Features: Features{Required: requiredFeatures, Optional: optionalFeatures}, Files: files,
		Format: Format, License: request.License, MinKoderVersion: request.Chunk.MinKoderVersion,
		Package: request.Package, Publisher: request.Publisher, SchemaVersion: SchemaVersion,
	}
	return manifest, payloads, nil
}

func validateExportRequest(request ExportRequest) error {
	if !uuidV7Pattern.MatchString(request.Package.ID) {
		return errors.New("knowledge package ID must be a lowercase UUIDv7")
	}
	if !versionPattern.MatchString(request.Package.Version) {
		return errors.New("knowledge package version must be semantic version syntax")
	}
	if !tokenPattern.MatchString(request.Publisher.ID) || strings.TrimSpace(request.Publisher.Name) == "" {
		return errors.New("knowledge package publisher ID and name are required")
	}
	if err := validateOptionalURL("publisher URL", request.Publisher.URL); err != nil {
		return err
	}
	if strings.TrimSpace(request.License.Name) == "" {
		return errors.New("knowledge package license name is required")
	}
	if err := validateOptionalURL("license URL", request.License.URL); err != nil {
		return err
	}
	_, createdAtOffset := request.CreatedAt.Zone()
	if request.CreatedAt.IsZero() || createdAtOffset != 0 || request.CreatedAt.Year() < 1980 || request.CreatedAt.Year() > 2107 {
		return errors.New("knowledge package created_at must be UTC in the ZIP timestamp range 1980 through 2107")
	}
	if err := request.Chunk.Validate(); err != nil {
		return fmt.Errorf("validate knowledge package chunk: %w", err)
	}
	if !buildPattern.MatchString(request.Chunk.MinKoderVersion) {
		return errors.New("knowledge package chunk min_koder_version must use rNNNN syntax")
	}
	if request.Chunk.Publisher.ID != "" && request.Chunk.Publisher.ID != request.Publisher.ID {
		return errors.New("knowledge package publisher does not match chunk publisher")
	}
	if request.Chunk.License != "" && request.Chunk.License != request.License.Name {
		return errors.New("knowledge package license does not match chunk license")
	}
	if err := validateFeatures(request.Features); err != nil {
		return err
	}
	if err := validateDependencies(request.Chunk, request.Dependencies); err != nil {
		return err
	}
	return validatePayload(request)
}

func validatePayload(request ExportRequest) error {
	entries := make(map[string]struct{}, len(request.Entries))
	evidence := make(map[knowledge.EvidenceID]struct{}, len(request.Evidence))
	links := make(map[knowledge.LinkID]struct{}, len(request.Links))
	for _, record := range request.Evidence {
		if err := record.Validate(); err != nil {
			return fmt.Errorf("validate knowledge package evidence %s: %w", record.ID, err)
		}
		if _, exists := evidence[record.ID]; exists {
			return fmt.Errorf("knowledge package has duplicate evidence ID %s", record.ID)
		}
		evidence[record.ID] = struct{}{}
	}
	for _, record := range request.Entries {
		if err := record.Validate(); err != nil {
			return fmt.Errorf("validate knowledge package entry %s: %w", record.ID, err)
		}
		if record.ChunkID != request.Chunk.ID {
			return fmt.Errorf("knowledge package entry %s belongs to chunk %s", record.ID, record.ChunkID)
		}
		if _, exists := entries[string(record.ID)]; exists {
			return fmt.Errorf("knowledge package has duplicate entry ID %s", record.ID)
		}
		entries[string(record.ID)] = struct{}{}
		if err := requireEvidence(record.EvidenceIDs, evidence, "entry "+string(record.ID)); err != nil {
			return err
		}
		if err := requireEvidence(record.Verification.EvidenceIDs, evidence, "entry verification "+string(record.ID)); err != nil {
			return err
		}
	}
	dependencyChunks := make(map[string]struct{}, len(request.Dependencies))
	for _, dependency := range request.Dependencies {
		dependencyChunks[dependency.ChunkID] = struct{}{}
	}
	for _, record := range request.Links {
		if err := record.Validate(); err != nil {
			return fmt.Errorf("validate knowledge package link %s: %w", record.ID, err)
		}
		if _, exists := links[record.ID]; exists {
			return fmt.Errorf("knowledge package has duplicate link ID %s", record.ID)
		}
		links[record.ID] = struct{}{}
		if err := requireEndpoint(record.Source, request.Chunk.ID, entries, dependencyChunks); err != nil {
			return fmt.Errorf("knowledge package link %s source: %w", record.ID, err)
		}
		if err := requireEndpoint(record.Target, request.Chunk.ID, entries, dependencyChunks); err != nil {
			return fmt.Errorf("knowledge package link %s target: %w", record.ID, err)
		}
		if err := requireEvidence(record.EvidenceIDs, evidence, "link "+string(record.ID)); err != nil {
			return err
		}
	}
	paths := map[string]struct{}{"edges.jsonl": {}, "sources.jsonl": {}}
	for id := range entries {
		paths["entries/"+id+".md"] = struct{}{}
	}
	for _, asset := range request.Assets {
		if err := validateAsset(asset); err != nil {
			return err
		}
		if _, exists := paths[asset.Path]; exists {
			return fmt.Errorf("knowledge package has duplicate payload path %q", asset.Path)
		}
		paths[asset.Path] = struct{}{}
	}
	return nil
}

func validateDependencies(chunk knowledge.Chunk, dependencies []Dependency) error {
	want := make(map[string]struct{}, len(chunk.DependencyIDs))
	for _, id := range chunk.DependencyIDs {
		want[string(id)] = struct{}{}
	}
	seenPackages := make(map[string]struct{}, len(dependencies))
	seenChunks := make(map[string]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		if !uuidV7Pattern.MatchString(dependency.PackageID) || !uuidV7Pattern.MatchString(dependency.ChunkID) || !versionPattern.MatchString(dependency.Version) || strings.TrimSpace(dependency.Title) == "" {
			return errors.New("knowledge package dependency has invalid identity, version, or title")
		}
		if _, exists := seenPackages[dependency.PackageID]; exists {
			return fmt.Errorf("knowledge package has duplicate dependency package %s", dependency.PackageID)
		}
		if _, exists := seenChunks[dependency.ChunkID]; exists {
			return fmt.Errorf("knowledge package has duplicate dependency chunk %s", dependency.ChunkID)
		}
		seenPackages[dependency.PackageID] = struct{}{}
		seenChunks[dependency.ChunkID] = struct{}{}
		if _, exists := want[dependency.ChunkID]; !exists {
			return fmt.Errorf("knowledge package dependency chunk %s is not declared by the chunk", dependency.ChunkID)
		}
	}
	if len(seenChunks) != len(want) {
		return errors.New("knowledge package dependencies do not cover every chunk dependency")
	}
	return nil
}

func validateFeatures(features Features) error {
	seen := make(map[string]string, len(features.Required)+len(features.Optional))
	for kind, values := range map[string][]string{"required": features.Required, "optional": features.Optional} {
		for _, value := range values {
			if !tokenPattern.MatchString(value) {
				return fmt.Errorf("knowledge package %s feature %q is invalid", kind, value)
			}
			if previous, exists := seen[value]; exists {
				return fmt.Errorf("knowledge package feature %q appears in %s and %s", value, previous, kind)
			}
			seen[value] = kind
		}
	}
	return nil
}

func validateAsset(asset Asset) error {
	if !strings.HasPrefix(asset.Path, "assets/") || path.Clean(asset.Path) != asset.Path || strings.Contains(asset.Path, "\\") || !utf8.ValidString(asset.Path) {
		return fmt.Errorf("knowledge package asset path %q is invalid", asset.Path)
	}
	for _, component := range strings.Split(strings.TrimPrefix(asset.Path, "assets/"), "/") {
		if component == "" || component == "." || component == ".." || strings.HasPrefix(component, ".") {
			return fmt.Errorf("knowledge package asset path %q is invalid", asset.Path)
		}
	}
	if strings.TrimSpace(asset.MediaType) == "" || strings.ContainsAny(asset.MediaType, "\r\n") {
		return fmt.Errorf("knowledge package asset %q has invalid media type", asset.Path)
	}
	return nil
}

func validateOptionalURL(name, value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return fmt.Errorf("knowledge package %s must be an HTTP(S) URL", name)
	}
	return nil
}

func requireEndpoint(ref knowledge.ObjectRef, chunkID knowledge.ChunkID, entries, dependencyChunks map[string]struct{}) error {
	switch ref.Kind {
	case knowledge.ObjectKindEntry:
		if _, exists := entries[ref.ID]; !exists {
			return fmt.Errorf("entry %s is not included", ref.ID)
		}
	case knowledge.ObjectKindChunk:
		if ref.ID != string(chunkID) {
			if _, exists := dependencyChunks[ref.ID]; !exists {
				return fmt.Errorf("chunk %s is not included or declared as a dependency", ref.ID)
			}
		}
	default:
		return fmt.Errorf("object kind %s cannot be packaged as an endpoint", ref.Kind)
	}
	return nil
}

func requireEvidence(ids []knowledge.EvidenceID, included map[knowledge.EvidenceID]struct{}, owner string) error {
	for _, id := range ids {
		if _, exists := included[id]; !exists {
			return fmt.Errorf("knowledge package %s references missing evidence %s", owner, id)
		}
	}
	return nil
}

func entryMarkdown(entry knowledge.Entry) ([]byte, error) {
	body := normalizeText(entry.Body)
	encoded, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	var metadata map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&metadata); err != nil {
		return nil, err
	}
	delete(metadata, "body")
	frontMatter, err := canonicalJSON(metadata, false)
	if err != nil {
		return nil, err
	}
	result := make([]byte, 0, len(frontMatter)+len(body)+10)
	result = append(result, "---\n"...)
	result = append(result, frontMatter...)
	result = append(result, "\n---\n"...)
	result = append(result, body...)
	return result, nil
}

func normalizeText(value string) []byte {
	value = strings.TrimPrefix(value, "\uFEFF")
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	if value != "" && !strings.HasSuffix(value, "\n") {
		value += "\n"
	}
	return []byte(value)
}

func canonicalJSONLines[T any](records []T) ([]byte, error) {
	var output bytes.Buffer
	for _, record := range records {
		line, err := canonicalJSON(record, false)
		if err != nil {
			return nil, err
		}
		output.Write(line)
		output.WriteByte('\n')
	}
	return output.Bytes(), nil
}

func canonicalJSON(value any, indent bool) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if indent {
		encoded, err = json.MarshalIndent(document, "", "  ")
		encoded = append(encoded, '\n')
	} else {
		encoded, err = json.Marshal(document)
	}
	return encoded, err
}

func normalizedSet(values []string) []string {
	result := append([]string{}, values...)
	slices.Sort(result)
	return result
}

func zipTime(value time.Time) (uint16, uint16) {
	value = value.UTC()
	date := uint16(value.Day()) | uint16(value.Month())<<5 | uint16(value.Year()-1980)<<9
	clock := uint16(value.Second()/2) | uint16(value.Minute())<<5 | uint16(value.Hour())<<11
	return date, clock
}

func writeZIPFile(archive *zip.Writer, name string, data []byte, date, clock uint16) error {
	header := &zip.FileHeader{Name: name, Method: zip.Store, ModifiedDate: date, ModifiedTime: clock}
	header.Flags = 0x800
	header.CreatorVersion = 20
	header.ReaderVersion = 20
	entry, err := archive.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create knowledge package file %q: %w", name, err)
	}
	if _, err := entry.Write(data); err != nil {
		return fmt.Errorf("write knowledge package file %q: %w", name, err)
	}
	return nil
}
