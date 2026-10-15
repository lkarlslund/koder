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
	"slices"
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
	mu       sync.Mutex
	trace    []string
	report   func(current string, steps []string)
	finished bool
}

func (t *taskTracker) step(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.trace = append(t.trace, text)
	t.reportLocked("")
}

func (t *taskTracker) doing(current string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reportLocked(current)
}

func (t *taskTracker) reportLocked(current string) {
	if t.report != nil && !t.finished {
		t.report(current, slices.Clone(t.trace))
	}
}

// finish stops reporting; the task has returned.
func (t *taskTracker) finish() {
	t.mu.Lock()
	t.finished = true
	t.mu.Unlock()
}

// steps returns the finished steps.
func (t *taskTracker) steps() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.trace)
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
	cfg, resolveDecider, resolveExtractor := m.cfg, m.deciders, m.extractors
	m.mu.Unlock()
	tracker := &taskTracker{report: request.Progress}
	defer tracker.finish()
	if cfg.TaskEngine == "" || cfg.TaskEngine == "obscura" {
		if executable, lookupErr := exec.LookPath("obscura"); lookupErr == nil {
			var result browserapi.TaskResult
			var taskErr error
			if outcome == TaskOutcomeInformation {
				// The extractor reads each page anyway, so it also picks the
				// links; the decider only steps in without one.
				extractor, extractorNote := taskExtractor(ctx, resolveExtractor, chat)
				tracker.step(extractorNote)
				var decider Decider
				if extractor == nil {
					decider = m.findTaskDecider(ctx, resolveDecider, tracker)
				}
				result, taskErr = m.runInformationTask(ctx, executable, decider, extractor, cfg.TaskMaxSteps, goal, start, tracker)
			} else {
				result, taskErr = m.runObscuraTask(ctx, executable, m.findTaskDecider(ctx, resolveDecider, tracker), cfg.TaskMaxSteps, goal, start, tracker)
			}
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

// findTaskDecider resolves the task's decider and records the choice.
func (m *Manager) findTaskDecider(ctx context.Context, resolve DeciderResolver, tracker *taskTracker) Decider {
	tracker.doing("Finding a model for decisions")
	decider, note := taskDecider(ctx, resolve)
	tracker.step(note)
	return decider
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
				result.Status, result.SourceURL, result.File, result.Trace = "completed", current.URL, &binary, tracker.steps()
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
		links = normalizeTaskLinks(current.URL, links, visited, downloadLinkHint, linkScope{}, taskCandidateLimit)
		frontier = mergeTaskFrontier(frontier, m.scoreTaskLinks(ctx, decider, goal, current.URL, links, tracker), maxSteps*taskCandidateLimit)
	}
	result.Trace = tracker.steps()
	return result, errors.New("no verified download was found within the lightweight browsing limit")
}

const (
	// informationBatch is how many pages an information task reads at once.
	informationBatch = 3
	// informationMinText is the least readable text a page needs to be
	// judged; less is a menu, a consent wall or an empty shell.
	informationMinText = 200
	// informationPageText and informationResultText bound the text one page
	// and the whole result return to the caller when no model pulls facts
	// out of the pages.
	informationPageText   = 12 << 10
	informationResultText = 48 << 10
	// informationEnoughPages ends a task once this many pages gave facts.
	informationEnoughPages = 4
	// informationLinkCandidates is how many of a page's links, filtered and
	// ranked by keywords, the decider judges.
	informationLinkCandidates = 8
	// informationLinkChoices is how many of a page's links the extractor
	// chooses from, and informationFollow how many it picks.
	informationLinkChoices = 40
	informationFollow      = 3
	// informationFoundFacts bounds the facts found so far that the
	// extractor sees when it picks links.
	informationFoundFacts = 6000
)

// runInformationTask reads pages from the start URL outwards within its
// site. The extractor pulls the facts for the goal out of each page and
// picks the links to follow for what is still missing. Without an
// extractor, the decider judges how much each page helps and which links to
// follow, and the task returns the most relevant pages' text.
func (m *Manager) runInformationTask(ctx context.Context, executable string, decider Decider, extractor Extractor, maxSteps int, goal string, start *url.URL, tracker *taskTracker) (browserapi.TaskResult, error) {
	if maxSteps <= 0 {
		maxSteps = 8
	}
	storage, err := os.MkdirTemp("", "koder-obscura-task-")
	if err != nil {
		return browserapi.TaskResult{}, fmt.Errorf("create Obscura task storage: %w", err)
	}
	defer func() { _ = os.RemoveAll(storage) }()

	result := browserapi.TaskResult{Status: "incomplete", Backend: "obscura"}
	// A start page that does not exist is reported at once; models
	// sometimes guess article URLs.
	if status, statusErr := obscuraStatus(ctx, executable, start.String()); statusErr == nil && (status == http.StatusNotFound || status == http.StatusGone) {
		tracker.step(fmt.Sprintf("Skipped %s: it is %s (HTTP %d)", start.String(), blockedMissing, status))
		result.Status, result.SourceURL, result.Trace = "completed", start.String(), tracker.steps()
		return result, nil
	}
	// The extractor picks links knowing the goal, so it may follow a page's
	// sources to other sites; a decider stays within the start site.
	scope := linkScope{site: siteOf(start.Hostname()), otherSites: extractor != nil}
	frontier := []taskLink{{URL: canonicalURL(start), Label: start.String()}}
	visited := map[string]bool{}
	var pages []browserapi.TaskPage
	withFacts := 0
	// seenFacts holds the facts found so far, so a page repeating them
	// (models copy from the facts they are shown) adds nothing.
	seenFacts := map[string]bool{}
	linkLimit := informationLinkCandidates
	if extractor != nil {
		linkLimit = informationLinkChoices
	}
	// walled records a bot challenge or access wall, which the visible
	// browser may get past.
	walled := false
	for read := 0; read < maxSteps && len(frontier) > 0 && withFacts < informationEnoughPages; {
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
		var readable []readTaskPage
		for index, fetched := range fetchTaskPages(ctx, executable, storage, batch) {
			read++
			if fetched.err != nil {
				tracker.step(fmt.Sprintf("Could not read %s: %v", batch[index], fetched.err))
				continue
			}
			page := readTaskPage{page: fetched.page}
			if page.page.Blocked != "" {
				tracker.step(fmt.Sprintf("Skipped %s: it is %s, not the page", page.page.URL, page.page.Blocked))
				walled = walled || page.page.Blocked != blockedMissing
				continue
			}
			// Links found once the reading budget is spent are never
			// followed, so they are not worth judging.
			if read < maxSteps {
				page.links = normalizeTaskLinks(page.page.URL, page.page.Links, visited, goal, scope, linkLimit)
			}
			readable = append(readable, page)
		}
		// The batch's pages are read concurrently.
		found := foundFacts(pages)
		var wg sync.WaitGroup
		for i := range readable {
			if extractor != nil {
				wg.Go(func() { extractTaskPage(ctx, extractor, goal, found, &readable[i], tracker) })
			} else {
				wg.Go(func() { m.judgeTaskPage(ctx, decider, goal, &readable[i], tracker) })
			}
		}
		wg.Wait()
		for _, page := range readable {
			frontier = mergeTaskFrontier(frontier, page.links, maxSteps*taskCandidateLimit)
			if page.result != nil && page.result.Facts != "" {
				if page.result.Facts = newFacts(page.result.Facts, seenFacts); page.result.Facts == "" {
					tracker.step(fmt.Sprintf("Dropped the facts from %s: all were found before", page.page.URL))
					continue
				}
			}
			if page.result != nil {
				pages = append(pages, *page.result)
				if page.result.Facts != "" {
					withFacts++
				}
			}
		}
	}
	result.Trace = tracker.steps()
	if len(pages) == 0 && walled {
		return result, errors.New("the pages are behind a bot challenge or access wall")
	}
	if len(pages) == 0 && extractor == nil {
		return result, errors.New("no page with readable text was found within the lightweight browsing limit")
	}
	// Without pages, the steps say what each page was.
	result.Status, result.SourceURL = "completed", start.String()
	if extractor == nil {
		result.Pages = mostRelevantPages(pages)
		return result, nil
	}
	result.Pages = pagesWithFacts(pages)
	return result, nil
}

// readTaskPage is a page an information task read, the links on it worth
// following, and what the task made of it.
type readTaskPage struct {
	page   taskPage
	links  []taskLink
	result *browserapi.TaskPage
}

// judgeTaskPage has the decider score a page's links and judge how much
// the page helps the goal.
func (m *Manager) judgeTaskPage(ctx context.Context, decider Decider, goal string, read *readTaskPage, tracker *taskTracker) {
	page := read.page
	if len(read.links) > 0 {
		read.links = m.scoreTaskLinks(ctx, decider, goal, page.URL, read.links, tracker)
	}
	if len(page.Text) < informationMinText {
		tracker.step(fmt.Sprintf("Read %s: no article text, following its links", page.URL))
		return
	}
	tracker.doing("Judging how much " + page.URL + " helps the goal")
	read.result = &browserapi.TaskPage{URL: page.URL, Title: page.Title, Relevance: m.pageRelevance(ctx, decider, goal, page, tracker), Text: page.Data}
	tracker.step(fmt.Sprintf("Read %s: relevance %.2f, %d characters of text", page.URL, read.result.Relevance, len(page.Text)))
}

// extractTaskPage has the extractor pull a page's facts for the goal and
// pick its links to follow. When the extractor fails, the page keeps its
// text so the caller can still read it, and its links are ranked by
// keywords.
func extractTaskPage(ctx context.Context, extractor Extractor, goal, found string, read *readTaskPage, tracker *taskTracker) {
	page := read.page
	if page.Data == "" && len(read.links) == 0 {
		tracker.step(fmt.Sprintf("Read %s: no text and no links to follow", page.URL))
		return
	}
	descriptions := make([]string, len(read.links))
	for i, link := range read.links {
		descriptions[i] = strings.TrimSpace(link.Label + " — " + link.URL)
	}
	tracker.doing("Pulling facts for the goal out of " + page.URL)
	extraction, err := extractor.Extract(ctx, ExtractRequest{
		Goal: goal, PageURL: page.URL, Text: page.Data, Found: found, Links: descriptions, Follow: informationFollow,
	})
	if err != nil {
		read.links = read.links[:min(len(read.links), informationFollow)]
		if len(page.Text) >= informationMinText {
			read.result = &browserapi.TaskPage{URL: page.URL, Title: page.Title, Text: truncateOnLine(page.Data, informationPageText)}
		}
		tracker.step(fmt.Sprintf("Read %s, but pulling out facts failed, so its text is kept: %v", page.URL, err))
		return
	}
	var follow []taskLink
	for rank, index := range extraction.Follow {
		if index >= 0 && index < len(read.links) && !slices.ContainsFunc(follow, func(link taskLink) bool { return link.URL == read.links[index].URL }) {
			link := read.links[index]
			link.Score = float64(len(extraction.Follow) - rank)
			follow = append(follow, link)
		}
	}
	read.links = follow
	held := "nothing for the goal"
	if facts := strings.TrimSpace(extraction.Facts); facts != "" {
		read.result = &browserapi.TaskPage{URL: page.URL, Title: page.Title, Facts: facts}
		held = "found facts for the goal"
	}
	tracker.step(fmt.Sprintf("Read %s: %s, following %d of its %d links", page.URL, held, len(follow), len(descriptions)))
}

// newFacts drops the bullets of a page's facts that were found before and
// records the rest. It returns "" when no bullet is new.
func newFacts(facts string, seen map[string]bool) string {
	var out []string
	fresh := false
	for line := range strings.SplitSeq(facts, "\n") {
		key := factKey(line)
		if key == "" {
			out = append(out, line)
			continue
		}
		if seen[key] {
			continue
		}
		seen[key], fresh = true, true
		out = append(out, line)
	}
	if !fresh {
		return ""
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// factKey identifies a bullet by its words and figures, ignoring markup
// and punctuation. Lines that are not bullets have no key.
func factKey(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "*") && !strings.HasPrefix(line, "•") {
		return ""
	}
	return strings.Join(strings.FieldsFunc(strings.ToLower(line), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '%' && r != '€'
	}), " ")
}

// foundFacts joins the facts pages gave so far, keeping the most recent
// when they run long.
func foundFacts(pages []browserapi.TaskPage) string {
	var parts []string
	for _, page := range pages {
		if page.Facts != "" {
			parts = append(parts, page.Facts)
		}
	}
	found := strings.Join(parts, "\n")
	if len(found) > informationFoundFacts {
		found = found[len(found)-informationFoundFacts:]
		if cut := strings.IndexByte(found, '\n'); cut >= 0 {
			found = found[cut+1:]
		}
	}
	return found
}

// pagesWithFacts returns the pages that gave facts, or kept their text
// because extraction failed, in the order they were read.
func pagesWithFacts(pages []browserapi.TaskPage) []browserapi.TaskPage {
	var out []browserapi.TaskPage
	for _, page := range pages {
		if page.Facts != "" || page.Text != "" {
			out = append(out, page)
		}
	}
	return mostRelevantPages(out)
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
		budget -= len(page.Text) + len(page.Facts)
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
	return browserapi.TaskResult{Status: "needs_browser", Backend: "chrome", SourceURL: tab.URL, Trace: tracker.steps()}, nil
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

// linkScope says which links a task may follow: with a site, links within
// it, minus copies of a page in other languages, and, with otherSites,
// links to other sites too.
type linkScope struct {
	site       string
	otherSites bool
}

// normalizeTaskLinks resolves, canonicalizes and dedupes a page's links,
// drops sharing, account, legal and other junk links and those out of
// scope, and keeps the limit best by keyword relevance to hint.
func normalizeTaskLinks(baseURL string, links []taskLink, visited map[string]bool, hint string, scope linkScope, limit int) []taskLink {
	base, _ := url.Parse(baseURL)
	seen := map[string]bool{}
	out := make([]taskLink, 0, len(links))
	for _, link := range links {
		parsed, err := url.Parse(strings.TrimSpace(link.URL))
		if err != nil || base == nil {
			continue
		}
		parsed = base.ResolveReference(parsed)
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			continue
		}
		if junkLink.MatchString(parsed.String()) || isSocialHost(parsed.Hostname()) ||
			(scope.site != "" && !scope.otherSites && siteOf(parsed.Hostname()) != scope.site) {
			continue
		}
		// A copy of the page in another language repeats what was read.
		sameSite := siteOf(parsed.Hostname()) == siteOf(base.Hostname())
		if language := languageOf(parsed); scope.site != "" && sameSite && language != "" && language != languageOf(base) {
			continue
		}
		canonical := canonicalURL(parsed)
		if canonical == canonicalURL(base) || visited[canonical] || seen[canonical] {
			continue
		}
		seen[canonical] = true
		link.URL = canonical
		link.Score = lexicalRelevance(link.Label+" "+link.URL, hint)
		out = append(out, link)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
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

// taskExtractor resolves the extractor for a chat's information task and
// describes the choice for the trace. Without one, the task returns page
// text instead of facts.
func taskExtractor(ctx context.Context, resolve ExtractorResolver, chat browserapi.Chat) (Extractor, string) {
	if resolve == nil {
		return nil, "No model is available to pull out facts; returning page text"
	}
	extractor, name, err := resolve(ctx, chat)
	if err != nil {
		return nil, "No model is available to pull out facts, returning page text: " + err.Error()
	}
	return extractor, "Pulling out facts with " + name
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

// commonWords are English function words and the words goals use to ask
// for information, which say nothing about where it is.
var commonWords = map[string]bool{}

func init() {
	for word := range strings.FieldsSeq("the and for any are but not all can its has have had was were been with from that this these those " +
		"what which who whom whose when where why how than then there their them they you your our out into onto over under about " +
		"also such only each per may will would should could more most other some very just get got give find list show tell " +
		"extract facts fact information info details detail including include includes etc") {
		commonWords[word] = true
	}
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

// goalWords returns the goal's words worth matching: longer than two
// letters and not a common word, which would match any link or page.
func goalWords(goal string) []string {
	var words []string
	for _, word := range strings.FieldsFunc(strings.ToLower(goal), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(word) > 2 && !commonWords[word] {
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
