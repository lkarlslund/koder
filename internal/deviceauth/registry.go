// Package deviceauth owns persistent per-device voice credentials and
// short-lived invitations used to bind an Android installation.
package deviceauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/id"
)

const invitationLifetime = 30 * time.Minute
const metadataLimit = 160

var ErrInvitationInvalid = errors.New("device binding invitation is invalid or expired")

// DeviceInfo is handset metadata supplied during binding and authenticated
// requests. It contains no secret and is bounded before persistence.
type DeviceInfo struct {
	InstallationID string `json:"installation_id,omitempty"`
	Name           string `json:"name,omitempty"`
	Manufacturer   string `json:"manufacturer,omitempty"`
	Model          string `json:"model,omitempty"`
	AndroidVersion string `json:"android_version,omitempty"`
	AppVersion     string `json:"app_version,omitempty"`
	AppID          string `json:"app_id,omitempty"`
}

// Device is the public registration shown in Koder's security settings.
type Device struct {
	ID string `json:"id"`
	DeviceInfo
	RegisteredAt time.Time  `json:"registered_at"`
	LastSeenAt   time.Time  `json:"last_seen_at,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

type record struct {
	Device
	TokenSHA256 string `json:"token_sha256"`
}

type diskState struct {
	Version int      `json:"version"`
	Devices []record `json:"devices"`
}

// Invitation is a one-time binding code. Invitations intentionally live only
// in memory, expire quickly, and are consumed by a successful bind.
type Invitation struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Binding returns the one-time cleartext credential to its new phone.
type Binding struct {
	Device Device `json:"device"`
	Token  string `json:"token"`
}

// Registry persists only token digests. Its zero value is not usable; use Open.
type Registry struct {
	mu          sync.Mutex
	path        string
	devices     []record
	invitations map[string]time.Time
	now         func() time.Time
}

// Open loads or creates the registry under Koder's state directory.
func Open(stateDir string) (*Registry, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil, errors.New("device registry state directory is required")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create device registry directory: %w", err)
	}
	r := &Registry{
		path:        filepath.Join(stateDir, "voice-devices.json"),
		invitations: make(map[string]time.Time),
		now:         func() time.Time { return time.Now().UTC() },
	}
	raw, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read device registry: %w", err)
	}
	var state diskState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode device registry: %w", err)
	}
	if state.Version != 1 {
		return nil, fmt.Errorf("unsupported device registry version %d", state.Version)
	}
	for _, item := range state.Devices {
		if strings.TrimSpace(item.ID) == "" || len(item.TokenSHA256) != sha256.Size*2 {
			return nil, errors.New("device registry contains an invalid record")
		}
	}
	r.devices = state.Devices
	return r, nil
}

// ImportLegacy converts the configured shared voice token into a revocable
// device credential without persisting its cleartext value.
func (r *Registry) ImportLegacy(token string) (Device, bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Device{}, false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	digest := tokenDigest(token)
	for _, item := range r.devices {
		if equalDigest(item.TokenSHA256, digest) {
			return item.Device, false, nil
		}
	}
	now := r.now()
	item := record{
		Device: Device{
			ID:           string(id.New()),
			DeviceInfo:   DeviceInfo{Name: "Migrated Android phone"},
			RegisteredAt: now,
		},
		TokenSHA256: digest,
	}
	r.devices = append(r.devices, item)
	if err := r.persistLocked(); err != nil {
		r.devices = r.devices[:len(r.devices)-1]
		return Device{}, false, err
	}
	return item.Device, true, nil
}

// CreateInvitation allocates a one-time binding code.
func (r *Registry) CreateInvitation() (Invitation, error) {
	code, err := randomCredential("kdb1_", 24)
	if err != nil {
		return Invitation{}, fmt.Errorf("create device invitation: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneInvitationsLocked()
	expiresAt := r.now().Add(invitationLifetime)
	r.invitations[tokenDigest(code)] = expiresAt
	return Invitation{Code: code, ExpiresAt: expiresAt}, nil
}

// InvitationValid reports whether code can still download the APK and bind.
// It does not consume the invitation.
func (r *Registry) InvitationValid(code string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneInvitationsLocked()
	expiresAt, ok := r.invitations[tokenDigest(strings.TrimSpace(code))]
	return ok && expiresAt.After(r.now())
}

// Bind consumes an invitation and issues a per-device token.
func (r *Registry) Bind(code string, info DeviceInfo) (Binding, error) {
	token, err := randomCredential("kdv1_", 32)
	if err != nil {
		return Binding{}, fmt.Errorf("create device token: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneInvitationsLocked()
	digest := tokenDigest(strings.TrimSpace(code))
	if _, ok := r.invitations[digest]; !ok {
		return Binding{}, ErrInvitationInvalid
	}
	info = normalizeInfo(info)
	if info.Name == "" {
		info.Name = "Android phone"
	}
	now := r.now()
	item := record{
		Device:      Device{ID: string(id.New()), DeviceInfo: info, RegisteredAt: now, LastSeenAt: now},
		TokenSHA256: tokenDigest(token),
	}
	previous := slices.Clone(r.devices)
	if info.InstallationID != "" {
		for index := range r.devices {
			if r.devices[index].InstallationID == info.InstallationID {
				item.ID = r.devices[index].ID
				r.devices[index] = item
				delete(r.invitations, digest)
				if err := r.persistLocked(); err != nil {
					r.devices = previous
					r.invitations[digest] = r.now().Add(invitationLifetime)
					return Binding{}, err
				}
				return Binding{Device: item.Device, Token: token}, nil
			}
		}
	}
	r.devices = append(r.devices, item)
	delete(r.invitations, digest)
	if err := r.persistLocked(); err != nil {
		r.devices = previous
		r.invitations[digest] = r.now().Add(invitationLifetime)
		return Binding{}, err
	}
	return Binding{Device: item.Device, Token: token}, nil
}

// Authorize validates a device token and refreshes bounded handset metadata.
func (r *Registry) Authorize(token string, info DeviceInfo) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	digest := tokenDigest(token)
	for index := range r.devices {
		item := &r.devices[index]
		if !equalDigest(item.TokenSHA256, digest) || item.RevokedAt != nil {
			continue
		}
		now := r.now()
		nextInfo := mergeInfo(item.DeviceInfo, normalizeInfo(info))
		changed := nextInfo != item.DeviceInfo || item.LastSeenAt.IsZero() || now.Sub(item.LastSeenAt) >= time.Minute
		if changed {
			item.DeviceInfo = nextInfo
			item.LastSeenAt = now
			_ = r.persistLocked()
		}
		return true
	}
	return false
}

// List returns newest-seen devices first, including revoked registrations.
func (r *Registry) List() []Device {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Device, len(r.devices))
	for index := range r.devices {
		out[index] = r.devices[index].Device
	}
	slices.SortStableFunc(out, func(a, b Device) int {
		aTime, bTime := a.LastSeenAt, b.LastSeenAt
		if aTime.IsZero() {
			aTime = a.RegisteredAt
		}
		if bTime.IsZero() {
			bTime = b.RegisteredAt
		}
		return bTime.Compare(aTime)
	})
	return out
}

// Revoke permanently disables one device credential.
func (r *Registry) Revoke(deviceID string) (Device, error) {
	deviceID = strings.TrimSpace(deviceID)
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.devices {
		if r.devices[index].ID != deviceID {
			continue
		}
		if r.devices[index].RevokedAt == nil {
			now := r.now()
			r.devices[index].RevokedAt = &now
			if err := r.persistLocked(); err != nil {
				r.devices[index].RevokedAt = nil
				return Device{}, err
			}
		}
		return r.devices[index].Device, nil
	}
	return Device{}, fmt.Errorf("device %q was not found", deviceID)
}

func (r *Registry) pruneInvitationsLocked() {
	now := r.now()
	for digest, expiresAt := range r.invitations {
		if !expiresAt.After(now) {
			delete(r.invitations, digest)
		}
	}
}

func (r *Registry) persistLocked() error {
	state := diskState{Version: 1, Devices: r.devices}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode device registry: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(r.path), ".voice-devices-*.tmp")
	if err != nil {
		return fmt.Errorf("create device registry file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect device registry file: %w", err)
	}
	if _, err := temporary.Write(append(raw, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write device registry: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync device registry: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close device registry: %w", err)
	}
	if err := os.Rename(temporaryPath, r.path); err != nil {
		return fmt.Errorf("replace device registry: %w", err)
	}
	return nil
}

func normalizeInfo(info DeviceInfo) DeviceInfo {
	info.InstallationID = bounded(info.InstallationID)
	info.Name = bounded(info.Name)
	info.Manufacturer = bounded(info.Manufacturer)
	info.Model = bounded(info.Model)
	info.AndroidVersion = bounded(info.AndroidVersion)
	info.AppVersion = bounded(info.AppVersion)
	info.AppID = bounded(info.AppID)
	return info
}

func mergeInfo(current, update DeviceInfo) DeviceInfo {
	if update.InstallationID != "" {
		current.InstallationID = update.InstallationID
	}
	if update.Name != "" {
		current.Name = update.Name
	}
	if update.Manufacturer != "" {
		current.Manufacturer = update.Manufacturer
	}
	if update.Model != "" {
		current.Model = update.Model
	}
	if update.AndroidVersion != "" {
		current.AndroidVersion = update.AndroidVersion
	}
	if update.AppVersion != "" {
		current.AppVersion = update.AppVersion
	}
	if update.AppID != "" {
		current.AppID = update.AppID
	}
	return current
}

func bounded(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > metadataLimit {
		runes = runes[:metadataLimit]
	}
	return string(runes)
}

func randomCredential(prefix string, size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func tokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func equalDigest(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
