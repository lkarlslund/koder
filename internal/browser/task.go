package browser

import (
	"bytes"
	"context"
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
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lkarlslund/koder/internal/browserapi"
)

const taskCandidateLimit = 24

type taskLink struct {
	URL   string
	Label string
	Score float64
}

// taskTracker records a task's trace and reports it live: finished steps
// go into the trace, and the current activity says what the task is doing or
// waiting on.
type taskTracker struct {
	trace    []string
	report   func(current string, steps []string)
	finished bool
}

func (t *taskTracker) step(text string) {
	t.trace = append(t.trace, text)
	t.doing("")
}

func (t *taskTracker) doing(current string) {
	if t.report != nil && !t.finished {
		t.report(current, t.trace)
	}
}

// Task outcomes: a download finishes on a verified file; information
// finishes with the most relevant pages' text for the caller to use.
const (
	TaskOutcomeDownload    = "download"
	TaskOutcomeInformation = "information"
)

// Task runs a bounded, goal-oriented public browsing job. Obscura is deliberately
// ephemeral: authenticated and otherwise incompatible work is handed to the
// managed visible browser instead of silently weakening its profile semantics.
func (m *Manager) Task(ctx context.Context, chat browserapi.Chat, request browserapi.TaskRequest) (browserapi.TaskResult, error) {
	goal := strings.TrimSpace(request.Goal)
	if goal == "" {
		return browserapi.TaskResult{}, errors.New("browser task goal is required")
	}
	outcome := strings.TrimSpace(request.Outcome)
	if outcome == "" {
		outcome = TaskOutcomeDownload
	}
	if outcome != TaskOutcomeDownload && outcome != TaskOutcomeInformation {
		return browserapi.TaskResult{}, fmt.Errorf("browser task outcome must be %s or %s", TaskOutcomeDownload, TaskOutcomeInformation)
	}
	start, err := taskURL(request.StartURL)
	if err != nil {
		return browserapi.TaskResult{}, err
	}
	m.mu.Lock()
	cfg, resolveDecider := m.cfg, m.deciders
	m.mu.Unlock()
	tracker := &taskTracker{report: request.Progress}
	defer func() { tracker.finished = true }()
	if cfg.TaskEngine == "" || cfg.TaskEngine == "obscura" {
		if executable, lookupErr := exec.LookPath("obscura"); lookupErr == nil {
			tracker.doing("Finding a model for decisions")
			decider, deciderNote := taskDecider(ctx, resolveDecider)
			tracker.step(deciderNote)
			run := m.runObscuraTask
			if outcome == TaskOutcomeInformation {
				run = m.runInformationTask
			}
			result, taskErr := run(ctx, executable, decider, cfg.TaskMaxSteps, goal, start, tracker)
			if taskErr == nil && result.Status == "completed" {
				return result, nil
			}
			if taskErr != nil {
				tracker.step("Obscura could not complete the task: " + taskErr.Error())
			}
		}
	}
	return m.handoffTaskToChrome(ctx, chat, start.String(), tracker)
}

func (m *Manager) runObscuraTask(ctx context.Context, executable string, decider Decider, maxSteps int, goal string, start *url.URL, tracker *taskTracker) (browserapi.TaskResult, error) {
	if maxSteps <= 0 {
		maxSteps = 8
	}
	storage, err := os.MkdirTemp("", "koder-obscura-task-")
	if err != nil {
		return browserapi.TaskResult{}, fmt.Errorf("create Obscura task storage: %w", err)
	}
	defer func() { _ = os.RemoveAll(storage) }()

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

		if likelyDownload(current) {
			tracker.doing(fmt.Sprintf("Downloading %s (page %d of at most %d)", current.URL, step+1, maxSteps))
			binary, binaryErr := obscuraDownload(ctx, executable, storage, current)
			if binaryErr == nil && isUsefulDownload(binary, goal) {
				tracker.step(fmt.Sprintf("Examined %s", current.URL))
				tracker.step(fmt.Sprintf("Downloaded and verified %s (%d bytes)", binary.Name, len(binary.Data)))
				result.Status, result.SourceURL, result.File, result.Trace = "completed", current.URL, &binary, tracker.trace
				return result, nil
			}
		}

		tracker.doing(fmt.Sprintf("Fetching %s (page %d of at most %d)", current.URL, step+1, maxSteps))
		links, fetchErr := obscuraLinks(ctx, executable, storage, current.URL)
		tracker.step(fmt.Sprintf("Examined %s", current.URL))
		if fetchErr != nil {
			tracker.step("Lightweight fetch failed: " + fetchErr.Error())
			continue
		}
		links = normalizeTaskLinks(current.URL, links, visited, downloadLinkHint)
		frontier = mergeTaskFrontier(frontier, m.scoreTaskLinks(ctx, decider, goal, current.URL, links, tracker), maxSteps*taskCandidateLimit)
	}
	result.Trace = tracker.trace
	return result, errors.New("no verified download was found within the lightweight browsing limit")
}

const (
	// informationBatch is how many pages an information task reads at once.
	informationBatch = 3
	// informationMinText is the least readable text a page needs to be
	// judged; less is a menu, a consent wall or an empty shell.
	informationMinText = 200
	// informationPageText and informationResultText bound the text one page
	// and the whole result return to the caller.
	informationPageText   = 12 << 10
	informationResultText = 48 << 10
)

// runInformationTask reads pages from the start URL outwards, keeps the
// readable text of each, has the decider judge how much it helps the goal,
// and follows the links it rates best. It returns the most relevant pages.
func (m *Manager) runInformationTask(ctx context.Context, executable string, decider Decider, maxSteps int, goal string, start *url.URL, tracker *taskTracker) (browserapi.TaskResult, error) {
	if maxSteps <= 0 {
		maxSteps = 8
	}
	storage, err := os.MkdirTemp("", "koder-obscura-task-")
	if err != nil {
		return browserapi.TaskResult{}, fmt.Errorf("create Obscura task storage: %w", err)
	}
	defer func() { _ = os.RemoveAll(storage) }()

	result := browserapi.TaskResult{Status: "incomplete", Backend: "obscura"}
	frontier := []taskLink{{URL: start.String(), Label: start.String()}}
	visited := map[string]bool{}
	var pages []browserapi.TaskPage
	for read := 0; read < maxSteps && len(frontier) > 0; {
		var batch []string
		for len(frontier) > 0 && len(batch) < min(informationBatch, maxSteps-read) {
			next := frontier[0].URL
			frontier = frontier[1:]
			if !visited[next] {
				visited[next] = true
				batch = append(batch, next)
			}
		}
		if len(batch) == 0 {
			break
		}
		tracker.doing("Reading " + strings.Join(batch, ", "))
		for index, fetched := range fetchTaskPages(ctx, executable, storage, batch) {
			read++
			if fetched.err != nil {
				tracker.step(fmt.Sprintf("Could not read %s: %v", batch[index], fetched.err))
				continue
			}
			page := fetched.page
			if len(page.Text) >= informationMinText {
				tracker.doing("Judging how much " + page.URL + " helps the goal")
				relevance := m.pageRelevance(ctx, decider, goal, page, tracker)
				pages = append(pages, browserapi.TaskPage{URL: page.URL, Title: page.Title, Relevance: relevance, Text: page.Text})
				tracker.step(fmt.Sprintf("Read %s: relevance %.2f, %d characters of text", page.URL, relevance, len(page.Text)))
			} else {
				tracker.step(fmt.Sprintf("Read %s: no article text, following its links", page.URL))
			}
			links := normalizeTaskLinks(page.URL, page.Links, visited, goal)
			frontier = mergeTaskFrontier(frontier, m.scoreTaskLinks(ctx, decider, goal, page.URL, links, tracker), maxSteps*taskCandidateLimit)
		}
	}
	result.Trace = tracker.trace
	if len(pages) == 0 {
		return result, errors.New("no page with readable text was found within the lightweight browsing limit")
	}
	result.Status, result.SourceURL, result.Pages = "completed", start.String(), mostRelevantPages(pages)
	return result, nil
}

type fetchedTaskPage struct {
	page taskPage
	err  error
}

// fetchTaskPages reads pages concurrently, each in its own Obscura storage
// so parallel fetches do not share a cookie jar file.
func fetchTaskPages(ctx context.Context, executable, storage string, urls []string) []fetchedTaskPage {
	out := make([]fetchedTaskPage, len(urls))
	var wg sync.WaitGroup
	for index, rawURL := range urls {
		wg.Go(func() {
			dir := filepath.Join(storage, fmt.Sprint(index))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				out[index].err = err
				return
			}
			out[index].page, out[index].err = obscuraPage(ctx, executable, dir, rawURL)
		})
	}
	wg.Wait()
	return out
}

// mostRelevantPages orders pages by relevance and trims their text to the
// result budget, dropping pages once it is spent.
func mostRelevantPages(pages []browserapi.TaskPage) []browserapi.TaskPage {
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Relevance > pages[j].Relevance })
	budget := informationResultText
	var out []browserapi.TaskPage
	for _, page := range pages {
		// A sliver of text is no use; stop once the budget cannot hold a
		// page worth judging.
		if budget < informationMinText {
			break
		}
		page.Text = truncateOnLine(page.Text, min(informationPageText, budget))
		budget -= len(page.Text)
		out = append(out, page)
	}
	return out
}

// truncateOnLine cuts text to at most limit bytes, at a line break when one
// is in the second half, else at a rune boundary.
func truncateOnLine(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	if cut := strings.LastIndexByte(text[:limit], '\n'); cut > limit/2 {
		return text[:cut]
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

func (m *Manager) handoffTaskToChrome(ctx context.Context, chat browserapi.Chat, start string, tracker *taskTracker) (browserapi.TaskResult, error) {
	tracker.doing("Waiting for the managed browser to open " + start)
	tab, err := m.NewTab(ctx, chat, start)
	if err != nil {
		return browserapi.TaskResult{}, fmt.Errorf("start visible browser fallback: %w", err)
	}
	tracker.step("Continued in the managed browser because the lightweight backend did not complete the goal")
	return browserapi.TaskResult{Status: "needs_browser", Backend: "chrome", SourceURL: tab.URL, Trace: tracker.trace}, nil
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
	defer func() { _ = os.Remove(path) }()
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

// downloadLinkHint pre-ranks links for download tasks before the decider
// sees them.
const downloadLinkHint = "manual pdf download documentation guide support"

// normalizeTaskLinks resolves and dedupes a page's links and keeps the
// taskCandidateLimit best by keyword relevance to hint.
func normalizeTaskLinks(baseURL string, links []taskLink, visited map[string]bool, hint string) []taskLink {
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
		link.Score = lexicalRelevance(link.Label+" "+link.URL, hint)
		out = append(out, link)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > taskCandidateLimit {
		out = out[:taskCandidateLimit]
	}
	return out
}

// taskDecider resolves the task's decider and describes the choice for the
// trace. Without one, links and pages are judged by keywords.
func taskDecider(ctx context.Context, resolve DeciderResolver) (Decider, string) {
	if resolve == nil {
		return nil, "No model is available for decisions; judging by keywords"
	}
	decider, name, err := resolve(ctx)
	if err != nil {
		return nil, "No model is available for decisions, judging by keywords: " + err.Error()
	}
	return decider, "Making decisions with " + name
}

// scoreTaskLinks scores links with the decider, or by keywords without one
// or when it fails, and returns them best first.
func (m *Manager) scoreTaskLinks(ctx context.Context, decider Decider, goal, pageURL string, links []taskLink, tracker *taskTracker) []taskLink {
	if decider != nil && len(links) > 1 {
		tracker.doing(fmt.Sprintf("Asking the model to rank %d links from %s", len(links), pageURL))
	}
	if err := rankTaskLinks(ctx, decider, goal, pageURL, links); err != nil {
		tracker.step("Link ranking failed, ranked links by keywords: " + err.Error())
	}
	return links
}

// rankTaskLinks orders links by the decider's scores, falling back to
// keyword relevance when there is no decider or it fails.
func rankTaskLinks(ctx context.Context, decider Decider, goal, pageURL string, links []taskLink) error {
	var err error
	if decider != nil && len(links) > 1 {
		descriptions := make([]string, len(links))
		for i, link := range links {
			descriptions[i] = strings.TrimSpace(link.Label + " — " + link.URL)
		}
		var scores []float64
		if scores, err = decider.ScoreLinks(ctx, goal, pageURL, descriptions); err == nil && len(scores) != len(links) {
			err = fmt.Errorf("model scored %d of %d links", len(scores), len(links))
		}
		if err == nil {
			for i := range links {
				links[i].Score += scores[i] * 100
			}
		}
	}
	if decider == nil || err != nil {
		for i := range links {
			links[i].Score += lexicalRelevance(links[i].Label+" "+links[i].URL, goal)
		}
	}
	sort.SliceStable(links, func(i, j int) bool { return links[i].Score > links[j].Score })
	return err
}

// pageRelevance has the decider judge a page, falling back to the share of
// goal words the page text contains.
func (m *Manager) pageRelevance(ctx context.Context, decider Decider, goal string, page taskPage, tracker *taskTracker) float64 {
	if decider != nil {
		score, err := decider.PageRelevance(ctx, goal, page.URL, page.Text)
		if err == nil {
			return min(max(score, 0), 1)
		}
		tracker.step("Relevance judgment failed, judging " + page.URL + " by keywords: " + err.Error())
	}
	words := goalWords(goal)
	if len(words) == 0 {
		return 0
	}
	return lexicalRelevance(page.Text, goal) / float64(len(words))
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
	for _, word := range goalWords(goal) {
		if strings.Contains(value, word) {
			score++
		}
	}
	return score
}

// goalWords returns the goal's words worth matching: longer than two letters.
func goalWords(goal string) []string {
	var words []string
	for _, word := range strings.FieldsFunc(strings.ToLower(goal), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(word) > 2 {
			words = append(words, word)
		}
	}
	return words
}

func taskURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("start_url must be an absolute HTTP or HTTPS URL")
	}
	return parsed, nil
}
