package deviceauth

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestInvitationBindsPersistsAndRevokesDevice(t *testing.T) {
	stateDir := t.TempDir()
	registry, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	registry.now = func() time.Time { return now }
	invitation, err := registry.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	if !registry.InvitationValid(invitation.Code) {
		t.Fatal("new invitation is invalid")
	}
	binding, err := registry.Bind(invitation.Code, DeviceInfo{
		InstallationID: "phone-installation-1",
		Name:           "Lak's Pixel",
		Manufacturer:   "Google",
		Model:          "Pixel 9",
		AndroidVersion: "16",
		AppVersion:     "0.1.0-local.test",
		AppID:          "com.lkarlslund.koder.dev",
	})
	if err != nil {
		t.Fatal(err)
	}
	if binding.Token == "" || binding.Device.ID == "" || binding.Device.Name != "Lak's Pixel" {
		t.Fatalf("binding = %#v", binding)
	}
	if registry.InvitationValid(invitation.Code) {
		t.Fatal("used invitation remains valid")
	}
	raw, err := os.ReadFile(registry.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), binding.Token) || strings.Contains(string(raw), invitation.Code) {
		t.Fatal("registry persisted a cleartext credential")
	}
	info, err := os.Stat(registry.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("registry permissions = %o, want 600", info.Mode().Perm())
	}

	now = now.Add(2 * time.Minute)
	if !registry.Authorize(binding.Token, DeviceInfo{AppVersion: "0.1.1"}) {
		t.Fatal("bound token was not authorized")
	}
	devices := registry.List()
	if len(devices) != 1 || devices[0].AppVersion != "0.1.1" || !devices[0].LastSeenAt.Equal(now) {
		t.Fatalf("devices = %#v", devices)
	}
	if _, err := registry.Revoke(binding.Device.ID); err != nil {
		t.Fatal(err)
	}
	if registry.Authorize(binding.Token, DeviceInfo{}) {
		t.Fatal("revoked token was authorized")
	}
	reloaded, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.List()
	if len(got) != 1 || got[0].RevokedAt == nil {
		t.Fatalf("reloaded devices = %#v", got)
	}
}

func TestLegacyTokenIsImportedOnceAndLearnsHandsetIdentity(t *testing.T) {
	registry, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	device, created, err := registry.ImportLegacy("existing-shared-secret")
	if err != nil || !created || device.Name != "Migrated Android phone" {
		t.Fatalf("first import device=%#v created=%v err=%v", device, created, err)
	}
	again, created, err := registry.ImportLegacy("existing-shared-secret")
	if err != nil || created || again.ID != device.ID {
		t.Fatalf("second import device=%#v created=%v err=%v", again, created, err)
	}
	if !registry.Authorize("existing-shared-secret", DeviceInfo{
		InstallationID: "current-phone",
		Name:           "Samsung S25",
		Manufacturer:   "Samsung",
		Model:          "SM-S931B",
	}) {
		t.Fatal("imported token was not authorized")
	}
	got := registry.List()
	if len(got) != 1 || got[0].InstallationID != "current-phone" || got[0].Name != "Samsung S25" {
		t.Fatalf("migrated device metadata = %#v", got)
	}
}

func TestInvitationExpires(t *testing.T) {
	registry, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	registry.now = func() time.Time { return now }
	invitation, err := registry.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	now = invitation.ExpiresAt
	if registry.InvitationValid(invitation.Code) {
		t.Fatal("expired invitation is valid")
	}
	if _, err := registry.Bind(invitation.Code, DeviceInfo{}); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("bind expired invitation error = %v", err)
	}
}
