package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/accesssettings"
)

func TestArgsBuildsReadOnlyRootWritableWorkspaceNoNetwork(t *testing.T) {
	t.Setenv(envDisableNetUnshare, "")

	args, err := Args(Command{
		Executable: "/bin/bash",
		Args:       []string{"-lc", "pwd"},
		Workdir:    t.TempDir(),
		Settings: accesssettings.Settings{
			Root:    accesssettings.ModeReadOnly,
			Project: accesssettings.ModeReadWrite,
			Tmp:     accesssettings.TmpEphemeral,
		},
	})
	if err != nil {
		t.Fatalf("build args: %v", err)
	}
	joined := strings.Join(args, "\x00")
	for _, want := range []string{"--unshare-net", "--ro-bind\x00/\x00/", "--tmpfs\x00/etc/ssh", "--bind", "--tmpfs\x00/tmp", "--\x00/bin/bash\x00-lc\x00pwd"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected args to contain %q, got %#v", want, args)
		}
	}
}

func TestArgsHidesHostSSHConfigWhenRootMounted(t *testing.T) {
	args, err := Args(Command{
		Executable: "/bin/bash",
		Args:       []string{"-lc", "ssh -G example.com"},
		Workdir:    t.TempDir(),
		Settings: accesssettings.Settings{
			Network: true,
			Root:    accesssettings.ModeReadOnly,
			Project: accesssettings.ModeReadWrite,
			Tmp:     accesssettings.TmpEphemeral,
		},
	})
	if err != nil {
		t.Fatalf("build args: %v", err)
	}
	joined := strings.Join(args, "\x00")
	if !strings.Contains(joined, "--tmpfs\x00/etc/ssh") {
		t.Fatalf("expected host /etc/ssh to be hidden, got %#v", args)
	}
}

func TestArgsCanDisableNetworkNamespaceForRestrictedRunners(t *testing.T) {
	t.Setenv(envDisableNetUnshare, "1")

	args, err := Args(Command{
		Executable: "/bin/bash",
		Args:       []string{"-lc", "pwd"},
		Workdir:    t.TempDir(),
		Settings: accesssettings.Settings{
			Root:    accesssettings.ModeReadOnly,
			Project: accesssettings.ModeReadWrite,
			Tmp:     accesssettings.TmpEphemeral,
		},
	})
	if err != nil {
		t.Fatalf("build args: %v", err)
	}
	if slices.Contains(args, "--unshare-net") {
		t.Fatalf("expected network namespace unshare to be disabled: %#v", args)
	}
}

func TestArgsHonorsNetworkAndExtraMounts(t *testing.T) {
	extra := t.TempDir()
	args, err := Args(Command{
		Executable: "/bin/sh",
		Workdir:    t.TempDir(),
		Settings: accesssettings.Settings{
			Network: true,
			Root:    accesssettings.ModeReadWrite,
			Project: accesssettings.ModeReadOnly,
			Tmp:     accesssettings.TmpEphemeral,
			Mounts: []accesssettings.Mount{{
				Path: extra,
				Mode: accesssettings.ModeReadWrite,
			}},
		},
	})
	if err != nil {
		t.Fatalf("build args: %v", err)
	}
	if slices.Contains(args, "--unshare-net") {
		t.Fatalf("network-enabled profile should not unshare network: %#v", args)
	}
	joined := strings.Join(args, "\x00")
	for _, want := range []string{"--bind\x00/\x00/", "--ro-bind", "--bind\x00" + extra + "\x00" + extra} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected args to contain %q, got %#v", want, args)
		}
	}
}

func TestArgsRejectsInvalidMountMode(t *testing.T) {
	_, err := Args(Command{
		Executable: "/bin/sh",
		Workdir:    t.TempDir(),
		Settings: accesssettings.Settings{
			Root:    "bogus",
			Project: accesssettings.ModeReadOnly,
		},
	})
	if err != nil {
		t.Fatalf("missing modes should be normalized, got %v", err)
	}
	_, err = Args(Command{
		Executable: "/bin/sh",
		Workdir:    t.TempDir(),
		Settings: accesssettings.Settings{
			Root:    accesssettings.ModeReadOnly,
			Project: accesssettings.ModeReadOnly,
			Mounts: []accesssettings.Mount{{
				Path: t.TempDir(),
				Mode: "bogus",
			}},
		},
	})
	if err != nil {
		t.Fatalf("mount modes should be normalized, got %v", err)
	}
}

func TestBwrapEnforcesWorkspaceMode(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	workdir := t.TempDir()
	run := func(mode accesssettings.Mode) error {
		exe, args, err := WrapCommand(Command{
			Executable: "bash",
			Args:       []string{"-lc", "echo ok > sandbox-write-test"},
			Workdir:    workdir,
			Settings: accesssettings.Settings{
				Root:    accesssettings.ModeReadOnly,
				Project: mode,
			},
		})
		if err != nil {
			return err
		}
		return exec.Command(exe, args...).Run()
	}
	if err := run(accesssettings.ModeReadWrite); err != nil {
		t.Fatalf("expected workspace readwrite to allow write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "sandbox-write-test")); err != nil {
		t.Fatalf("expected written file: %v", err)
	}
	if err := os.Remove(filepath.Join(workdir, "sandbox-write-test")); err != nil {
		t.Fatal(err)
	}
	if err := run(accesssettings.ModeReadOnly); err == nil {
		t.Fatal("expected workspace readonly to block write")
	}
}
