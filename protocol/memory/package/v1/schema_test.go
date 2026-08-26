package memorypackagev1_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/lkarlslund/koder/internal/memory"
	memorypackagev1 "github.com/lkarlslund/koder/protocol/memory/package/v1"
)

type exampleManifest struct {
	Chunk struct {
		ID string `json:"id"`
	} `json:"chunk"`
	Content struct {
		Entries struct {
			Count int `json:"count"`
		} `json:"entries"`
		Edges struct {
			Count int `json:"count"`
		} `json:"edges"`
		Sources struct {
			Count int `json:"count"`
		} `json:"sources"`
		Assets struct {
			Count int `json:"count"`
		} `json:"assets"`
	} `json:"content"`
	Files []struct {
		Path   string `json:"path"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

func TestEmbeddedManifestSchemaMatchesPublishedFileAndIsCopied(t *testing.T) {
	t.Parallel()
	want := readPackageTestFile(t, "manifest.schema.json")
	got := memorypackagev1.ManifestSchema()
	if !bytes.Equal(got, want) {
		t.Fatal("embedded manifest schema differs from the published file")
	}
	got[0] ^= 0xff
	if bytes.Equal(got, memorypackagev1.ManifestSchema()) {
		t.Fatal("ManifestSchema returned mutable embedded storage")
	}
}

func TestCanonicalPackageExample(t *testing.T) {
	t.Parallel()
	root := "examples/linux-partition-tools"
	manifestData := readPackageTestFile(t, filepath.Join(root, "manifest.json"))
	schemaData := readPackageTestFile(t, "manifest.schema.json")
	assertCanonicalJSON(t, "manifest.json", manifestData, true)

	schemaDocument, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	if err != nil {
		t.Fatalf("parse manifest schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("manifest.schema.json", schemaDocument); err != nil {
		t.Fatalf("load manifest schema: %v", err)
	}
	schema, err := compiler.Compile("manifest.schema.json")
	if err != nil {
		t.Fatalf("compile manifest schema: %v", err)
	}
	manifestDocument, err := jsonschema.UnmarshalJSON(bytes.NewReader(manifestData))
	if err != nil {
		t.Fatalf("parse example manifest: %v", err)
	}
	if err := schema.Validate(manifestDocument); err != nil {
		t.Fatalf("validate example manifest: %v", err)
	}

	var manifest exampleManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode example manifest: %v", err)
	}
	paths := make([]string, len(manifest.Files))
	declared := make(map[string]struct{}, len(manifest.Files))
	for index, file := range manifest.Files {
		paths[index] = file.Path
		if _, exists := declared[file.Path]; exists {
			t.Fatalf("duplicate manifest file %q", file.Path)
		}
		declared[file.Path] = struct{}{}
		data := readPackageTestFile(t, filepath.Join(root, filepath.FromSlash(file.Path)))
		if int64(len(data)) != file.Size {
			t.Errorf("%s size = %d, want %d", file.Path, len(data), file.Size)
		}
		digest := sha256.Sum256(data)
		if got := hex.EncodeToString(digest[:]); got != file.SHA256 {
			t.Errorf("%s sha256 = %s, want %s", file.Path, got, file.SHA256)
		}
	}
	if !slices.IsSorted(paths) {
		t.Errorf("manifest files are not bytewise path sorted: %v", paths)
	}

	actual := packagePayloadPaths(t, root)
	if !slices.Equal(paths, actual) {
		t.Fatalf("manifest payload inventory = %v, actual files = %v", paths, actual)
	}

	entryPaths, err := filepath.Glob(filepath.Join(root, "entries", "*.md"))
	if err != nil {
		t.Fatalf("list example entries: %v", err)
	}
	for _, path := range entryPaths {
		entry := parsePackageEntry(t, path)
		if err := entry.Validate(); err != nil {
			t.Errorf("validate %s: %v", path, err)
		}
		if string(entry.ChunkID) != manifest.Chunk.ID {
			t.Errorf("%s chunk_id = %s, want %s", path, entry.ChunkID, manifest.Chunk.ID)
		}
		if string(entry.ID)+".md" != filepath.Base(path) {
			t.Errorf("%s ID does not match filename", path)
		}
	}
	if len(entryPaths) != manifest.Content.Entries.Count {
		t.Errorf("entry count = %d, want %d", len(entryPaths), manifest.Content.Entries.Count)
	}
	if got := validateJSONLines[memory.Link](t, filepath.Join(root, "edges.jsonl")); got != manifest.Content.Edges.Count {
		t.Errorf("edge count = %d, want %d", got, manifest.Content.Edges.Count)
	}
	if got := validateJSONLines[memory.Evidence](t, filepath.Join(root, "sources.jsonl")); got != manifest.Content.Sources.Count {
		t.Errorf("source count = %d, want %d", got, manifest.Content.Sources.Count)
	}
	if manifest.Content.Assets.Count != 0 {
		t.Errorf("canonical example asset count = %d, want 0", manifest.Content.Assets.Count)
	}
}

func readPackageTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func packagePayloadPaths(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || path == filepath.Join(root, "manifest.json") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatalf("walk package example: %v", err)
	}
	slices.Sort(paths)
	return paths
}

func parsePackageEntry(t *testing.T, path string) memory.Entry {
	t.Helper()
	data := readPackageTestFile(t, path)
	const opening = "---\n"
	if !bytes.HasPrefix(data, []byte(opening)) {
		t.Fatalf("%s has no JSON front matter", path)
	}
	parts := bytes.SplitN(data[len(opening):], []byte("\n---\n"), 2)
	if len(parts) != 2 {
		t.Fatalf("%s has no closing front matter delimiter", path)
	}
	var entry memory.Entry
	if err := json.Unmarshal(parts[0], &entry); err != nil {
		t.Fatalf("decode %s front matter: %v", path, err)
	}
	assertCanonicalJSON(t, path+" front matter", parts[0], false)
	entry.Body = string(parts[1])
	return entry
}

func validateJSONLines[T interface{ Validate() error }](t *testing.T, path string) int {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	data := readPackageTestFile(t, path)
	if len(data) > 0 && !bytes.HasSuffix(data, []byte{'\n'}) {
		t.Fatalf("%s has no final newline", path)
	}
	count := 0
	var priorID string
	for scanner.Scan() {
		count++
		line := scanner.Bytes()
		assertCanonicalJSON(t, path, line, false)
		var id struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(line, &id); err != nil {
			t.Fatalf("decode %s line %d identity: %v", path, count, err)
		}
		if strings.Compare(priorID, id.ID) >= 0 {
			t.Fatalf("%s records are not uniquely ID-sorted at %q", path, id.ID)
		}
		priorID = id.ID
		var value T
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatalf("decode %s line %d: %v", path, count, err)
		}
		if err := value.Validate(); err != nil {
			t.Errorf("validate %s line %d: %v", path, count, err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	if count == 0 && len(data) != 0 {
		t.Fatalf("%s has bytes but no JSON Lines records", path)
	}
	return count
}

func assertCanonicalJSON(t *testing.T, name string, data []byte, indent bool) {
	t.Helper()
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode canonical JSON %s: %v", name, err)
	}
	var canonical []byte
	var err error
	if indent {
		canonical, err = json.MarshalIndent(document, "", "  ")
		canonical = append(canonical, '\n')
	} else {
		canonical, err = json.Marshal(document)
	}
	if err != nil {
		t.Fatalf("encode canonical JSON %s: %v", name, err)
	}
	if !bytes.Equal(data, canonical) {
		t.Errorf("%s is not canonical JSON\ngot:\n%s\nwant:\n%s", name, data, canonical)
	}
}
