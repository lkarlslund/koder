package version

import (
	"os"
	"runtime"
	"strings"
	"time"
)

var (
	Name      = "koder"
	Version   = "0.1.0"
	Commit    = "dev"
	Dirty     = "unknown"
	BuildTime = "unknown"
	startedAt = time.Now().UTC()

	executableFunc = os.Executable
)

type Info struct {
	Name           string    `json:"name"`
	Version        string    `json:"version"`
	Commit         string    `json:"commit"`
	Dirty          string    `json:"dirty"`
	BuildTime      string    `json:"build_time"`
	GoVersion      string    `json:"go_version"`
	ExecutablePath string    `json:"executable_path"`
	PID            int       `json:"pid"`
	StartedAt      time.Time `json:"started_at"`
}

func Current() Info {
	return Info{
		Name:           Name,
		Version:        Version,
		Commit:         strings.TrimSpace(Commit),
		Dirty:          strings.TrimSpace(Dirty),
		BuildTime:      strings.TrimSpace(BuildTime),
		GoVersion:      runtime.Version(),
		ExecutablePath: executablePath(),
		PID:            os.Getpid(),
		StartedAt:      startedAt,
	}
}

func executablePath() string {
	path, err := executableFunc()
	if err != nil {
		return ""
	}
	return path
}
