package browser

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// junkLink matches links that never help a browsing goal: sharing,
// accounts, legal and consent pages, feeds and wiki tooling.
var junkLink = regexp.MustCompile(`(?i)(` +
	`/share|sharer|share\?|[?&](text|status)=https?|wa\.me/|mailto:|/intent/|` +
	`/login|/signin|/sign-in|/signup|/register|/account|/cart|/checkout|` +
	`/privacy|/cookie|/terms|/legal|/impressum|/disclaimer|/copyright|` +
	`/advertis|/careers|/jobs|/press|/feed|\.rss|/rss|` +
	`[?&]action=|[?&]mobileaction=|[?&]useparsoid=|[?&]oldid=|[?&]printable=|/Special:|/Talk:|/User:` +
	`)`)

// socialHosts are sites a page links to for sharing or following, never
// for content.
var socialHosts = []string{"facebook.com", "twitter.com", "x.com", "linkedin.com", "instagram.com", "pinterest.com",
	"reddit.com", "tiktok.com", "youtube.com", "t.me", "whatsapp.com", "wa.me", "spotify.com", "apple.com", "google.com"}

// secondLevelLabels are the generic labels country domains register names
// under.
var secondLevelLabels = []string{"co", "com", "net", "org", "gov", "edu", "ac", "gob", "nic", "mil", "ltd", "plc", "or", "ne", "go"}

// canonicalURL drops what makes one page look like many: the fragment,
// tracking parameters and a trailing slash.
func canonicalURL(parsed *url.URL) string {
	out := *parsed
	out.Fragment = ""
	if out.RawQuery != "" {
		query := out.Query()
		for key := range query {
			lower := strings.ToLower(key)
			if strings.HasPrefix(lower, "utm_") || lower == "fbclid" || lower == "gclid" || lower == "ref" || lower == "source" {
				query.Del(key)
			}
		}
		out.RawQuery = query.Encode()
	}
	if len(out.Path) > 1 {
		out.Path = strings.TrimSuffix(out.Path, "/")
		out.RawPath = ""
	}
	return out.String()
}

// siteOf returns the registrable part of a host, such as wikipedia.org for
// en.wikipedia.org, or bbc.co.uk for www.bbc.co.uk.
func siteOf(host string) string {
	labels := strings.Split(strings.TrimSuffix(strings.ToLower(host), "."), ".")
	keep := 2
	// Country domains with a generic second level (co.uk, com.au, gov.pt)
	// keep three labels.
	if n := len(labels); n >= 3 && len(labels[n-1]) == 2 && slices.Contains(secondLevelLabels, labels[n-2]) {
		keep = 3
	}
	if len(labels) <= keep {
		return strings.Join(labels, ".")
	}
	return strings.Join(labels[len(labels)-keep:], ".")
}

func isSocialHost(host string) bool {
	return slices.Contains(socialHosts, siteOf(host))
}

// languages are the path prefixes sites put translated pages under:
// language codes, and the country codes some sites use instead.
var languages = []string{"ar", "bg", "cs", "da", "de", "el", "en", "es", "et", "fi", "fr", "he", "hi", "hr", "hu", "id", "it", "ja", "ko",
	"lt", "lv", "nl", "no", "pl", "pt", "ro", "ru", "sk", "sl", "sr", "sv", "th", "tr", "uk", "vi", "zh",
	"at", "be", "br", "ch", "cn", "cz", "dk", "gr", "jp", "kr", "mx", "nb", "se", "ua"}

// languageOf returns the language prefix of a URL's path, such as "fr" for
// /fr/nouvelles/x or "pt" for /pt-br/x, or "" when it has none.
func languageOf(parsed *url.URL) string {
	segment, _, _ := strings.Cut(strings.TrimPrefix(parsed.Path, "/"), "/")
	language, _, _ := strings.Cut(strings.ToLower(segment), "-")
	if len(segment) > 5 || !slices.Contains(languages, language) {
		return ""
	}
	return language
}
