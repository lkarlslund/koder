package codexdriver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/codexapp"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/sandbox"
)

// AccessSettings resolves the effective filesystem and network policy for a
// session. Keeping this resolver outside the process factory lets live config
// changes take effect the next time a chat acquires its Codex process.
type AccessSettings func(domain.Session) accesssettings.Settings

// ProcessFactory creates command configurations for short-lived discovery
// processes and isolated per-chat app-server processes.
type ProcessFactory interface {
	DiscoveryConfig(context.Context) (codexapp.Config, error)
	ChatConfig(context.Context, domain.Session, domain.Chat, string) (ChatProcessConfig, error)
	RemoveChat(domain.ID) error
}

type ChatProcessConfig struct {
	Client      codexapp.Config
	Fingerprint string
	Network     bool
}

type SandboxProcessConfig struct {
	Client   codexapp.Config
	StateDir string
	Access   AccessSettings
}

type sandboxProcessFactory struct {
	cfg SandboxProcessConfig
}

func NewSandboxProcessFactory(cfg SandboxProcessConfig) ProcessFactory {
	return &sandboxProcessFactory{cfg: cfg}
}

func (f *sandboxProcessFactory) DiscoveryConfig(context.Context) (codexapp.Config, error) {
	return normalizedClientConfig(f.cfg.Client), nil
}

func (f *sandboxProcessFactory) ChatConfig(_ context.Context, session domain.Session, chatRecord domain.Chat, threadID string) (ChatProcessConfig, error) {
	if strings.TrimSpace(session.ProjectRoot) == "" {
		return ChatProcessConfig{}, fmt.Errorf("session %s has no project root", session.ID)
	}
	home, err := f.chatHome(chatRecord.ID)
	if err != nil {
		return ChatProcessConfig{}, err
	}
	if err := seedCodexHome(home, f.baseHome(), threadID); err != nil {
		return ChatProcessConfig{}, err
	}
	settings := accesssettings.Normalize(session.AccessSettings)
	if f.cfg.Access != nil {
		settings = accesssettings.Normalize(f.cfg.Access(session))
	}
	if settings.Tmp == accesssettings.TmpSession && strings.TrimSpace(settings.TmpDir) == "" {
		settings.TmpDir = filepath.Join(os.TempDir(), "koder-session-tmp", string(session.ID))
		if err := os.MkdirAll(settings.TmpDir, 0o700); err != nil {
			return ChatProcessConfig{}, fmt.Errorf("create Codex session tmp: %w", err)
		}
	}
	settings.Mounts = append(settings.Mounts, accesssettings.Mount{Path: home, Mode: accesssettings.ModeReadWrite})

	client := normalizedClientConfig(f.cfg.Client)
	args := append([]string(nil), client.Args...)
	if len(args) == 0 {
		args = []string{"app-server", "--stdio"}
	}
	executable, wrappedArgs, err := sandbox.WrapCommand(sandbox.Command{
		Executable: client.Executable,
		Args:       args,
		Workdir:    session.ProjectRoot,
		Settings:   settings,
	})
	if err != nil {
		return ChatProcessConfig{}, fmt.Errorf("prepare Codex sandbox: %w", err)
	}
	client.Executable = executable
	client.Args = wrappedArgs
	client.CodexHome = home

	fingerprintInput := struct {
		Executable  string
		Args        []string
		Env         []string
		CodexHome   string
		ProjectRoot string
		Settings    accesssettings.Settings
	}{client.Executable, client.Args, client.Env, client.CodexHome, filepath.Clean(session.ProjectRoot), settings}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return ChatProcessConfig{}, fmt.Errorf("fingerprint Codex sandbox: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return ChatProcessConfig{Client: client, Fingerprint: hex.EncodeToString(digest[:]), Network: settings.Network}, nil
}

func (f *sandboxProcessFactory) RemoveChat(chatID domain.ID) error {
	home, err := f.chatHome(chatID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(home); err != nil {
		return fmt.Errorf("remove private Codex home: %w", err)
	}
	return nil
}

func (f *sandboxProcessFactory) chatHome(chatID domain.ID) (string, error) {
	value := strings.TrimSpace(string(chatID))
	if value == "" || filepath.Base(value) != value || value == "." || value == ".." {
		return "", fmt.Errorf("invalid Codex chat id %q", chatID)
	}
	stateDir := strings.TrimSpace(f.cfg.StateDir)
	if stateDir == "" {
		return "", errors.New("codex state directory is required")
	}
	return filepath.Join(stateDir, "codex", "chats", value), nil
}

func (f *sandboxProcessFactory) baseHome() string {
	if configured := strings.TrimSpace(f.cfg.Client.CodexHome); configured != "" {
		return configured
	}
	if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); configured != "" {
		return configured
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func normalizedClientConfig(cfg codexapp.Config) codexapp.Config {
	if strings.TrimSpace(cfg.Executable) == "" {
		cfg.Executable = "codex"
	}
	return cfg
}

func seedCodexHome(target, source, threadID string) error {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return fmt.Errorf("create private Codex home: %w", err)
	}
	if strings.TrimSpace(source) == "" {
		return nil
	}
	for _, name := range []string{"auth.json", "config.toml", "installation_id", "models_cache.json"} {
		if err := copySeedFile(filepath.Join(source, name), filepath.Join(target, name)); err != nil {
			return err
		}
	}
	return seedCodexThread(target, source, strings.TrimSpace(threadID))
}

func seedCodexThread(target, source, threadID string) error {
	if threadID == "" || filepath.Clean(target) == filepath.Clean(source) {
		return nil
	}
	for _, subdir := range []string{"sessions", "archived_sessions"} {
		root := filepath.Join(source, subdir)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.Contains(entry.Name(), threadID) {
				return nil
			}
			relative, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			destination := filepath.Join(target, relative)
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return err
			}
			return copySeedFile(path, destination)
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("seed private Codex thread %s: %w", threadID, err)
		}
	}
	return nil
}

func copySeedFile(source, target string) error {
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect private Codex configuration %s: %w", filepath.Base(target), err)
	}
	in, err := os.Open(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Codex configuration %s: %w", filepath.Base(source), err)
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("inspect Codex configuration %s: %w", filepath.Base(source), err)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create private Codex configuration %s: %w", filepath.Base(target), err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(target)
		return fmt.Errorf("copy private Codex configuration %s: %w", filepath.Base(target), errors.Join(copyErr, closeErr))
	}
	return nil
}
