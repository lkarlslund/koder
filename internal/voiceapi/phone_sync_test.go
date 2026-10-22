package voiceapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/deviceauth"
	"github.com/lkarlslund/koder/internal/phonesync"
)

type fakePhoneSync struct {
	store       *phonesync.Store
	logsEnabled bool
}

func (f *fakePhoneSync) PhoneSyncStore() *phonesync.Store { return f.store }
func (f *fakePhoneSync) PhoneLogsEnabled() bool           { return f.logsEnabled }

func TestPhoneSyncStoresLogsForBoundDeviceAndDeliversAlerts(t *testing.T) {
	registry, err := deviceauth.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := registry.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := registry.Bind(invitation.Code, deviceauth.DeviceInfo{InstallationID: "phone-1", Name: "Pixel"})
	if err != nil {
		t.Fatal(err)
	}
	sync := &fakePhoneSync{store: phonesync.Open(t.TempDir()), logsEnabled: true}
	handler := NewHandler(&fakeBackend{}, "")
	handler.Auth = registry
	handler.Sync = sync
	server := httptest.NewServer(handler)
	defer server.Close()

	post := func(token, body string) (*http.Response, phonesync.SyncResponse) {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.URL+"/voice/v1/device/sync", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		var decoded phonesync.SyncResponse
		if response.StatusCode == http.StatusOK {
			if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
				t.Fatal(err)
			}
		}
		return response, decoded
	}

	if response, _ := post("wrong", `{"protocol":"voice.v1"}`); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated sync status = %d", response.StatusCode)
	}
	upload := `{"protocol":"voice.v1","logs_allowed":true,"alerts_allowed":true,"log_epoch":"e1","logs":[{"seq":1,"time":"2026-10-21T10:00:00Z","level":"W","tag":"popup","message":"Audio playback failed (-6)"}]}`
	response, decoded := post(binding.Token, upload)
	if response.StatusCode != http.StatusOK || !decoded.LogsEnabled || decoded.LogCursor != 1 || decoded.Protocol != protocolVersion {
		t.Fatalf("sync status=%d body=%+v", response.StatusCode, decoded)
	}
	device, logs, err := sync.store.Logs(phonesync.LogQuery{})
	if err != nil || device.ID != binding.Device.ID || device.Name != "Pixel" || len(logs) != 1 || logs[0].Tag != "popup" {
		t.Fatalf("device=%+v logs=%+v err=%v", device, logs, err)
	}

	if _, _, err := sync.store.ScheduleAlert("", "Stretch", "", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	sync.logsEnabled = false
	response, decoded = post(binding.Token, `{"protocol":"voice.v1","logs_allowed":true,"alerts_allowed":true,"log_epoch":"e1"}`)
	if response.StatusCode != http.StatusOK || decoded.LogsEnabled || len(decoded.Alerts) != 1 || decoded.Alerts[0].Title != "Stretch" {
		t.Fatalf("sync status=%d body=%+v", response.StatusCode, decoded)
	}
	if response, _ := post(binding.Token, `{"protocol":"voice.v0"}`); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong protocol status = %d", response.StatusCode)
	}
}

// The fixtures are shared with the Android client's PhoneSyncTest.
func TestPhoneSyncMatchesSharedFixtures(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", "protocol", "voice", "v1", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	var request phonesync.SyncRequest
	if err := json.Unmarshal(read("device_sync_request.json"), &request); err != nil {
		t.Fatal(err)
	}
	if request.LogEpoch != "epoch-fixture-1" || !request.LogsAllowed || !request.AlertsAllowed || len(request.Logs) != 2 || request.Logs[1].Tag != "playback" ||
		request.Logs[0].Time.UnixMilli()%1000 != 250 || len(request.Alerts) != 1 || request.Alerts[0].Status != phonesync.AlertScheduled {
		t.Fatalf("request fixture decoded as %+v", request)
	}
	var fixture, encoded map[string]any
	if err := json.Unmarshal(read("device_sync_response.json"), &fixture); err != nil {
		t.Fatal(err)
	}
	var response phonesync.SyncResponse
	if err := json.Unmarshal(read("device_sync_response.json"), &response); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture, encoded) {
		t.Fatalf("server response shape drifted from fixture:\n%s", data)
	}
}
