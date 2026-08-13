package attachment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/id"
)

func TestImportSessionData(t *testing.T) {
	m := NewManager(t.TempDir())
	meta, err := m.ImportSessionData(id.ID("session-1"), []byte("hello"), "result.txt", "text/plain", SourceBrowser)
	if err != nil {
		t.Fatalf("import session data: %v", err)
	}
	if meta.Source != SourceBrowser || meta.Size != 5 || filepath.Base(meta.Path) == "result.txt" {
		t.Fatalf("unexpected metadata: %#v", meta)
	}
	got, err := os.ReadFile(meta.Path)
	if err != nil || string(got) != "hello" {
		t.Fatalf("read imported attachment: %q, %v", got, err)
	}
}

func TestImportSessionFileCopiesMedia(t *testing.T) {
	m := NewManager(t.TempDir())
	source := filepath.Join(t.TempDir(), "demo.mp4")
	if err := os.WriteFile(source, []byte("video-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := m.ImportSessionFile("session-1", source, "video/mp4", "show_media")
	if err != nil {
		t.Fatal(err)
	}
	if meta.MIME != "video/mp4" || meta.Source != "show_media" || meta.Original != source || meta.Size != 10 {
		t.Fatalf("unexpected metadata: %#v", meta)
	}
	if got, err := os.ReadFile(meta.Path); err != nil || string(got) != "video-data" {
		t.Fatalf("read copied media: %q, %v", got, err)
	}
}

func TestClassifyMIMERecognizesBrowserMedia(t *testing.T) {
	if got := ClassifyMIME("audio/mpeg"); got != KindAudio {
		t.Fatalf("audio kind = %q", got)
	}
	if got := ClassifyMIME("video/mp4; codecs=avc1"); got != KindVideo {
		t.Fatalf("video kind = %q", got)
	}
}

func TestDeleteSessionData(t *testing.T) {
	m := NewManager(t.TempDir())
	if _, err := m.ImportSessionData("session-1", []byte("hello"), "result.txt", "text/plain", SourceBrowser); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteSessionData("session-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.SessionDir("session-1")); !os.IsNotExist(err) {
		t.Fatalf("expected session attachments removed, got %v", err)
	}
}

func TestImportClipboardImageAndAdoptDraft(t *testing.T) {
	manager := NewManager(t.TempDir())

	draft, err := manager.ImportClipboardImage([]byte("\x89PNG\r\n\x1a\nfake"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ClassifyMIME(draft.MIME); got != KindImage {
		t.Fatalf("unexpected draft kind: %s", got)
	}
	if _, err := os.Stat(draft.Path); err != nil {
		t.Fatalf("expected draft file to exist: %v", err)
	}

	adopted, err := manager.AdoptDraft(draft, "session-42")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(adopted.Path, filepath.Join("sessions", "session-42")) {
		t.Fatalf("expected adopted path to be under session dir, got %q", adopted.Path)
	}
	if _, err := os.Stat(adopted.Path); err != nil {
		t.Fatalf("expected adopted file to exist: %v", err)
	}
}

func TestImportClipboardImageDataUsesPastedNameAndMIME(t *testing.T) {
	manager := NewManager(t.TempDir())

	draft, err := manager.ImportClipboardImageData([]byte("\x89PNG\r\n\x1a\nfake"), "pasted-image", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Name != "pasted-image" || draft.MIME != "image/png" || filepath.Ext(draft.Path) != ".png" {
		t.Fatalf("unexpected draft: %#v", draft)
	}
	validated, err := manager.ValidateDraft(draft)
	if err != nil {
		t.Fatal(err)
	}
	if validated.Size != int64(len("\x89PNG\r\n\x1a\nfake")) {
		t.Fatalf("unexpected validated size: %d", validated.Size)
	}
}

func TestValidateDraftRejectsPathOutsideDrafts(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	outside := filepath.Join(root, "outside.png")
	if err := os.WriteFile(outside, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := manager.ValidateDraft(Draft{Metadata: Metadata{
		ID:   "outside",
		Name: "outside.png",
		MIME: "image/png",
		Path: outside,
	}})
	if err == nil {
		t.Fatal("expected outside draft path to be rejected")
	}
}

func TestImportFileAndReadText(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	src := filepath.Join(root, "note.txt")
	if err := os.WriteFile(src, []byte("hello attachments"), 0o644); err != nil {
		t.Fatal(err)
	}

	draft, err := manager.ImportFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := ClassifyMIME(draft.MIME); got != KindText {
		t.Fatalf("unexpected draft kind: %s", got)
	}
	adopted, err := manager.AdoptDraft(draft, "session-7")
	if err != nil {
		t.Fatal(err)
	}
	body, err := manager.ReadText(adopted)
	if err != nil {
		t.Fatal(err)
	}
	if body != "hello attachments" {
		t.Fatalf("unexpected text attachment body: %q", body)
	}
}

func TestCopyToSession(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	src := filepath.Join(root, "doc.txt")
	if err := os.WriteFile(src, []byte("copy me"), 0o644); err != nil {
		t.Fatal(err)
	}
	draft, err := manager.ImportFile(src)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := manager.AdoptDraft(draft, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	copied, err := manager.CopyToSession(meta, "session-2")
	if err != nil {
		t.Fatal(err)
	}
	if copied.Path == meta.Path {
		t.Fatal("expected copied attachment to have a distinct path")
	}
	if !strings.Contains(copied.Path, filepath.Join("sessions", "session-2")) {
		t.Fatalf("expected copied attachment to be under session 2, got %q", copied.Path)
	}
}

func TestEncodeDecodeMeta(t *testing.T) {
	meta := Metadata{
		ID:       "abc123",
		Name:     "note.txt",
		MIME:     "text/plain; charset=utf-8",
		Path:     "/tmp/note.txt",
		Size:     9,
		Source:   SourceFileImport,
		Original: "/home/user/note.txt",
	}
	raw, err := EncodeMeta(meta)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMeta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Path != meta.Path || decoded.Name != meta.Name || decoded.Original != meta.Original {
		t.Fatalf("unexpected decoded metadata: %#v", decoded)
	}
}

func TestImportFileRejectsUnsupportedType(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	src := filepath.Join(root, "archive.bin")
	if err := os.WriteFile(src, []byte{0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ImportFile(src); err == nil {
		t.Fatal("expected unsupported attachment type to be rejected")
	}
}
