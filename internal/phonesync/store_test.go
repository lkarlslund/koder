package phonesync

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	store := Open(t.TempDir())
	store.now = func() time.Time { return now }
	return store, &now
}

func entry(seq uint64, level, tag, message string) LogEntry {
	return LogEntry{Seq: seq, Time: time.Date(2026, 10, 21, 11, 0, int(seq), 0, time.UTC), Level: level, Tag: tag, Message: message}
}

func TestSyncKeepsOnlyNewLogsWhileEnabled(t *testing.T) {
	store, _ := testStore(t)
	request := SyncRequest{LogsAllowed: true, AlertsAllowed: true, LogEpoch: "e1", Logs: []LogEntry{entry(2, "W", "popup", "Audio failed"), entry(1, "I", "call", "started")}}
	response, err := store.Sync("dev1", "Pixel", request, false)
	if err != nil {
		t.Fatal(err)
	}
	if response.LogsEnabled || response.LogCursor != 0 {
		t.Fatalf("disabled sync stored logs: %+v", response)
	}
	if _, logs, _ := store.Logs(LogQuery{}); len(logs) != 0 {
		t.Fatalf("logs kept while disabled: %+v", logs)
	}
	if response, err = store.Sync("dev1", "Pixel", request, true); err != nil || response.LogCursor != 2 {
		t.Fatalf("cursor = %d, err = %v", response.LogCursor, err)
	}
	request.Logs = append(request.Logs, entry(3, "E", "audio", "write stalled"))
	if response, err = store.Sync("dev1", "Pixel", request, true); err != nil || response.LogCursor != 3 {
		t.Fatalf("cursor = %d, err = %v", response.LogCursor, err)
	}
	device, logs, err := store.Logs(LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if device.Name != "Pixel" || len(logs) != 3 || logs[0].Seq != 1 || logs[2].Message != "write stalled" {
		t.Fatalf("device %+v logs %+v", device, logs)
	}
}

func TestNewLogEpochRestartsCursor(t *testing.T) {
	store, _ := testStore(t)
	if _, err := store.Sync("dev1", "Pixel", SyncRequest{LogsAllowed: true, AlertsAllowed: true, LogEpoch: "e1", Logs: []LogEntry{entry(5, "I", "a", "old")}}, true); err != nil {
		t.Fatal(err)
	}
	response, err := store.Sync("dev1", "Pixel", SyncRequest{LogsAllowed: true, AlertsAllowed: true, LogEpoch: "e2", Logs: []LogEntry{entry(1, "I", "a", "after reinstall")}}, true)
	if err != nil || response.LogCursor != 1 {
		t.Fatalf("cursor = %d, err = %v", response.LogCursor, err)
	}
}

func TestLogsFilterByLevelTagTextAndLimit(t *testing.T) {
	store, _ := testStore(t)
	logs := []LogEntry{
		entry(1, "D", "audio", "track created"),
		entry(2, "W", "popup", "Speech audio arrived out of order"),
		entry(3, "E", "audio", "write blocked 12s"),
		entry(4, "I", "call", "ended"),
	}
	if _, err := store.Sync("dev1", "Pixel", SyncRequest{LogsAllowed: true, AlertsAllowed: true, LogEpoch: "e", Logs: logs}, true); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		query LogQuery
		want  []uint64
	}{
		{LogQuery{MinLevel: "W"}, []uint64{2, 3}},
		{LogQuery{Tag: "AUDIO"}, []uint64{1, 3}},
		{LogQuery{Contains: "out of ORDER"}, []uint64{2}},
		{LogQuery{Limit: 2}, []uint64{3, 4}},
		{LogQuery{Since: logs[2].Time}, []uint64{3, 4}},
	}
	for _, tc := range cases {
		_, got, err := store.Logs(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		seqs := make([]uint64, 0, len(got))
		for _, line := range got {
			seqs = append(seqs, line.Seq)
		}
		if !slices.Equal(seqs, tc.want) {
			t.Fatalf("%+v: got %v want %v", tc.query, seqs, tc.want)
		}
	}
}

func TestAlertLifecycle(t *testing.T) {
	store, now := testStore(t)
	if _, _, err := store.ScheduleAlert("", "Stretch", "", now.Add(time.Hour)); err == nil {
		t.Fatal("expected an error before any phone synced")
	}
	if _, err := store.Sync("dev1", "Pixel", SyncRequest{LogsAllowed: true, AlertsAllowed: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ScheduleAlert("", "Late", "", now.Add(-time.Minute)); err == nil {
		t.Fatal("expected past fire time to fail")
	}
	_, alert, err := store.ScheduleAlert("", "Stretch", "Stand up", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	response, err := store.Sync("dev1", "Pixel", SyncRequest{LogsAllowed: true, AlertsAllowed: true}, false)
	if err != nil || len(response.Alerts) != 1 || response.Alerts[0].ID != alert.ID {
		t.Fatalf("pending alert not delivered: %+v %v", response, err)
	}
	response, err = store.Sync("dev1", "Pixel", SyncRequest{LogsAllowed: true, AlertsAllowed: true, Alerts: []AlertReport{{ID: alert.ID, Status: AlertScheduled, Detail: "exact"}}}, false)
	if err != nil || len(response.Alerts) != 0 {
		t.Fatalf("scheduled alert delivered again: %+v %v", response, err)
	}
	if _, err := store.CancelAlert(alert.ID); err != nil {
		t.Fatal(err)
	}
	response, err = store.Sync("dev1", "Pixel", SyncRequest{LogsAllowed: true, AlertsAllowed: true}, false)
	if err != nil || len(response.CancelAlerts) != 1 || response.CancelAlerts[0] != alert.ID {
		t.Fatalf("cancel not delivered: %+v %v", response, err)
	}
	if _, err := store.Sync("dev1", "Pixel", SyncRequest{LogsAllowed: true, AlertsAllowed: true, Alerts: []AlertReport{{ID: alert.ID, Status: AlertCancelled}}}, false); err != nil {
		t.Fatal(err)
	}
	_, alerts, err := store.Alerts("")
	if err != nil || len(alerts) != 1 || alerts[0].Status != AlertCancelled {
		t.Fatalf("alerts = %+v, err = %v", alerts, err)
	}
	*now = now.Add(8 * 24 * time.Hour)
	if _, err := store.Sync("dev1", "Pixel", SyncRequest{LogsAllowed: true, AlertsAllowed: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, alerts, _ = store.Alerts(""); len(alerts) != 0 {
		t.Fatalf("finished alert not pruned: %+v", alerts)
	}
}

func TestCancellingPendingAlertNeverReachesPhone(t *testing.T) {
	store, now := testStore(t)
	if _, err := store.Sync("dev1", "Pixel", SyncRequest{LogsAllowed: true, AlertsAllowed: true}, false); err != nil {
		t.Fatal(err)
	}
	_, alert, err := store.ScheduleAlert("Pixel", "Call back", "", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelAlert(alert.ID); err != nil {
		t.Fatal(err)
	}
	response, err := store.Sync("dev1", "Pixel", SyncRequest{LogsAllowed: true, AlertsAllowed: true}, false)
	if err != nil || len(response.Alerts)+len(response.CancelAlerts) != 0 {
		t.Fatalf("cancelled pending alert still sent: %+v %v", response, err)
	}
}

func TestDeviceChoiceWithSeveralPhones(t *testing.T) {
	store, _ := testStore(t)
	for _, device := range []string{"dev1", "dev2"} {
		if _, err := store.Sync(device, "Phone "+device, SyncRequest{LogsAllowed: true, AlertsAllowed: true}, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := store.Logs(LogQuery{}); err == nil || !strings.Contains(err.Error(), "more than one phone") {
		t.Fatalf("err = %v", err)
	}
	if device, _, err := store.Logs(LogQuery{DeviceID: "phone dev2"}); err != nil || device.ID != "dev2" {
		t.Fatalf("device = %+v err = %v", device, err)
	}
	if _, err := store.Sync("../x", "", SyncRequest{LogsAllowed: true, AlertsAllowed: true}, true); err == nil {
		t.Fatal("expected invalid device id to fail")
	}
}

func TestPhoneSwitchesGateLogsAndAlerts(t *testing.T) {
	store, _ := testStore(t)
	request := SyncRequest{LogEpoch: "e1", Logs: []LogEntry{entry(1, "W", "popup", "Audio failed")}}
	response, err := store.Sync("dev1", "Pixel", request, true)
	if err != nil {
		t.Fatal(err)
	}
	if response.LogsEnabled || response.LogCursor != 0 {
		t.Fatalf("phone that does not share logs was asked for them: %+v", response)
	}
	if store.Available(FeatureLogs) || store.Available(FeatureAlerts) {
		t.Fatal("features available although the phone allows neither")
	}
	if _, _, err := store.Logs(LogQuery{}); err == nil || !strings.Contains(err.Error(), "Share diagnostic log") {
		t.Fatalf("logs err = %v", err)
	}
	if _, _, err := store.ScheduleAlert("Pixel", "Tea", "", time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "Accept alerts") {
		t.Fatalf("schedule err = %v", err)
	}

	request.AlertsAllowed = true
	if _, err := store.Sync("dev1", "Pixel", request, true); err != nil {
		t.Fatal(err)
	}
	_, alert, err := store.ScheduleAlert("", "Tea", "", time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// Switching alerts off before the phone picked it up fails the alert.
	request.AlertsAllowed = false
	if response, err = store.Sync("dev1", "Pixel", request, true); err != nil || len(response.Alerts) != 0 {
		t.Fatalf("response %+v, err %v", response, err)
	}
	request.AlertsAllowed = true
	if _, err := store.Sync("dev1", "Pixel", request, true); err != nil {
		t.Fatal(err)
	}
	_, alerts, err := store.Alerts("")
	if err != nil || len(alerts) != 1 || alerts[0].ID != alert.ID || alerts[0].Status != AlertFailed || alerts[0].Detail != AlertsOffDetail {
		t.Fatalf("alerts %+v, err %v", alerts, err)
	}
}
