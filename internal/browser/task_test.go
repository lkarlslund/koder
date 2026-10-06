package browser

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/browserapi"
)

func TestNormalizeTaskLinksResolvesFiltersAndBounds(t *testing.T) {
	links := []taskLink{
		{URL: "/manual.pdf", Label: "Product manual"},
		{URL: "#section", Label: "same page"},
		{URL: "mailto:support@example.com", Label: "email"},
		{URL: "/manual.pdf", Label: "duplicate"},
	}
	got := normalizeTaskLinks("https://example.com/products/widget", links, map[string]bool{"https://example.com/products/widget": true}, downloadLinkHint)
	if len(got) != 1 || got[0].URL != "https://example.com/manual.pdf" {
		t.Fatalf("normalizeTaskLinks() = %#v", got)
	}
}

// fakeDecider scores by a fixed table and can fail.
type fakeDecider struct {
	links     map[string]float64
	relevance float64
	err       error
}

func (d fakeDecider) ScoreLinks(_ context.Context, _, _ string, links []string) ([]float64, error) {
	if d.err != nil {
		return nil, d.err
	}
	scores := make([]float64, len(links))
	for i, link := range links {
		scores[i] = d.links[link]
	}
	return scores, nil
}

func (d fakeDecider) PageRelevance(context.Context, string, string, string) (float64, error) {
	return d.relevance, d.err
}

func TestRankTaskLinksUsesDeciderScores(t *testing.T) {
	decider := fakeDecider{links: map[string]float64{"Documentation — https://example.com/file": 0.99, "Contact — https://example.com/contact": 0.01}}
	links := []taskLink{{URL: "https://example.com/contact", Label: "Contact"}, {URL: "https://example.com/file", Label: "Documentation"}}
	if err := rankTaskLinks(context.Background(), decider, "download the manual", "https://example.com", links); err != nil {
		t.Fatal(err)
	}
	if links[0].Label != "Documentation" {
		t.Fatalf("rankTaskLinks() = %#v", links)
	}
}

func TestRankTaskLinksFallsBackToKeywordsWhenDeciderFails(t *testing.T) {
	links := []taskLink{{URL: "https://example.com/contact", Label: "Contact"}, {URL: "https://example.com/manual.pdf", Label: "Manual"}}
	if err := rankTaskLinks(context.Background(), fakeDecider{err: errors.New("decision service down")}, "download the manual", "https://example.com", links); err == nil {
		t.Fatal("decider failure was not reported")
	}
	if links[0].Label != "Manual" {
		t.Fatalf("keyword fallback order = %#v", links)
	}
}

func TestTaskDeciderTracesTheModel(t *testing.T) {
	if _, trace := taskDecider(context.Background(), nil); !strings.Contains(trace, "keywords") {
		t.Fatalf("trace without resolver = %q", trace)
	}
	failing := func(context.Context) (Decider, string, error) {
		return nil, "", errors.New("no provider serves a decision model")
	}
	if decider, trace := taskDecider(context.Background(), failing); decider != nil || !strings.Contains(trace, "no provider serves a decision model") {
		t.Fatalf("trace for failed resolve = %q", trace)
	}
	resolved := func(context.Context) (Decider, string, error) { return fakeDecider{}, "laya/laya", nil }
	if decider, trace := taskDecider(context.Background(), resolved); decider == nil || !strings.Contains(trace, "laya/laya") {
		t.Fatalf("trace for resolved decider = %q", trace)
	}
}

func TestParseMarkdownPageSeparatesTextFromNavigation(t *testing.T) {
	markdown := "[Gå til indhold](#main)\n\n### Sektioner\n\n1. [Seneste nyt](/nyheder/seneste)\n1. [Trafikmeldinger](/trafik)\n\n" +
		"# **Uvedkommende har haft adgang til [8,8 millioner](/tag/cpr)** CPR-numre\n\n" +
		"![foto](https://img.example/1.jpg)\n" +
		"CPR-administrationen har konstateret, at uvedkommende har skaffet sig adgang til oplysninger om borgere.\n\n" +
		"- [![Relateret](https://img.example/2.jpg)](/nyheder/relateret-historie)\n" +
		"Ministeren opfordrer alle borgere til at orientere sig på [sikkerdigital.dk](http://sikkerdigital.dk) i den kommende tid.\n"
	page := parseMarkdownPage("https://www.dr.dk/nyheder/x", markdown)
	if page.Title != "Uvedkommende har haft adgang til 8,8 millioner CPR-numre" {
		t.Fatalf("title = %q", page.Title)
	}
	wantText := "CPR-administrationen har konstateret, at uvedkommende har skaffet sig adgang til oplysninger om borgere.\n" +
		"Ministeren opfordrer alle borgere til at orientere sig på sikkerdigital.dk i den kommende tid."
	if page.Text != wantText {
		t.Fatalf("text = %q", page.Text)
	}
	var urls []string
	for _, link := range page.Links {
		urls = append(urls, link.URL)
	}
	if !slices.Contains(urls, "/trafik") || !slices.Contains(urls, "/nyheder/relateret-historie") || slices.Contains(urls, "https://img.example/1.jpg") {
		t.Fatalf("links = %q", urls)
	}
}

func TestMostRelevantPagesOrdersAndBudgetsText(t *testing.T) {
	long := strings.Repeat("Relevant line of article text.\n", 1000)
	pages := mostRelevantPages([]browserapi.TaskPage{
		{URL: "https://a", Relevance: 0.2, Text: long},
		{URL: "https://b", Relevance: 0.9, Text: long},
		{URL: "https://c", Relevance: 0.5, Text: long},
		{URL: "https://d", Relevance: 0.4, Text: long},
		{URL: "https://e", Relevance: 0.3, Text: long},
	})
	total := 0
	for _, page := range pages {
		total += len(page.Text)
		if len(page.Text) > informationPageText || strings.HasSuffix(page.Text, "Relevant line") {
			t.Fatalf("page %s text not cut on a line: %d bytes", page.URL, len(page.Text))
		}
	}
	if pages[0].URL != "https://b" || pages[1].URL != "https://c" || total > informationResultText || len(pages) != 4 {
		t.Fatalf("pages = %d, first %s, total %d bytes", len(pages), pages[0].URL, total)
	}
}

func TestUsefulDownloadRequiresPDFWhenGoalDoes(t *testing.T) {
	if isUsefulDownload(browserapi.Binary{MIME: "text/plain", Data: []byte("hello")}, "find the PDF") {
		t.Fatal("plain text satisfied a PDF goal")
	}
	if !isUsefulDownload(browserapi.Binary{MIME: "application/pdf", Data: []byte("%PDF-1.7")}, "find the PDF") {
		t.Fatal("valid PDF did not satisfy a PDF goal")
	}
}

func TestTaskURLRejectsNonHTTP(t *testing.T) {
	for _, value := range []string{"", "file:///tmp/manual.pdf", "javascript:alert(1)", "relative/path"} {
		if _, err := taskURL(value); err == nil {
			t.Fatalf("taskURL(%q) unexpectedly succeeded", value)
		}
	}
}

func TestTaskTrackerReportsStepsAndCurrentActivity(t *testing.T) {
	type report struct {
		current string
		steps   int
	}
	var reports []report
	tracker := &taskTracker{report: func(current string, steps []string) {
		reports = append(reports, report{current, len(steps)})
	}}
	tracker.doing("Fetching https://example.com")
	tracker.step("Examined https://example.com")
	tracker.doing("Asking the decision model to rank 3 links")
	tracker.finished = true
	tracker.step("late step")

	want := []report{{"Fetching https://example.com", 0}, {"", 1}, {"Asking the decision model to rank 3 links", 1}}
	if !slices.Equal(reports, want) {
		t.Fatalf("reports = %+v, want %+v", reports, want)
	}
	if len(tracker.trace) != 2 {
		t.Fatalf("trace = %q, want both steps recorded", tracker.trace)
	}
}
