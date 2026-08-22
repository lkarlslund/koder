package kpackage

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/lkarlslund/koder/internal/knowledge"
)

func TestExportMatchesCanonicalExampleAndIsDeterministic(t *testing.T) {
	t.Parallel()
	request := canonicalExampleRequest(t)
	var first bytes.Buffer
	result, err := Export(&first, request)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	var second bytes.Buffer
	secondResult, err := Export(&second, request)
	if err != nil {
		t.Fatalf("second Export() error = %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) || !reflect.DeepEqual(result, secondResult) {
		t.Fatal("repeated exports did not produce identical bytes and metadata")
	}
	if result.Size != int64(first.Len()) {
		t.Errorf("result size = %d, want %d", result.Size, first.Len())
	}
	digest := sha256.Sum256(first.Bytes())
	if got := hex.EncodeToString(digest[:]); result.SHA256 != got {
		t.Errorf("result SHA-256 = %s, want %s", result.SHA256, got)
	}

	reader, err := zip.NewReader(bytes.NewReader(first.Bytes()), int64(first.Len()))
	if err != nil {
		t.Fatalf("read exported ZIP: %v", err)
	}
	wantNames := []string{
		"manifest.json",
		"edges.jsonl",
		"entries/01a02b00-0000-7000-8000-000000000002.md",
		"entries/01a02b00-0000-7000-8000-000000000003.md",
		"sources.jsonl",
	}
	gotNames := make([]string, len(reader.File))
	for index, file := range reader.File {
		gotNames[index] = file.Name
		if file.Method != zip.Store {
			t.Errorf("%s compression method = %d, want store", file.Name, file.Method)
		}
		if file.ModifiedDate != 23830 || file.ModifiedTime != 24736 {
			t.Errorf("%s ZIP timestamp date=%d time=%d", file.Name, file.ModifiedDate, file.ModifiedTime)
		}
		if len(file.Extra) != 0 {
			t.Errorf("%s has platform extra fields %x", file.Name, file.Extra)
		}
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("ZIP entries = %v, want %v", gotNames, wantNames)
	}

	exampleRoot := filepath.Join("..", "..", "..", "protocol", "knowledge", "package", "v1", "examples", "linux-partition-tools")
	for _, file := range reader.File {
		got := readZIPFile(t, file)
		want := readExportFixture(t, filepath.Join(exampleRoot, filepath.FromSlash(file.Name)))
		if !bytes.Equal(got, want) {
			t.Errorf("exported %s does not match canonical example\ngot:\n%s\nwant:\n%s", file.Name, got, want)
		}
	}
	assertManifestSchema(t, readZIPFile(t, reader.File[0]))
}

func TestExportSortsAssetsAndFeaturesAndInventoriesExactBytes(t *testing.T) {
	t.Parallel()
	request := canonicalExampleRequest(t)
	request.Assets = []Asset{
		{Path: "assets/zeta.txt", MediaType: "text/plain", Data: []byte("zeta\n")},
		{Path: "assets/alpha/data.bin", MediaType: "application/octet-stream", Data: []byte{0, 1, 2}},
	}
	request.Features = Features{Required: []string{"zeta", "alpha"}, Optional: []string{"preview"}}
	var output bytes.Buffer
	result, err := Export(&output, request)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if !slices.Equal(result.Manifest.Features.Required, []string{"alpha", "zeta"}) {
		t.Errorf("required features = %v", result.Manifest.Features.Required)
	}
	if result.Manifest.Content.Assets.Count != 2 {
		t.Errorf("asset count = %d", result.Manifest.Content.Assets.Count)
	}
	paths := make([]string, len(result.Manifest.Files))
	for index, file := range result.Manifest.Files {
		paths[index] = file.Path
	}
	if !slices.IsSorted(paths) || !slices.Equal(paths[:2], []string{"assets/alpha/data.bin", "assets/zeta.txt"}) {
		t.Errorf("manifest paths are not sorted: %v", paths)
	}
	reader, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range result.Manifest.Files {
		index := slices.IndexFunc(reader.File, func(entry *zip.File) bool { return entry.Name == file.Path })
		if index < 0 {
			t.Errorf("manifest file %s is absent from ZIP", file.Path)
			continue
		}
		data := readZIPFile(t, reader.File[index])
		digest := sha256.Sum256(data)
		if file.Size != int64(len(data)) || file.SHA256 != hex.EncodeToString(digest[:]) {
			t.Errorf("manifest integrity for %s is incorrect", file.Path)
		}
	}
}

func TestExportRejectsInvalidInputBeforeWriting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*ExportRequest)
		want   string
	}{
		{name: "package ID", mutate: func(value *ExportRequest) { value.Package.ID = "bad" }, want: "UUIDv7"},
		{name: "missing evidence", mutate: func(value *ExportRequest) { value.Evidence = nil }, want: "missing evidence"},
		{name: "asset traversal", mutate: func(value *ExportRequest) {
			value.Assets = []Asset{{Path: "assets/../secret", MediaType: "text/plain"}}
		}, want: "asset path"},
		{name: "duplicate feature", mutate: func(value *ExportRequest) {
			value.Features = Features{Required: []string{"same"}, Optional: []string{"same"}}
		}, want: "appears in"},
		{name: "non-UTC time", mutate: func(value *ExportRequest) { value.CreatedAt = value.CreatedAt.In(time.FixedZone("test", 3600)) }, want: "must be UTC"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := canonicalExampleRequest(t)
			test.mutate(&request)
			writer := new(bytes.Buffer)
			_, err := Export(writer, request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Export() error = %v, want containing %q", err, test.want)
			}
			if writer.Len() != 0 {
				t.Fatalf("invalid export wrote %d bytes", writer.Len())
			}
		})
	}
}

func TestExportReturnsWriterFailure(t *testing.T) {
	t.Parallel()
	_, err := Export(failingWriter{}, canonicalExampleRequest(t))
	if err == nil || !strings.Contains(err.Error(), "knowledge package") {
		t.Fatalf("Export() error = %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("synthetic writer failure") }

func canonicalExampleRequest(t *testing.T) ExportRequest {
	t.Helper()
	root := filepath.Join("..", "..", "..", "protocol", "knowledge", "package", "v1", "examples", "linux-partition-tools")
	entries := []knowledge.Entry{
		readExportEntry(t, filepath.Join(root, "entries", "01a02b00-0000-7000-8000-000000000003.md")),
		readExportEntry(t, filepath.Join(root, "entries", "01a02b00-0000-7000-8000-000000000002.md")),
	}
	links := readExportJSONLines[knowledge.Link](t, filepath.Join(root, "edges.jsonl"))
	evidence := readExportJSONLines[knowledge.Evidence](t, filepath.Join(root, "sources.jsonl"))
	createdAt := exportTime(t, "2026-08-22T12:05:00Z")
	chunkUpdatedAt := exportTime(t, "2026-08-22T12:00:00Z")
	return ExportRequest{
		Package:   Identity{ID: "01a02b00-0000-7000-8000-000000000000", Version: "1.0.0"},
		Publisher: Publisher{ID: "publisher:example", Name: "Koder canonical examples", URL: "https://github.com/lkarlslund/koder"},
		License:   License{Name: "CC-BY-4.0", URL: "https://creativecommons.org/licenses/by/4.0/"},
		CreatedAt: createdAt,
		Chunk: knowledge.Chunk{
			ID: "01a02b00-0000-7000-8000-000000000001", Title: "Linux partition tools",
			Description: "Verified, safety-conscious procedures for Linux partition tables.", Aliases: []string{"disk partitioning"},
			Tags: []string{"linux", "partitioning", "sfdisk"}, Kind: knowledge.ChunkKindReference,
			Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Visibility: knowledge.VisibilityPublic,
			Language: "en", Locale: "en-DK", Domain: "system_administration", Risk: []knowledge.RiskClass{knowledge.RiskClassPhysicalSafety},
			State: knowledge.ChunkStateActive, SchemaVersion: 1,
			Revision:  knowledge.Revision{Number: 1, ID: "01a02b00-0000-7000-8000-000000000101", Actor: knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "publisher:example"}, CreatedAt: chunkUpdatedAt},
			Publisher: PublisherToKnowledge(Publisher{ID: "publisher:example", Name: "Koder canonical examples"}),
			License:   "CC-BY-4.0", SourcePolicy: "Require an authoritative util-linux source and an explicit target-device check.",
			MinKoderVersion: "r1847", CreatedAt: chunkUpdatedAt, UpdatedAt: chunkUpdatedAt,
			ReviewAfter: exportTime(t, "2026-11-22T00:00:00Z"), Counts: knowledge.ChunkCounts{Entries: 2, Links: 1, Evidence: 1},
		},
		Entries: entries, Links: links, Evidence: evidence,
	}
}

func PublisherToKnowledge(value Publisher) knowledge.Publisher {
	return knowledge.Publisher{ID: value.ID, Name: value.Name}
}

func readExportEntry(t *testing.T, path string) knowledge.Entry {
	t.Helper()
	data := readExportFixture(t, path)
	parts := bytes.SplitN(bytes.TrimPrefix(data, []byte("---\n")), []byte("\n---\n"), 2)
	if len(parts) != 2 {
		t.Fatalf("invalid entry fixture %s", path)
	}
	var entry knowledge.Entry
	if err := json.Unmarshal(parts[0], &entry); err != nil {
		t.Fatalf("decode entry fixture %s: %v", path, err)
	}
	entry.Body = string(parts[1])
	return entry
}

func readExportJSONLines[T any](t *testing.T, path string) []T {
	t.Helper()
	lines := bytes.Split(bytes.TrimSuffix(readExportFixture(t, path), []byte{'\n'}), []byte{'\n'})
	values := make([]T, 0, len(lines))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var value T
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatalf("decode JSON Lines fixture %s: %v", path, err)
		}
		values = append(values, value)
	}
	return values
}

func readExportFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return data
}

func readZIPFile(t *testing.T, file *zip.File) []byte {
	t.Helper()
	reader, err := file.Open()
	if err != nil {
		t.Fatalf("open ZIP file %s: %v", file.Name, err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close ZIP file %s: %v", file.Name, err)
		}
	}()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read ZIP file %s: %v", file.Name, err)
	}
	return data
}

func assertManifestSchema(t *testing.T, manifest []byte) {
	t.Helper()
	path := filepath.Join("..", "..", "..", "protocol", "knowledge", "package", "v1", "manifest.schema.json")
	schemaData := readExportFixture(t, path)
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("manifest.schema.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("manifest.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(manifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("generated manifest does not match schema: %v", err)
	}
}

func exportTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
