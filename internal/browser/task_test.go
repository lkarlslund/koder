package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestRankTaskLinksUsesJEVProbabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"next":{"probabilities":{"c0":0.01,"c1":0.99}}}}`))
	}))
	defer server.Close()
	links := []taskLink{{URL: "https://example.com/contact", Label: "Contact"}, {URL: "https://example.com/file", Label: "Documentation"}}
	rankTaskLinks(context.Background(), server.URL, "download the manual", "https://example.com", links)
	if links[0].Label != "Documentation" {
		t.Fatalf("rankTaskLinks() = %#v", links)
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
