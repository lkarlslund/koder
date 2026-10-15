package browser

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
	got := normalizeTaskLinks("https://www.example.com/start", links, map[string]bool{}, "rail airport", "example.com", taskCandidateLimit)
	var urls []string
	for _, link := range got {
		urls = append(urls, link.URL)
	}
	want := []string{"https://www.example.com/rail", "https://blog.example.com/airport"}
	if strings.Join(urls, " ") != strings.Join(want, " ") {
		t.Fatalf("links = %q, want %q", urls, want)
	}
	// Without a site, other sites are kept but junk still goes.
	if got := normalizeTaskLinks("https://www.example.com/start", links, map[string]bool{}, "rail", "", taskCandidateLimit); len(got) != 3 {
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

// fakeExtractor returns facts per page URL, or fails. It follows the links
// listed for a page, or without a list every link it may.
type fakeExtractor struct {
	facts  map[string]string
	follow map[string][]string
	fail   map[string]bool
	seen   *[]ExtractRequest
}

func (x fakeExtractor) Extract(_ context.Context, request ExtractRequest) (Extraction, error) {
	if x.seen != nil {
		*x.seen = append(*x.seen, request)
	}
	if x.fail[request.PageURL] {
		return Extraction{}, errors.New("model unavailable")
	}
	out := Extraction{Facts: x.facts[request.PageURL]}
	for index, link := range request.Links {
		want, listed := x.follow[request.PageURL]
		if len(out.Follow) < request.Follow && (!listed || slices.ContainsFunc(want, func(url string) bool { return strings.HasSuffix(link, " "+url) })) {
			out.Follow = append(out.Follow, index)
		}
	}
	return out, nil
}

func TestExtractTaskPageKeepsFactsPicksLinksAndFallsBackToText(t *testing.T) {
	text := strings.Repeat("Some readable article text about the goal. ", 20)
	links := []taskLink{{URL: "https://a/1", Label: "One"}, {URL: "https://a/2", Label: "Two"}, {URL: "https://a/3", Label: "Three"}}
	reads := []readTaskPage{
		{page: taskPage{URL: "https://a", Text: text, Data: text}, links: slices.Clone(links)},
		{page: taskPage{URL: "https://b", Text: text, Data: text}},
		{page: taskPage{URL: "https://c", Text: text, Data: text}, links: slices.Clone(links)},
	}
	var seen []ExtractRequest
	extractor := fakeExtractor{
		facts:  map[string]string{"https://a": "- Lisbon: €6,124/m² (Q1 2026)"},
		follow: map[string][]string{"https://a": {"https://a/3", "https://a/1"}},
		fail:   map[string]bool{"https://c": true},
		seen:   &seen,
	}
	tracker := &taskTracker{}
	for i := range reads {
		extractTaskPage(context.Background(), extractor, "prices", "- found before", &reads[i], tracker)
	}
	if reads[0].result == nil || reads[0].result.Facts != "- Lisbon: €6,124/m² (Q1 2026)" || reads[0].result.Text != "" {
		t.Fatalf("page with facts = %#v", reads[0].result)
	}
	if got := reads[0].links; len(got) != 2 || got[0].URL != "https://a/1" || got[1].URL != "https://a/3" || got[0].Score <= got[1].Score {
		t.Fatalf("followed links = %#v, want the picks only, best first", got)
	}
	if seen[0].Found != "- found before" || len(seen[0].Links) != 3 || seen[0].Links[0] != "One — https://a/1" {
		t.Fatalf("extract request = %#v", seen[0])
	}
	if reads[1].result != nil {
		t.Fatalf("page without facts = %#v", reads[1].result)
	}
	if reads[2].result == nil || reads[2].result.Text == "" || len(reads[2].links) != informationFollow {
		t.Fatalf("page whose extraction failed = %#v, links %d", reads[2].result, len(reads[2].links))
	}
	kept := pagesWithFacts([]browserapi.TaskPage{*reads[0].result, *reads[2].result})
	if len(kept) != 2 || kept[0].URL != "https://a" || kept[1].URL != "https://c" {
		t.Fatalf("pagesWithFacts = %#v", kept)
	}
	if trace := strings.Join(tracker.trace, "\n"); !strings.Contains(trace, "nothing for the goal") || !strings.Contains(trace, "pulling out facts failed") ||
		!strings.Contains(trace, "following 2 of its 3 links") {
		t.Fatalf("trace = %s", trace)
	}
}

func TestGoalWordsSkipCommonWords(t *testing.T) {
	if got := strings.Join(goalWords("Extract the facts about prices and any warnings for Crete"), " "); got != "prices warnings crete" {
		t.Fatalf("goalWords = %q", got)
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
	// Batch mode (--file -) prints a status line, 404 for pages it lacks.
	script := "#!/bin/sh\nif [ \"$2\" = --file ]; then read -r last; else for last; do :; done; fi\n" +
		"name=${last##*/}\ncase \"$name\" in *.*|'') name=index;; esac\n" +
		"if [ \"$2\" = --file ]; then status=404; [ -f \"" + dir + "/$name.md\" ] && status=200; echo \"{\\\"status\\\":$status}\"; exit 0; fi\n" +
		"cat \"" + dir + "/$name.md\" 2>/dev/null || exit 1\n"
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

func TestParseMarkdownPageStartsAtTheMainHeadingAndKeepsFigures(t *testing.T) {
	consent := strings.Repeat("_ga**Used to send data to Google Analytics about the visitor's device and behavior across sites.\n", 2000)
	markdown := "[Home](/)[News](/news)\n" + consent +
		"# Portugal Median Country Speeds\n\n" +
		"Ranking mobile and fixed broadband speeds from around the world on a monthly basis, updated mid-month.\n" +
		"Mobile\nRank\n28\nDownload\n209.01\nMbps\n\n" +
		"Results are updated mid-month for the previous three months, using the Precision Threshold.\n"
	page := parseMarkdownPage("https://www.speedtest.net/global-index/portugal", markdown)
	if strings.Contains(page.Text, "Google Analytics") || strings.Contains(page.Data, "Google Analytics") {
		t.Fatal("consent text before the main heading was kept")
	}
	if !strings.Contains(page.Data, "Mobile Rank 28 Download 209.01 Mbps") {
		t.Fatalf("data = %q, want the figures joined into one line", page.Data)
	}
	if strings.Contains(page.Text, "209.01") {
		t.Fatalf("prose kept the short figure lines: %q", page.Text)
	}
}

func TestNormalizeTaskLinksSkipsTranslations(t *testing.T) {
	links := []taskLink{
		{URL: "/news/2026/tourism", Label: "Tourism"},
		{URL: "/fr/nouvelles/2026/tourism", Label: "Tourisme"},
		{URL: "/ru/ru-news/2026/tourism", Label: "Туризм"},
		{URL: "/se/nyheter/2026/turism", Label: "Turism"},
		{URL: "/br/noticias/2026/turismo", Label: "Turismo"},
		{URL: "/usa/travel", Label: "Travel"},
	}
	got := normalizeTaskLinks("https://www.example.com/news/2026/start", links, map[string]bool{}, "tourism", "example.com", taskCandidateLimit)
	var urls []string
	for _, link := range got {
		urls = append(urls, link.URL)
	}
	if strings.Join(urls, " ") != "https://www.example.com/news/2026/tourism https://www.example.com/usa/travel" {
		t.Fatalf("links = %q", urls)
	}
}

func TestInformationTaskReportsAStartPageThatDoesNotExist(t *testing.T) {
	executable := fakeObscura(t, map[string]string{"index": "# Home\n\n" + strings.Repeat("Readable text about other things entirely. ", 10)})
	start, _ := url.Parse("https://www.example.com/news/guessed-article")
	result, err := (&Manager{}).runInformationTask(context.Background(), executable, fakeDecider{relevance: 0.8}, fakeExtractor{}, 8, "rail plan", start, &taskTracker{})
	if err != nil || result.Status != "completed" || len(result.Trace) != 1 || !strings.Contains(result.Trace[0], "it is a missing page (HTTP 404)") {
		t.Fatalf("result = %#v, %v", result, err)
	}
}

func TestInformationTaskReportsAMissingStartPageButHandsOffAWall(t *testing.T) {
	start, _ := url.Parse("https://www.example.com")
	run := func(markdown string) (browserapi.TaskResult, error) {
		executable := fakeObscura(t, map[string]string{"index": markdown})
		return (&Manager{}).runInformationTask(context.Background(), executable, fakeDecider{relevance: 0.8}, fakeExtractor{}, 8, "rail plan", start, &taskTracker{})
	}
	result, err := run("# 404\n\nThe page you were looking for could not be found on this server, sorry about that.\n")
	if err != nil || result.Status != "completed" || !strings.Contains(strings.Join(result.Trace, "\n"), "it is a missing page") {
		t.Fatalf("missing start page: %#v, %v", result, err)
	}
	if _, err := run("# www.example.com\n\n## Performing security verification\n\nThis website uses a security service to protect against malicious bots.\n"); err == nil {
		t.Fatal("a bot challenge did not hand the task to the visible browser")
	}
}

func TestNewFactsDropsBulletsFoundBefore(t *testing.T) {
	seen := map[string]bool{}
	if got := newFacts("- Lisbon: €6,124/m²\n- Porto: €3,900/m²", seen); got != "- Lisbon: €6,124/m²\n- Porto: €3,900/m²" {
		t.Fatalf("first page = %q", got)
	}
	if got := newFacts("**Prices**\n*   Lisbon: €6,124/m².\n- Faro: €4,000/m²", seen); got != "**Prices**\n- Faro: €4,000/m²" {
		t.Fatalf("second page = %q", got)
	}
	if got := newFacts("- **Porto**: €3,900/m²", seen); got != "" {
		t.Fatalf("page with only repeats = %q", got)
	}
}
