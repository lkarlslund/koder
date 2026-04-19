package workspace

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type FileStatus struct {
	Code string
	Path string
}

type Status struct {
	Available bool
	Branch    string
	Upstream  string
	Summary   string
	Files     []FileStatus
	Added     int
	Modified  int
	Deleted   int
	Untracked int
}

func Snapshot(ctx context.Context, dir string) (Status, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--short", "--branch")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return Status{}, nil
	}
	return parseStatus(string(output)), nil
}

func parseStatus(raw string) Status {
	status := Status{Available: true}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			status.Branch, status.Upstream, status.Summary = parseBranchLine(strings.TrimPrefix(line, "## "))
			continue
		}
		file := parseFileLine(line)
		status.Files = append(status.Files, file)
		switch {
		case file.Code == "??":
			status.Untracked++
		case strings.Contains(file.Code, "A"):
			status.Added++
		case strings.Contains(file.Code, "D"):
			status.Deleted++
		case strings.Contains(file.Code, "M"), strings.Contains(file.Code, "R"), strings.Contains(file.Code, "C"):
			status.Modified++
		}
	}
	return status
}

func parseBranchLine(line string) (branch string, upstream string, summary string) {
	parts := strings.SplitN(line, "...", 2)
	branch = strings.TrimSpace(parts[0])
	if len(parts) == 1 {
		return branch, "", ""
	}
	upstream = parts[1]
	if idx := strings.Index(upstream, " ["); idx >= 0 {
		summary = strings.TrimSuffix(strings.TrimPrefix(upstream[idx:], " ["), "]")
		upstream = upstream[:idx]
	}
	return strings.TrimSpace(branch), strings.TrimSpace(upstream), strings.TrimSpace(summary)
}

func parseFileLine(line string) FileStatus {
	code := line
	path := ""
	if len(line) >= 2 {
		code = line[:2]
	}
	if len(line) > 3 {
		path = strings.TrimSpace(line[3:])
	}
	if idx := strings.Index(path, " -> "); idx >= 0 {
		path = strings.TrimSpace(path[idx+4:])
	}
	return FileStatus{
		Code: strings.TrimSpace(code),
		Path: path,
	}
}

func (s Status) SummaryLine() string {
	if !s.Available {
		return "No git repository"
	}
	parts := []string{
		fmt.Sprintf("+%d", s.Added),
		fmt.Sprintf("~%d", s.Modified),
		fmt.Sprintf("-%d", s.Deleted),
		fmt.Sprintf("?%d", s.Untracked),
	}
	return strings.Join(parts, "  ")
}
