package websearchtool

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestWebSearchDomainFilteringAndURLNormalization(t *testing.T) {
	body := `
<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fdocs.example.com%2Fguide">Example Docs</a></div>
<div>Useful guide</div>
<a class="result__a" href="https://other.example.net/post">Other Result</a></div>
<div>Other snippet</div>
`
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/html"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}),
	}

	req, err := tools.Normalize(tools.Request{
		Tool: domain.ToolKindWebSearch,
		Args: map[string]string{
			"query":           "example docs 2026",
			"limit":           "3.00000",
			"allowed_domains": "docs.example.com",
			"blocked_domains": "other.example.net",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Execute(context.Background(), tools.Runtime{
		Workdir:    t.TempDir(),
		HTTPClient: client,
	}, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["results"] != "1" {
		t.Fatalf("expected one filtered result, got %#v", result.Meta)
	}
	if !strings.Contains(result.Output, "https://docs.example.com/guide") {
		t.Fatalf("expected normalized target URL, got %q", result.Output)
	}
	if strings.Contains(result.Output, "other.example.net") {
		t.Fatalf("expected blocked domain to be filtered, got %q", result.Output)
	}
}

func TestNormalizeDomainList(t *testing.T) {
	if got := normalizeDomainList(" https://www.Example.com/,docs.example.com,example.com "); got != "docs.example.com,example.com" {
		t.Fatalf("unexpected normalized domains: %q", got)
	}
}
