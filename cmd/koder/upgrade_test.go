package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestUpgrade(t *testing.T, busy string, verifyErr error) *stagedUpgrade {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "koder")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe+".new", []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &stagedUpgrade{
		executable: exe,
		busyReason: func(context.Context) string { return busy },
		verify:     func(context.Context, string) (string, error) { return "koder r1", verifyErr },
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestStagedUpgradeInstallsWhenIdle(t *testing.T) {
	u := newTestUpgrade(t, "", nil)
	installed, err := u.tryInstall(context.Background(), time.Now().Add(time.Minute))
	if err != nil || !installed {
		t.Fatalf("installed=%v err=%v", installed, err)
	}
	if got := readFile(t, u.executable); got != "new" {
		t.Fatalf("executable = %q", got)
	}
	if got := readFile(t, u.executable+".prev"); got != "old" {
		t.Fatalf("previous = %q", got)
	}
	if _, err := os.Stat(u.stagedPath()); !os.IsNotExist(err) {
		t.Fatalf("staged binary still present: %v", err)
	}
}

func TestStagedUpgradeWaitsForSettledCopyAndIdle(t *testing.T) {
	u := newTestUpgrade(t, "", nil)
	if installed, _ := u.tryInstall(context.Background(), time.Now()); installed {
		t.Fatal("installed a binary that was still being written")
	}
	u.busyReason = func(context.Context) string { return "chat c1 is running" }
	if installed, _ := u.tryInstall(context.Background(), time.Now().Add(time.Minute)); installed {
		t.Fatal("installed while busy")
	}
	if got := readFile(t, u.executable); got != "old" {
		t.Fatalf("executable = %q", got)
	}
}

func TestStagedUpgradeRejectsBinaryThatDoesNotRun(t *testing.T) {
	u := newTestUpgrade(t, "", errors.New("exec format error"))
	installed, err := u.tryInstall(context.Background(), time.Now().Add(time.Minute))
	if installed || err == nil {
		t.Fatalf("installed=%v err=%v", installed, err)
	}
	if got := readFile(t, u.executable); got != "old" {
		t.Fatalf("executable = %q", got)
	}
	if got := readFile(t, u.stagedPath()+".rejected"); got != "new" {
		t.Fatalf("rejected = %q", got)
	}
}
