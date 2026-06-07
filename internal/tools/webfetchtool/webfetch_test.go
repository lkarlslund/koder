package webfetchtool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestWebFetchMarkdownAndRedirectMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/page", http.StatusFound)
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><head><title>Example Title</title></head><body><h1>Guide</h1><p>Hello <a href="https://example.com/docs">docs</a>.</p><ul><li>One</li></ul></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	req, err := tools.Normalize(tools.Request{
		Tool: domain.ToolKindWebFetch,
		Args: map[string]string{"url": server.URL + "/start"},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		Workdir:    t.TempDir(),
		HTTPClient: server.Client(),
	}, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["format"] != "markdown" {
		t.Fatalf("expected default markdown format, got %q", result.Meta["format"])
	}
	if result.Meta["final_url"] != server.URL+"/page" {
		t.Fatalf("expected redirected final URL, got %q", result.Meta["final_url"])
	}
	if !strings.Contains(result.Output, "# Example Title") {
		t.Fatalf("expected markdown title, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "[docs](https://example.com/docs)") {
		t.Fatalf("expected markdown link, got %q", result.Output)
	}
}

func TestWebFetchTextAndHTMLFormats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1>Title</h1><p>Hello <strong>world</strong>.</p></body></html>`))
	}))
	defer server.Close()

	runtime := tools.Runtime{Workdir: t.TempDir(), HTTPClient: server.Client()}

	textReq, err := tools.Normalize(tools.Request{
		Tool: domain.ToolKindWebFetch,
		Args: map[string]string{"url": server.URL, "format": "text"},
	})
	if err != nil {
		t.Fatal(err)
	}
	textResult, err := tool{}.Call(context.Background(), tools.Options{Runtime: runtime, Request: textReq})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(textResult.Output, "<h1>") {
		t.Fatalf("expected extracted text output, got %q", textResult.Output)
	}
	if !strings.Contains(textResult.Output, "Hello world .") && !strings.Contains(textResult.Output, "Hello world.") {
		t.Fatalf("expected plain text output, got %q", textResult.Output)
	}

	htmlReq, err := tools.Normalize(tools.Request{
		Tool: domain.ToolKindWebFetch,
		Args: map[string]string{"url": server.URL, "format": "html"},
	})
	if err != nil {
		t.Fatal(err)
	}
	htmlResult, err := tool{}.Call(context.Background(), tools.Options{Runtime: runtime, Request: htmlReq})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(htmlResult.Output, "<h1>Title</h1>") {
		t.Fatalf("expected raw html output, got %q", htmlResult.Output)
	}
}

func TestWebFetchMaxCharsLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("a", 100)))
	}))
	defer server.Close()

	req, err := tools.Normalize(tools.Request{
		Tool: domain.ToolKindWebFetch,
		Args: map[string]string{"url": server.URL, "max_chars": "20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		Workdir:    t.TempDir(),
		HTTPClient: server.Client(),
	}, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["truncated"] != "true" {
		t.Fatalf("expected truncated metadata, got %#v", result.Meta)
	}
	if !strings.Contains(result.Output, "... truncated") {
		t.Fatalf("expected truncated output footer, got %q", result.Output)
	}
}
