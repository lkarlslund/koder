package memorytool

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/tools"
)

func call(t *testing.T, runtime tools.Runtime, args map[string]string) (string, error) {
	t.Helper()
	result, err := tools.Call(t.Context(), tools.Options{Runtime: runtime, Request: tools.Request{Tool: tools.Memory, Args: args}})
	return result.Output, err
}

func TestMemoryToolCreatesReadsUpdatesAndDeletes(t *testing.T) {
	runtime := tools.Runtime{Workdir: "/work/koder", Memory: memory.NewStore(t.TempDir(), t.TempDir())}
	out, err := call(t, runtime, map[string]string{"action": "create", "scope": "global", "name": "terse-answers", "description": "User wants terse answers", "content": "No closing summaries. Why: they read the diff."})
	if err != nil || out != "Created global memory terse-answers: User wants terse answers" {
		t.Fatalf("create = %q, %v", out, err)
	}
	// Without a scope, get finds the memory in whichever scope has it.
	out, err = call(t, runtime, map[string]string{"action": "get", "name": "terse-answers"})
	if err != nil || !strings.Contains(out, "No closing summaries. Why: they read the diff.") || !strings.HasPrefix(out, "global memory terse-answers") {
		t.Fatalf("get = %q, %v", out, err)
	}
	if _, err = call(t, runtime, map[string]string{"action": "update", "name": "terse-answers", "content": "No closing summaries."}); err != nil {
		t.Fatal(err)
	}
	out, _ = call(t, runtime, map[string]string{"action": "get", "name": "terse-answers"})
	if !strings.Contains(out, "User wants terse answers") || !strings.HasSuffix(out, "No closing summaries.") {
		t.Fatalf("update kept the description and replaced the content? %q", out)
	}
	out, _ = call(t, runtime, map[string]string{"action": "list"})
	if !strings.Contains(out, "- terse-answers: User wants terse answers") || !strings.Contains(out, "Project memory (/work/koder), 0 of 100") {
		t.Fatalf("list = %q", out)
	}
	if out, err = call(t, runtime, map[string]string{"action": "delete", "name": "terse-answers"}); err != nil || out != "Deleted global memory terse-answers" {
		t.Fatalf("delete = %q, %v", out, err)
	}
	if _, err = call(t, runtime, map[string]string{"action": "get", "name": "terse-answers"}); err == nil {
		t.Fatal("get after delete succeeded")
	}
}

func TestMemoryToolNeedsScopeForAmbiguousName(t *testing.T) {
	runtime := tools.Runtime{Workdir: "/work/koder", Memory: memory.NewStore(t.TempDir(), t.TempDir())}
	for _, scope := range []string{"global", "project"} {
		if _, err := call(t, runtime, map[string]string{"action": "create", "scope": scope, "name": "build", "description": scope + " build note", "content": "c"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := call(t, runtime, map[string]string{"action": "get", "name": "build"}); err == nil || !strings.Contains(err.Error(), "give a scope") {
		t.Fatalf("ambiguous get error = %v", err)
	}
	out, err := call(t, runtime, map[string]string{"action": "get", "scope": "project", "name": "build"})
	if err != nil || !strings.Contains(out, "project build note") {
		t.Fatalf("scoped get = %q, %v", out, err)
	}
}

func TestMemoryToolValidatesArguments(t *testing.T) {
	runtime := tools.Runtime{Memory: memory.NewStore(t.TempDir(), t.TempDir())}
	for _, args := range []map[string]string{
		{"action": "remember", "content": "x"},
		{"action": "create", "scope": "global", "name": "x", "description": "d"},
		{"action": "update", "name": "x"},
		{"action": "get"},
		{"action": "create", "scope": "team", "name": "x", "description": "d", "content": "c"},
	} {
		if _, err := call(t, runtime, args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}
