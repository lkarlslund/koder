package accesssettings

import (
	"path/filepath"
	"testing"
)

func TestAllowsProjectReadWrite(t *testing.T) {
	root := t.TempDir()
	settings := Default()
	path := filepath.Join(root, "file.txt")

	if err := Allows(settings, Request{Kind: AccessRead, Path: path, ProjectRoot: root}); err != nil {
		t.Fatalf("read project path: %v", err)
	}
	if err := Allows(settings, Request{Kind: AccessWrite, Path: path, ProjectRoot: root}); err != nil {
		t.Fatalf("write project path: %v", err)
	}
}

func TestAllowsBlocksProjectWriteWhenReadOnly(t *testing.T) {
	root := t.TempDir()
	settings := Default()
	settings.Project = ModeReadOnly

	if err := Allows(settings, Request{Kind: AccessWrite, Path: filepath.Join(root, "file.txt"), ProjectRoot: root}); err == nil {
		t.Fatal("expected project write to be blocked")
	}
}

func TestAllowsExtraMount(t *testing.T) {
	root := t.TempDir()
	mount := t.TempDir()
	settings := LockedDown()
	settings.Mounts = []Mount{{Path: mount, Mode: ModeReadWrite}}

	if err := Allows(settings, Request{Kind: AccessWrite, Path: filepath.Join(mount, "file.txt"), ProjectRoot: root}); err != nil {
		t.Fatalf("write mounted path: %v", err)
	}
}

func TestAllowsBlocksNetwork(t *testing.T) {
	if err := Allows(LockedDown(), Request{Kind: AccessNetwork}); err == nil {
		t.Fatal("expected network to be blocked")
	}
}

func TestValidateRejectsRelativeMount(t *testing.T) {
	settings := Default()
	settings.Mounts = []Mount{{Path: "relative", Mode: ModeReadOnly}}

	if err := Validate(settings); err == nil {
		t.Fatal("expected relative mount to be rejected")
	}
}
