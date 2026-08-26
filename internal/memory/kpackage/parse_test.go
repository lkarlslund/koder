package kpackage

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
)

func TestParseReadsBoundedExportAndReturnsCopies(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	exported, err := Export(&archive, canonicalExampleRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseBytes(archive.Bytes(), ParseLimits{})
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if parsed.Manifest.Package != exported.Manifest.Package || parsed.Manifest.Chunk.ID != exported.Manifest.Chunk.ID {
		t.Fatalf("parsed manifest = %#v", parsed.Manifest)
	}
	wantPaths := []string{
		"manifest.json", "edges.jsonl",
		"entries/01a02b00-0000-7000-8000-000000000002.md",
		"entries/01a02b00-0000-7000-8000-000000000003.md",
		"sources.jsonl",
	}
	if got := parsed.Paths(); !slices.Equal(got, wantPaths) {
		t.Fatalf("Paths() = %v, want %v", got, wantPaths)
	}
	manifest := parsed.ManifestBytes()
	manifest[0] ^= 0xff
	if bytes.Equal(manifest, parsed.ManifestBytes()) {
		t.Fatal("ManifestBytes returned mutable internal storage")
	}
	entry, exists := parsed.ReadFile(wantPaths[2])
	if !exists || len(entry) == 0 {
		t.Fatalf("ReadFile(%q) exists=%v bytes=%d", wantPaths[2], exists, len(entry))
	}
	entry[0] ^= 0xff
	again, _ := parsed.ReadFile(wantPaths[2])
	if bytes.Equal(entry, again) {
		t.Fatal("ReadFile returned mutable internal storage")
	}
	if _, exists := parsed.ReadFile("missing"); exists {
		t.Fatal("ReadFile reported a missing path")
	}
}

func TestParseAcceptsStoreAndDeflateRegularFiles(t *testing.T) {
	t.Parallel()
	archive := makeTestZIP(t, []testZIPEntry{
		{name: "manifest.json", data: []byte("{}\n"), method: zip.Store},
		{name: "assets/compressed.txt", data: []byte(strings.Repeat("compressible", 100)), method: zip.Deflate},
	})
	parsed, err := ParseBytes(archive, ParseLimits{MaxArchiveBytes: int64(len(archive)), MaxUncompressedBytes: 4096, MaxFileBytes: 2048, MaxFiles: 2, MaxPathBytes: 64, MaxPathDepth: 3})
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if data, exists := parsed.ReadFile("assets/compressed.txt"); !exists || len(data) != 1200 {
		t.Fatalf("compressed data exists=%v length=%d", exists, len(data))
	}
}

func TestParseRejectsUnsafePathsAndNonRegularEntries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		entries []testZIPEntry
		want    string
	}{
		{name: "manifest not first", entries: []testZIPEntry{{name: "edges.jsonl"}, {name: "manifest.json", data: []byte("{}")}}, want: "must be the first"},
		{name: "traversal", entries: regularArchiveEntry("../secret"), want: "unsafe ZIP path"},
		{name: "absolute", entries: regularArchiveEntry("/secret"), want: "unsafe ZIP path"},
		{name: "backslash", entries: regularArchiveEntry(`assets\secret`), want: "unsafe ZIP path"},
		{name: "drive path", entries: regularArchiveEntry("C:/secret"), want: "unsafe ZIP path"},
		{name: "control", entries: regularArchiveEntry("assets/bad\x01name"), want: "unsafe ZIP path"},
		{name: "directory", entries: append(manifestEntry(), testZIPEntry{name: "assets/directory", mode: os.ModeDir | 0o755}), want: "not a regular file"},
		{name: "symlink", entries: append(manifestEntry(), testZIPEntry{name: "assets/link", data: []byte("../../secret"), mode: os.ModeSymlink | 0o777}), want: "not a regular file"},
		{name: "encrypted", entries: append(manifestEntry(), testZIPEntry{name: "assets/secret", data: []byte("secret"), flags: 0x1}), want: "encrypted entry"},
		{name: "duplicate", entries: append(manifestEntry(), testZIPEntry{name: "assets/same"}, testZIPEntry{name: "assets/same"}), want: "duplicate normalized path"},
		{name: "unicode duplicate", entries: append(manifestEntry(), testZIPEntry{name: "assets/e\u0301"}, testZIPEntry{name: "assets/é"}), want: "duplicate normalized path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			archive := makeTestZIP(t, test.entries)
			_, err := ParseBytes(archive, ParseLimits{})
			if err == nil || !errors.Is(err, ErrInvalidArchive) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseBytes() error = %v, want invalid archive containing %q", err, test.want)
			}
		})
	}
}

func TestParseEnforcesEveryLimit(t *testing.T) {
	t.Parallel()
	archive := makeTestZIP(t, append(manifestEntry(),
		testZIPEntry{name: "assets/one", data: []byte("12345")},
		testZIPEntry{name: "assets/two", data: []byte("67890")},
	))
	tests := []struct {
		name   string
		limits ParseLimits
	}{
		{name: "archive bytes", limits: ParseLimits{MaxArchiveBytes: int64(len(archive) - 1)}},
		{name: "total bytes", limits: ParseLimits{MaxUncompressedBytes: 10, MaxFileBytes: 5}},
		{name: "file bytes", limits: ParseLimits{MaxUncompressedBytes: 100, MaxFileBytes: 4}},
		{name: "files", limits: ParseLimits{MaxFiles: 2}},
		{name: "path bytes", limits: ParseLimits{MaxPathBytes: 10}},
		{name: "path depth", limits: ParseLimits{MaxPathDepth: 1}},
		{name: "above hard bound", limits: ParseLimits{MaxFiles: HardMaxFiles + 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseBytes(archive, test.limits)
			if !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("ParseBytes() error = %v, want limit exceeded", err)
			}
		})
	}
}

func TestParseRejectsCorruptAndAmbiguousManifests(t *testing.T) {
	t.Parallel()
	t.Run("invalid JSON", func(t *testing.T) {
		archive := makeTestZIP(t, []testZIPEntry{{name: "manifest.json", data: []byte("not JSON")}})
		if _, err := ParseBytes(archive, ParseLimits{}); !errors.Is(err, ErrInvalidArchive) {
			t.Fatalf("ParseBytes() error = %v", err)
		}
	})
	t.Run("multiple JSON values", func(t *testing.T) {
		archive := makeTestZIP(t, []testZIPEntry{{name: "manifest.json", data: []byte("{}\n{}\n")}})
		if _, err := ParseBytes(archive, ParseLimits{}); !errors.Is(err, ErrInvalidArchive) || !strings.Contains(err.Error(), "multiple") {
			t.Fatalf("ParseBytes() error = %v", err)
		}
	})
	t.Run("CRC mismatch", func(t *testing.T) {
		archive := makeTestZIP(t, []testZIPEntry{{name: "manifest.json", data: []byte("{}\n")}})
		position := bytes.Index(archive, []byte("{}\n"))
		if position < 0 {
			t.Fatal("manifest payload not found")
		}
		archive[position] ^= 0x01
		if _, err := ParseBytes(archive, ParseLimits{}); !errors.Is(err, ErrInvalidArchive) {
			t.Fatalf("ParseBytes() error = %v", err)
		}
	})
	t.Run("empty ZIP", func(t *testing.T) {
		archive := makeTestZIP(t, nil)
		if _, err := ParseBytes(archive, ParseLimits{}); err == nil {
			t.Fatal("empty ZIP was accepted")
		}
	})
	t.Run("not ZIP", func(t *testing.T) {
		if _, err := ParseBytes([]byte("not a zip"), ParseLimits{}); !errors.Is(err, ErrInvalidArchive) {
			t.Fatalf("ParseBytes() error = %v", err)
		}
	})
}

func FuzzParseNeverPanics(f *testing.F) {
	f.Add([]byte("not a zip"))
	f.Add(makeTestZIP(f, manifestEntry()))
	limits := ParseLimits{
		MaxArchiveBytes: 1 << 20, MaxUncompressedBytes: 1 << 20, MaxFileBytes: 64 << 10,
		MaxFiles: 128, MaxPathBytes: 256, MaxPathDepth: 8,
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseBytes(data, limits)
	})
}

func FuzzValidateArchivePathCannotEscape(f *testing.F) {
	f.Add("assets/reference.txt")
	f.Add("../escape")
	f.Add("C:\\escape")
	f.Add("/absolute")
	limits, err := normalizeParseLimits(ParseLimits{})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, name string) {
		normalized, err := validateArchivePath(name, limits)
		if err != nil {
			return
		}
		if normalized == "" || path.IsAbs(normalized) || path.Clean(normalized) != normalized || strings.Contains(normalized, "\\") {
			t.Fatalf("validateArchivePath(%q) returned unsafe path %q", name, normalized)
		}
		for _, component := range strings.Split(normalized, "/") {
			if component == "" || component == "." || component == ".." {
				t.Fatalf("validateArchivePath(%q) returned unsafe component in %q", name, normalized)
			}
		}
	})
}

type testZIPEntry struct {
	name   string
	data   []byte
	method uint16
	mode   os.FileMode
	flags  uint16
	date   uint16
	clock  uint16
}

func manifestEntry() []testZIPEntry {
	return []testZIPEntry{{name: "manifest.json", data: []byte("{}\n")}}
}

func regularArchiveEntry(name string) []testZIPEntry {
	return append(manifestEntry(), testZIPEntry{name: name, data: []byte("data")})
}

func makeTestZIP(t testing.TB, entries []testZIPEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, item := range entries {
		header := &zip.FileHeader{Name: item.name, Method: item.method, Flags: item.flags, ModifiedDate: item.date, ModifiedTime: item.clock} //nolint:staticcheck // Malformed v1 fixtures must control the raw DOS fields.
		if item.mode != 0 {
			header.SetMode(item.mode)
		}
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatalf("create ZIP entry %q: %v", item.name, err)
		}
		if _, err := writer.Write(item.data); err != nil {
			t.Fatalf("write ZIP entry %q: %v", item.name, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close ZIP: %v", err)
	}
	return output.Bytes()
}
