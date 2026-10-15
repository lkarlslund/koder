package browser

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// blockedMissing is the reason a page is a stand-in for one that does not
// exist, which, unlike a bot challenge, no browser gets past.
const blockedMissing = "a missing page"

const (
	// minReadableLine is the shortest line of prose a page's readable text
	// keeps; shorter lines are navigation, buttons and captions.
	minReadableLine = 60
	// maxReadableText caps a page's readable text; beyond it is site
	// furniture, comments or endless listings.
	maxReadableText = 64 << 10
)

var (
	markdownImage = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	markdownLink  = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	markdownTitle = regexp.MustCompile(`(?m)^#\s+(.+)$`)
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
// readable text and links.
func obscuraPage(ctx context.Context, executable, storage, rawURL string) (taskPage, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, executable, "fetch", "--quiet", "--dump", "markdown", "--storage-dir", storage, rawURL)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return taskPage{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseMarkdownPage(rawURL, stdout.String()), nil
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
