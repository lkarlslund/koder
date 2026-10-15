package browser

import (
	"context"

	"github.com/lkarlslund/koder/internal/browserapi"
)

// Decider makes the judgments a browser task needs. Scores run from 0 (no
// help) to 1 (certainly helps the goal). Koder backs it with a decision
// model or, when configured, a chat model.
type Decider interface {
	// ScoreLinks rates how likely following each link helps the goal. It
	// returns one score per link, in order.
	ScoreLinks(ctx context.Context, goal, pageURL string, links []string) ([]float64, error)
	// PageRelevance rates how much a page's text helps the goal.
	PageRelevance(ctx context.Context, goal, pageURL, text string) (float64, error)
}

// DeciderResolver picks the decider for one task and returns its model's
// name for the task trace.
type DeciderResolver func(ctx context.Context) (Decider, string, error)

// Extractor reads a page for an information task: it pulls out the facts
// that answer the goal and picks the page's links most likely to lead to
// what is still missing.
type Extractor interface {
	Extract(ctx context.Context, request ExtractRequest) (Extraction, error)
}

// ExtractRequest is one page for an Extractor.
type ExtractRequest struct {
	Goal    string
	PageURL string
	Text    string
	// Found is the facts other pages already gave, so links are picked for
	// what is still missing.
	Found string
	// Links describes the page's links worth following, as "label — URL".
	Links []string
	// Follow is the most links to pick.
	Follow int
}

// Extraction is what an Extractor made of a page: its facts, "" when it
// holds none, and the links to follow as indexes into the request's Links,
// best first.
type Extraction struct {
	Facts  string
	Follow []int
}

// ExtractorResolver picks the extractor for a chat's task, usually the
// chat's own model, and returns its name for the task trace.
type ExtractorResolver func(ctx context.Context, chat browserapi.Chat) (Extractor, string, error)
