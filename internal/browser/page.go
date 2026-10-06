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

// minReadableLine is the shortest line of prose a page's readable text keeps;
// shorter lines are navigation, buttons and captions.
const minReadableLine = 60

var (
	markdownImage = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	markdownLink  = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	markdownTitle = regexp.MustCompile(`(?m)^#\s+(.+)$`)
)

// taskPage is one page a task read: its readable text and its links.
type taskPage struct {
	URL   string
	Title string
	Text  string
	Links []taskLink
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
	return page
}

// readableText keeps the prose of a markdown page: lines with enough text
// that is not link labels. Menus, footers and link lists drop out.
func readableText(markdown string) string {
	var out []string
	for _, line := range strings.Split(markdown, "\n") {
		line = markdownImage.ReplaceAllString(line, "")
		linkText := 0
		for _, match := range markdownLink.FindAllStringSubmatch(line, -1) {
			linkText += len(match[1])
		}
		line = strings.Trim(markdownLink.ReplaceAllString(line, "$1"), " -#*>\t")
		if len(line) >= minReadableLine && linkText*2 < len(line) {
			out = append(out, line)
		}
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
