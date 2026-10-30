package websearch

import (
	"slices"

	"github.com/lkarlslund/koder/internal/config"
)

// Ordered returns every provider, those named in order first and in that
// order, then the rest in their default order.
func Ordered(order []string) []Provider {
	out := make([]Provider, 0, len(catalog))
	for _, id := range order {
		if p, ok := Lookup(id); ok && !slices.ContainsFunc(out, func(q Provider) bool { return q.ID == id }) {
			out = append(out, p)
		}
	}
	for _, p := range catalog {
		if !slices.ContainsFunc(out, func(q Provider) bool { return q.ID == p.ID }) {
			out = append(out, p)
		}
	}
	return out
}

// Backend is a provider ready to search, with its settings.
type Backend struct {
	Provider Provider
	Settings Settings
}

// SettingsFrom reads a provider's settings from its configuration.
func SettingsFrom(cfg config.SearchProvider) Settings {
	return Settings{APIKey: cfg.ResolvedAPIKey(), BaseURL: cfg.BaseURL}
}

// Backends returns the enabled providers whose settings are complete, most
// preferred first. It is never nil, so an empty result means none.
func Backends(cfg config.Search) []Backend {
	out := []Backend{}
	for _, p := range Ordered(cfg.Order) {
		pc := cfg.Providers[p.ID]
		settings := SettingsFrom(pc)
		if !pc.EnabledOr(p.DefaultEnabled) || p.Ready(settings) != nil {
			continue
		}
		out = append(out, Backend{Provider: p, Settings: settings})
	}
	return out
}
