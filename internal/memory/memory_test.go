package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreCreatesUpdatesAndDeletesMemories(t *testing.T) {
	store := NewStore(t.TempDir(), t.TempDir())
	root := "/home/lak/github-repos/koder"
	created, err := store.Create(Memory{Scope: ScopeProject, Name: "deploy-local", Description: "How to deploy to port 7979", Content: "Run scripts/build-koder, then restart koder.service."}, root)
	if err != nil {
		t.Fatal(err)
	}
	if created.Content != "Run scripts/build-koder, then restart koder.service." || created.UpdatedAt.IsZero() {
		t.Fatalf("created = %+v", created)
	}
	if _, err := store.Create(Memory{Scope: ScopeProject, Name: "deploy-local", Description: "x", Content: "y"}, root); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate create error = %v", err)
	}
	if _, err := store.Update(Memory{Scope: ScopeProject, Name: "deploy-local", Description: "Deploy steps for 7979", Content: "Check /debug/chats first."}, root); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ScopeProject, root, "deploy-local")
	if err != nil || got.Description != "Deploy steps for 7979" || got.Content != "Check /debug/chats first." {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if list, _ := store.List(ScopeGlobal, root); len(list) != 0 {
		t.Fatalf("project memory leaked into global: %+v", list)
	}
	if err := store.Delete(ScopeProject, root, "deploy-local"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ScopeProject, root, "deploy-local"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete error = %v", err)
	}
	if _, err := store.Update(Memory{Scope: ScopeGlobal, Name: "missing", Description: "x", Content: "y"}, root); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update of missing memory error = %v", err)
	}
}

func TestStoreKeepsProjectsApart(t *testing.T) {
	store := NewStore(t.TempDir(), t.TempDir())
	if _, err := store.Create(Memory{Scope: ScopeProject, Name: "note", Description: "a", Content: "a"}, "/work/a"); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.List(ScopeProject, "/work/b"); len(list) != 0 {
		t.Fatalf("project b sees project a's memory: %+v", list)
	}
	if _, err := store.Create(Memory{Scope: ScopeProject, Name: "note", Description: "b", Content: "b"}, ""); err == nil {
		t.Fatal("project memory without a project was accepted")
	}
}

func TestStoreRejectsBadInput(t *testing.T) {
	store := NewStore(t.TempDir(), t.TempDir())
	cases := map[string]Memory{
		"name":             {Scope: ScopeGlobal, Name: "Bad Name", Description: "d", Content: "c"},
		"description":      {Scope: ScopeGlobal, Name: "ok", Description: "", Content: "c"},
		"multiline":        {Scope: ScopeGlobal, Name: "ok", Description: "one\ntwo", Content: "c"},
		"content":          {Scope: ScopeGlobal, Name: "ok", Description: "d", Content: " "},
		"scope":            {Scope: "team", Name: "ok", Description: "d", Content: "c"},
		"private key":      {Scope: ScopeGlobal, Name: "ok", Description: "d", Content: "-----BEGIN OPENSSH PRIVATE KEY-----\nabc"},
		"password":         {Scope: ScopeGlobal, Name: "ok", Description: "d", Content: "the router password: hunter2secret"},
		"github token":     {Scope: ScopeGlobal, Name: "ok", Description: "d", Content: "use ghp_" + strings.Repeat("a", 36)},
		"oversize content": {Scope: ScopeGlobal, Name: "ok", Description: "d", Content: strings.Repeat("x", MaxContentLen+1)},
	}
	for name, memory := range cases {
		if _, err := store.Create(memory, ""); err == nil {
			t.Errorf("%s: accepted %+v", name, memory)
		}
	}
}

func TestStoreRefusesCreateWhenFull(t *testing.T) {
	store := NewStore(t.TempDir(), t.TempDir())
	for i := range MaxMemories {
		if _, err := store.Create(Memory{Scope: ScopeGlobal, Name: "m" + strings.Repeat("a", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26)), Description: "d", Content: "c"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	_, err := store.Create(Memory{Scope: ScopeGlobal, Name: "one-more", Description: "d", Content: "c"}, "")
	if err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("create in a full scope error = %v", err)
	}
}

func TestStoreReadsHandEditedFiles(t *testing.T) {
	configDir := t.TempDir()
	store := NewStore(configDir, t.TempDir())
	dir, _ := store.Dir(ScopeGlobal, "")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "editor.md"), []byte("# Uses Neovim\r\nWith LazyVim.\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Not A Memory.md"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := store.List(ScopeGlobal, "")
	if err != nil || len(list) != 1 || list[0].Description != "Uses Neovim" || list[0].Content != "# Uses Neovim\nWith LazyVim." {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestInstructionsListBothScopes(t *testing.T) {
	store := NewStore(t.TempDir(), t.TempDir())
	root := "/work/koder"
	_, _ = store.Create(Memory{Scope: ScopeGlobal, Name: "terse-answers", Description: "User wants terse answers", Content: "c"}, root)
	_, _ = store.Create(Memory{Scope: ScopeProject, Name: "deploy-local", Description: "How to deploy to 7979", Content: "c"}, root)
	text := store.Instructions(root)
	for _, want := range []string{"Global memory", "1 of 100", "- terse-answers: User wants terse answers", "Project memory (/work/koder)", "- deploy-local: How to deploy to 7979"} {
		if !strings.Contains(text, want) {
			t.Fatalf("instructions lack %q:\n%s", want, text)
		}
	}
	if text := store.Instructions(""); strings.Contains(text, "Project memory") || !strings.Contains(text, "terse-answers") {
		t.Fatalf("instructions without a project:\n%s", text)
	}
}
