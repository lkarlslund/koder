package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/lkarlslund/koder/internal/browserapi"
)

const taskCandidateLimit = 24

type taskLink struct {
	URL   string
	Label string
	Score float64
}

// Task runs a bounded, goal-oriented public browsing job. Obscura is deliberately
// ephemeral: authenticated and otherwise incompatible work is handed to the
// managed visible browser instead of silently weakening its profile semantics.
func (m *Manager) Task(ctx context.Context, chat browserapi.Chat, request browserapi.TaskRequest) (browserapi.TaskResult, error) {
	goal := strings.TrimSpace(request.Goal)
	if goal == "" {
		return browserapi.TaskResult{}, errors.New("browser task goal is required")
	}
	start, err := taskURL(request.StartURL)
	if err != nil {
		return browserapi.TaskResult{}, err
	}
	m.mu.Lock()
	cfg := m.cfg
	m.mu.Unlock()
	if cfg.TaskEngine == "" || cfg.TaskEngine == "obscura" {
		if executable, lookupErr := exec.LookPath("obscura"); lookupErr == nil {
			result, taskErr := m.runObscuraTask(ctx, executable, cfg.TaskDecisionURL, cfg.TaskMaxSteps, goal, start)
			if taskErr == nil && result.Status == "completed" {
				return result, nil
			}
			trace := result.Trace
			if taskErr != nil {
				trace = append(trace, "Obscura could not complete the task: "+taskErr.Error())
			}
			return m.handoffTaskToChrome(ctx, chat, goal, start.String(), trace)
		}
	}
	return m.handoffTaskToChrome(ctx, chat, goal, start.String(), nil)
}

func (m *Manager) runObscuraTask(ctx context.Context, executable, decisionURL string, maxSteps int, goal string, start *url.URL) (browserapi.TaskResult, error) {
	if maxSteps <= 0 {
		maxSteps = 8
	}
	storage, err := os.MkdirTemp("", "koder-obscura-task-")
	if err != nil {
		return browserapi.TaskResult{}, fmt.Errorf("create Obscura task storage: %w", err)
	}
	defer os.RemoveAll(storage)

	result := browserapi.TaskResult{Status: "incomplete", Backend: "obscura"}
	frontier := []taskLink{{URL: start.String(), Label: start.String()}}
	visited := map[string]bool{}
	for step := 0; step < maxSteps && len(frontier) > 0; step++ {
		current := frontier[0]
		frontier = frontier[1:]
		if visited[current.URL] {
			step--
			continue
		}
		visited[current.URL] = true
		result.Trace = append(result.Trace, fmt.Sprintf("Examined %s", current.URL))

		if likelyDownload(current) {
			binary, binaryErr := obscuraDownload(ctx, executable, storage, current)
			if binaryErr == nil && isUsefulDownload(binary, goal) {
				result.Status, result.SourceURL, result.File = "completed", current.URL, &binary
				result.Trace = append(result.Trace, fmt.Sprintf("Downloaded and verified %s (%d bytes)", binary.Name, len(binary.Data)))
				return result, nil
			}
		}

		links, fetchErr := obscuraLinks(ctx, executable, storage, current.URL)
		if fetchErr != nil {
			result.Trace = append(result.Trace, "Lightweight fetch failed: "+fetchErr.Error())
			continue
		}
		links = normalizeTaskLinks(current.URL, links, visited)
		if len(links) == 0 {
			continue
		}
		rankTaskLinks(ctx, decisionURL, goal, current.URL, links)
		frontier = mergeTaskFrontier(frontier, links, maxSteps*taskCandidateLimit)
	}
	return result, errors.New("no verified download was found within the lightweight browsing limit")
}

func (m *Manager) handoffTaskToChrome(ctx context.Context, chat browserapi.Chat, goal, start string, trace []string) (browserapi.TaskResult, error) {
	tab, err := m.NewTab(ctx, chat, start)
	if err != nil {
		return browserapi.TaskResult{}, fmt.Errorf("start visible browser fallback: %w", err)
	}
	trace = append(trace, "Continued in the managed browser because the lightweight backend did not complete the goal")
	return browserapi.TaskResult{Status: "needs_browser", Backend: "chrome", SourceURL: tab.URL, Trace: trace}, nil
}

func obscuraLinks(ctx context.Context, executable, storage, rawURL string) ([]taskLink, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, executable, "fetch", "--quiet", "--dump", "links", "--storage-dir", storage, rawURL)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var links []taskLink
	for _, line := range strings.Split(stdout.String(), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		link := taskLink{URL: parts[0]}
		if len(parts) == 2 {
			link.Label = strings.TrimSpace(parts[1])
		}
		links = append(links, link)
	}
	return links, nil
}

func obscuraDownload(ctx context.Context, executable, storage string, link taskLink) (browserapi.Binary, error) {
	file, err := os.CreateTemp("", "koder-obscura-download-")
	if err != nil {
		return browserapi.Binary{}, err
	}
	path := file.Name()
	_ = file.Close()
	defer os.Remove(path)
	commandCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, executable, "fetch", "--quiet", "--dump", "original", "--storage-dir", storage, "--output", path, link.URL)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		return browserapi.Binary{}, fmt.Errorf("download with Obscura: %w: %s", runErr, strings.TrimSpace(string(output)))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return browserapi.Binary{}, err
	}
	if len(data) == 0 || len(data) > maxBinarySize {
		return browserapi.Binary{}, fmt.Errorf("download size %d is outside the supported range", len(data))
	}
	parsed, _ := url.Parse(link.URL)
	name := filepath.Base(parsed.Path)
	if name == "." || name == "/" || name == "" {
		name = "download"
	}
	mimeType := http.DetectContentType(data)
	if extension, _ := mime.ExtensionsByType(mimeType); filepath.Ext(name) == "" && len(extension) > 0 {
		name += extension[0]
	}
	return browserapi.Binary{Name: name, MIME: mimeType, Data: data}, nil
}

func normalizeTaskLinks(baseURL string, links []taskLink, visited map[string]bool) []taskLink {
	base, _ := url.Parse(baseURL)
	seen := map[string]bool{}
	out := make([]taskLink, 0, len(links))
	for _, link := range links {
		parsed, err := url.Parse(strings.TrimSpace(link.URL))
		if err != nil || base == nil {
			continue
		}
		parsed = base.ResolveReference(parsed)
		parsed.Fragment = ""
		if (parsed.Scheme != "http" && parsed.Scheme != "https") || visited[parsed.String()] || seen[parsed.String()] {
			continue
		}
		seen[parsed.String()] = true
		link.URL = parsed.String()
		link.Score = lexicalRelevance(link.Label+" "+link.URL, "manual pdf download documentation guide support")
		out = append(out, link)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > taskCandidateLimit {
		out = out[:taskCandidateLimit]
	}
	return out
}

func rankTaskLinks(ctx context.Context, endpoint, goal, page string, links []taskLink) {
	if endpoint == "" || len(links) < 2 {
		for i := range links {
			links[i].Score += lexicalRelevance(links[i].Label+" "+links[i].URL, goal)
		}
		return
	}
	criteria := make(map[string]string, len(links))
	for i, link := range links {
		criteria[fmt.Sprintf("c%d", i)] = strings.TrimSpace(link.Label + " — " + link.URL)
	}
	payload := map[string]any{"model": "jev-latest", "state": map[string]string{"goal": goal, "url": page}, "questions": map[string]any{"next": map[string]any{"type": "choice", "instructions": "Rank the links by how likely they are to complete the browser task. Prefer direct requested files and official product documentation.", "criteria": criteria}}}
	body, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	var decoded struct {
		Answers map[string]struct {
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
	}
	if response.StatusCode/100 != 2 || json.NewDecoder(response.Body).Decode(&decoded) != nil {
		return
	}
	for i := range links {
		links[i].Score += decoded.Answers["next"].Probabilities[fmt.Sprintf("c%d", i)] * 100
	}
	sort.SliceStable(links, func(i, j int) bool { return links[i].Score > links[j].Score })
}

func mergeTaskFrontier(frontier, links []taskLink, limit int) []taskLink {
	frontier = append(frontier, links...)
	sort.SliceStable(frontier, func(i, j int) bool { return frontier[i].Score > frontier[j].Score })
	if len(frontier) > limit {
		frontier = frontier[:limit]
	}
	return frontier
}

func likelyDownload(link taskLink) bool {
	value := strings.ToLower(link.URL + " " + link.Label)
	return strings.Contains(value, ".pdf") || strings.Contains(value, "download") || strings.Contains(value, "manual") || strings.Contains(value, "guide")
}

func isUsefulDownload(binary browserapi.Binary, goal string) bool {
	if len(binary.Data) < 5 {
		return false
	}
	if bytes.HasPrefix(binary.Data, []byte("%PDF-")) {
		return true
	}
	if strings.Contains(strings.ToLower(goal), "pdf") {
		return false
	}
	return !strings.HasPrefix(strings.ToLower(binary.MIME), "text/html")
}

func lexicalRelevance(value, goal string) float64 {
	value = strings.ToLower(value)
	score := 0.0
	for _, word := range strings.FieldsFunc(strings.ToLower(goal), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(word) > 2 && strings.Contains(value, word) {
			score++
		}
	}
	return score
}

func taskURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("start_url must be an absolute HTTP or HTTPS URL")
	}
	return parsed, nil
}
