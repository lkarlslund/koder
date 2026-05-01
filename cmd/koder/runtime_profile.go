package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
)

const (
	envProfile = "KODER_PROFILE"
)

type runtimeProfileConfig struct {
	Prefix string
}

type runtimeProfiler struct {
	cpuFile *os.File
	memPath string
}

func runtimeProfileConfigFromEnv(getenv func(string) string) runtimeProfileConfig {
	return runtimeProfileConfig{
		Prefix: strings.TrimSpace(getenv(envProfile)),
	}
}

func startRuntimeProfilingFromEnv() (func() error, error) {
	profiler, err := startRuntimeProfiling(runtimeProfileConfigFromEnv(os.Getenv))
	if err != nil || profiler == nil {
		return nil, err
	}
	return profiler.Stop, nil
}

func startRuntimeProfiling(cfg runtimeProfileConfig) (*runtimeProfiler, error) {
	if cfg.Prefix == "" {
		return nil, nil
	}
	cpuPath, memPath := profilePaths(cfg.Prefix)
	profiler := &runtimeProfiler{memPath: memPath}
	file, err := createProfileFile(cpuPath)
	if err != nil {
		return nil, fmt.Errorf("create CPU profile %q: %w", cpuPath, err)
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("start CPU profile %q: %w", cpuPath, err)
	}
	profiler.cpuFile = file
	return profiler, nil
}

func (p *runtimeProfiler) Stop() error {
	if p == nil {
		return nil
	}
	var errs []error
	if p.cpuFile != nil {
		pprof.StopCPUProfile()
		if err := p.cpuFile.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close CPU profile: %w", err))
		}
		p.cpuFile = nil
	}
	if p.memPath != "" {
		runtime.GC()
		file, err := createProfileFile(p.memPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("create memory profile %q: %w", p.memPath, err))
		} else {
			if err := pprof.WriteHeapProfile(file); err != nil {
				errs = append(errs, fmt.Errorf("write memory profile %q: %w", p.memPath, err))
			}
			if err := file.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close memory profile %q: %w", p.memPath, err))
			}
		}
		p.memPath = ""
	}
	return errors.Join(errs...)
}

func createProfileFile(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return os.Create(path)
}

func profilePaths(prefix string) (cpuPath, memPath string) {
	return prefix + ".cpu.out", prefix + ".mem.out"
}
