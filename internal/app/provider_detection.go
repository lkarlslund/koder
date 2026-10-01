package app

import (
	"cmp"
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
)

const providerDetectionTimeout = 20 * time.Second

// DetectProvidersInBackground detects providers saved before feature
// detection existed and migrates the legacy browser decision endpoint into a
// provider. Providers that cannot be reached are retried on the next start.
func (c *Controller) DetectProvidersInBackground(ctx context.Context) {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	var pending []string
	for id, providerCfg := range cfg.Providers {
		if !providerCfg.Disabled && providerCfg.Features == nil {
			pending = append(pending, id)
		}
	}
	legacyURL := cfg.Browser.LegacyTaskDecisionURL
	if len(pending) == 0 && legacyURL == "" {
		return
	}
	go func() {
		detected := map[string]provider.Detection{}
		for _, id := range pending {
			if detection, err := detectProvider(ctx, id, cfg.Providers[id]); err == nil {
				detected[id] = detection
			} else {
				slog.Info("provider detection failed", "provider", id, "error", err)
			}
		}
		var decisionProvider *provider.Detection
		dropLegacy := false
		if legacyURL != "" {
			decisionProvider, dropLegacy = detectLegacyDecisionProvider(ctx, cfg, legacyURL)
		}
		c.applyProviderDetections(detected, decisionProvider, dropLegacy)
	}()
}

func detectProvider(ctx context.Context, id string, providerCfg config.Provider) (provider.Detection, error) {
	ctx, cancel := context.WithTimeout(ctx, providerDetectionTimeout)
	defer cancel()
	return provider.Detect(ctx, id, providerCfg, nil)
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

func (c *Controller) applyProviderDetections(detected map[string]provider.Detection, decisionProvider *provider.Detection, dropLegacy bool) {
	c.mu.Lock()
	changed := false
	for id, detection := range detected {
		providerCfg, ok := c.cfg.Providers[id]
		if !ok || providerCfg.Features != nil {
			continue
		}
		providerCfg.Features = &detection.Features
		providerCfg.Transport = detection.Transport
		c.cfg.Providers[id] = providerCfg
		changed = true
	}
	if decisionProvider != nil {
		if c.cfg.Providers == nil {
			c.cfg.Providers = map[string]config.Provider{}
		}
		id := provider.UniqueProviderID(cmp.Or(providerIDFromModels(decisionProvider.Models), "decisions"), c.cfg.Providers)
		next := config.Provider{
			TemplateID: provider.ProviderKindCompatible,
			Kind:       provider.ProviderKindCompatible,
			Name:       id,
			BaseURL:    decisionProvider.BaseURL,
			Transport:  decisionProvider.Transport,
			Features:   &decisionProvider.Features,
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
