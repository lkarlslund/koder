package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeProfileConfigFromEnv(t *testing.T) {
	env := map[string]string{
		envProfile: " profiles/run ",
	}
	cfg := runtimeProfileConfigFromEnv(func(key string) string {
		return env[key]
	})

	if cfg.Prefix != "profiles/run" {
		t.Fatalf("expected profile prefix to be trimmed, got %q", cfg.Prefix)
	}
}

func TestProfilePathsAddsCPUAndMemorySuffixes(t *testing.T) {
	cpuPath, memPath := profilePaths("profiles/run")
	if cpuPath != "profiles/run.cpu.out" {
		t.Fatalf("expected CPU profile path, got %q", cpuPath)
	}
	if memPath != "profiles/run.mem.out" {
		t.Fatalf("expected memory profile path, got %q", memPath)
	}
}

func TestStartRuntimeProfilingNoEnvIsNoop(t *testing.T) {
	profiler, err := startRuntimeProfiling(runtimeProfileConfig{})
	if err != nil {
		t.Fatalf("start profiling: %v", err)
	}
	if profiler != nil {
		t.Fatalf("expected nil profiler, got %#v", profiler)
	}
}

func TestRuntimeProfilerWritesMemoryProfile(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "nested", "run")
	profiler, err := startRuntimeProfiling(runtimeProfileConfig{Prefix: prefix})
	if err != nil {
		t.Fatalf("start profiling: %v", err)
	}
	if profiler == nil {
		t.Fatal("expected profiler")
	}
	if err := profiler.Stop(); err != nil {
		t.Fatalf("stop profiling: %v", err)
	}

	cpuPath, memPath := profilePaths(prefix)
	for _, path := range []string{cpuPath, memPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat profile %q: %v", path, err)
		}
		if info.Size() == 0 {
			t.Fatalf("expected non-empty profile %q", path)
		}
	}
}

func TestRuntimeProfilerWritesMemoryOnlyWhenCPUAlreadyActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.mem.out")
	profiler := &runtimeProfiler{memPath: path}
	if err := profiler.Stop(); err != nil {
		t.Fatalf("stop profiling: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat memory profile: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("expected non-empty memory profile")
	}
}
