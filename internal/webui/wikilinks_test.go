package webui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveWikilinksLikeObsidian(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{
		"notes/.obsidian/app.json",
		"notes/hub.md",
		"notes/buses/CAN-ECAN.md",
		"notes/ecus/BMS.md",
		"notes/ecus/readme.md",
		"notes/readme.md",
		"notes/assets/diagram.png",
		"other/BMS.md",
	} {
		full := filepath.Join(root, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	content := "See [[CAN-ECAN]], [[bms|the BMS]], [[ecus/readme#Wiring]], [[readme]], ![[diagram.png|300]], [[#Local]] and [[Missing note]]."
	got := resolveWikilinks(root, "notes/ecus/BMS.md", content)
	want := map[string]string{
		"CAN-ECAN":    "notes/buses/CAN-ECAN.md",
		"bms":         "notes/ecus/BMS.md",
		"ecus/readme": "notes/ecus/readme.md",
		// Both readme notes match; the one beside the linking note wins.
		"readme":      "notes/ecus/readme.md",
		"diagram.png": "notes/assets/diagram.png",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wikilinks = %v, want %v", got, want)
	}
	// From the vault root, the root readme is nearer.
	if got := resolveWikilinks(root, "notes/hub.md", "[[readme]]"); got["readme"] != "notes/readme.md" {
		t.Fatalf("from the vault root: %v", got)
	}
}

func TestDetectObsidianNote(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("strict/.obsidian/app.json", `{"strictLineBreaks": true}`)
	write("strict/note.md", "plain")
	write("knowledge/hub.md", "see [[people/someone]]")
	write("knowledge/people/someone.md", "no links here")
	write("README.md", "# Project\nplain markdown")
	write("docs/guide.md", "plain guide")

	if got := detectObsidianNote(root, "strict/note.md", "plain"); got == nil || !got.StrictLineBreaks {
		t.Fatalf("vault with app.json: %+v", got)
	}
	// No .obsidian: a note without links of its own is still in the vault
	// when a note in a parent folder links.
	if got := detectObsidianNote(root, "knowledge/people/someone.md", "no links here"); got == nil || got.StrictLineBreaks {
		t.Fatalf("note in a linked folder: %+v", got)
	}
	for _, rel := range []string{"README.md", "docs/guide.md"} {
		if got := detectObsidianNote(root, rel, "plain"); got != nil {
			t.Fatalf("%s is plain Markdown, got %+v", rel, got)
		}
	}
}
