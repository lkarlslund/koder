package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lkarlslund/koder/internal/provider"
)

const agentsFileName = "AGENTS.md"

type FileInfo struct {
	Path         string    `json:"path"`
	Kind         string    `json:"kind"`
	Priority     int       `json:"priority"`
	ModTime      time.Time `json:"mod_time"`
	Checksum     string    `json:"checksum"`
	Size         int64     `json:"size"`
	DiscoveredBy string    `json:"discovered_by,omitempty"`
}

type Snapshot struct {
	CWD         string     `json:"cwd"`
	ProjectRoot string     `json:"project_root"`
	GlobalPath  string     `json:"global_path,omitempty"`
	Checksum    string     `json:"checksum"`
	Files       []FileInfo `json:"files"`
}

type Resolution struct {
	Snapshot        Snapshot  `json:"snapshot"`
	ResolvedAgents  string    `json:"resolved_agents"`
	ConflictSummary string    `json:"conflict_summary"`
	GeneratedAt     time.Time `json:"generated_at"`
}

type resolverResponse struct {
	ResolvedAgentsMD string `json:"resolved_agents_md"`
	ConflictSummary  string `json:"conflict_summary"`
}

type Manager struct {
	stateDir   string
	globalPath string
}

func NewManager(stateDir string, globalPath string) *Manager {
	return &Manager{
		stateDir:   strings.TrimSpace(stateDir),
		globalPath: strings.TrimSpace(globalPath),
	}
}

func NormalizeProjectRoot(projectRoot string) string {
	start := strings.TrimSpace(projectRoot)
	if start == "" {
		start = "."
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	return cleanPath(abs)
}

func (m *Manager) Discover(ctx context.Context, projectRoot string) (Snapshot, error) {
	return m.DiscoverProject(ctx, projectRoot, projectRoot)
}

// DiscoverProject discovers instructions without allowing project-root inference
// to expand beyond the root owned by the session.
func (m *Manager) DiscoverProject(ctx context.Context, projectRoot, cwd string) (Snapshot, error) {
	root := cleanPath(projectRoot)
	if root == "" {
		root = cleanPath(cwd)
	}
	cwd = cleanPath(cwd)
	if cwd == "" || !pathWithin(cwd, root) {
		cwd = root
	}
	snapshot := Snapshot{
		CWD:         cwd,
		ProjectRoot: root,
		GlobalPath:  m.globalPath,
	}
	visited := map[string]struct{}{}
	if info, ok := readTrackedFile(m.globalPath); ok {
		snapshot.Files = append(snapshot.Files, FileInfo{
			Path:     info.Path,
			Kind:     "global",
			Priority: 0,
			ModTime:  info.ModTime,
			Checksum: info.Checksum,
			Size:     info.Size,
		})
		visited[info.Path] = struct{}{}
	}
	priority := 1000
	for _, dir := range dirsFromCWDToRoot(cwd, root) {
		select {
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		default:
		}
		path := filepath.Join(dir, agentsFileName)
		info, ok := readTrackedFile(path)
		if !ok {
			continue
		}
		if _, seen := visited[info.Path]; seen {
			continue
		}
		snapshot.Files = append(snapshot.Files, FileInfo{
			Path:     info.Path,
			Kind:     "project",
			Priority: priority,
			ModTime:  info.ModTime,
			Checksum: info.Checksum,
			Size:     info.Size,
		})
		visited[info.Path] = struct{}{}
		priority--
	}
	sort.SliceStable(snapshot.Files, func(i, j int) bool {
		if snapshot.Files[i].Priority != snapshot.Files[j].Priority {
			return snapshot.Files[i].Priority > snapshot.Files[j].Priority
		}
		if snapshot.Files[i].Kind != snapshot.Files[j].Kind {
			return snapshot.Files[i].Kind < snapshot.Files[j].Kind
		}
		return snapshot.Files[i].Path < snapshot.Files[j].Path
	})
	sum := sha256.New()
	for _, item := range snapshot.Files {
		sum.Write([]byte(item.Path))
		sum.Write([]byte{0})
		sum.Write([]byte(item.Kind))
		sum.Write([]byte{0})
		sum.Write([]byte(item.Checksum))
		sum.Write([]byte{0})
	}
	snapshot.Checksum = hex.EncodeToString(sum.Sum(nil))
	return snapshot, nil
}

func (m *Manager) Load(snapshot Snapshot) (Resolution, bool, error) {
	path := m.cachePath(snapshot.ProjectRoot, snapshot.Checksum)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Resolution{}, false, nil
		}
		return Resolution{}, false, err
	}
	var cached Resolution
	if err := json.Unmarshal(data, &cached); err != nil {
		return Resolution{}, false, err
	}
	return cached, true, nil
}

func (m *Manager) Save(resolution Resolution) error {
	if err := os.MkdirAll(filepath.Dir(m.cachePath(resolution.Snapshot.ProjectRoot, resolution.Snapshot.Checksum)), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(resolution, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(m.cachePath(resolution.Snapshot.ProjectRoot, resolution.Snapshot.Checksum), data, 0o644)
}

func (m *Manager) Resolve(ctx context.Context, client *provider.Client, modelID string, snapshot Snapshot) (Resolution, error) {
	if len(snapshot.Files) == 0 {
		return Resolution{
			Snapshot:        snapshot,
			ResolvedAgents:  "",
			ConflictSummary: "No AGENTS.md files found.",
			GeneratedAt:     time.Now().UTC(),
		}, nil
	}
	if cached, ok, err := m.Load(snapshot); err != nil {
		return Resolution{}, err
	} else if ok {
		return cached, nil
	}
	var prompt strings.Builder
	prompt.WriteString("You are resolving multiple AGENTS.md instruction sources into one agreed AGENTS.md without losing detail.\n")
	prompt.WriteString("Rules:\n")
	prompt.WriteString("- Preserve all non-conflicting instructions.\n")
	prompt.WriteString("- Resolve conflicts by priority: larger priority number wins; project files override global; closer AGENTS.md files override broader ones.\n")
	prompt.WriteString("- Return strict JSON with keys resolved_agents_md and conflict_summary.\n")
	prompt.WriteString("- conflict_summary must be either 'No conflicts' or a concise list of overrides/behavior changes.\n\n")
	for _, item := range snapshot.Files {
		select {
		case <-ctx.Done():
			return Resolution{}, ctx.Err()
		default:
		}
		body, err := os.ReadFile(item.Path)
		if err != nil {
			continue
		}
		fmt.Fprintf(&prompt, "FILE: %s\nKIND: %s\nPRIORITY: %d\nMODIFIED: %s\nCHECKSUM: %s\nCONTENT:\n%s\n\n---\n\n",
			item.Path,
			item.Kind,
			item.Priority,
			item.ModTime.UTC().Format(time.RFC3339),
			item.Checksum,
			string(body))
	}
	resp, err := client.CompleteChat(ctx, provider.ChatRequest{
		Model: modelID,
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "Resolve project instruction files into a single AGENTS.md and conflict summary."},
			{Role: provider.RoleUser, Content: prompt.String()},
		},
	})
	if err != nil {
		return Resolution{}, err
	}
	parsed, err := parseResolverResponse(resp.Text)
	if err != nil {
		return Resolution{}, err
	}
	resolution := Resolution{
		Snapshot:        snapshot,
		ResolvedAgents:  strings.TrimSpace(parsed.ResolvedAgentsMD),
		ConflictSummary: strings.TrimSpace(parsed.ConflictSummary),
		GeneratedAt:     time.Now().UTC(),
	}
	if resolution.ConflictSummary == "" {
		resolution.ConflictSummary = "No conflicts"
	}
	if err := m.Save(resolution); err != nil {
		return Resolution{}, err
	}
	return resolution, nil
}

func (m *Manager) cachePath(projectRoot, checksum string) string {
	rootHash := sha256.Sum256([]byte(cleanPath(projectRoot)))
	return filepath.Join(m.stateDir, "agents-cache", hex.EncodeToString(rootHash[:]), checksum+".json")
}

type trackedFile struct {
	Path     string
	Checksum string
	ModTime  time.Time
	Size     int64
}

func readTrackedFile(path string) (trackedFile, bool) {
	path = cleanPath(path)
	if strings.TrimSpace(path) == "" {
		return trackedFile{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || !isPlainText(path, data) {
		return trackedFile{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return trackedFile{}, false
	}
	sum := sha256.Sum256(data)
	return trackedFile{
		Path:     path,
		Checksum: hex.EncodeToString(sum[:]),
		ModTime:  info.ModTime().UTC(),
		Size:     info.Size(),
	}, true
}

func parseResolverResponse(raw string) (resolverResponse, error) {
	text := strings.TrimSpace(raw)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	var out resolverResponse
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return resolverResponse{}, fmt.Errorf("parse agents resolver response: %w", err)
	}
	if strings.TrimSpace(out.ConflictSummary) == "" {
		out.ConflictSummary = "No conflicts"
	}
	return out, nil
}

func dirsFromCWDToRoot(cwd string, root string) []string {
	current := cleanPath(cwd)
	root = cleanPath(root)
	var dirs []string
	for {
		dirs = append(dirs, current)
		if current == root {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return dirs
}

func cleanPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isPlainText(path string, data []byte) bool {
	if len(data) == 0 {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf", ".zip", ".gz", ".tar", ".tgz", ".bz2", ".xz", ".7z", ".jar", ".class", ".so", ".dll", ".exe", ".woff", ".woff2":
		return false
	}
	if !utf8.Valid(data) {
		return false
	}
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
}
