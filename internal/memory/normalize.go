package memory

import (
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// NormalizeTitle converts a user-facing title to canonical Unicode and whitespace while
// preserving its spelling and capitalization.
func NormalizeTitle(value string) string {
	return strings.Join(strings.Fields(norm.NFC.String(value)), " ")
}

// NormalizeComparisonKey creates a canonical case-insensitive key for exact title and
// alias lookup. It is not tokenization; use LexicalTerms for full-text search.
func NormalizeComparisonKey(value string) string {
	value = norm.NFKC.String(value)
	value = cases.Fold().String(value)
	return strings.Join(strings.Fields(value), " ")
}

// NormalizeEvidenceIdentity canonicalizes the stable source/hash pair used to deduplicate
// immutable evidence snapshots. Source IDs retain case because some source namespaces are
// case-sensitive; conventional content hashes are case-insensitive.
func NormalizeEvidenceIdentity(sourceID, contentHash string) (string, string) {
	return strings.TrimSpace(sourceID), strings.ToLower(strings.TrimSpace(contentHash))
}

// NormalizeAliases canonicalizes aliases, removes empty/title-equivalent values, and
// deduplicates them case-insensitively while preserving first-seen display order.
func NormalizeAliases(title string, values []string) []string {
	titleKey := NormalizeComparisonKey(NormalizeTitle(title))
	seen := make(map[string]struct{}, len(values))
	output := make([]string, 0, len(values))
	for _, value := range values {
		alias := NormalizeTitle(value)
		key := NormalizeComparisonKey(alias)
		if key == "" || key == titleKey {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		output = append(output, alias)
	}
	if len(output) == 0 {
		return nil
	}
	return output
}

// NormalizeTags converts tags to case-folded, whitespace-free set values. Tags are sorted
// because their input order has no domain meaning.
func NormalizeTags(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	output := make([]string, 0, len(values))
	for _, value := range values {
		value = cases.Fold().String(norm.NFKC.String(value))
		tag := strings.Join(strings.Fields(value), "-")
		tag = strings.Trim(tag, "-_.")
		if tag == "" {
			continue
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		output = append(output, tag)
	}
	if len(output) == 0 {
		return nil
	}
	slices.Sort(output)
	return output
}

// NormalizeLocale converts a language or locale to its canonical BCP 47 spelling.
func NormalizeLocale(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	tag, err := language.Parse(strings.ReplaceAll(value, "_", "-"))
	if err != nil {
		return "", invalid("locale", "must be a valid BCP 47 language tag")
	}
	return tag.String(), nil
}

// LexicalTerms creates deterministic, case-folded search terms. It retains punctuation
// that carries meaning inside technical identifiers such as c++, c#, and go.mod.
func LexicalTerms(value string) []string {
	value = cases.Fold().String(norm.NFKC.String(value))
	terms := make([]string, 0, 16)
	current := make([]rune, 0, 24)
	hasLetterOrDigit := false
	flush := func() {
		if !hasLetterOrDigit {
			current = current[:0]
			hasLetterOrDigit = false
			return
		}
		term := strings.Trim(string(current), "-_.")
		if term != "" {
			terms = append(terms, term)
		}
		current = current[:0]
		hasLetterOrDigit = false
	}

	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			current = append(current, r)
			hasLetterOrDigit = true
		case r == '+' || r == '#' || r == '.' || r == '_' || r == '-':
			if len(current) > 0 {
				current = append(current, r)
			}
		default:
			flush()
		}
	}
	flush()
	if len(terms) == 0 {
		return nil
	}
	return terms
}

// NormalizeChunk returns a canonicalized copy suitable for validation and storage.
func NormalizeChunk(value Chunk) (Chunk, error) {
	value.Title = NormalizeTitle(value.Title)
	value.Aliases = NormalizeAliases(value.Title, value.Aliases)
	value.Tags = NormalizeTags(value.Tags)
	var err error
	value.Language, err = NormalizeLocale(value.Language)
	if err != nil {
		return Chunk{}, err
	}
	value.Locale, err = NormalizeLocale(value.Locale)
	if err != nil {
		return Chunk{}, err
	}
	return value, nil
}

// NormalizeEntry returns a canonicalized copy suitable for validation and storage.
func NormalizeEntry(value Entry) (Entry, error) {
	value.Title = NormalizeTitle(value.Title)
	value.Body = NormalizeMarkdown(value.Body)
	value.Aliases = NormalizeAliases(value.Title, value.Aliases)
	value.Tags = NormalizeTags(value.Tags)
	locales := make([]string, 0, len(value.Applicability.Locales))
	seenLocales := make(map[string]struct{}, len(value.Applicability.Locales))
	for _, locale := range value.Applicability.Locales {
		normalized, err := NormalizeLocale(locale)
		if err != nil {
			return Entry{}, err
		}
		if normalized == "" {
			continue
		}
		if _, exists := seenLocales[normalized]; exists {
			continue
		}
		seenLocales[normalized] = struct{}{}
		locales = append(locales, normalized)
	}
	slices.Sort(locales)
	value.Applicability.Locales = locales
	return value, nil
}

// NormalizeLink canonicalizes user-facing relationship text without changing endpoint
// direction or typed identity.
func NormalizeLink(value Link) Link {
	value.Label = NormalizeTitle(value.Label)
	value.Notes = strings.TrimSpace(value.Notes)
	if IsSymmetricLinkKind(value.Kind) && compareObjectRefs(value.Source, value.Target) > 0 {
		value.Source, value.Target = value.Target, value.Source
	}
	return value
}

func compareObjectRefs(left, right ObjectRef) int {
	if left.Kind < right.Kind {
		return -1
	}
	if left.Kind > right.Kind {
		return 1
	}
	return strings.Compare(left.ID, right.ID)
}
