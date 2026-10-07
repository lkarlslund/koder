// Package memory keeps notes the model carries between chats as markdown
// files: one file per memory, in a global folder (the user, their machine,
// preferences that hold everywhere) and a private folder per project. Each
// memory has a one-line description; the descriptions form the index that
// goes into every chat's instructions, and the full text is read on demand.
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Scope says where a memory applies.
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

// Limits keep the index small enough to sit in every prompt and push the
// model to update or delete memories rather than pile them up.
const (
	MaxMemories       = 100
	MaxNameLen        = 64
	MaxDescriptionLen = 200
	MaxContentLen     = 8 << 10
)

var (
	ErrNotFound = errors.New("memory not found")
	ErrExists   = errors.New("memory already exists")

	validName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// Memory is one note.
type Memory struct {
	Scope       Scope     `json:"scope"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Content     string    `json:"content"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store reads and writes memory files.
type Store struct {
	globalDir   string
	projectsDir string
}

// NewStore keeps global memory in configDir/memory, beside the config file
// where the user can read and edit it, and project memory under stateDir.
func NewStore(configDir, stateDir string) *Store {
	return &Store{
		globalDir:   filepath.Join(configDir, "memory"),
		projectsDir: filepath.Join(stateDir, "memory", "projects"),
	}
}

// Dir returns the folder holding a scope's memories. Project memory needs
// the project root, and is kept outside it so it is never committed.
func (s *Store) Dir(scope Scope, projectRoot string) (string, error) {
	switch scope {
	case ScopeGlobal:
		return s.globalDir, nil
	case ScopeProject:
		root := strings.TrimSpace(projectRoot)
		if root == "" {
			return "", errors.New("project memory needs a project; this chat has none")
		}
		sum := sha256.Sum256([]byte(filepath.Clean(root)))
		return filepath.Join(s.projectsDir, filepath.Base(root)+"-"+hex.EncodeToString(sum[:4])), nil
	default:
		return "", fmt.Errorf("memory scope must be %s or %s", ScopeGlobal, ScopeProject)
	}
}

// List returns a scope's memories sorted by name. A project scope without
// a project has none.
func (s *Store) List(scope Scope, projectRoot string) ([]Memory, error) {
	if scope == ScopeProject && strings.TrimSpace(projectRoot) == "" {
		return nil, nil
	}
	dir, err := s.Dir(scope, projectRoot)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s memory: %w", scope, err)
	}
	var memories []Memory
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".md")
		if !ok || entry.IsDir() || !validName.MatchString(name) {
			continue
		}
		memory, err := s.read(scope, dir, name)
		if err != nil {
			return nil, err
		}
		memories = append(memories, memory)
	}
	return memories, nil
}

// Get reads one memory.
func (s *Store) Get(scope Scope, projectRoot, name string) (Memory, error) {
	dir, err := s.Dir(scope, projectRoot)
	if err != nil {
		return Memory{}, err
	}
	if err := checkName(name); err != nil {
		return Memory{}, err
	}
	return s.read(scope, dir, name)
}

// Create writes a new memory, refusing a name already in use or a full
// scope.
func (s *Store) Create(memory Memory, projectRoot string) (Memory, error) {
	if err := check(memory); err != nil {
		return Memory{}, err
	}
	existing, err := s.List(memory.Scope, projectRoot)
	if err != nil {
		return Memory{}, err
	}
	if slices.ContainsFunc(existing, func(m Memory) bool { return m.Name == memory.Name }) {
		return Memory{}, fmt.Errorf("%w: %s memory %q; update it instead", ErrExists, memory.Scope, memory.Name)
	}
	if len(existing) >= MaxMemories {
		return Memory{}, fmt.Errorf("%s memory is full (%d memories); update or delete memories before creating more", memory.Scope, MaxMemories)
	}
	return s.write(memory, projectRoot)
}

// Update replaces an existing memory's description and content.
func (s *Store) Update(memory Memory, projectRoot string) (Memory, error) {
	if err := check(memory); err != nil {
		return Memory{}, err
	}
	if _, err := s.Get(memory.Scope, projectRoot, memory.Name); err != nil {
		return Memory{}, err
	}
	return s.write(memory, projectRoot)
}

// Delete removes a memory.
func (s *Store) Delete(scope Scope, projectRoot, name string) error {
	if _, err := s.Get(scope, projectRoot, name); err != nil {
		return err
	}
	dir, _ := s.Dir(scope, projectRoot)
	if err := os.Remove(filepath.Join(dir, name+".md")); err != nil {
		return fmt.Errorf("delete %s memory %q: %w", scope, name, err)
	}
	return nil
}

func (s *Store) read(scope Scope, dir, name string) (Memory, error) {
	path := filepath.Join(dir, name+".md")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Memory{}, fmt.Errorf("%w: %s memory %q", ErrNotFound, scope, name)
	}
	if err != nil {
		return Memory{}, fmt.Errorf("read %s memory %q: %w", scope, name, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Memory{}, err
	}
	description, content := parseFile(string(data))
	return Memory{Scope: scope, Name: name, Description: description, Content: content, UpdatedAt: info.ModTime()}, nil
}

func (s *Store) write(memory Memory, projectRoot string) (Memory, error) {
	dir, err := s.Dir(memory.Scope, projectRoot)
	if err != nil {
		return Memory{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Memory{}, fmt.Errorf("create %s memory folder: %w", memory.Scope, err)
	}
	path := filepath.Join(dir, memory.Name+".md")
	temp := path + ".tmp"
	if err := os.WriteFile(temp, []byte(formatFile(memory)), 0o600); err != nil {
		return Memory{}, fmt.Errorf("write %s memory %q: %w", memory.Scope, memory.Name, err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return Memory{}, fmt.Errorf("write %s memory %q: %w", memory.Scope, memory.Name, err)
	}
	return s.read(memory.Scope, dir, memory.Name)
}

// A memory file is front matter with the description, then the content:
//
//	---
//	description: User prefers terse answers without summaries
//	---
//	Content…
func formatFile(memory Memory) string {
	return "---\ndescription: " + memory.Description + "\n---\n" + strings.TrimSpace(memory.Content) + "\n"
}

// parseFile reads a memory file, tolerating hand edits: a file without
// front matter is all content, with its first line as the description.
func parseFile(text string) (description, content string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if header, body, ok := strings.Cut(rest, "\n---\n"); ok {
			for line := range strings.SplitSeq(header, "\n") {
				if value, ok := strings.CutPrefix(line, "description:"); ok {
					description = strings.TrimSpace(value)
				}
			}
			return description, strings.TrimSpace(body)
		}
	}
	content = strings.TrimSpace(text)
	description, _, _ = strings.Cut(content, "\n")
	return strings.TrimSpace(strings.TrimLeft(description, "# ")), content
}

func checkName(name string) error {
	if len(name) == 0 || len(name) > MaxNameLen || !validName.MatchString(name) {
		return fmt.Errorf("memory name %q must be 1 to %d lowercase letters, digits and single hyphens, like prefers-terse-answers", name, MaxNameLen)
	}
	return nil
}

func check(memory Memory) error {
	if memory.Scope != ScopeGlobal && memory.Scope != ScopeProject {
		return fmt.Errorf("memory scope must be %s or %s", ScopeGlobal, ScopeProject)
	}
	if err := checkName(memory.Name); err != nil {
		return err
	}
	description := strings.TrimSpace(memory.Description)
	switch {
	case description == "":
		return errors.New("memory description is required: one line saying what the memory is")
	case strings.ContainsAny(description, "\r\n"):
		return errors.New("memory description must be one line")
	case len(description) > MaxDescriptionLen:
		return fmt.Errorf("memory description is %d characters; keep it under %d", len(description), MaxDescriptionLen)
	case strings.TrimSpace(memory.Content) == "":
		return errors.New("memory content is required")
	case len(memory.Content) > MaxContentLen:
		return fmt.Errorf("memory content is %d bytes; keep it under %d", len(memory.Content), MaxContentLen)
	}
	if kind := secretKind(memory.Description + "\n" + memory.Content); kind != "" {
		return fmt.Errorf("memory looks like it contains a %s; never store credentials in memory", kind)
	}
	return nil
}

var secretPatterns = []struct {
	kind    string
	pattern *regexp.Regexp
}{
	{"private key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"AWS access key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"GitHub token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`)},
	{"API key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`)},
	{"password or token", regexp.MustCompile(`(?i)\b(password|passwd|secret|api[_-]?key|access[_-]?token)\s*[:=]\s*\S{6,}`)},
}

func secretKind(text string) string {
	for _, secret := range secretPatterns {
		if secret.pattern.MatchString(text) {
			return secret.kind
		}
	}
	return ""
}
