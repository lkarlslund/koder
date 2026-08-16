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

func TestAllowsMountOverridesProject(t *testing.T) {
	root := t.TempDir()
	mount := filepath.Join(root, "generated")
	settings := Default()
	settings.Project = ModeReadWrite
	settings.Mounts = []Mount{{Path: mount, Mode: ModeReadOnly}}

	path := filepath.Join(mount, "file.txt")
	if err := Allows(settings, Request{Kind: AccessRead, Path: path, ProjectRoot: root}); err != nil {
		t.Fatalf("read mounted project path: %v", err)
	}
	if err := Allows(settings, Request{Kind: AccessWrite, Path: path, ProjectRoot: root}); err == nil {
		t.Fatal("expected mount policy to override project write access")
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

func TestMapPathMapsSessionTmp(t *testing.T) {
	settings := Default()
	settings.TmpDir = filepath.Join(t.TempDir(), "session-tmp")

	got, err := MapPath(settings, Request{Path: "/tmp/image.png", ProjectRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(settings.TmpDir, "image.png"); got != want {
		t.Fatalf("mapped path = %q, want %q", got, want)
	}
}

func TestMapPathPreservesProjectMappingInsideTmp(t *testing.T) {
	project := t.TempDir()
	target := filepath.Join(project, "image.png")
	settings := Default()
	settings.TmpDir = filepath.Join(t.TempDir(), "session-tmp")

	got, err := MapPath(settings, Request{Path: target, ProjectRoot: project})
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Fatalf("mapped project path = %q, want %q", got, target)
	}
}

func TestMapPathRejectsEphemeralTmp(t *testing.T) {
	settings := LockedDown()
	if _, err := MapPath(settings, Request{Path: "/tmp/image.png", ProjectRoot: "/workspace"}); err == nil {
		t.Fatal("expected ephemeral /tmp to be unavailable outside its command")
	}
}
