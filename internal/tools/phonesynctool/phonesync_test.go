package phonesynctool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/phonesync"
	"github.com/lkarlslund/koder/internal/tools"
)

func call(t *testing.T, store *phonesync.Store, kind tools.ID, args map[string]string) (tools.Result, error) {
	t.Helper()
	return tools.Call(context.Background(), tools.Options{
		Runtime: tools.Runtime{SessionID: "s", ChatID: "c", Services: RuntimeService(store)},
		Request: tools.Request{Tool: kind, Args: args},
	})
}

func TestLogsToolFormatsFilteredLines(t *testing.T) {
	store := phonesync.Open(t.TempDir())
	now := time.Now().UTC()
	logs := []phonesync.LogEntry{
		{Seq: 1, Time: now.Add(-3 * time.Hour), Level: "E", Tag: "audio", Message: "old failure"},
		{Seq: 2, Time: now.Add(-time.Minute), Level: "I", Tag: "call", Message: "started"},
		{Seq: 3, Time: now.Add(-30 * time.Second), Level: "W", Tag: "popup", Message: "Speech audio arrived out of order"},
	}
	if _, err := store.Sync("dev1", "Pixel", phonesync.SyncRequest{LogsAllowed: true, AlertsAllowed: true, LogEpoch: "e", Logs: logs}, true); err != nil {
		t.Fatal(err)
	}
	result, err := call(t, store, tools.PhoneLogs, map[string]string{"since": "1h", "level": "warn"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Phone Pixel (dev1), last synced") ||
		!strings.Contains(result.Output, " W popup: Speech audio arrived out of order") ||
		strings.Contains(result.Output, "old failure") || strings.Contains(result.Output, "started") {
		t.Fatalf("output:\n%s", result.Output)
	}
	if _, err := call(t, store, tools.PhoneLogs, map[string]string{"level": "loud"}); err == nil {
		t.Fatal("expected invalid level to fail")
	}
}

func TestAlertsToolSchedulesListsAndCancels(t *testing.T) {
	store := phonesync.Open(t.TempDir())
	if _, err := store.Sync("dev1", "Pixel", phonesync.SyncRequest{LogsAllowed: true, AlertsAllowed: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, store, tools.PhoneAlerts, map[string]string{"action": "schedule", "title": "Tea"}); err == nil {
		t.Fatal("expected schedule without a time to fail")
	}
	result, err := call(t, store, tools.PhoneAlerts, map[string]string{"action": "schedule", "title": "Tea", "message": "Kettle", "in": "45m"})
	if err != nil {
		t.Fatal(err)
	}
	alertID := result.Meta["alert_id"]
	if alertID == "" || !strings.Contains(result.Output, `"Tea"`) || !strings.Contains(result.Output, "in 45m") {
		t.Fatalf("schedule output: %s meta %v", result.Output, result.Meta)
	}
	result, err = call(t, store, tools.PhoneAlerts, map[string]string{"action": "list"})
	if err != nil || !strings.Contains(result.Output, alertID+" ") || !strings.Contains(result.Output, "pending") {
		t.Fatalf("list output: %s err %v", result.Output, err)
	}
	result, err = call(t, store, tools.PhoneAlerts, map[string]string{"action": "cancel", "alert_id": alertID})
	if err != nil || !strings.Contains(result.Output, "before it reached the phone") {
		t.Fatalf("cancel output: %s err %v", result.Output, err)
	}
}

func TestToolsAppearOnlyForPhonesThatAllowThem(t *testing.T) {
	store := phonesync.Open(t.TempDir())
	offered := func() map[string]bool {
		names := map[string]bool{}
		for _, def := range tools.Definitions(tools.Runtime{SessionID: "s", ChatID: "c", Services: RuntimeService(store)}) {
			names[def.Function.Name] = true
		}
		return names
	}
	if names := offered(); names[tools.PhoneLogs.String()] || names[tools.PhoneAlerts.String()] {
		t.Fatalf("phone tools offered before any sync: %v", names)
	}
	if _, err := store.Sync("dev1", "Pixel", phonesync.SyncRequest{AlertsAllowed: true}, false); err != nil {
		t.Fatal(err)
	}
	if names := offered(); names[tools.PhoneLogs.String()] || !names[tools.PhoneAlerts.String()] {
		t.Fatalf("tools offered for a phone that accepts only alerts: %v", names)
	}
	if _, err := store.Sync("dev1", "Pixel", phonesync.SyncRequest{LogsAllowed: true, AlertsAllowed: true}, false); err != nil {
		t.Fatal(err)
	}
	if names := offered(); !names[tools.PhoneLogs.String()] || !names[tools.PhoneAlerts.String()] {
		t.Fatalf("phone tools missing after sync: %v", names)
	}
}
