package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/websearch"
)

// SearchPreferences are the web search providers, most preferred first.
type SearchPreferences struct {
	Providers []SearchProviderPreference `json:"providers"`
}

// SearchProviderPreference is one web search provider in Settings. The API
// key is never sent to the browser: APIKeySet says whether one is stored, a
// non-empty APIKey replaces it, and ClearAPIKey removes it.
type SearchProviderPreference struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Pricing      string `json:"pricing"`
	SignupURL    string `json:"signup_url"`
	NeedsKey     bool   `json:"needs_key"`
	NeedsBaseURL bool   `json:"needs_base_url"`
	Enabled      bool   `json:"enabled"`
	APIKey       string `json:"api_key,omitempty"`
	APIKeySet    bool   `json:"api_key_set"`
	ClearAPIKey  bool   `json:"clear_api_key,omitempty"`
	APIKeyEnv    string `json:"api_key_env"`
	BaseURL      string `json:"base_url"`
}

func searchPreferencesFromConfig(cfg config.Search) SearchPreferences {
	prefs := SearchPreferences{Providers: []SearchProviderPreference{}}
	for _, p := range websearch.Ordered(cfg.Order) {
		pc := cfg.Providers[p.ID]
		prefs.Providers = append(prefs.Providers, SearchProviderPreference{
			ID: p.ID, Title: p.Title, Description: p.Description, Pricing: p.Pricing, SignupURL: p.SignupURL,
			NeedsKey: p.NeedsKey, NeedsBaseURL: p.NeedsBaseURL,
			Enabled:   pc.EnabledOr(p.DefaultEnabled),
			APIKeySet: strings.TrimSpace(pc.APIKey) != "",
			APIKeyEnv: pc.APIKeyEnv,
			BaseURL:   pc.BaseURL,
		})
	}
	return prefs
}

// applySearchPreferences stores the providers' order and settings. An empty
// list, from a client that does not know search settings, changes nothing.
func applySearchPreferences(cfg *config.Config, prefs SearchPreferences) error {
	if len(prefs.Providers) == 0 {
		return nil
	}
	next := config.Search{Providers: map[string]config.SearchProvider{}}
	for _, pref := range prefs.Providers {
		p, ok := websearch.Lookup(pref.ID)
		if !ok {
			return fmt.Errorf("unknown search provider %q", pref.ID)
		}
		pc := searchProviderFromPreference(cfg.Search.Providers[p.ID], pref)
		if pref.Enabled {
			if err := p.Ready(websearch.SettingsFrom(pc)); err != nil {
				return fmt.Errorf("search: %w, or turn it off", err)
			}
		}
		next.Order = append(next.Order, p.ID)
		next.Providers[p.ID] = pc
	}
	cfg.Search = next
	return nil
}

// searchProviderFromPreference applies a preference to a provider's stored
// configuration, keeping the stored key unless the preference changes it.
func searchProviderFromPreference(existing config.SearchProvider, pref SearchProviderPreference) config.SearchProvider {
	enabled := pref.Enabled
	next := config.SearchProvider{
		Enabled:   &enabled,
		APIKey:    existing.APIKey,
		APIKeyEnv: strings.TrimSpace(pref.APIKeyEnv),
		BaseURL:   strings.TrimRight(strings.TrimSpace(pref.BaseURL), "/"),
	}
	if pref.ClearAPIKey {
		next.APIKey = ""
	}
	if key := strings.TrimSpace(pref.APIKey); key != "" {
		next.APIKey = key
	}
	return next
}

// searchHealthIssues reports when web search has no working provider.
func searchHealthIssues(cfg config.Search, add func(area, target, source, severity, message string)) {
	if len(websearch.Backends(cfg)) == 0 {
		add("search", "search", "search-providers", "warning", "No web search provider is enabled; web_search will fail")
	}
}

// SearchTestResult reports a trial search.
type SearchTestResult struct {
	Provider  string `json:"provider"`
	Results   int    `json:"results"`
	FirstURL  string `json:"first_url,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

// TestSearchProvider runs a trial search with a provider's draft settings,
// falling back to the stored key when the draft has none.
func (c *Controller) TestSearchProvider(ctx context.Context, pref SearchProviderPreference) (SearchTestResult, error) {
	p, ok := websearch.Lookup(pref.ID)
	if !ok {
		return SearchTestResult{}, fmt.Errorf("unknown search provider %q", pref.ID)
	}
	c.mu.RLock()
	existing := c.cfg.Search.Providers[p.ID]
	c.mu.RUnlock()
	settings := websearch.SettingsFrom(searchProviderFromPreference(existing, pref))
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	started := time.Now()
	results, err := p.Search(ctx, nil, settings, websearch.Query{Text: "golang tutorial", Limit: 3})
	if err != nil {
		return SearchTestResult{}, err
	}
	out := SearchTestResult{Provider: p.Title, Results: len(results), ElapsedMS: time.Since(started).Milliseconds()}
	if len(results) > 0 {
		out.FirstURL = results[0].URL
	}
	return out, nil
}
