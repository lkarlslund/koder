package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lkarlslund/koder/internal/id"
)

const (
	fileBrowserAssetPath     = "assets/files.html"
	maxFileTreeEntries       = 2000
	maxFileBrowserFileBytes  = 2 << 20
	fileBrowserBinaryPreview = 8192
)

var fileBrowserHTML = mustReadAsset(fileBrowserAssetPath)

type fileTreeEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size,omitempty"`
	Modified string `json:"modified,omitempty"`
}

type fileTreeResponse struct {
	SessionID   id.ID           `json:"session_id"`
	ProjectRoot string          `json:"project_root"`
	Path        string          `json:"path"`
	Entries     []fileTreeEntry `json:"entries"`
	Truncated   bool            `json:"truncated,omitempty"`
}

type fileContentResponse struct {
	SessionID   id.ID  `json:"session_id"`
	ProjectRoot string `json:"project_root"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	Language    string `json:"language,omitempty"`
	MIME        string `json:"mime,omitempty"`
	Size        int64  `json:"size"`
	Modified    string `json:"modified,omitempty"`
	Content     string `json:"content,omitempty"`
	Binary      bool   `json:"binary,omitempty"`
	Image       bool   `json:"image,omitempty"`
	TooLarge    bool   `json:"too_large,omitempty"`
	Markdown    bool   `json:"markdown,omitempty"`
}

func renderFileBrowserHTML() string {
	return strings.ReplaceAll(fileBrowserHTML, assetHashPlaceholder, currentAssetHash)
}

func fileBrowserSessionFromPath(rawPath string) (id.ID, bool) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(rawPath), "/"), "/")
	if len(parts) != 3 || parts[0] != "s" || parts[2] != "files" || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	return id.ID(strings.TrimSpace(parts[1])), true
}

func (s *Server) handleSessionFilesAPI(w http.ResponseWriter, r *http.Request, sessionID id.ID, parts []string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	switch parts[0] {
	case "tree":
		s.handleSessionFileTree(w, r, sessionID)
	case "read":
		s.handleSessionFileRead(w, r, sessionID)
	case "raw":
		s.handleSessionFileRaw(w, r, sessionID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSessionFileTree(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	root, rel, full, err := s.sessionFilePath(r.Context(), sessionID, r.URL.Query().Get("path"))
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	info, err := os.Stat(full)
	if err != nil {
		writeFileBrowserError(w, fmt.Errorf("stat path: %w", err))
		return
	}
	if !info.IsDir() {
		http.Error(w, "path is not a directory", http.StatusBadRequest)
		return
	}
	entries, truncated, err := readFileTreeEntries(full, rel)
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	writeFileBrowserJSON(w, r, fileTreeResponse{
		SessionID:   sessionID,
		ProjectRoot: root,
		Path:        rel,
		Entries:     entries,
		Truncated:   truncated,
	})
}

func (s *Server) handleSessionFileRead(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	root, rel, full, err := s.sessionFilePath(r.Context(), sessionID, r.URL.Query().Get("path"))
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	info, err := os.Stat(full)
	if err != nil {
		writeFileBrowserError(w, fmt.Errorf("stat path: %w", err))
		return
	}
	if info.IsDir() {
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return
	}
	resp := fileContentResponse{
		SessionID:   sessionID,
		ProjectRoot: root,
		Path:        rel,
		Name:        info.Name(),
		Language:    languageForFile(rel),
		Size:        info.Size(),
		Modified:    info.ModTime().Format(timeFormatRFC3339()),
		Markdown:    isMarkdownFile(rel),
	}
	mimeType, err := detectFileBrowserMIME(full, rel)
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	resp.MIME = mimeType
	resp.Image = isFileBrowserImageMIME(mimeType)
	if resp.Image {
		writeFileBrowserJSON(w, r, resp)
		return
	}
	if info.Size() > maxFileBrowserFileBytes {
		resp.TooLarge = true
		writeFileBrowserJSON(w, r, resp)
		return
	}
	data, err := os.ReadFile(full)
	if err != nil {
		writeFileBrowserError(w, fmt.Errorf("read file: %w", err))
		return
	}
	if looksBinary(data) {
		resp.Binary = true
		writeFileBrowserJSON(w, r, resp)
		return
	}
	resp.Content = string(data)
	writeFileBrowserJSON(w, r, resp)
}

func (s *Server) handleSessionFileRaw(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	_, rel, full, err := s.sessionFilePath(r.Context(), sessionID, r.URL.Query().Get("path"))
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	info, err := os.Stat(full)
	if err != nil {
		writeFileBrowserError(w, fmt.Errorf("stat path: %w", err))
		return
	}
	if info.IsDir() {
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return
	}
	mimeType, err := detectFileBrowserMIME(full, rel)
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	if !isFileBrowserImageMIME(mimeType) {
		http.Error(w, "file is not a supported image", http.StatusBadRequest)
		return
	}
	file, err := os.Open(full)
	if err != nil {
		writeFileBrowserError(w, fmt.Errorf("open file: %w", err))
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (s *Server) sessionFilePath(ctx context.Context, sessionID id.ID, rawRel string) (string, string, string, error) {
	session, err := s.controller.SessionByID(ctx, sessionID)
	if err != nil {
		return "", "", "", err
	}
	root := strings.TrimSpace(session.ProjectRoot)
	if root == "" {
		return "", "", "", fmt.Errorf("session has no project root")
	}
	rel, err := cleanFileBrowserPath(rawRel)
	if err != nil {
		return "", "", "", err
	}
	full, err := resolveFileBrowserPath(root, rel)
	if err != nil {
		return "", "", "", err
	}
	return root, rel, full, nil
}

func cleanFileBrowserPath(raw string) (string, error) {
	value := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	value = strings.TrimPrefix(value, "/")
	if value == "" {
		return "", nil
	}
	clean := path.Clean(value)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes project root")
	}
	return clean, nil
}

func resolveFileBrowserPath(root, rel string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	fullAbs, err := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if evaluated, err := filepath.EvalSymlinks(fullAbs); err == nil {
		fullAbs = evaluated
	}
	rootEval := rootAbs
	if evaluated, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootEval = evaluated
	}
	relative, err := filepath.Rel(rootEval, fullAbs)
	if err != nil {
		return "", fmt.Errorf("resolve relative path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("path escapes project root")
	}
	return fullAbs, nil
}

func readFileTreeEntries(dir, rel string) ([]fileTreeEntry, bool, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false, fmt.Errorf("read directory: %w", err)
	}
	truncated := len(dirEntries) > maxFileTreeEntries
	if truncated {
		dirEntries = dirEntries[:maxFileTreeEntries]
	}
	entries := make([]fileTreeEntry, 0, len(dirEntries))
	for _, entry := range dirEntries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		entryPath := entry.Name()
		if rel != "" {
			entryPath = rel + "/" + entry.Name()
		}
		entries = append(entries, fileTreeEntry{
			Name:     entry.Name(),
			Path:     entryPath,
			Dir:      entry.IsDir(),
			Size:     info.Size(),
			Modified: info.ModTime().Format(timeFormatRFC3339()),
		})
	}
	slices.SortFunc(entries, func(a, b fileTreeEntry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return entries, truncated, nil
}

func looksBinary(data []byte) bool {
	if len(data) > fileBrowserBinaryPreview {
		data = data[:fileBrowserBinaryPreview]
	}
	if len(data) == 0 {
		return false
	}
	if strings.Contains(string(data), "\x00") {
		return true
	}
	return !utf8.Valid(data)
}

func detectFileBrowserMIME(full, rel string) (string, error) {
	extMIME := strings.TrimSpace(mime.TypeByExtension(strings.ToLower(filepath.Ext(rel))))
	if isFileBrowserImageMIME(extMIME) {
		return extMIME, nil
	}
	file, err := os.Open(full)
	if err != nil {
		return extMIME, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()
	var sniff [512]byte
	n, err := file.Read(sniff[:])
	if err != nil && n == 0 {
		return extMIME, nil
	}
	detected := http.DetectContentType(sniff[:n])
	if detected == "application/octet-stream" && extMIME != "" {
		return extMIME, nil
	}
	if detected != "" {
		return detected, nil
	}
	return extMIME, nil
}

func isFileBrowserImageMIME(mimeType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	return strings.HasPrefix(normalized, "image/")
}

func isMarkdownFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	default:
		return false
	}
}

func languageForFile(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".jsx":
		return "javascript"
	case ".json":
		return "json"
	case ".md", ".markdown", ".mdown", ".mkd":
		return "markdown"
	case ".html", ".htm":
		return "xml"
	case ".css":
		return "css"
	case ".sh", ".bash", ".zsh":
		return "bash"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hh", ".hpp":
		return "cpp"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".xml":
		return "xml"
	case ".sql":
		return "sql"
	default:
		return ""
	}
}

func writeFileBrowserJSON(w http.ResponseWriter, r *http.Request, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func writeFileBrowserError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if os.IsNotExist(err) {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}

func timeFormatRFC3339() string {
	return "2006-01-02T15:04:05Z07:00"
}
