package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
)

func searchPref(t *testing.T, prefs PreferencesState, id string) *SearchProviderPreference {
	t.Helper()
	for i := range prefs.Search.Providers {
		if prefs.Search.Providers[i].ID == id {
			return &prefs.Search.Providers[i]
		}
	}
	t.Fatalf("no search provider %q in %+v", id, prefs.Search.Providers)
	return nil
}

func TestSearchPreferencesKeepKeysOutOfTheBrowser(t *testing.T) {
	ctrl, _ := newPersistentTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{"test": {BaseURL: "https://example.invalid/v1"}}
	})
	ctx := context.Background()
	prefs, err := ctrl.Preferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefs.Search.Providers) != 14 || !searchPref(t, prefs, "duckduckgo").Enabled || searchPref(t, prefs, "exa").Enabled {
		t.Fatalf("default providers = %+v", prefs.Search.Providers)
	}

	// Enabling a provider without its key is refused.
	searchPref(t, prefs, "exa").Enabled = true
	if _, err := ctrl.SavePreferences(ctx, prefs); err == nil || !strings.Contains(err.Error(), "Exa needs an API key") {
		t.Fatalf("save without key: %v", err)
	}

	// Exa moves to the end of the order, with a key.
	exa := *searchPref(t, prefs, "exa")
	exa.APIKey = "exa-secret"
	var reordered []SearchProviderPreference
	for _, p := range prefs.Search.Providers {
		if p.ID != "exa" {
			reordered = append(reordered, p)
		}
	}
	prefs.Search.Providers = append(reordered, exa)
	saved, err := ctrl.SavePreferences(ctx, prefs)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(saved)
	if strings.Contains(string(data), "exa-secret") {
		t.Fatal("the API key reached the browser")
	}
	last := saved.Search.Providers[len(saved.Search.Providers)-1]
	if last.ID != "exa" || !last.Enabled || !last.APIKeySet {
		t.Fatalf("exa after save = %+v", last)
	}
	if ctrl.cfg.Search.Providers["exa"].APIKey != "exa-secret" {
		t.Fatalf("stored config = %+v", ctrl.cfg.Search.Providers["exa"])
	}

	// Saving again without a key keeps it; clearing it removes it.
	searchPref(t, saved, "duckduckgo").Enabled = false
	if saved, err = ctrl.SavePreferences(ctx, saved); err != nil || !searchPref(t, saved, "exa").APIKeySet {
		t.Fatalf("resave: %+v, %v", searchPref(t, saved, "exa"), err)
	}
	searchPref(t, saved, "exa").Enabled = false
	searchPref(t, saved, "exa").ClearAPIKey = true
	if saved, err = ctrl.SavePreferences(ctx, saved); err != nil || searchPref(t, saved, "exa").APIKeySet {
		t.Fatalf("clear key: %+v, %v", searchPref(t, saved, "exa"), err)
	}
	found := false
	for _, issue := range saved.Health.Issues {
		found = found || issue.Area == "search"
	}
	if !found {
		t.Fatalf("no health issue with every provider off: %+v", saved.Health.Issues)
	}
}
