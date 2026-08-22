package knowledge

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeTitle(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"  Linux\n partition\ttools  ": "Linux partition tools",
		"A\u030arhus":                  "Århus",
		"":                             "",
	}
	for input, want := range tests {
		if got := NormalizeTitle(input); got != want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeAliases(t *testing.T) {
	t.Parallel()
	got := NormalizeAliases("Linux partition tools", []string{
		" disk\npartitioning ",
		"DISK PARTITIONING",
		"Linux partition tools",
		"LINUX PARTITION TOOLS",
		"sfdisk",
		"",
	})
	want := []string{"disk partitioning", "sfdisk"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeAliases() = %#v, want %#v", got, want)
	}
}

func TestNormalizeTags(t *testing.T) {
	t.Parallel()
	got := NormalizeTags([]string{" Linux ", "physical safety", "LINUX", " Århus ", "---", "C++"})
	want := []string{"c++", "linux", "physical-safety", "århus"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeTags() = %#v, want %#v", got, want)
	}
}

func TestNormalizeLocale(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"":        "",
		" en ":    "en",
		"EN_dk":   "en-DK",
		"zh_hant": "zh-Hant",
	}
	for input, want := range tests {
		got, err := NormalizeLocale(input)
		if err != nil {
			t.Fatalf("NormalizeLocale(%q) error = %v", input, err)
		}
		if got != want {
			t.Errorf("NormalizeLocale(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := NormalizeLocale("not a locale !!!"); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("invalid locale error = %v, want ErrInvalidRecord", err)
	}
}

func TestLexicalTerms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  []string
	}{
		{input: "Linux partition tools", want: []string{"linux", "partition", "tools"}},
		{input: "Århus and A\u030ARHUS", want: []string{"århus", "and", "århus"}},
		{input: "Use sfdisk --dump with go.mod, C++ and C#.", want: []string{"use", "sfdisk", "dump", "with", "go.mod", "c++", "and", "c#"}},
		{input: "... --- +++ ###", want: nil},
	}
	for _, tt := range tests {
		if got := LexicalTerms(tt.input); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("LexicalTerms(%q) = %#v, want %#v", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeChunk(t *testing.T) {
	t.Parallel()
	chunk := validChunk()
	chunk.Title = "  Linux\npartition tools "
	chunk.Aliases = []string{"linux partition tools", " Disk tools "}
	chunk.Tags = []string{" Storage ", "storage", "Linux"}
	chunk.Language = "EN"
	chunk.Locale = "en_dk"

	got, err := NormalizeChunk(chunk)
	if err != nil {
		t.Fatalf("NormalizeChunk() error = %v", err)
	}
	if got.Title != "Linux partition tools" || !reflect.DeepEqual(got.Aliases, []string{"Disk tools"}) {
		t.Fatalf("NormalizeChunk() title/aliases = %q/%#v", got.Title, got.Aliases)
	}
	if !reflect.DeepEqual(got.Tags, []string{"linux", "storage"}) || got.Language != "en" || got.Locale != "en-DK" {
		t.Fatalf("NormalizeChunk() tags/language/locale = %#v/%q/%q", got.Tags, got.Language, got.Locale)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("normalized chunk Validate() error = %v", err)
	}
}

func TestNormalizeEntry(t *testing.T) {
	t.Parallel()
	entry := validEntry()
	entry.Title = "  Use\tsfdisk "
	entry.Aliases = []string{"USE SFDISK", " partition disk "}
	entry.Tags = []string{" Storage Tools ", "Linux"}
	entry.Applicability.Locales = []string{"EN_dk", "da_dk", "en-DK"}

	got, err := NormalizeEntry(entry)
	if err != nil {
		t.Fatalf("NormalizeEntry() error = %v", err)
	}
	if got.Title != "Use sfdisk" || !reflect.DeepEqual(got.Aliases, []string{"partition disk"}) {
		t.Fatalf("NormalizeEntry() title/aliases = %q/%#v", got.Title, got.Aliases)
	}
	if !reflect.DeepEqual(got.Tags, []string{"linux", "storage-tools"}) {
		t.Fatalf("NormalizeEntry() tags = %#v", got.Tags)
	}
	if !reflect.DeepEqual(got.Applicability.Locales, []string{"da-DK", "en-DK"}) {
		t.Fatalf("NormalizeEntry() locales = %#v", got.Applicability.Locales)
	}
}

func TestNormalizeLinkCanonicalizesOnlySymmetricEndpoints(t *testing.T) {
	t.Parallel()
	low := ObjectRef{Kind: ObjectKindChunk, ID: string(testChunkID)}
	high := ObjectRef{Kind: ObjectKindEntry, ID: string(testEntryID)}
	symmetric := NormalizeLink(Link{Source: high, Target: low, Kind: LinkKindRelatedTo, Label: "  Same   topic "})
	if symmetric.Source != low || symmetric.Target != high || symmetric.Label != "Same topic" {
		t.Fatalf("normalized symmetric link = %#v", symmetric)
	}
	directed := NormalizeLink(Link{Source: high, Target: low, Kind: LinkKindRequires})
	if directed.Source != high || directed.Target != low {
		t.Fatalf("directed endpoints were reordered: %#v", directed)
	}
}
