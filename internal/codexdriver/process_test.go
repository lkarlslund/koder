package codexdriver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/codexapp"
	"github.com/lkarlslund/koder/internal/domain"
)

func TestSandboxProcessFactoryBuildsPrivatePerChatHome(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	baseHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseHome, "auth.json"), []byte(`{"token":"test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	project := t.TempDir()
	factory := NewSandboxProcessFactory(SandboxProcessConfig{
		Client: codexapp.Config{
			Executable: "/bin/sh",
			Args:       []string{"-c", `printf '%s' "$CODEX_HOME"; touch sandbox-write-ok`},
			CodexHome:  baseHome,
		},
		StateDir: stateDir,
		Access: func(domain.Session) accesssettings.Settings {
			settings := accesssettings.LockedDown()
			settings.Project = accesssettings.ModeReadWrite
			return settings
		},
	})
	spec, err := factory.ChatConfig(context.Background(), domain.Session{ID: "session-1", ProjectRoot: project}, domain.Chat{ID: "chat-1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := spec.Client
	if spec.Fingerprint == "" || cfg.Executable == "/bin/sh" {
		t.Fatalf("sandbox config = %#v, fingerprint %q", cfg, spec.Fingerprint)
	}
	cmd := exec.Command(cfg.Executable, cfg.Args...)
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "CODEX_HOME="+cfg.CodexHome)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run sandboxed command: %v: %s", err, output)
	}
	wantHome := filepath.Join(stateDir, "codex", "chats", "chat-1")
	if string(output) != wantHome || cfg.CodexHome != wantHome {
		t.Fatalf("private home output = %q, config = %q, want %q", output, cfg.CodexHome, wantHome)
	}
	if _, err := os.Stat(filepath.Join(project, "sandbox-write-ok")); err != nil {
		t.Fatalf("project write was blocked: %v", err)
	}
	seed, err := os.ReadFile(filepath.Join(wantHome, "auth.json"))
	if err != nil || string(seed) != `{"token":"test"}` {
		t.Fatalf("private auth seed = %q, %v", seed, err)
	}
}

func TestSandboxProcessFactoryPolicyChangesFingerprintAndNetworkNamespace(t *testing.T) {
	settings := accesssettings.Default()
	factory := NewSandboxProcessFactory(SandboxProcessConfig{
		Client:   codexapp.Config{Executable: "/bin/true"},
		StateDir: t.TempDir(),
		Access:   func(domain.Session) accesssettings.Settings { return settings },
	})
	session := domain.Session{ID: "session-policy", ProjectRoot: t.TempDir()}
	chatRecord := domain.Chat{ID: "chat-policy"}
	enabled, err := factory.ChatConfig(context.Background(), session, chatRecord, "")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(enabled.Client.Args, "--unshare-net") || !enabled.Network {
		t.Fatalf("network-enabled config = %#v", enabled)
	}
	settings.Network = false
	disabled, err := factory.ChatConfig(context.Background(), session, chatRecord, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(disabled.Client.Args, "--unshare-net") || disabled.Network {
		t.Fatalf("network-disabled config = %#v", disabled)
	}
	if enabled.Fingerprint == disabled.Fingerprint {
		t.Fatal("access policy change did not change process fingerprint")
	}
}

func TestSandboxProcessFactoryRejectsUnsafeChatID(t *testing.T) {
	factory := NewSandboxProcessFactory(SandboxProcessConfig{StateDir: t.TempDir()})
	_, err := factory.ChatConfig(context.Background(), domain.Session{ProjectRoot: t.TempDir()}, domain.Chat{ID: "../escape"}, "")
	if err == nil || !strings.Contains(err.Error(), "invalid Codex chat id") {
		t.Fatalf("unsafe chat id error = %v", err)
	}
}

func TestSandboxProcessFactoryMigratesOnlyBoundLegacyThread(t *testing.T) {
	baseHome := t.TempDir()
	boundID := "019f132e-4f40-796f-b07d-7a08cdacae47"
	otherID := "01a020a6-84d5-7b03-a995-bb2cfb4528b0"
	legacyDir := filepath.Join(baseHome, "sessions", "2026", "08", "21")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	boundName := "rollout-2026-08-21T10-00-00-" + boundID + ".jsonl"
	otherName := "rollout-2026-08-21T10-01-00-" + otherID + ".jsonl"
	if err := os.WriteFile(filepath.Join(legacyDir, boundName), []byte("bound\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, otherName), []byte("other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	factory := NewSandboxProcessFactory(SandboxProcessConfig{
		Client:   codexapp.Config{Executable: "/bin/true", CodexHome: baseHome},
		StateDir: stateDir,
	})
	_, err := factory.ChatConfig(context.Background(), domain.Session{ID: "session-migrate", ProjectRoot: t.TempDir()}, domain.Chat{ID: "chat-migrate"}, boundID)
	if err != nil {
		t.Fatal(err)
	}
	privateDir := filepath.Join(stateDir, "codex", "chats", "chat-migrate", "sessions", "2026", "08", "21")
	if data, err := os.ReadFile(filepath.Join(privateDir, boundName)); err != nil || string(data) != "bound\n" {
		t.Fatalf("bound rollout = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(privateDir, otherName)); !os.IsNotExist(err) {
		t.Fatalf("unrelated rollout was copied: %v", err)
	}
}
