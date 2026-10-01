package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/lkarlslund/koder/internal/browser"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/provider"
)

// resolveBrowserRanker returns a ranker backed by the browser settings'
// decision model, or by the first decision model a detected provider lists.
func (e *Engine) resolveBrowserRanker(ctx context.Context) (browser.ChoiceRanker, string, error) {
	cfg := e.settings.Snapshot()
	providerID, modelID := cfg.Browser.TaskDecisionProviderID, cfg.Browser.TaskDecisionModelID
	var client *provider.Client
	if modelID != "" {
		providerCfg, ok := cfg.Providers[providerID]
		if !ok {
			return nil, "", fmt.Errorf("decision provider %q is not configured", providerID)
		}
		var err error
		if client, err = provider.New(providerID, providerCfg, e.debug, e.health); err != nil {
			return nil, "", err
		}
	} else {
		var err error
		if providerID, modelID, client, err = e.firstDecisionModel(ctx, cfg); err != nil {
			return nil, "", err
		}
	}
	ranker := func(ctx context.Context, state map[string]string, instructions string, criteria map[string]string) (map[string]float64, error) {
		response, err := client.Decide(ctx, provider.DecisionRequest{
			Model:     modelID,
			State:     state,
			Questions: map[string]provider.DecisionQuestion{"next": {Type: "choice", Instructions: instructions, Criteria: criteria}},
		})
		if err != nil {
			return nil, err
		}
		answer, ok := response.Answers["next"]
		if !ok {
			return nil, errors.New("decision model returned no answer")
		}
		return answer.Probabilities, nil
	}
	return ranker, providerID + "/" + modelID, nil
}

func (e *Engine) firstDecisionModel(ctx context.Context, cfg config.Config) (string, string, *provider.Client, error) {
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
				return providerID, model.ID, client, nil
			}
		}
	}
	return "", "", nil, errors.New("no provider serves a decision model")
}
