package claudedriver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/domain"
)

func TestSandboxProcessFactorySharesCredentialsWithPrivateConfig(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	account := t.TempDir()
	credentials := filepath.Join(account, ".credentials.json")
	if err := os.WriteFile(credentials, []byte("token-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(account, "history.jsonl"), []byte("private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	project := t.TempDir()
	factory := NewSandboxProcessFactory(SandboxProcessConfig{
		Executable: "/bin/sh",
		ConfigDir:  account,
		StateDir:   stateDir,
		Access: func(domain.Session) accesssettings.Settings {
			settings := accesssettings.LockedDown()
			settings.Project = accesssettings.ModeReadWrite
			return settings
		},
	})
	// A token refresh rewrites the file in place; the account must see it.
	script := `cat "$CLAUDE_CONFIG_DIR/.credentials.json"; printf 'token-2\n' > "$CLAUDE_CONFIG_DIR/.credentials.json"; ls -A "$CLAUDE_CONFIG_DIR"; touch sandbox-write-ok`
	spec, err := factory.ChatConfig(context.Background(), domain.Session{ID: "session-1", ProjectRoot: project}, domain.Chat{ID: "chat-1"}, []string{"-c", script})
	if err != nil {
		t.Fatal(err)
	}
	cfg := spec.Client
	privateDir := filepath.Join(stateDir, "claude", "chats", "chat-1")
	if spec.Fingerprint == "" || cfg.Executable == "/bin/sh" || !slices.Contains(cfg.Env, "CLAUDE_CONFIG_DIR="+privateDir) {
		t.Fatalf("sandbox config = %#v, fingerprint %q", cfg, spec.Fingerprint)
	}
	cmd := exec.Command(cfg.Executable, cfg.Args...)
	cmd.Dir = cfg.Dir
	cmd.Env = append(os.Environ(), cfg.Env...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run sandboxed command: %v: %s", err, output)
	}
	if string(output) != "token-1\n.credentials.json\n" {
		t.Fatalf("sandboxed output = %q", output)
	}
	if data, err := os.ReadFile(credentials); err != nil || string(data) != "token-2\n" {
		t.Fatalf("account credentials = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(project, "sandbox-write-ok")); err != nil {
		t.Fatalf("project write was blocked: %v", err)
	}

	if err := factory.RemoveChat("chat-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(privateDir); !os.IsNotExist(err) {
		t.Fatalf("private configuration survived removal: %v", err)
	}
	if data, err := os.ReadFile(credentials); err != nil || string(data) != "token-2\n" {
		t.Fatalf("removing the chat touched the account credentials: %q, %v", data, err)
	}
}

func TestSandboxProcessFactoryWithoutAccountCredentials(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	factory := NewSandboxProcessFactory(SandboxProcessConfig{Executable: "/bin/true", ConfigDir: t.TempDir(), StateDir: t.TempDir()})
	spec, err := factory.ChatConfig(context.Background(), domain.Session{ID: "session-1", ProjectRoot: t.TempDir()}, domain.Chat{ID: "chat-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(spec.Client.Args, func(arg string) bool { return filepath.Base(arg) == ".credentials.json" }) {
		t.Fatalf("bound missing credentials: %q", spec.Client.Args)
	}
}

func TestSandboxProcessFactoryRejectsUnsafeChatID(t *testing.T) {
	factory := NewSandboxProcessFactory(SandboxProcessConfig{StateDir: t.TempDir()})
	for _, chatID := range []domain.ID{"", "..", "a/b"} {
		if _, err := factory.ChatConfig(context.Background(), domain.Session{ProjectRoot: t.TempDir()}, domain.Chat{ID: chatID}, nil); err == nil {
			t.Fatalf("chat id %q was accepted", chatID)
		}
	}
}
