package attachment

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	SourceClipboardImage = "clipboard_image"
	SourceFileImport     = "file_import"
)

type Metadata struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MIME     string `json:"mime"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Source   string `json:"source,omitempty"`
	Original string `json:"original,omitempty"`
}

type Draft struct {
	Metadata
}

type Manager struct {
	root string
}

func NewManager(stateDir string) *Manager {
	if strings.TrimSpace(stateDir) == "" {
		stateDir = filepath.Join(os.TempDir(), "koder")
	}
	return &Manager{root: filepath.Join(stateDir, "attachments")}
}

func (m *Manager) DraftsDir() string {
	return filepath.Join(m.root, "drafts")
}

func (m *Manager) SessionDir(sessionID int64) string {
	return filepath.Join(m.root, "sessions", fmt.Sprintf("%d", sessionID))
}

func (m *Manager) ImportClipboardImage(png []byte) (Draft, error) {
	if len(png) == 0 {
		return Draft{}, fmt.Errorf("clipboard image is empty")
	}
	id, err := newID()
	if err != nil {
		return Draft{}, err
	}
	name := "clipboard.png"
	dst := filepath.Join(m.DraftsDir(), id+".png")
	if err := writeFile(dst, png); err != nil {
		return Draft{}, err
	}
	return Draft{Metadata: Metadata{
		ID:     id,
		Name:   name,
		MIME:   "image/png",
		Path:   dst,
		Size:   int64(len(png)),
		Source: SourceClipboardImage,
	}}, nil
}

func (m *Manager) ImportFile(path string) (Draft, error) {
	src := strings.TrimSpace(path)
	if src == "" {
		return Draft{}, fmt.Errorf("attachment path is empty")
	}
	info, err := os.Stat(src)
	if err != nil {
		return Draft{}, fmt.Errorf("stat attachment: %w", err)
	}
	if info.IsDir() {
		return Draft{}, fmt.Errorf("attachment path %q is a directory", src)
	}
	kind, mimeType, err := classifyFile(src)
	if err != nil {
		return Draft{}, err
	}
	if kind == KindUnsupported {
		return Draft{}, fmt.Errorf("unsupported attachment type %q", mimeType)
	}
	id, err := newID()
	if err != nil {
		return Draft{}, err
	}
	ext := filepath.Ext(src)
	dst := filepath.Join(m.DraftsDir(), id+ext)
	size, err := copyFile(src, dst)
	if err != nil {
		return Draft{}, err
	}
	return Draft{Metadata: Metadata{
		ID:       id,
		Name:     filepath.Base(src),
		MIME:     mimeType,
		Path:     dst,
		Size:     size,
		Source:   SourceFileImport,
		Original: src,
	}}, nil
}

func (m *Manager) AdoptDraft(draft Draft, sessionID int64) (Metadata, error) {
	if strings.TrimSpace(draft.Path) == "" {
		return Metadata{}, fmt.Errorf("draft attachment path is empty")
	}
	id, err := newID()
	if err != nil {
		return Metadata{}, err
	}
	dst := filepath.Join(m.SessionDir(sessionID), id+filepath.Ext(draft.Path))
	size, err := copyFile(draft.Path, dst)
	if err != nil {
		return Metadata{}, err
	}
	return Metadata{
		ID:       id,
		Name:     draft.Name,
		MIME:     draft.MIME,
		Path:     dst,
		Size:     size,
		Source:   draft.Source,
		Original: draft.Original,
	}, nil
}

func (m *Manager) CopyToSession(meta Metadata, sessionID int64) (Metadata, error) {
	if strings.TrimSpace(meta.Path) == "" {
		return Metadata{}, fmt.Errorf("attachment path is empty")
	}
	id, err := newID()
	if err != nil {
		return Metadata{}, err
	}
	dst := filepath.Join(m.SessionDir(sessionID), id+filepath.Ext(meta.Path))
	size, err := copyFile(meta.Path, dst)
	if err != nil {
		return Metadata{}, err
	}
	meta.ID = id
	meta.Path = dst
	meta.Size = size
	return meta, nil
}

func (m *Manager) ReadText(meta Metadata) (string, error) {
	if ClassifyMIME(meta.MIME) != KindText {
		return "", fmt.Errorf("attachment %q is not text", meta.Name)
	}
	buf, err := os.ReadFile(meta.Path)
	if err != nil {
		return "", fmt.Errorf("read attachment %q: %w", meta.Name, err)
	}
	return string(buf), nil
}

func (m *Manager) ReadBytes(meta Metadata) ([]byte, error) {
	buf, err := os.ReadFile(meta.Path)
	if err != nil {
		return nil, fmt.Errorf("read attachment %q: %w", meta.Name, err)
	}
	return buf, nil
}

func EncodeMeta(meta Metadata) (string, error) {
	buf, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("marshal attachment metadata: %w", err)
	}
	return string(buf), nil
}

func DecodeMeta(raw string) (Metadata, error) {
	var meta Metadata
	if strings.TrimSpace(raw) == "" {
		return meta, fmt.Errorf("attachment metadata is empty")
	}
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return meta, fmt.Errorf("decode attachment metadata: %w", err)
	}
	if strings.TrimSpace(meta.Path) == "" || strings.TrimSpace(meta.MIME) == "" {
		return meta, fmt.Errorf("attachment metadata is incomplete")
	}
	return meta, nil
}

type Kind string

const (
	KindImage       Kind = "image"
	KindPDF         Kind = "pdf"
	KindText        Kind = "text"
	KindUnsupported Kind = "unsupported"
)

func ClassifyMIME(mimeType string) Kind {
	normalized := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	switch {
	case strings.HasPrefix(normalized, "image/"):
		return KindImage
	case normalized == "application/pdf":
		return KindPDF
	case strings.HasPrefix(normalized, "text/"):
		return KindText
	case normalized == "application/json",
		normalized == "application/xml",
		normalized == "application/javascript",
		normalized == "application/x-yaml",
		normalized == "application/yaml":
		return KindText
	default:
		return KindUnsupported
	}
}

func classifyFile(path string) (Kind, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return KindUnsupported, "", fmt.Errorf("open attachment: %w", err)
	}
	defer file.Close()

	var sniff [512]byte
	n, err := file.Read(sniff[:])
	if err != nil && err != io.EOF {
		return KindUnsupported, "", fmt.Errorf("read attachment header: %w", err)
	}
	mimeType := http.DetectContentType(sniff[:n])
	if mimeType == "application/octet-stream" {
		if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); byExt != "" {
			mimeType = byExt
		}
	}
	kind := ClassifyMIME(mimeType)
	if kind == KindUnsupported {
		return kind, mimeType, fmt.Errorf("unsupported attachment type %q", mimeType)
	}
	return kind, mimeType, nil
}

func newID() (string, error) {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate attachment id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create attachment dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write attachment: %w", err)
	}
	return nil
}

func copyFile(src, dst string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, fmt.Errorf("create attachment dir: %w", err)
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open attachment source: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return 0, fmt.Errorf("create attachment destination: %w", err)
	}
	defer out.Close()

	size, err := io.Copy(out, in)
	if err != nil {
		return 0, fmt.Errorf("copy attachment: %w", err)
	}
	if err := out.Close(); err != nil {
		return 0, fmt.Errorf("close attachment destination: %w", err)
	}
	return size, nil
}
