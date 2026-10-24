package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	upgradePollInterval = 10 * time.Second
	// upgradeSettleTime is how long a staged binary must stay unchanged
	// before it is used, so a copy still in progress is not picked up.
	upgradeSettleTime = 5 * time.Second
)

// stagedUpgrade installs koder.new, staged next to the running executable,
// once Koder is idle. The process then exits for restart, and its supervisor
// starts the new binary.
type stagedUpgrade struct {
	executable  string
	busyReason  func(context.Context) string
	verify      func(context.Context, string) (string, error)
	lastWaitMsg string
}

func newStagedUpgrade(busyReason func(context.Context) string) (*stagedUpgrade, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return &stagedUpgrade{executable: executable, busyReason: busyReason, verify: verifyStagedBinary}, nil
}

func (u *stagedUpgrade) stagedPath() string { return u.executable + ".new" }

// watch sends on ready when a staged binary has been installed.
func (u *stagedUpgrade) watch(ctx context.Context, ready chan<- struct{}) {
	slog.Info("auto-upgrade watching for a staged binary", "path", u.stagedPath())
	ticker := time.NewTicker(upgradePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		installed, err := u.tryInstall(ctx, time.Now())
		if err != nil {
			slog.Error("auto-upgrade", "error", err)
		}
		if installed {
			ready <- struct{}{}
			return
		}
	}
}

// tryInstall swaps in the staged binary when it has settled and Koder is
// idle. A binary that does not run is renamed koder.new.rejected.
func (u *stagedUpgrade) tryInstall(ctx context.Context, now time.Time) (bool, error) {
	staged := u.stagedPath()
	info, err := os.Stat(staged)
	if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) < upgradeSettleTime {
		return false, nil
	}
	if reason := u.busyReason(ctx); reason != "" {
		if reason != u.lastWaitMsg {
			slog.Info("auto-upgrade waiting for idle", "reason", reason)
			u.lastWaitMsg = reason
		}
		return false, nil
	}
	newVersion, err := u.verify(ctx, staged)
	if err != nil {
		rejected := staged + ".rejected"
		if renameErr := os.Rename(staged, rejected); renameErr != nil {
			return false, fmt.Errorf("staged binary does not run (%v), and it could not be moved aside: %w", err, renameErr)
		}
		return false, fmt.Errorf("staged binary does not run, moved to %s: %w", rejected, err)
	}
	if err := os.Rename(u.executable, u.executable+".prev"); err != nil {
		return false, fmt.Errorf("move current binary aside: %w", err)
	}
	if err := os.Rename(staged, u.executable); err != nil {
		_ = os.Rename(u.executable+".prev", u.executable)
		return false, fmt.Errorf("install staged binary: %w", err)
	}
	slog.Info("auto-upgrade installed staged binary", "version", newVersion, "previous", u.executable+".prev")
	return true, nil
}

// verifyStagedBinary runs the staged binary's version command, which fails
// for a binary built for another architecture or CPU level.
func verifyStagedBinary(ctx context.Context, path string) (string, error) {
	if err := os.Chmod(path, 0o755); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, text)
	}
	first, _, _ := strings.Cut(text, "\n")
	return first, nil
}
