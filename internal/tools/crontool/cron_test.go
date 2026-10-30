package crontool

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/cron"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func call(t *testing.T, runtime tools.Runtime, args map[string]string) (tools.Result, error) {
	t.Helper()
	return tools.Call(t.Context(), tools.Options{Runtime: runtime, Request: tools.Request{Tool: tools.Cron, Args: args}})
}

func TestCronToolCreatesListsAndDeletes(t *testing.T) {
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	scheduler := cron.NewScheduler(st, nil)
	runtime := tools.Runtime{SessionID: "s1", ChatID: "c1", Services: RuntimeService(scheduler)}

	created, err := call(t, runtime, map[string]string{"action": "create", "schedule": "7 * * * *", "prompt": "Check the build queue"})
	if err != nil || !strings.HasPrefix(created.Output, "Scheduled recurring job ") || !strings.Contains(created.Output, "It expires") {
		t.Fatalf("create = %q, %v", created.Output, err)
	}
	if _, err := call(t, runtime, map[string]string{"action": "create", "schedule": "17 9 1 1 *", "prompt": "Happy new year", "recurring": "false"}); err != nil {
		t.Fatal(err)
	}
	listed, err := call(t, runtime, map[string]string{"action": "list"})
	if err != nil || listed.Meta["job_count"] != "2" || !strings.Contains(listed.Output, "recurring (7 * * * *)") || !strings.Contains(listed.Output, "one-shot (17 9 1 1 *)") {
		t.Fatalf("list = %q, %v", listed.Output, err)
	}
	other := runtime
	other.ChatID = "c2"
	if _, err := call(t, other, map[string]string{"action": "delete", "id": created.Meta["job_id"]}); err == nil {
		t.Fatal("another chat deleted the job")
	}
	if out, err := call(t, runtime, map[string]string{"action": "delete", "id": created.Meta["job_id"]}); err != nil || !strings.HasPrefix(out.Output, "Deleted cron job ") {
		t.Fatalf("delete = %q, %v", out.Output, err)
	}
}

func TestCronToolRejectsBadArguments(t *testing.T) {
	for _, args := range []map[string]string{
		{"action": "create", "prompt": "x"},
		{"action": "create", "schedule": "5m"},
		{"action": "create", "schedule": "5m", "prompt": "x", "recurring": "sometimes"},
		{"action": "delete"},
		{"action": "pause"},
	} {
		if _, err := (tool{}).NormalizeArgs(args); err == nil {
			t.Errorf("NormalizeArgs(%v) succeeded", args)
		}
	}
}
