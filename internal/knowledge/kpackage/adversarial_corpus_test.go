package kpackage

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestAdversarialArchiveCorpus(t *testing.T) {
	t.Parallel()
	canonical := exportedArchiveBytes(t, canonicalExampleRequest(t))
	central := bytes.Index(canonical, []byte{'P', 'K', 1, 2})
	if central < 0 {
		t.Fatal("canonical fixture has no central directory")
	}
	hugeDeclaration := bytes.Clone(canonical)
	binary.LittleEndian.PutUint32(hugeDeclaration[central+24:central+28], ^uint32(0))
	corruptPayload := bytes.Clone(canonical)
	manifestPayload := bytes.Index(corruptPayload, []byte("{\n"))
	if manifestPayload < 0 {
		t.Fatal("canonical fixture has no manifest payload")
	}
	corruptPayload[manifestPayload] ^= 1

	cases := []struct {
		name   string
		data   []byte
		limits ParseLimits
		want   error
	}{
		{name: "not zip", data: []byte("not a zip"), want: ErrInvalidArchive},
		{name: "empty", data: nil, want: ErrInvalidArchive},
		{name: "truncated local header", data: canonical[:3], want: ErrInvalidArchive},
		{name: "truncated payload", data: canonical[:len(canonical)/2], want: ErrInvalidArchive},
		{name: "truncated end record", data: canonical[:len(canonical)-1], want: ErrInvalidArchive},
		{name: "polyglot prefix", data: append([]byte("#!/bin/sh\n"), canonical...), want: ErrInvalidArchive},
		{name: "trailing executable data", data: append(bytes.Clone(canonical), []byte("<script>alert(1)</script>")...), want: ErrInvalidArchive},
		{name: "corrupt payload", data: corruptPayload, want: ErrInvalidArchive},
		{name: "oversized declaration", data: hugeDeclaration, limits: ParseLimits{MaxArchiveBytes: int64(len(hugeDeclaration)), MaxUncompressedBytes: 1 << 20, MaxFileBytes: 1 << 20}, want: ErrLimitExceeded},
		{name: "duplicate path", data: makeTestZIP(t, append(manifestEntry(), testZIPEntry{name: "assets/same"}, testZIPEntry{name: "assets/same"})), want: ErrInvalidArchive},
		{name: "unicode duplicate", data: makeTestZIP(t, append(manifestEntry(), testZIPEntry{name: "assets/e\u0301"}, testZIPEntry{name: "assets/é"})), want: ErrInvalidArchive},
		{name: "traversal", data: makeTestZIP(t, regularArchiveEntry("../escape")), want: ErrInvalidArchive},
		{name: "absolute path", data: makeTestZIP(t, regularArchiveEntry("/escape")), want: ErrInvalidArchive},
		{name: "symlink", data: makeTestZIP(t, append(manifestEntry(), testZIPEntry{name: "assets/link", mode: os.ModeSymlink | 0o777})), want: ErrInvalidArchive},
		{name: "encrypted", data: makeTestZIP(t, append(manifestEntry(), testZIPEntry{name: "assets/secret", flags: 1})), want: ErrInvalidArchive},
		{name: "compression budget", data: makeTestZIP(t, append(manifestEntry(), testZIPEntry{name: "assets/bomb", method: zip.Deflate, data: []byte(strings.Repeat("A", 8192))})), limits: ParseLimits{MaxArchiveBytes: 1 << 20, MaxUncompressedBytes: 1024, MaxFileBytes: 1024}, want: ErrLimitExceeded},
		{name: "file count", data: makeTestZIP(t, append(manifestEntry(), testZIPEntry{name: "one"}, testZIPEntry{name: "two"})), limits: ParseLimits{MaxFiles: 2}, want: ErrLimitExceeded},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseBytes(test.data, test.limits)
			if !errors.Is(err, test.want) {
				t.Fatalf("ParseBytes() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestAdversarialArchiveCorpusRejectsZIPComments(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	archive.SetComment("hidden payload")
	writer, err := archive.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseBytes(output.Bytes(), ParseLimits{}); !errors.Is(err, ErrInvalidArchive) || !strings.Contains(err.Error(), "comments") {
		t.Fatalf("ParseBytes(comment) error = %v", err)
	}
}

func exportedArchiveBytes(t *testing.T, request ExportRequest) []byte {
	t.Helper()
	var output bytes.Buffer
	if _, err := Export(&output, request); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
