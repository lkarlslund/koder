package browser

import "context"

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
