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
