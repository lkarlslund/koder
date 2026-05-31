package accesssettings

import (
	"fmt"
	"os"
	"path/filepath"
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
	out := make([]Mount, 0, len(settings.Mounts))
	for _, mount := range settings.Mounts {
		mount.Path = strings.TrimSpace(mount.Path)
		if mount.Path == "" {
			continue
		}
		mount.Mode = normalizeMode(mount.Mode, ModeReadOnly)
		out = append(out, mount)
	}
	settings.Mounts = out
	return settings
}

func Validate(settings Settings) error {
	settings = Normalize(settings)
	for _, mount := range settings.Mounts {
		if !filepath.IsAbs(mount.Path) {
			return fmt.Errorf("mount path %q must be absolute", mount.Path)
		}
		if mount.Mode == ModeNone {
			return fmt.Errorf("mount path %q has no access mode", mount.Path)
		}
	}
	return nil
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

func modeForPath(settings Settings, abs string, projectRoot string) Mode {
	if projectRoot != "" && pathContains(projectRoot, abs) {
		return settings.Project
	}
	for _, mount := range settings.Mounts {
		if pathContains(mount.Path, abs) {
			return mount.Mode
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && pathContains(home, abs) {
		return settings.Home
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
