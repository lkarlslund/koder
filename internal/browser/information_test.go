package browser

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/browserapi"
)

func TestNormalizeTaskLinksDropsJunkAndOtherSites(t *testing.T) {
	links := []taskLink{
		{URL: "/rail?utm_source=nav", Label: "High-speed rail"},
		{URL: "/rail/", Label: "duplicate after canonicalizing"},
		{URL: "https://blog.example.com/airport", Label: "New airport"},
		{URL: "https://twitter.com/share?url=https://www.example.com/rail", Label: "Tweet"},
		{URL: "https://wa.me/?text=https://www.example.com/rail", Label: "WhatsApp"},
		{URL: "/privacy-policy", Label: "Privacy"},
		{URL: "/w/index.php?title=Rail&action=info", Label: "Page information"},
		{URL: "https://www.cloudflare.com/careers/?utm_source=challenge", Label: "Careers"},
		{URL: "https://other.org/rail", Label: "Rail elsewhere"},
	}
	got := normalizeTaskLinks("https://www.example.com/start", links, map[string]bool{}, "rail airport", "example.com")
	var urls []string
	for _, link := range got {
		urls = append(urls, link.URL)
	}
	want := []string{"https://www.example.com/rail", "https://blog.example.com/airport"}
	if strings.Join(urls, " ") != strings.Join(want, " ") {
		t.Fatalf("links = %q, want %q", urls, want)
	}
	// Without a site, other sites are kept but junk still goes.
	if got := normalizeTaskLinks("https://www.example.com/start", links, map[string]bool{}, "rail", ""); len(got) != 3 {
		t.Fatalf("links without a site = %#v", got)
	}
}

func TestSiteOf(t *testing.T) {
	for host, want := range map[string]string{
		"en.wikipedia.org": "wikipedia.org",
		"wikipedia.org":    "wikipedia.org",
		"www.bbc.co.uk":    "bbc.co.uk",
		"www.gov.pt":       "www.gov.pt",
		"portal.gov.pt":    "portal.gov.pt",
		"a.b.dr.dk":        "dr.dk",
		"localhost":        "localhost",
	} {
		if got := siteOf(host); got != want {
			t.Errorf("siteOf(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestParseMarkdownPageDropsConsentText(t *testing.T) {
	markdown := "# Portugal ranks among most tourism resistant destinations\n\n" +
		"We and our 1087 partners process your personal data, e.g. your IP-number, using technology such as cookies.\n" +
		"The technical storage or access is strictly necessary for the legitimate purpose of enabling a service.\n" +
		"Maximum Storage Duration**: Persistent**Type**: HTML Local Storage and some more words here.\n" +
		"New research has measured resistance to tourism in major European destinations amid rising tensions.\n"
	page := parseMarkdownPage("https://www.theportugalnews.com/news/x", markdown)
	if page.Text != "New research has measured resistance to tourism in major European destinations amid rising tensions." {
		t.Fatalf("text = %q", page.Text)
	}
	if page.Blocked != "" {
		t.Fatalf("article judged blocked: %q", page.Blocked)
	}
}

func TestParseMarkdownPageDetectsStandInPages(t *testing.T) {
	for markdown, want := range map[string]string{
		"# www.railjournal.com\n\nJust a moment...\nVerifying you are human. This may take a few seconds.\n": "a bot challenge",
		"# 404\n\nThe page you were looking for could not be found on this server, sorry about that.\n":      "a missing page",
		"# Access denied\n\nYou do not have permission to access this resource on this server at all.\n":     "an access wall",
	} {
		if got := parseMarkdownPage("https://example.com/x", markdown).Blocked; got != want {
			t.Errorf("Blocked for %q = %q, want %q", markdown, got, want)
		}
	}
}

// fakeExtractor returns facts per page URL, or fails.
type fakeExtractor struct {
	facts map[string]string
	fail  map[string]bool
}

func (x fakeExtractor) ExtractFacts(_ context.Context, _, pageURL, _ string) (string, error) {
	if x.fail[pageURL] {
		return "", errors.New("model unavailable")
	}
	return x.facts[pageURL], nil
}

func TestExtractTaskFactsKeepsFactsAndFallsBackToText(t *testing.T) {
	text := strings.Repeat("Some readable article text about the goal. ", 20)
	pages := []browserapi.TaskPage{
		{URL: "https://a", Relevance: 0.9, Text: text},
		{URL: "https://b", Relevance: 0.5, Text: text},
		{URL: "https://c", Relevance: 0.4, Text: text},
	}
	extractor := fakeExtractor{facts: map[string]string{"https://a": "- Lisbon: €6,124/m² (Q1 2026)"}, fail: map[string]bool{"https://c": true}}
	tracker := &taskTracker{}
	if found := extractTaskFacts(context.Background(), extractor, "prices", pages, []int{0, 1, 2}, tracker); found != 1 {
		t.Fatalf("found = %d, want 1", found)
	}
	if pages[0].Facts != "- Lisbon: €6,124/m² (Q1 2026)" || pages[0].Text != "" {
		t.Fatalf("page with facts = %#v", pages[0])
	}
	if pages[1].Facts != "" || pages[1].Text != "" {
		t.Fatalf("page without facts = %#v", pages[1])
	}
	if pages[2].Text == "" {
		t.Fatal("page whose extraction failed lost its text")
	}
	kept := pagesWithFacts(pages)
	if len(kept) != 2 || kept[0].URL != "https://a" || kept[1].URL != "https://c" {
		t.Fatalf("pagesWithFacts = %#v", kept)
	}
	if trace := strings.Join(tracker.trace, "\n"); !strings.Contains(trace, "nothing for the goal") || !strings.Contains(trace, "pulling out facts failed") {
		t.Fatalf("trace = %s", trace)
	}
}

// fakeObscura writes an obscura stand-in that prints the markdown file
// named after the last path segment of the URL it is asked to fetch.
func fakeObscura(t *testing.T, pages map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, markdown := range pages {
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(markdown), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\nfor last; do :; done\nname=${last##*/}\ncase \"$name\" in *.*|'') name=index;; esac\ncat \"" + dir + "/$name.md\" 2>/dev/null || exit 1\n"
	path := filepath.Join(dir, "obscura")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInformationTaskFollowsSiteLinksAndReturnsFacts(t *testing.T) {
	prose := func(topic string) string {
		return strings.Repeat("This paragraph explains "+topic+" in enough words to count as readable text. ", 5) + "\n"
	}
	executable := fakeObscura(t, map[string]string{
		"index": "# Rail\n\n" + prose("the rail plan") +
			"[Lisbon to Porto line](https://www.example.com/porto)\n[Blocked page](https://www.example.com/blocked)\n" +
			"[Share](https://twitter.com/share?url=https://www.example.com/porto)\n[Elsewhere](https://other.org/rail)\n",
		"porto":   "# Porto line\n\n" + prose("the Porto line"),
		"blocked": "# Just a moment...\n\nChecking your browser before accessing the site.\n",
	})
	extractor := fakeExtractor{facts: map[string]string{
		"https://www.example.com/porto": "- Lisbon–Porto opens in 2030",
		"https://www.example.com":       "- The plan has three phases",
	}}
	start, _ := url.Parse("https://www.example.com")
	tracker := &taskTracker{}
	result, err := (&Manager{}).runInformationTask(context.Background(), executable, fakeDecider{relevance: 0.8}, extractor, 8, "rail plan Porto", start, tracker)
	if err != nil {
		t.Fatal(err)
	}
	trace := strings.Join(result.Trace, "\n")
	if len(result.Pages) != 2 || result.Pages[0].Facts == "" || result.Pages[1].Facts == "" {
		t.Fatalf("pages = %#v\ntrace:\n%s", result.Pages, trace)
	}
	if !strings.Contains(trace, "Skipped https://www.example.com/blocked: it is a bot challenge") {
		t.Fatalf("blocked page not reported:\n%s", trace)
	}
	if strings.Contains(trace, "twitter.com") || strings.Contains(trace, "other.org") {
		t.Fatalf("followed junk or another site:\n%s", trace)
	}
}
