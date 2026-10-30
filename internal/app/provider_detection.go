package app

import (
	"cmp"
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
)

// providerProbeTimeout bounds one provider's probe, which lists its models,
// checks its endpoints and sends two one-token chat requests.
const providerProbeTimeout = 45 * time.Second

// ProbeProvidersInBackground probes every enabled provider again, so what
// each server supports is current after a restart, and migrates the legacy
// browser decision endpoint into a provider. A provider that cannot be
// reached keeps what was found last time.
func (c *Controller) ProbeProvidersInBackground(ctx context.Context) {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	var pending []string
	for id, providerCfg := range cfg.Providers {
		if !providerCfg.Disabled {
			pending = append(pending, id)
		}
	}
	legacyURL := cfg.Browser.LegacyTaskDecisionURL
	if len(pending) == 0 && legacyURL == "" {
		return
	}
	go func() {
		var mu sync.Mutex
		var wg sync.WaitGroup
		probed := map[string]provider.ProbeResult{}
		for _, id := range pending {
			wg.Go(func() {
				result, err := probeProvider(ctx, id, cfg.Providers[id])
				if err != nil {
					slog.Info("provider probe failed", "provider", id, "error", err)
					return
				}
				mu.Lock()
				probed[id] = result
				mu.Unlock()
			})
		}
		wg.Wait()
		var decisionProvider *provider.Detection
		dropLegacy := false
		if legacyURL != "" {
			decisionProvider, dropLegacy = detectLegacyDecisionProvider(ctx, cfg, legacyURL)
		}
		c.applyProviderProbes(probed, decisionProvider, dropLegacy)
	}()
}

func probeProvider(ctx context.Context, id string, providerCfg config.Provider) (provider.ProbeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, providerProbeTimeout)
	defer cancel()
	return provider.Probe(ctx, provider.BuildDraftForExisting(id, providerCfg), nil)
}

func detectProvider(ctx context.Context, id string, providerCfg config.Provider) (provider.Detection, error) {
	ctx, cancel := context.WithTimeout(ctx, providerProbeTimeout)
	defer cancel()
	return provider.Detect(ctx, id, providerCfg, nil)
}

// applyProbe records what a probe found on a provider's server.
func applyProbe(cfg *config.Provider, result provider.ProbeResult) {
	features := result.Features
	cfg.Features = &features
	cfg.Transport = result.Transport
	if result.BaseURL != "" {
		cfg.BaseURL = result.BaseURL
	}
	if result.PromptProgressProbed {
		cfg.PromptProgressProbed = true
		cfg.PromptProgressSupported = result.PromptProgressSupported
		cfg.PromptProgressCheckedAt = result.PromptProgressCheckedAt
		cfg.PromptProgressTarget = result.PromptProgressTarget
	}
}

// detectLegacyDecisionProvider resolves the old task_decision_url setting.
// It returns a detection to add as a provider when the endpoint is a decision
// server no provider covers yet, and whether the setting is settled.
func detectLegacyDecisionProvider(ctx context.Context, cfg config.Config, legacyURL string) (*provider.Detection, bool) {
	baseURL := strings.TrimSuffix(strings.TrimRight(legacyURL, "/"), "/systemone")
	for _, providerCfg := range cfg.Providers {
		if strings.TrimRight(providerCfg.BaseURL, "/") == baseURL {
			return nil, true
		}
	}
	detection, err := detectProvider(ctx, "", config.Provider{BaseURL: baseURL})
	if err != nil {
		slog.Info("legacy browser decision endpoint unavailable", "url", legacyURL, "error", err)
		return nil, false
	}
	if !detection.Features.Decisions {
		return nil, true
	}
	return &detection, true
}

func (c *Controller) applyProviderProbes(probed map[string]provider.ProbeResult, decisionProvider *provider.Detection, dropLegacy bool) {
	c.mu.Lock()
	changed := false
	for id, result := range probed {
		providerCfg, ok := c.cfg.Providers[id]
		if !ok {
			continue
		}
		applyProbe(&providerCfg, result)
		c.cfg.Providers[id] = providerCfg
		changed = true
	}
	if decisionProvider != nil {
		if c.cfg.Providers == nil {
			c.cfg.Providers = map[string]config.Provider{}
		}
		id := provider.UniqueProviderID(cmp.Or(providerIDFromModels(decisionProvider.Models), "decisions"), c.cfg.Providers)
		next := config.Provider{
			Kind:      provider.ProviderKindCompatible,
			Name:      id,
			BaseURL:   decisionProvider.BaseURL,
			Transport: decisionProvider.Transport,
			Features:  &decisionProvider.Features,
		}
		applyNewProviderDefaults(&next)
		c.cfg.Providers[id] = next
		changed = true
	}
	if dropLegacy && c.cfg.Browser.LegacyTaskDecisionURL != "" {
		c.cfg.Browser.LegacyTaskDecisionURL = ""
		changed = true
	}
	if !changed {
		c.mu.Unlock()
		return
	}
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		slog.Warn("save detected provider features failed", "error", err)
		return
	}
	if c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	c.mu.Unlock()
	c.broadcast("snapshot", c.State())
}

// decisionProviderID names a migrated decision provider after its first
// model, so a laya server becomes provider "laya".
func providerIDFromModels(models []domain.Model) string {
	for _, model := range models {
		id := strings.Map(func(r rune) rune {
			switch {
			case unicode.IsLetter(r) || unicode.IsDigit(r):
				return unicode.ToLower(r)
			case r == '-' || r == '_':
				return r
			default:
				return -1
			}
		}, model.ID)
		if id != "" {
			return id
		}
	}
	return ""
}
