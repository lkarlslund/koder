package accesssettings

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type Mode string

const (
	ModeNone      Mode = "none"
	ModeReadOnly  Mode = "readonly"
	ModeReadWrite Mode = "readwrite"
)

type TmpMode string

const (
	TmpEphemeral TmpMode = "ephemeral"
	TmpSession   TmpMode = "session"
	TmpHost      TmpMode = "host"
)

type Settings struct {
	Network bool    `toml:"network" json:"network"`
	Project Mode    `toml:"project" json:"project"`
	Home    Mode    `toml:"home" json:"home"`
	Root    Mode    `toml:"root" json:"root"`
	Tmp     TmpMode `toml:"tmp" json:"tmp"`
	Mounts  []Mount `toml:"mounts" json:"mounts"`
	TmpDir  string  `toml:"-" json:"-"`
}

type Mount struct {
	Path string `toml:"path" json:"path"`
	Mode Mode   `toml:"mode" json:"mode"`
}

type Preset struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Settings    Settings `json:"settings"`
}

type Request struct {
	Kind        AccessKind
	Path        string
	ProjectRoot string
}

type AccessKind string

const (
	AccessRead    AccessKind = "read"
	AccessWrite   AccessKind = "write"
	AccessNetwork AccessKind = "network"
)

func Default() Settings {
	return Settings{
		Network: true,
		Project: ModeReadWrite,
		Home:    ModeNone,
		Root:    ModeReadOnly,
		Tmp:     TmpSession,
	}
}

func IsZero(settings Settings) bool {
	return !settings.Network &&
		settings.Project == "" &&
		settings.Home == "" &&
		settings.Root == "" &&
		settings.Tmp == "" &&
		len(settings.Mounts) == 0 &&
		strings.TrimSpace(settings.TmpDir) == ""
}

func LockedDown() Settings {
	return Settings{
		Network: false,
		Project: ModeReadOnly,
		Home:    ModeNone,
		Root:    ModeReadOnly,
		Tmp:     TmpEphemeral,
	}
}

func AllowAll() Settings {
	return Settings{
		Network: true,
		Project: ModeReadWrite,
		Home:    ModeReadWrite,
		Root:    ModeReadWrite,
		Tmp:     TmpHost,
	}
}

func Presets() []Preset {
	return []Preset{
		{ID: "locked-down", Label: "Locked down", Description: "No network, project read-only, no home, root read-only, fresh /tmp per call.", Settings: LockedDown()},
		{ID: "normal-coding", Label: "Normal coding", Description: "Network on, project read-write, no home, root read-only, persistent session /tmp.", Settings: Default()},
		{ID: "allow-all", Label: "Allow all", Description: "Network on, project/home/root read-write, host /tmp.", Settings: AllowAll()},
	}
}

func Normalize(settings Settings) Settings {
	settings.Project = normalizeMode(settings.Project, Default().Project)
	settings.Home = normalizeMode(settings.Home, Default().Home)
	settings.Root = normalizeMode(settings.Root, Default().Root)
	if settings.Root == ModeNone {
		settings.Root = ModeReadOnly
	}
	settings.Tmp = normalizeTmp(settings.Tmp)
	settings.Mounts = NormalizeMounts(settings.Mounts)
	return settings
}

func Validate(settings Settings) error {
	settings = Normalize(settings)
	return ValidateMounts(settings.Mounts)
}

// NormalizeMounts canonicalizes explicit host folder grants. A leading ~/ is
// expanded for configuration and UI convenience; other shell expansions are
// deliberately unsupported.
func NormalizeMounts(mounts []Mount) []Mount {
	out := make([]Mount, 0, len(mounts))
	for _, mount := range mounts {
		mount.Path = expandHome(strings.TrimSpace(mount.Path))
		if mount.Path == "" {
			continue
		}
		if filepath.IsAbs(mount.Path) {
			mount.Path = filepath.Clean(mount.Path)
		}
		mount.Mode = normalizeMode(mount.Mode, ModeReadOnly)
		out = append(out, mount)
	}
	return out
}

// ValidateMounts verifies a standalone list of host folder grants.
func ValidateMounts(mounts []Mount) error {
	for _, mount := range NormalizeMounts(mounts) {
		if !filepath.IsAbs(mount.Path) {
			return fmt.Errorf("mount path %q must be absolute", mount.Path)
		}
		if mount.Mode == ModeNone {
			return fmt.Errorf("mount path %q has no access mode", mount.Path)
		}
	}
	return nil
}

// WithInheritedMounts returns the effective settings for a session. Global
// grants are applied first, then session-specific grants. More specific paths
// are mounted later so they can safely narrow or widen a parent folder.
func WithInheritedMounts(settings Settings, inherited []Mount) Settings {
	settings = Normalize(settings)
	mounts := append(NormalizeMounts(inherited), settings.Mounts...)
	if len(mounts) == 0 {
		return settings
	}

	// A session-specific entry for the exact same path overrides the inherited
	// entry because it appears later in the combined list.
	byPath := make(map[string]Mount, len(mounts))
	order := make(map[string]int, len(mounts))
	for idx, mount := range mounts {
		byPath[mount.Path] = mount
		order[mount.Path] = idx
	}
	mounts = mounts[:0]
	for _, mount := range byPath {
		mounts = append(mounts, mount)
	}
	slices.SortStableFunc(mounts, func(a, b Mount) int {
		if depth := mountDepth(a.Path) - mountDepth(b.Path); depth != 0 {
			return depth
		}
		return order[a.Path] - order[b.Path]
	})
	settings.Mounts = mounts
	return settings
}

func Allows(settings Settings, req Request) error {
	settings = Normalize(settings)
	if req.Kind == AccessNetwork {
		if !settings.Network {
			return fmt.Errorf("network access is disabled")
		}
		return nil
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	abs = filepath.Clean(abs)
	required := ModeReadOnly
	if req.Kind == AccessWrite {
		required = ModeReadWrite
	}
	mode := modeForPath(settings, abs, strings.TrimSpace(req.ProjectRoot))
	if !modeAllows(mode, required) {
		return fmt.Errorf("%s access to %s is blocked by sandbox settings", req.Kind, abs)
	}
	return nil
}

// MapPath translates an absolute path from the sandbox namespace to the path
// used by the Koder process. Explicit project, home, and mount mappings take
// precedence over the sandbox's /tmp mapping, matching bubblewrap mount order.
func MapPath(settings Settings, req Request) (string, error) {
	settings = Normalize(settings)
	abs, err := filepath.Abs(strings.TrimSpace(req.Path))
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	tmpRoot := filepath.Clean("/tmp")
	if !pathContains(tmpRoot, abs) || hasExplicitMapping(settings, abs, strings.TrimSpace(req.ProjectRoot)) {
		return abs, nil
	}
	switch settings.Tmp {
	case TmpHost:
		return abs, nil
	case TmpSession:
		tmpDir := strings.TrimSpace(settings.TmpDir)
		if tmpDir == "" {
			return "", fmt.Errorf("session /tmp is unavailable")
		}
		rel, err := filepath.Rel(tmpRoot, abs)
		if err != nil {
			return "", fmt.Errorf("map session /tmp path: %w", err)
		}
		return filepath.Join(filepath.Clean(tmpDir), rel), nil
	case TmpEphemeral:
		return "", fmt.Errorf("%s is in ephemeral /tmp and is only available within the command that created it", abs)
	default:
		return "", fmt.Errorf("unsupported /tmp access mode %q", settings.Tmp)
	}
}

func hasExplicitMapping(settings Settings, abs string, projectRoot string) bool {
	if projectRoot != "" && pathContains(projectRoot, abs) {
		return true
	}
	for _, mount := range settings.Mounts {
		if pathContains(mount.Path, abs) {
			return true
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && pathContains(home, abs) {
		return true
	}
	return false
}

func modeForPath(settings Settings, abs string, projectRoot string) Mode {
	for idx := len(settings.Mounts) - 1; idx >= 0; idx-- {
		mount := settings.Mounts[idx]
		if pathContains(mount.Path, abs) {
			return mount.Mode
		}
	}
	if projectRoot != "" && pathContains(projectRoot, abs) {
		return settings.Project
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && pathContains(home, abs) {
		return settings.Home
	}
	if settings.Tmp == TmpSession && strings.TrimSpace(settings.TmpDir) != "" && pathContains(settings.TmpDir, abs) {
		return ModeReadWrite
	}
	if settings.Tmp == TmpHost && pathContains(filepath.Clean("/tmp"), abs) {
		return ModeReadWrite
	}
	return settings.Root
}

func modeAllows(actual Mode, required Mode) bool {
	if actual == ModeNone {
		return false
	}
	if required == ModeReadOnly {
		return actual == ModeReadOnly || actual == ModeReadWrite
	}
	return actual == ModeReadWrite
}

func pathContains(root string, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return path
	}
	if path == "~" {
		return filepath.Clean(home)
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"+string(filepath.Separator)))
}

func mountDepth(path string) int {
	return strings.Count(filepath.Clean(path), string(filepath.Separator))
}

func normalizeMode(mode Mode, fallback Mode) Mode {
	switch mode {
	case ModeNone, ModeReadOnly, ModeReadWrite:
		return mode
	default:
		return fallback
	}
}

func normalizeTmp(mode TmpMode) TmpMode {
	switch mode {
	case TmpEphemeral, TmpSession, TmpHost:
		return mode
	default:
		return TmpSession
	}
}
