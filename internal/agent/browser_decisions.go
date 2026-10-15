package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/browser"
	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/settings"
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
	return chatModelDecider{client: client, model: model.SourceModelID, extraBody: e.quickChatBody(model)}, name + " (chat model)", nil
}

// quickChatBody is the extra request body for a quick answer from a chat
// model: thinking off, which the overlay maps onto whatever field the
// server uses for it, and no progress events. The model's thinking settings
// go, since servers reject a reasoning effort with thinking off.
func (e *Engine) quickChatBody(model settings.ModelSettings) map[string]any {
	modelCfg := model.Model
	modelCfg.Options = maps.Clone(modelCfg.Options)
	if modelCfg.Options == nil {
		modelCfg.Options = map[string]any{}
	}
	for _, option := range []string{"reasoning_effort", "thinking_budget", "preserve_thinking"} {
		delete(modelCfg.Options, option)
	}
	modelCfg.ReasoningEffort, modelCfg.ThinkingBudget = "", 0
	modelCfg.Options["thinking_mode"] = "disabled"
	extra := provider.RequestExtraBody(model.Provider, modelCfg, e.modelOverlays)
	delete(extra, "return_progress")
	return extra
}

// resolveBrowserExtractor returns the chat's own model for pulling facts
// out of the pages an information task reads.
func (e *Engine) resolveBrowserExtractor(_ context.Context, chat browserapi.Chat) (browser.Extractor, string, error) {
	model, err := e.settings.Model(domain.Chat{ID: chat.ChatID, ProviderID: chat.ProviderID, ModelID: chat.ModelID})
	if err != nil {
		return nil, "", err
	}
	client, err := provider.New(model.SourceProviderID, model.Provider, e.debug, e.health)
	if err != nil {
		return nil, "", err
	}
	return chatModelExtractor{client: client, model: model.SourceModelID, extraBody: e.quickChatBody(model)}, model.SourceProviderID + "/" + model.SourceModelID, nil
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
	// Decision models get one link or a page excerpt per question, with a
	// shortened goal. A model with a short state (laya keeps 512 tokens)
	// rejects a long excerpt, which is then halved until it fits.
	decisionGoalChars    = 400
	decisionExcerptChars = 6000
	decisionMinExcerpt   = 600
	decisionConcurrency  = 8
	// chatPageChars bounds the page text a chat model judges.
	chatPageChars = 8000
	// extractPageChars bounds the page text a chat model reads for facts,
	// and extractMaxTokens the facts it writes.
	extractPageChars = 24000
	extractMaxTokens = 1500
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
	for excerpt := decisionExcerptChars; ; excerpt /= 2 {
		score, err := d.yes(ctx, map[string]string{"goal": clip(goal, decisionGoalChars), "page": pageURL, "text": clip(text, excerpt)},
			"Does this page contain information that helps complete the goal?")
		if err == nil || !errors.Is(err, provider.ErrDecisionTooLong) || excerpt/2 < decisionMinExcerpt || len(text) <= excerpt/2 {
			return score, err
		}
	}
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
	response, err := d.client.CompleteChat(ctx, provider.ChatRequest{
		Model:     d.model,
		Messages:  []provider.Message{{Role: provider.RoleUser, Content: prompt}},
		ExtraBody: withMaxTokens(d.extraBody, maxTokens),
	})
	if err != nil {
		return nil, err
	}
	return parseScores(response.Text)
}

// chatModelExtractor asks a chat model for the facts on a page that help a
// goal, as markdown bullets, and for the page's links that most likely lead
// to what is still missing.
type chatModelExtractor struct {
	client    *provider.Client
	model     string
	extraBody map[string]any
}

const (
	extractNothing = "NONE"
	extractFollow  = "FOLLOW:"
)

// extractRetries is how many times a page's extraction is retried when the
// model's server is too busy to take it, which happens when a task reads
// several pages at once.
const extractRetries = 2

func (x chatModelExtractor) Extract(ctx context.Context, request browser.ExtractRequest) (browser.Extraction, error) {
	var response provider.ChatResponse
	var err error
	for attempt := 0; ; attempt++ {
		response, err = x.client.CompleteChat(ctx, provider.ChatRequest{
			Model:     x.model,
			Messages:  []provider.Message{{Role: provider.RoleUser, Content: extractPrompt(request)}},
			ExtraBody: withMaxTokens(x.extraBody, extractMaxTokens),
		})
		var apiErr *provider.APIError
		if attempt == extractRetries || !errors.As(err, &apiErr) ||
			(apiErr.StatusCode != http.StatusTooManyRequests && apiErr.StatusCode != http.StatusServiceUnavailable) {
			break
		}
		select {
		case <-ctx.Done():
			return browser.Extraction{}, err
		case <-time.After(max(apiErr.RetryAfter, time.Duration(attempt+1)*time.Second)):
		}
	}
	if err != nil {
		return browser.Extraction{}, err
	}
	reply := strings.TrimSpace(response.Text)
	if reply == "" {
		return browser.Extraction{}, errors.New("the model returned no text")
	}
	return parseExtraction(reply, request.Follow), nil
}

func extractPrompt(request browser.ExtractRequest) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Goal: %s\n\n", request.Goal)
	if request.Found != "" {
		fmt.Fprintf(&prompt, "Facts already found on other pages:\n<<<\n%s\n>>>\n\n", request.Found)
	}
	fmt.Fprintf(&prompt, "Text of %s:\n<<<\n%s\n>>>\n\n", request.PageURL, clip(request.Text, extractPageChars))
	if len(request.Links) > 0 {
		prompt.WriteString("Links on this page:\n")
		for index, link := range request.Links {
			fmt.Fprintf(&prompt, "%d: %s\n", index, link)
		}
		prompt.WriteString("\n")
	}
	prompt.WriteString("List every fact in this text that helps the goal, as short markdown bullets. " +
		"Keep numbers, dates, names and units exactly as written, and say what period or place each figure is for when the text does. " +
		"Use only this text, not what you know, and list only what it says, not what it leaves out. " +
		"State the facts themselves; do not describe the text, its headings or its links.")
	if request.Found != "" {
		prompt.WriteString(" Skip facts already found unless this text adds to them or says otherwise.")
	}
	prompt.WriteString(" If it holds nothing new that helps the goal, write only " + extractNothing + ".")
	if len(request.Links) > 0 && request.Follow > 0 {
		fmt.Fprintf(&prompt, "\n\nThen, on a last line, write %s and the numbers of at most %d links most likely to lead to what the goal asks for "+
			"that is not found yet, best first, e.g. \"%s 4, 0\". Links to other sites are fine, and official or primary sources are best. "+
			"Check each part of the goal: write \"%s %s\" only if the facts found "+
			"already answer every part, or no link is likely to help with what is missing.",
			extractFollow, request.Follow, extractFollow, extractFollow, extractNothing)
	}
	return prompt.String()
}

var (
	// followLine matches the line of an extraction reply that picks links.
	followLine = regexp.MustCompile(`(?im)^[\s*_#>-]*FOLLOW\s*:(.*)$`)
	digits     = regexp.MustCompile(`\d+`)
	bullet     = regexp.MustCompile(`^\s*[-*•]\s+\S`)
	// absentFact matches a bullet that only says the text lacks something,
	// which models write despite being asked not to.
	absentFact = regexp.MustCompile(`(?i)^\s*[-*•]\s*(` +
		// It starts, after any label, by saying so,
		`(\*\*[^*]*\*\*:?|[^:*]{1,80}:)?\s*(NONE\b|not (mentioned|provided|stated|given|specified|listed|covered|included)\b|` +
		`(the|this) (text|page|article) (does not|doesn't|provides no|lists no|contains no|gives no|has no|mentions no)\b)` +
		// or ends saying so.
		`|.*(\bNONE\b|\bnot (mentioned|provided|stated|given|specified|listed|covered|included)( in (this|the) (text|page|article))?)[\s.)*_]*$` +
		// or is about one of the links it was shown.
		`|(\*\*)?[Ll]inks? \[?\d+)`)
	// linkNote matches a fact's reference to the links the model was shown,
	// such as "(Link 6)", which means nothing to the reader.
	linkNote = regexp.MustCompile(`\s*\((see )?[Ll]inks? \[?\d+\]?((,| and) \[?\d+\]?)*\)`)
	// lacksClause matches the end of a fact that goes on to say what the
	// text does not say.
	lacksClause = regexp.MustCompile(`(?i),?\s+(but|though|although|however)\b[^.]*\b(does not|doesn't|do not)\b.*$`)
)

// parseExtraction splits an extraction reply into its facts, "" for NONE,
// and the links it picks.
func parseExtraction(reply string, limit int) browser.Extraction {
	var out browser.Extraction
	facts := reply
	if loc := followLine.FindAllStringSubmatchIndex(reply, -1); loc != nil {
		last := loc[len(loc)-1]
		facts = reply[:last[0]]
		for _, number := range digits.FindAllString(reply[last[2]:last[3]], -1) {
			if index, err := strconv.Atoi(number); err == nil && len(out.Follow) < limit {
				out.Follow = append(out.Follow, index)
			}
		}
	}
	var kept []string
	bullets := 0
	for line := range strings.SplitSeq(facts, "\n") {
		if absentFact.MatchString(line) {
			continue
		}
		if bullet.MatchString(line) {
			line = lacksClause.ReplaceAllString(linkNote.ReplaceAllString(line, ""), ".")
		}
		kept = append(kept, line)
		if bullet.MatchString(line) && strings.Trim(line, " .*`-•") != extractNothing {
			bullets++
		}
	}
	if bullets > 0 {
		out.Facts = strings.TrimSpace(strings.Join(kept, "\n"))
	}
	return out
}

// withMaxTokens returns a copy of a request's extra body with max_tokens set.
func withMaxTokens(extraBody map[string]any, maxTokens int) map[string]any {
	body := maps.Clone(extraBody)
	if body == nil {
		body = map[string]any{}
	}
	body["max_tokens"] = maxTokens
	return body
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
