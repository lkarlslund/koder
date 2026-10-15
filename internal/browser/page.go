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
	// blockedPage matches pages that stand in for the real one: bot
	// challenges, errors and access walls.
	blockedPage = []struct {
		reason  string
		pattern *regexp.Regexp
	}{
		{"a bot challenge", regexp.MustCompile(`(?i)just a moment\.\.\.|checking (if the site connection is secure|your browser)|verify(ing)? you are (a )?human|cf-chl-|challenges\.cloudflare\.com|attention required! \| cloudflare|ddos protection by|please enable cookies`)},
		{"an access wall", regexp.MustCompile(`(?i)^(access denied|403 forbidden|you have been blocked)`)},
		{"a missing page", regexp.MustCompile(`(?i)^(404|page not found|not found|error 404|oops)`)},
	}
)

// taskPage is one page a task read: its readable text and its links, or
// why it is not the page asked for.
type taskPage struct {
	URL     string
	Title   string
	Text    string
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
	page := taskPage{URL: rawURL, Text: readableText(markdown), Links: markdownLinks(markdown)}
	if match := markdownTitle.FindStringSubmatch(markdown); match != nil {
		title := markdownLink.ReplaceAllString(match[1], "$1")
		page.Title = strings.Join(strings.Fields(strings.NewReplacer("**", "", "__", "").Replace(title)), " ")
	}
	page.Blocked = blockedReason(page.Title, markdown, page.Text)
	return page
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
		if len(line) < minReadableLine || linkText*2 >= len(line) || consentLine.MatchString(line) {
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
