package workspace

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/lkarlslund/koder/internal/agents"
)

type FileStatus struct {
	Code      string
	Path      string
	Additions int
	Deletions int
}

type Status struct {
	Available      bool
	ProjectRoot    string
	AgentsChecksum string
	AgentsFiles    int
	Branch         string
	Upstream       string
	Summary        string
	Files          []FileStatus
	Added          int
	Modified       int
	Deleted        int
	Untracked      int
}

func Snapshot(ctx context.Context, dir string) (Status, error) {
	projectRoot := agents.FindProjectRoot(dir)
	status := Status{ProjectRoot: projectRoot}
	snapshot, discoverErr := agents.NewManager("", "").Discover(ctx, dir)
	if discoverErr == nil {
		if snapshot.ProjectRoot != "" {
			status.ProjectRoot = snapshot.ProjectRoot
		}
		status.AgentsChecksum = snapshot.Checksum
		status.AgentsFiles = len(snapshot.Files)
	}
	statusCmd := exec.CommandContext(ctx, "git", "status", "--short", "--branch")
	statusCmd.Dir = projectRoot
	statusOutput, err := statusCmd.Output()
	if err != nil {
		return status, nil
	}

	numstatCmd := exec.CommandContext(ctx, "git", "diff", "--numstat", "--find-renames", "HEAD")
	numstatCmd.Dir = projectRoot
	numstatOutput, err := numstatCmd.Output()
	if err != nil {
		numstatOutput = nil
	}

	parsed := parseStatus(string(statusOutput), string(numstatOutput))
	parsed.ProjectRoot = status.ProjectRoot
	parsed.AgentsChecksum = status.AgentsChecksum
	parsed.AgentsFiles = status.AgentsFiles
	return parsed, nil
}

func parseStatus(raw string, numstatRaw string) Status {
	status := Status{Available: true}
	numstats := parseNumstat(numstatRaw)
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
		if stat, ok := numstats[file.Path]; ok {
			file.Additions = stat.Additions
			file.Deletions = stat.Deletions
		}
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

func parseNumstat(raw string) map[string]FileStatus {
	stats := make(map[string]FileStatus)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		path := fields[2]
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		stats[path] = FileStatus{
			Path:      path,
			Additions: parseNumstatCount(fields[0]),
			Deletions: parseNumstatCount(fields[1]),
		}
	}
	return stats
}

func parseNumstatCount(raw string) int {
	if raw == "-" {
		return 0
	}
	value := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0
		}
		value = value*10 + int(ch-'0')
	}
	return value
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
