package version

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestCurrentReturnsTrimmedMetadata(t *testing.T) {
	originalName := Name
	originalVersion := Version
	originalCommit := Commit
	originalDirty := Dirty
	originalBuildTime := BuildTime
	originalStartedAt := startedAt
	originalExecutableFunc := executableFunc
	t.Cleanup(func() {
		Name = originalName
		Version = originalVersion
		Commit = originalCommit
		Dirty = originalDirty
		BuildTime = originalBuildTime
		startedAt = originalStartedAt
		executableFunc = originalExecutableFunc
	})

	Name = "custom-koder"
	Version = "9.9.9"
	Commit = "  abc123  "
	Dirty = " true "
	BuildTime = " 2026-04-27T10:00:00Z "
	startedAt = time.Date(2026, time.April, 27, 10, 0, 0, 0, time.UTC)
	executableFunc = func() (string, error) {
		return "/tmp/koder-test", nil
	}

	info := Current()
	if info.Name != "custom-koder" {
		t.Fatalf("expected name to round-trip, got %q", info.Name)
	}
	if info.Version != "9.9.9" {
		t.Fatalf("expected version to round-trip, got %q", info.Version)
	}
	if info.Commit != "abc123" {
		t.Fatalf("expected trimmed commit, got %q", info.Commit)
	}
	if info.Dirty != "true" {
		t.Fatalf("expected trimmed dirty flag, got %q", info.Dirty)
	}
	if info.BuildTime != "2026-04-27T10:00:00Z" {
		t.Fatalf("expected trimmed build time, got %q", info.BuildTime)
	}
	if info.GoVersion != runtime.Version() {
		t.Fatalf("expected go version %q, got %q", runtime.Version(), info.GoVersion)
	}
	if info.ExecutablePath != "/tmp/koder-test" {
		t.Fatalf("expected executable path to use seam, got %q", info.ExecutablePath)
	}
	if info.PID != os.Getpid() {
		t.Fatalf("expected pid %d, got %d", os.Getpid(), info.PID)
	}
	if !info.StartedAt.Equal(startedAt) {
		t.Fatalf("expected startedAt %v, got %v", startedAt, info.StartedAt)
	}
}

func TestExecutablePathReturnsEmptyOnFailure(t *testing.T) {
	originalExecutableFunc := executableFunc
	t.Cleanup(func() {
		executableFunc = originalExecutableFunc
	})

	executableFunc = func() (string, error) {
		return "", errors.New("boom")
	}

	if got := executablePath(); got != "" {
		t.Fatalf("expected empty executable path on failure, got %q", got)
	}
}
