package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/lkarlslund/koder/internal/browser"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
)

// resolveBrowserDecider returns the decider browser tasks use: the model
// pinned in the browser settings, which may be a decision model or a chat
// model, or else the first decision model a detected provider lists.
func (e *Engine) resolveBrowserDecider(ctx context.Context) (browser.Decider, string, error) {
	cfg := e.settings.Snapshot()
	providerID, modelID := cfg.Browser.TaskDecisionProviderID, cfg.Browser.TaskDecisionModelID
	if modelID == "" {
		return e.firstDecisionModel(ctx, cfg)
	}
	model, err := e.settings.Model(domain.Chat{ProviderID: providerID, ModelID: modelID})
	if err != nil {
		return nil, "", fmt.Errorf("browser decision model %s/%s: %w", providerID, modelID, err)
	}
	client, err := provider.New(model.SourceProviderID, model.Provider, e.debug, e.health)
	if err != nil {
		return nil, "", err
	}
	name := providerID + "/" + modelID
	if servesDecisions(ctx, client, model.Provider, model.SourceModelID) {
		return decisionModelDecider{client: client, model: model.SourceModelID}, name, nil
	}
	// A chat model answers quickly without thinking; the overlay maps this
	// onto whatever field the server uses for it.
	modelCfg := model.Model
	modelCfg.Options = maps.Clone(modelCfg.Options)
	if modelCfg.Options == nil {
		modelCfg.Options = map[string]any{}
	}
	modelCfg.Options["thinking_mode"] = "disabled"
	extra := provider.RequestExtraBody(model.Provider, modelCfg, e.modelOverlays)
	delete(extra, "return_progress")
	return chatModelDecider{client: client, model: model.SourceModelID, extraBody: extra}, name + " (chat model)", nil
}

// servesDecisions reports whether a model answers decision requests rather
// than chat, from its provider's detected features or, when the provider
// serves both, its model listing.
func servesDecisions(ctx context.Context, client *provider.Client, providerCfg config.Provider, modelID string) bool {
	features := providerCfg.Features
	if features == nil || !features.Decisions {
		return false
	}
	if !features.Chat {
		return true
	}
	models, err := client.ListModels(ctx)
	if err != nil {
		return false
	}
	index := slices.IndexFunc(models, func(model domain.Model) bool { return model.ID == modelID })
	return index >= 0 && models[index].SupportsDecisions
}

func (e *Engine) firstDecisionModel(ctx context.Context, cfg config.Config) (browser.Decider, string, error) {
	for _, providerID := range slices.Sorted(maps.Keys(cfg.Providers)) {
		providerCfg := cfg.Providers[providerID]
		if providerCfg.Disabled || providerCfg.Features == nil || !providerCfg.Features.Decisions {
			continue
		}
		client, err := provider.New(providerID, providerCfg, e.debug, e.health)
		if err != nil {
			continue
		}
		models, err := client.ListModels(ctx)
		if err != nil {
			continue
		}
		for _, model := range models {
			if model.SupportsDecisions {
				return decisionModelDecider{client: client, model: model.ID}, providerID + "/" + model.ID, nil
			}
		}
	}
	return nil, "", errors.New("no provider serves a decision model")
}

const (
	// Decision models read a short state (laya keeps 512 tokens), so they
	// get one link or a page excerpt per question, with a shortened goal.
	decisionGoalChars    = 400
	decisionExcerptChars = 1200
	decisionConcurrency  = 8
	// chatPageChars bounds the page text a chat model judges.
	chatPageChars = 8000
)

// decisionModelDecider asks a decision model one yes/no question per link
// or page and uses the probability of yes as the score.
type decisionModelDecider struct {
	client *provider.Client
	model  string
}

func (d decisionModelDecider) ScoreLinks(ctx context.Context, goal, pageURL string, links []string) ([]float64, error) {
	scores := make([]float64, len(links))
	errs := make([]error, len(links))
	slots := make(chan struct{}, decisionConcurrency)
	var wg sync.WaitGroup
	for index, link := range links {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			scores[index], errs[index] = d.yes(ctx, map[string]string{"goal": clip(goal, decisionGoalChars), "page": pageURL, "link": link},
				"Would following this link help complete the goal?")
		})
	}
	wg.Wait()
	return scores, errors.Join(errs...)
}

func (d decisionModelDecider) PageRelevance(ctx context.Context, goal, pageURL, text string) (float64, error) {
	return d.yes(ctx, map[string]string{"goal": clip(goal, decisionGoalChars), "page": pageURL, "text": clip(text, decisionExcerptChars)},
		"Does this page contain information that helps complete the goal?")
}

func (d decisionModelDecider) yes(ctx context.Context, state map[string]string, question string) (float64, error) {
	response, err := d.client.Decide(ctx, provider.DecisionRequest{
		Model: d.model,
		State: state,
		Questions: map[string]provider.DecisionQuestion{"q": {
			Type:         "choice",
			Instructions: question,
			Criteria:     map[string]string{"yes": "Yes", "no": "No"},
		}},
	})
	if err != nil {
		return 0, err
	}
	answer, ok := response.Answers["q"]
	if !ok {
		return 0, errors.New("decision model returned no answer")
	}
	return answer.Probabilities["yes"], nil
}

// chatModelDecider asks a chat model for scores as a JSON object. One prompt
// covers all of a page's links, and a page is judged on more of its text
// than a decision model can read.
type chatModelDecider struct {
	client    *provider.Client
	model     string
	extraBody map[string]any
}

func (d chatModelDecider) ScoreLinks(ctx context.Context, goal, pageURL string, links []string) ([]float64, error) {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Goal: %s\n\nLinks on %s:\n", goal, pageURL)
	for index, link := range links {
		fmt.Fprintf(&prompt, "%d: %s\n", index, link)
	}
	prompt.WriteString("\nFor each link, give the probability (0 to 1) that following it helps complete the goal. " +
		`Reply with only a JSON object mapping each link number to its probability, e.g. {"0": 0.9, "1": 0.1}.`)
	values, err := d.answer(ctx, prompt.String(), 16*len(links)+32)
	if err != nil {
		return nil, err
	}
	scores := make([]float64, len(links))
	for index := range links {
		scores[index] = values[strconv.Itoa(index)]
	}
	return scores, nil
}

func (d chatModelDecider) PageRelevance(ctx context.Context, goal, pageURL, text string) (float64, error) {
	prompt := fmt.Sprintf("Goal: %s\n\nText of %s:\n%s\n\n"+
		"How much does this page help complete the goal, from 0 (not at all) to 1 (it holds what the goal asks for)? "+
		`Reply with only a JSON object, e.g. {"relevance": 0.7}.`, goal, pageURL, clip(text, chatPageChars))
	values, err := d.answer(ctx, prompt, 32)
	if err != nil {
		return 0, err
	}
	relevance, ok := values["relevance"]
	if !ok {
		return 0, errors.New("chat model reply has no relevance")
	}
	return relevance, nil
}

func (d chatModelDecider) answer(ctx context.Context, prompt string, maxTokens int) (map[string]float64, error) {
	body := maps.Clone(d.extraBody)
	if body == nil {
		body = map[string]any{}
	}
	body["max_tokens"] = maxTokens
	response, err := d.client.CompleteChat(ctx, provider.ChatRequest{
		Model:     d.model,
		Messages:  []provider.Message{{Role: provider.RoleUser, Content: prompt}},
		ExtraBody: body,
	})
	if err != nil {
		return nil, err
	}
	return parseScores(response.Text)
}

// parseScores reads the JSON object in a chat reply, tolerating text or a
// code fence around it.
func parseScores(reply string) (map[string]float64, error) {
	start, end := strings.IndexByte(reply, '{'), strings.LastIndexByte(reply, '}')
	if start < 0 || end < start {
		return nil, fmt.Errorf("chat model reply has no JSON object: %q", clip(reply, 200))
	}
	var values map[string]float64
	if err := json.Unmarshal([]byte(reply[start:end+1]), &values); err != nil {
		return nil, fmt.Errorf("chat model reply is not a JSON object of numbers: %w", err)
	}
	return values, nil
}

// clip shortens text to at most limit bytes on a rune boundary.
func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && text[limit]&0xC0 == 0x80 {
		limit--
	}
	return text[:limit]
}
