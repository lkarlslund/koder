package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	htmlpkg "html"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// staticRenderDelay is how long a page may render before it is also
// rendered without its scripts.
var staticRenderDelay = 6 * time.Second

// blockedMissing is the reason a page is a stand-in for one that does not
// exist, which, unlike a bot challenge, no browser gets past.
const blockedMissing = "a missing page"

const (
	// staticArticleText is how much readable text a page rendered without
	// its scripts needs to be used instead of waiting for the full render.
	staticArticleText = 2000
	// minReadableLine is the shortest line of prose a page's readable text
	// keeps; shorter lines are navigation, buttons and captions.
	minReadableLine = 60
	// maxReadableText caps a page's readable text; beyond it is site
	// furniture, comments or endless listings.
	maxReadableText = 64 << 10
)

var (
	markdownImage = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	// markdownLink matches a link whose target may hold one level of
	// parentheses, as Wikipedia's do.
	markdownLink  = regexp.MustCompile(`\[([^\]]*)\]\(((?:[^()\s]|\([^()\s]*\))+)(?:\s+"[^"]*")?\)`)
	markdownTitle = regexp.MustCompile(`(?m)^#\s+(.+)$`)
	htmlDocument  = regexp.MustCompile(`(?i)<(!doctype html|html)`)
	scriptElement = regexp.MustCompile(`(?is)<script\b.*?</script\s*>`)
	baseElement   = regexp.MustCompile(`(?i)<base\s`)
	headElement   = regexp.MustCompile(`(?i)<head\b[^>]*>`)
	// consentLine matches cookie banners and consent-manager declarations,
	// which can run to thousands of lines of seemingly readable text.
	consentLine = regexp.MustCompile(`(?i)cookie|consent|gdpr|storage duration|local storage|personal data|` +
		`personali[sz]ed (ads|content)|ads? (measurement|personali[sz]ation)|fingerprint|technical storage|subpoena|` +
		`\d+ partners|our partners|tracking technolog|analytics partners|(visitors?|users?)'? behaviou?r|` +
		`website (owner|operator)|enable javascript`)
	// consentRow matches a consent manager's cookie table row: a cookie
	// name glued to its description.
	consentRow = regexp.MustCompile(`^\S{2,}( \[x\d+\])?\*\*`)
	// blockedPage matches pages that stand in for the real one: bot
	// challenges, errors and access walls.
	blockedPage = []struct {
		reason  string
		pattern *regexp.Regexp
	}{
		{"a bot challenge", regexp.MustCompile(`(?i)just a moment\.\.\.|performing security verification|verifies you are not a bot|checking (if the site connection is secure|your browser)|verify(ing)? you are (a )?human|cf-chl-|challenges\.cloudflare\.com|attention required! \| cloudflare|ddos protection by|please enable cookies`)},
		{"an access wall", regexp.MustCompile(`(?i)^(access denied|403 forbidden|you have been blocked)`)},
		{blockedMissing, regexp.MustCompile(`(?i)^(404|page not found|not found|error 404|oops)`)},
	}
)

// taskPage is one page a task read: its prose, its data (the prose with
// short lines such as figures and table cells), its links, or why it is not
// the page asked for.
type taskPage struct {
	URL     string
	Title   string
	Text    string
	Data    string
	Links   []taskLink
	Blocked string
}

// obscuraPage fetches a page as markdown with Obscura and splits it into
// readable text and links. Some pages' scripts keep them from rendering for
// long, such as long Wikipedia articles, so a page that is slow to render is
// also rendered without its scripts, and that copy is used when it holds an
// article or the full render fails.
func obscuraPage(ctx context.Context, executable, storage, rawURL string) (taskPage, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type rendering struct {
		markdown string
		err      error
	}
	full, static := make(chan rendering, 1), make(chan rendering, 1)
	go func() {
		markdown, err := obscuraMarkdown(ctx, executable, storage, rawURL, 30*time.Second)
		full <- rendering{markdown, err}
	}()
	slow := time.NewTimer(staticRenderDelay)
	defer slow.Stop()
	var fullErr error
	started, waiting := false, false
	startStatic := func() {
		started = true
		go func() {
			markdown, err := obscuraStaticMarkdown(ctx, executable, storage, rawURL)
			static <- rendering{markdown, err}
		}()
	}
	var standby *rendering
	for {
		select {
		case got := <-full:
			if got.err == nil {
				return parseMarkdownPage(rawURL, got.markdown), nil
			}
			if ctx.Err() != nil {
				return taskPage{}, got.err
			}
			fullErr, full = got.err, nil
			if standby != nil {
				if standby.err != nil {
					return taskPage{}, fullErr
				}
				return parseMarkdownPage(rawURL, standby.markdown), nil
			}
			if !started {
				startStatic()
			}
			waiting = true
		case <-slow.C:
			if !started {
				startStatic()
			}
		case got := <-static:
			if got.err == nil && (waiting || len(readableText(mainContent(got.markdown))) >= staticArticleText) {
				return parseMarkdownPage(rawURL, got.markdown), nil
			}
			if waiting {
				return taskPage{}, fullErr
			}
			standby = &got
		}
	}
}

// obscuraMarkdown renders a URL as markdown with Obscura.
func obscuraMarkdown(ctx context.Context, executable, storage, rawURL string, timeout time.Duration) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, executable, "fetch", "--quiet", "--dump", "markdown", "--storage-dir", storage, rawURL)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = &stdout, &stderr, time.Second
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// obscuraStaticMarkdown fetches a page's HTML as served and renders it
// without its scripts.
func obscuraStaticMarkdown(ctx context.Context, executable, storage, rawURL string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	html, err := exec.CommandContext(commandCtx, executable, "fetch", "--quiet", "--dump", "original", rawURL).Output()
	if err != nil {
		return "", err
	}
	if !htmlDocument.Match(html[:min(len(html), 1024)]) {
		return "", errors.New("not an HTML page")
	}
	html = scriptElement.ReplaceAll(html, nil)
	storage = filepath.Join(storage, "static")
	if err := os.MkdirAll(storage, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(storage, "page.html")
	// The copy is rendered from a local file, whose links resolve there
	// unless the page names its own base.
	if !baseElement.Match(html) {
		html = headElement.ReplaceAll(html, []byte(`$0<base href="`+htmlpkg.EscapeString(rawURL)+`">`))
	}
	if err := os.WriteFile(path, html, 0o600); err != nil {
		return "", err
	}
	return obscuraMarkdown(ctx, executable, storage, (&url.URL{Scheme: "file", Path: path}).String(), 15*time.Second)
}

// obscuraStatus returns the HTTP status of a URL from a raw fetch, which
// tells a page that does not exist from one that merely looks empty.
func obscuraStatus(ctx context.Context, executable, rawURL string) (int, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, executable, "fetch", "--file", "-")
	cmd.Stdin = strings.NewReader(rawURL + "\n")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	var line struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &line); err != nil {
		return 0, fmt.Errorf("read status: %w", err)
	}
	return line.Status, nil
}

func parseMarkdownPage(rawURL, markdown string) taskPage {
	page := taskPage{URL: rawURL, Links: markdownLinks(markdown)}
	if match := markdownTitle.FindStringSubmatch(markdown); match != nil {
		title := markdownLink.ReplaceAllString(match[1], "$1")
		page.Title = strings.Join(strings.Fields(strings.NewReplacer("**", "", "__", "").Replace(title)), " ")
	}
	page.Text, page.Data = readableText(mainContent(markdown)), dataText(mainContent(markdown))
	page.Blocked = blockedReason(page.Title, markdown, page.Text)
	return page
}

// mainContent drops what comes before a page's main heading, which is
// navigation and consent text that can run to megabytes, unless the page
// has no readable text after it.
func mainContent(markdown string) string {
	loc := markdownTitle.FindStringIndex(markdown)
	if loc == nil || loc[0] == 0 || len(readableText(markdown[loc[0]:])) < 200 {
		return markdown
	}
	return markdown[loc[0]:]
}

// blockedReason says why a page is a stand-in rather than the page asked
// for, judged on its title and, for short pages, its whole text.
func blockedReason(title, markdown, text string) string {
	for _, blocked := range blockedPage {
		if blocked.pattern.MatchString(strings.TrimSpace(title)) {
			return blocked.reason
		}
	}
	// A challenge page is short; a long article that mentions one is not.
	if len(text) < 2000 && len(markdown) < 20000 && blockedPage[0].pattern.MatchString(markdown) {
		return blockedPage[0].reason
	}
	return ""
}

// readableText keeps the prose of a markdown page: lines with enough text
// that is not link labels. Menus, footers, link lists and consent banners
// drop out.
func readableText(markdown string) string {
	var out []string
	size := 0
	for _, line := range strings.Split(markdown, "\n") {
		line = markdownImage.ReplaceAllString(line, "")
		linkText := 0
		for _, match := range markdownLink.FindAllStringSubmatch(line, -1) {
			linkText += len(match[1])
		}
		line = strings.Trim(markdownLink.ReplaceAllString(line, "$1"), " -#*>\t")
		if len(line) < minReadableLine || linkText*2 >= len(line) || consentLine.MatchString(line) || consentRow.MatchString(line) {
			continue
		}
		if size += len(line) + 1; size > maxReadableText {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// markdownLinks returns a markdown page's links with their labels. Image
// links keep their target with an empty label.
func markdownLinks(markdown string) []taskLink {
	markdown = markdownImage.ReplaceAllString(markdown, "")
	var links []taskLink
	for _, match := range markdownLink.FindAllStringSubmatch(markdown, -1) {
		links = append(links, taskLink{URL: match[2], Label: strings.TrimSpace(strings.Trim(match[1], "*_"))})
	}
	return links
}

// dataText keeps a page's prose together with its short lines, such as
// figures, labels and table cells, joining runs of short lines so a
// statistic reads as one line: "Download 209.01 Mbps". It drops link lists
// and consent text like readableText.
func dataText(markdown string) string {
	var out []string
	var run []string
	size := 0
	flush := func() {
		if len(run) > 0 {
			out = append(out, strings.Join(run, " "))
			size += len(out[len(out)-1]) + 1
			run = run[:0]
		}
	}
	for line := range strings.SplitSeq(markdown, "\n") {
		if size > maxReadableText {
			break
		}
		line = markdownImage.ReplaceAllString(line, "")
		linkText := 0
		for _, match := range markdownLink.FindAllStringSubmatch(line, -1) {
			linkText += len(match[1])
		}
		line = strings.Trim(markdownLink.ReplaceAllString(line, "$1"), " -#*>|\t")
		if line == "" || (linkText > 0 && linkText*2 >= len(line)) || consentLine.MatchString(line) || consentRow.MatchString(line) {
			continue
		}
		if len(line) < minReadableLine {
			run = append(run, line)
			continue
		}
		flush()
		out = append(out, line)
		size += len(line) + 1
	}
	flush()
	return truncateOnLine(strings.Join(out, "\n"), maxReadableText)
}
