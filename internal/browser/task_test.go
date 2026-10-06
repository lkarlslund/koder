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
	got := normalizeTaskLinks("https://example.com/products/widget", links, map[string]bool{"https://example.com/products/widget": true})
	if len(got) != 1 || got[0].URL != "https://example.com/manual.pdf" {
		t.Fatalf("normalizeTaskLinks() = %#v", got)
	}
}

func TestRankTaskLinksUsesDecisionProbabilities(t *testing.T) {
	var gotCriteria map[string]string
	ranker := func(_ context.Context, state map[string]string, _ string, criteria map[string]string) (map[string]float64, error) {
		if state["goal"] != "download the manual" {
			t.Fatalf("state = %#v", state)
		}
		gotCriteria = criteria
		return map[string]float64{"c0": 0.01, "c1": 0.99}, nil
	}
	links := []taskLink{{URL: "https://example.com/contact", Label: "Contact"}, {URL: "https://example.com/file", Label: "Documentation"}}
	if err := rankTaskLinks(context.Background(), ranker, "download the manual", "https://example.com", links); err != nil {
		t.Fatal(err)
	}
	if links[0].Label != "Documentation" || len(gotCriteria) != 2 {
		t.Fatalf("rankTaskLinks() = %#v, criteria %#v", links, gotCriteria)
	}
}

func TestRankTaskLinksFallsBackToKeywordsWhenRankerFails(t *testing.T) {
	ranker := func(context.Context, map[string]string, string, map[string]string) (map[string]float64, error) {
		return nil, errors.New("decision service down")
	}
	links := []taskLink{{URL: "https://example.com/contact", Label: "Contact"}, {URL: "https://example.com/manual.pdf", Label: "Manual"}}
	if err := rankTaskLinks(context.Background(), ranker, "download the manual", "https://example.com", links); err == nil {
		t.Fatal("ranker failure was not reported")
	}
	if links[0].Label != "Manual" {
		t.Fatalf("keyword fallback order = %#v", links)
	}
}

func TestTaskRankerTracesTheDecisionModel(t *testing.T) {
	if _, trace := taskRanker(context.Background(), nil); !strings.Contains(trace, "keywords") {
		t.Fatalf("trace without resolver = %q", trace)
	}
	failing := func(context.Context) (ChoiceRanker, string, error) {
		return nil, "", errors.New("no provider serves a decision model")
	}
	if ranker, trace := taskRanker(context.Background(), failing); ranker != nil || !strings.Contains(trace, "no provider serves a decision model") {
		t.Fatalf("trace for failed resolve = %q", trace)
	}
	resolved := func(context.Context) (ChoiceRanker, string, error) {
		return func(context.Context, map[string]string, string, map[string]string) (map[string]float64, error) {
			return nil, nil
		}, "laya/laya", nil
	}
	if ranker, trace := taskRanker(context.Background(), resolved); ranker == nil || !strings.Contains(trace, "laya/laya") {
		t.Fatalf("trace for resolved ranker = %q", trace)
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
