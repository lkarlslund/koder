package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/lkarlslund/koder/internal/domain"
)

type Request struct {
	Tool domain.ToolKind
	Args map[string]string
}

type Result struct {
	Tool     domain.ToolKind
	Output   string
	DiffText string
}

type Registry struct {
	workdir string
	client  *http.Client
}

func NewRegistry(workdir string) *Registry {
	return &Registry{workdir: workdir, client: &http.Client{}}
}

func (r *Registry) Execute(ctx context.Context, req Request) (Result, error) {
	switch req.Tool {
	case domain.ToolKindRead:
		return r.read(req.Args["path"])
	case domain.ToolKindGlob:
		return r.glob(req.Args["pattern"])
	case domain.ToolKindGrep:
		return r.grep(ctx, req.Args["pattern"])
	case domain.ToolKindBash:
		return r.bash(ctx, req.Args["command"])
	case domain.ToolKindApplyPatch:
		return r.applyPatch(req.Args["path"], req.Args["content"])
	case domain.ToolKindTask:
		return Result{Tool: req.Tool, Output: req.Args["body"]}, nil
	case domain.ToolKindQuestion:
		return Result{Tool: req.Tool, Output: req.Args["question"]}, nil
	case domain.ToolKindWebFetch:
		return r.webFetch(ctx, req.Args["url"])
	case domain.ToolKindWebSearch:
		return Result{}, errors.New("websearch is not implemented yet")
	default:
		return Result{}, fmt.Errorf("unsupported tool %q", req.Tool)
	}
}

func (r *Registry) read(path string) (Result, error) {
	if path == "" {
		return Result{}, errors.New("path is empty")
	}
	data, err := os.ReadFile(filepath.Join(r.workdir, path))
	if err != nil {
		return Result{}, err
	}
	return Result{Tool: domain.ToolKindRead, Output: string(data)}, nil
}

func (r *Registry) glob(pattern string) (Result, error) {
	if pattern == "" {
		pattern = "*"
	}
	matches, err := filepath.Glob(filepath.Join(r.workdir, pattern))
	if err != nil {
		return Result{}, err
	}
	for i, item := range matches {
		rel, relErr := filepath.Rel(r.workdir, item)
		if relErr == nil {
			matches[i] = rel
		}
	}
	return Result{Tool: domain.ToolKindGlob, Output: strings.Join(matches, "\n")}, nil
}

func (r *Registry) grep(ctx context.Context, pattern string) (Result, error) {
	if pattern == "" {
		return Result{}, errors.New("pattern is empty")
	}
	if _, err := exec.LookPath("rg"); err == nil {
		cmd := exec.CommandContext(ctx, "rg", "-n", pattern, ".")
		cmd.Dir = r.workdir
		output, err := cmd.CombinedOutput()
		if err != nil && len(output) == 0 {
			return Result{}, err
		}
		return Result{Tool: domain.ToolKindGrep, Output: string(output)}, nil
	}
	return Result{}, errors.New("rg is required for grep fallback in this build")
}

func (r *Registry) bash(ctx context.Context, command string) (Result, error) {
	if command == "" {
		return Result{}, errors.New("command is empty")
	}
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = r.workdir
	output, err := cmd.CombinedOutput()
	return Result{Tool: domain.ToolKindBash, Output: string(output)}, err
}

func (r *Registry) applyPatch(path, content string) (Result, error) {
	if path == "" {
		return Result{}, errors.New("path is empty")
	}
	fullPath := filepath.Join(r.workdir, path)
	before, _ := os.ReadFile(fullPath)
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return Result{}, err
	}
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(string(before), content, false)
	return Result{
		Tool:     domain.ToolKindApplyPatch,
		Output:   "patched " + path,
		DiffText: dmp.DiffPrettyText(diffs),
	}, nil
}

func (r *Registry) webFetch(ctx context.Context, rawURL string) (Result, error) {
	if rawURL == "" {
		return Result{}, errors.New("url is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{}, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, resp.Body, 16*1024); err != nil && !errors.Is(err, io.EOF) {
		return Result{}, err
	}
	return Result{Tool: domain.ToolKindWebFetch, Output: buf.String()}, nil
}
