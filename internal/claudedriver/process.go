package claudedriver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/claudeapp"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/sandbox"
)

// AccessSettings resolves the effective filesystem and network policy for a
// session, so live config changes reach the next process a chat starts.
type AccessSettings func(domain.Session) accesssettings.Settings

// ProcessFactory creates command configurations for short-lived discovery
// processes and isolated per-chat Claude Code processes.
type ProcessFactory interface {
	DiscoveryConfig(context.Context) (claudeapp.Config, error)
	// ChatConfig wraps the Claude Code arguments args for one chat.
	ChatConfig(ctx context.Context, session domain.Session, chat domain.Chat, args []string) (ChatProcessConfig, error)
	RemoveChat(domain.ID) error
}

type ChatProcessConfig struct {
	Client claudeapp.Config
	// Fingerprint changes whenever the process must be restarted to apply a
	// new configuration or sandbox policy.
	Fingerprint string
}

type SandboxProcessConfig struct {
	Executable string
	// ConfigDir is the account's Claude Code configuration directory, whose
	// credentials every chat shares. Empty means CLAUDE_CONFIG_DIR or ~/.claude.
	ConfigDir string
	StateDir  string
	Access    AccessSettings
}

type sandboxProcessFactory struct {
	cfg SandboxProcessConfig
}

func NewSandboxProcessFactory(cfg SandboxProcessConfig) ProcessFactory {
	if strings.TrimSpace(cfg.Executable) == "" {
		cfg.Executable = "claude"
	}
	return &sandboxProcessFactory{cfg: cfg}
}

// The autoupdater would try to rewrite a read-only installation.
var processEnv = []string{"DISABLE_AUTOUPDATER=1"}

func (f *sandboxProcessFactory) DiscoveryConfig(context.Context) (claudeapp.Config, error) {
	args := append(append([]string(nil), claudeapp.StreamArgs...), "--setting-sources", "", "--no-session-persistence")
	env := append([]string(nil), processEnv...)
	if dir := strings.TrimSpace(f.cfg.ConfigDir); dir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+dir)
	}
	return claudeapp.Config{Executable: f.cfg.Executable, Args: args, Env: env, Dir: os.TempDir()}, nil
}

func (f *sandboxProcessFactory) ChatConfig(_ context.Context, session domain.Session, chatRecord domain.Chat, args []string) (ChatProcessConfig, error) {
	if strings.TrimSpace(session.ProjectRoot) == "" {
		return ChatProcessConfig{}, fmt.Errorf("session %s has no project root", session.ID)
	}
	configDir, err := f.chatConfigDir(chatRecord.ID)
	if err != nil {
		return ChatProcessConfig{}, err
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return ChatProcessConfig{}, fmt.Errorf("create private Claude configuration: %w", err)
	}
	binds, err := f.credentialBinds(configDir)
	if err != nil {
		return ChatProcessConfig{}, err
	}
	settings := accesssettings.Normalize(session.AccessSettings)
	if f.cfg.Access != nil {
		settings = accesssettings.Normalize(f.cfg.Access(session))
	}
	if settings.Tmp == accesssettings.TmpSession && strings.TrimSpace(settings.TmpDir) == "" {
		settings.TmpDir = filepath.Join(os.TempDir(), "koder-session-tmp", string(session.ID))
		if err := os.MkdirAll(settings.TmpDir, 0o700); err != nil {
			return ChatProcessConfig{}, fmt.Errorf("create Claude session tmp: %w", err)
		}
	}
	settings.Mounts = append(settings.Mounts, accesssettings.Mount{Path: configDir, Mode: accesssettings.ModeReadWrite})
	executable, runtimeMount, err := sandbox.ResolveExecutable(f.cfg.Executable, settings)
	if err != nil {
		return ChatProcessConfig{}, err
	}
	if runtimeMount != "" {
		settings.Mounts = append(settings.Mounts, accesssettings.Mount{Path: runtimeMount, Mode: accesssettings.ModeReadOnly})
	}
	wrapper, wrappedArgs, err := sandbox.WrapCommand(sandbox.Command{
		Executable: executable,
		Args:       args,
		Workdir:    session.ProjectRoot,
		Settings:   settings,
		Binds:      binds,
	})
	if err != nil {
		return ChatProcessConfig{}, fmt.Errorf("prepare Claude sandbox: %w", err)
	}
	client := claudeapp.Config{
		Executable: wrapper,
		Args:       wrappedArgs,
		Env:        append(append([]string(nil), processEnv...), "CLAUDE_CONFIG_DIR="+configDir),
		Dir:        session.ProjectRoot,
	}
	encoded, err := json.Marshal(client)
	if err != nil {
		return ChatProcessConfig{}, fmt.Errorf("fingerprint Claude sandbox: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return ChatProcessConfig{Client: client, Fingerprint: hex.EncodeToString(digest[:])}, nil
}

// credentialBinds shares the account's credentials file with a chat's private
// configuration. Claude Code rewrites the file in place when it refreshes a
// token, so every chat and the account itself keep seeing the newest one.
func (f *sandboxProcessFactory) credentialBinds(configDir string) ([]sandbox.Bind, error) {
	source := filepath.Join(f.accountConfigDir(), ".credentials.json")
	info, err := os.Stat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect Claude credentials: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	target := filepath.Join(configDir, ".credentials.json")
	// The bind needs a file to cover; it stays empty outside the sandbox.
	placeholder, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("prepare Claude credentials mount: %w", err)
	}
	if err := placeholder.Close(); err != nil {
		return nil, fmt.Errorf("prepare Claude credentials mount: %w", err)
	}
	return []sandbox.Bind{{Source: source, Target: target}}, nil
}

func (f *sandboxProcessFactory) accountConfigDir() string {
	if configured := strings.TrimSpace(f.cfg.ConfigDir); configured != "" {
		return configured
	}
	if configured := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configured != "" {
		return configured
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

func (f *sandboxProcessFactory) RemoveChat(chatID domain.ID) error {
	dir, err := f.chatConfigDir(chatID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove private Claude configuration: %w", err)
	}
	return nil
}

func (f *sandboxProcessFactory) chatConfigDir(chatID domain.ID) (string, error) {
	value := strings.TrimSpace(string(chatID))
	if value == "" || filepath.Base(value) != value || value == "." || value == ".." {
		return "", fmt.Errorf("invalid Claude chat id %q", chatID)
	}
	stateDir := strings.TrimSpace(f.cfg.StateDir)
	if stateDir == "" {
		return "", errors.New("claude state directory is required")
	}
	return filepath.Join(stateDir, "claude", "chats", value), nil
}
