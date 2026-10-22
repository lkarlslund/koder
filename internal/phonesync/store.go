// Package phonesync keeps what bound Android phones upload outside voice
// calls: their diagnostic logs, and the alerts Koder asks them to schedule.
package phonesync

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lkarlslund/koder/internal/id"
)

const (
	// MaxLogsPerSync bounds one upload; the phone sends the rest next time.
	MaxLogsPerSync = 2000
	maxMessageLen  = 8 << 10
	// maxLogBytes is when a device's log file is cut back to its newest half.
	maxLogBytes       = 8 << 20
	finishedAlertKeep = 7 * 24 * time.Hour
	maxAlertText      = 500
)

// AlertsOffDetail explains alerts the phone refused.
const AlertsOffDetail = "alerts are turned off in the Koder app on the phone"

// Levels in increasing severity. The phone sends the single-letter form.
var levels = []string{"D", "I", "W", "E"}

// LevelRank orders a level; unknown levels count as info.
func LevelRank(level string) int {
	if index := slices.Index(levels, strings.ToUpper(strings.TrimSpace(level))); index >= 0 {
		return index
	}
	return 1
}

// LogEntry is one line of the phone's diagnostic log.
type LogEntry struct {
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Tag     string    `json:"tag"`
	Message string    `json:"message"`
}

// AlertStatus tracks an alert from Koder's request to the phone firing it.
type AlertStatus string

const (
	AlertPending   AlertStatus = "pending"
	AlertScheduled AlertStatus = "scheduled"
	AlertFired     AlertStatus = "fired"
	AlertCancelled AlertStatus = "cancelled"
	AlertFailed    AlertStatus = "failed"
)

func (s AlertStatus) finished() bool {
	return s == AlertFired || s == AlertCancelled || s == AlertFailed
}

// Alert is a notification Koder asked a phone to show at FireAt.
type Alert struct {
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Body      string      `json:"body,omitempty"`
	FireAt    time.Time   `json:"fire_at"`
	CreatedAt time.Time   `json:"created_at"`
	Status    AlertStatus `json:"status"`
	// Detail is the phone's note on the status, such as "inexact".
	Detail          string    `json:"detail,omitempty"`
	CancelRequested bool      `json:"cancel_requested,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Device is a phone that has synced at least once.
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	LastSync  time.Time `json:"last_sync"`
	LogCursor uint64    `json:"log_cursor"`
	LogEpoch  string    `json:"log_epoch,omitempty"`
	// LogsAllowed and AlertsAllowed are the switches the user set in the app
	// at the last sync.
	LogsAllowed   bool `json:"logs_allowed,omitempty"`
	AlertsAllowed bool `json:"alerts_allowed,omitempty"`
}

type deviceState struct {
	Device
	Alerts []Alert `json:"alerts,omitempty"`
}

// AlertReport is the phone's status update for one alert.
type AlertReport struct {
	ID     string      `json:"id"`
	Status AlertStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

// SyncRequest is what the phone uploads.
type SyncRequest struct {
	Protocol string `json:"protocol"`
	// LogEpoch changes when the phone's log restarts its sequence numbers.
	LogEpoch string `json:"log_epoch"`
	// LogsAllowed and AlertsAllowed say whether the user shares the log and
	// accepts alerts on this phone.
	LogsAllowed   bool          `json:"logs_allowed"`
	AlertsAllowed bool          `json:"alerts_allowed"`
	Logs          []LogEntry    `json:"logs,omitempty"`
	Alerts        []AlertReport `json:"alerts,omitempty"`
}

// SyncAlert is an alert the phone should schedule.
type SyncAlert struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	Body   string    `json:"body,omitempty"`
	FireAt time.Time `json:"fire_at"`
}

// SyncResponse tells the phone what to upload and schedule next.
type SyncResponse struct {
	Protocol     string      `json:"protocol"`
	LogsEnabled  bool        `json:"logs_enabled"`
	LogCursor    uint64      `json:"log_cursor"`
	Alerts       []SyncAlert `json:"alerts"`
	CancelAlerts []string    `json:"cancel_alerts"`
}

// Store persists phone sync state under one directory per device.
type Store struct {
	mu   sync.Mutex
	root string
	now  func() time.Time
}

// Open uses stateDir/phone-sync for storage.
func Open(stateDir string) *Store {
	return &Store{root: filepath.Join(stateDir, "phone-sync"), now: time.Now}
}

// Sync stores an upload from deviceID and returns the phone's next work.
// Logs are kept only while logsEnabled and the phone shares them; otherwise
// the phone is told to stop. A phone that refuses alerts fails pending ones.
func (s *Store) Sync(deviceID, name string, req SyncRequest, logsEnabled bool) (SyncResponse, error) {
	dir, err := s.deviceDir(deviceID)
	if err != nil {
		return SyncResponse{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(dir)
	if err != nil {
		return SyncResponse{}, err
	}
	now := s.now()
	state.ID, state.LastSync = deviceID, now
	if name = strings.TrimSpace(name); name != "" {
		state.Name = name
	}
	state.LogsAllowed, state.AlertsAllowed = req.LogsAllowed, req.AlertsAllowed
	logsEnabled = logsEnabled && req.LogsAllowed
	if epoch := strings.TrimSpace(req.LogEpoch); epoch != state.LogEpoch {
		state.LogEpoch, state.LogCursor = epoch, 0
	}
	if logsEnabled {
		fresh := newLogs(req.Logs, state.LogCursor)
		if err := appendLogs(filepath.Join(dir, "logs.jsonl"), fresh); err != nil {
			return SyncResponse{}, err
		}
		if len(fresh) > 0 {
			state.LogCursor = fresh[len(fresh)-1].Seq
		}
	}
	applyReports(state.Alerts, req.Alerts, now)
	state.Alerts = slices.DeleteFunc(state.Alerts, func(alert Alert) bool {
		return alert.Status.finished() && now.Sub(alert.UpdatedAt) > finishedAlertKeep
	})
	response := SyncResponse{LogsEnabled: logsEnabled, LogCursor: state.LogCursor, Alerts: []SyncAlert{}, CancelAlerts: []string{}}
	for index, alert := range state.Alerts {
		switch {
		case alert.CancelRequested && alert.Status == AlertPending:
			state.Alerts[index].Status, state.Alerts[index].UpdatedAt = AlertCancelled, now
		case alert.Status == AlertPending && !req.AlertsAllowed:
			state.Alerts[index].Status, state.Alerts[index].Detail, state.Alerts[index].UpdatedAt = AlertFailed, AlertsOffDetail, now
		case alert.CancelRequested && alert.Status == AlertScheduled:
			response.CancelAlerts = append(response.CancelAlerts, alert.ID)
		case alert.Status == AlertPending:
			response.Alerts = append(response.Alerts, SyncAlert{ID: alert.ID, Title: alert.Title, Body: alert.Body, FireAt: alert.FireAt})
		}
	}
	return response, s.saveLocked(dir, state)
}

// newLogs keeps entries after cursor, in order, with bounded text.
func newLogs(entries []LogEntry, cursor uint64) []LogEntry {
	out := make([]LogEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Seq <= cursor {
			continue
		}
		entry.Level = levels[LevelRank(entry.Level)]
		entry.Tag = truncate(strings.TrimSpace(entry.Tag), 64)
		entry.Message = truncate(entry.Message, maxMessageLen)
		out = append(out, entry)
	}
	slices.SortFunc(out, func(a, b LogEntry) int { return cmp.Compare(a.Seq, b.Seq) })
	out = slices.CompactFunc(out, func(a, b LogEntry) bool { return a.Seq == b.Seq })
	if len(out) > MaxLogsPerSync {
		out = out[:MaxLogsPerSync]
	}
	return out
}

func applyReports(alerts []Alert, reports []AlertReport, now time.Time) {
	for _, report := range reports {
		index := slices.IndexFunc(alerts, func(alert Alert) bool { return alert.ID == report.ID })
		if index < 0 || alerts[index].Status.finished() {
			continue
		}
		switch report.Status {
		case AlertScheduled, AlertFired, AlertCancelled, AlertFailed:
			alerts[index].Status = report.Status
			alerts[index].Detail = truncate(strings.TrimSpace(report.Detail), 200)
			alerts[index].UpdatedAt = now
		}
	}
}

func appendLogs(path string, entries []LogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(buf.Bytes())
	info, statErr := file.Stat()
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if statErr == nil && info.Size() > maxLogBytes {
		return keepNewestHalf(path)
	}
	return nil
}

func keepNewestHalf(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cut := len(data) / 2
	if newline := bytes.IndexByte(data[cut:], '\n'); newline >= 0 {
		cut += newline + 1
	}
	return writeFileAtomic(path, data[cut:])
}

// LogQuery selects log lines; zero fields match everything.
type LogQuery struct {
	DeviceID string
	Since    time.Time
	Until    time.Time
	MinLevel string
	Tag      string
	Contains string
	// Limit keeps the newest matching lines.
	Limit int
}

// Logs returns the newest matching lines, oldest first, for one device.
func (s *Store) Logs(query LogQuery) (Device, []LogEntry, error) {
	device, err := s.resolveDevice(query.DeviceID, FeatureLogs)
	if err != nil {
		return Device{}, nil, err
	}
	dir, err := s.deviceDir(device.ID)
	if err != nil {
		return Device{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.Open(filepath.Join(dir, "logs.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return device, nil, nil
	}
	if err != nil {
		return Device{}, nil, err
	}
	defer func() { _ = file.Close() }()
	limit := query.Limit
	if limit <= 0 {
		limit = 200
	}
	minRank := LevelRank(query.MinLevel)
	if strings.TrimSpace(query.MinLevel) == "" {
		minRank = 0
	}
	tag := strings.ToLower(strings.TrimSpace(query.Tag))
	contains := strings.ToLower(strings.TrimSpace(query.Contains))
	var out []LogEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxMessageLen*8)
	for scanner.Scan() {
		var entry LogEntry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		if LevelRank(entry.Level) < minRank ||
			!query.Since.IsZero() && entry.Time.Before(query.Since) ||
			!query.Until.IsZero() && !entry.Time.Before(query.Until) ||
			tag != "" && strings.ToLower(entry.Tag) != tag ||
			contains != "" && !strings.Contains(strings.ToLower(entry.Tag+" "+entry.Message), contains) {
			continue
		}
		out = append(out, entry)
		if len(out) > 2*limit {
			out = slices.Delete(out, 0, len(out)-limit)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return device, out, scanner.Err()
}

// ScheduleAlert queues an alert for the phone's next sync.
func (s *Store) ScheduleAlert(deviceID, title, body string, fireAt time.Time) (Device, Alert, error) {
	title, body = truncate(strings.TrimSpace(title), maxAlertText), truncate(strings.TrimSpace(body), maxAlertText*4)
	if title == "" {
		return Device{}, Alert{}, errors.New("title is required")
	}
	if fireAt.IsZero() {
		return Device{}, Alert{}, errors.New("fire time is required")
	}
	device, err := s.resolveDevice(deviceID, FeatureAlerts)
	if err != nil {
		return Device{}, Alert{}, err
	}
	dir, err := s.deviceDir(device.ID)
	if err != nil {
		return Device{}, Alert{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(dir)
	if err != nil {
		return Device{}, Alert{}, err
	}
	now := s.now()
	if !fireAt.After(now) {
		return Device{}, Alert{}, fmt.Errorf("fire time %s is not in the future", fireAt.Format(time.RFC3339))
	}
	alert := Alert{ID: "alert-" + string(id.New()), Title: title, Body: body, FireAt: fireAt.UTC(), CreatedAt: now, Status: AlertPending, UpdatedAt: now}
	state.Alerts = append(state.Alerts, alert)
	return state.Device, alert, s.saveLocked(dir, state)
}

// CancelAlert withdraws an alert; a scheduled one is cancelled at next sync.
func (s *Store) CancelAlert(alertID string) (Alert, error) {
	alertID = strings.TrimSpace(alertID)
	for _, device := range s.Devices() {
		dir, err := s.deviceDir(device.ID)
		if err != nil {
			return Alert{}, err
		}
		s.mu.Lock()
		state, err := s.loadLocked(dir)
		if err != nil {
			s.mu.Unlock()
			return Alert{}, err
		}
		index := slices.IndexFunc(state.Alerts, func(alert Alert) bool { return alert.ID == alertID })
		if index < 0 {
			s.mu.Unlock()
			continue
		}
		alert := &state.Alerts[index]
		if alert.Status.finished() {
			s.mu.Unlock()
			return *alert, fmt.Errorf("alert %s is already %s", alertID, alert.Status)
		}
		alert.CancelRequested, alert.UpdatedAt = true, s.now()
		if alert.Status == AlertPending {
			alert.Status = AlertCancelled
		}
		out := *alert
		err = s.saveLocked(dir, state)
		s.mu.Unlock()
		return out, err
	}
	return Alert{}, fmt.Errorf("no alert %s", alertID)
}

// Alerts lists one device's alerts by fire time.
func (s *Store) Alerts(deviceID string) (Device, []Alert, error) {
	device, err := s.resolveDevice(deviceID, FeatureAlerts)
	if err != nil {
		return Device{}, nil, err
	}
	dir, err := s.deviceDir(device.ID)
	if err != nil {
		return Device{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(dir)
	if err != nil {
		return Device{}, nil, err
	}
	slices.SortFunc(state.Alerts, func(a, b Alert) int { return a.FireAt.Compare(b.FireAt) })
	return state.Device, state.Alerts, nil
}

// Devices lists phones that have synced, most recent first.
func (s *Store) Devices() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, _ := os.ReadDir(s.root)
	var out []Device
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		state, err := s.loadLocked(filepath.Join(s.root, entry.Name()))
		if err == nil && state.ID != "" {
			out = append(out, state.Device)
		}
	}
	slices.SortFunc(out, func(a, b Device) int { return b.LastSync.Compare(a.LastSync) })
	return out
}

// Feature is a sync feature the user switches on in the app.
type Feature int

const (
	FeatureLogs Feature = iota
	FeatureAlerts
)

// Allowed reports whether the phone's user switched the feature on.
func (d Device) Allowed(feature Feature) bool {
	if feature == FeatureLogs {
		return d.LogsAllowed
	}
	return d.AlertsAllowed
}

func (f Feature) setting() string {
	if f == FeatureLogs {
		return "Share diagnostic log"
	}
	return "Accept alerts"
}

// Available reports whether any synced phone allows the feature.
func (s *Store) Available(feature Feature) bool {
	return slices.ContainsFunc(s.Devices(), func(device Device) bool { return device.Allowed(feature) })
}

// resolveDevice picks deviceID, or the only phone allowing feature when it is
// empty.
func (s *Store) resolveDevice(deviceID string, feature Feature) (Device, error) {
	synced := s.Devices()
	if len(synced) == 0 {
		return Device{}, errors.New("no phone has synced with Koder yet; open the Koder app on the phone")
	}
	devices := slices.DeleteFunc(slices.Clone(synced), func(device Device) bool { return !device.Allowed(feature) })
	deviceID = strings.TrimSpace(deviceID)
	for _, device := range synced {
		if deviceID != "" && (device.ID == deviceID || strings.EqualFold(device.Name, deviceID)) {
			if !device.Allowed(feature) {
				return Device{}, fmt.Errorf("%s has %q turned off in the Koder app's settings", device.Name, feature.setting())
			}
			return device, nil
		}
	}
	switch {
	case len(devices) == 0:
		return Device{}, fmt.Errorf("no phone has %q turned on in the Koder app's settings", feature.setting())
	case deviceID != "":
		return Device{}, fmt.Errorf("no synced phone %q; known phones: %s", deviceID, deviceNames(devices))
	case len(devices) > 1:
		return Device{}, fmt.Errorf("more than one phone has synced; pass device as one of: %s", deviceNames(devices))
	}
	return devices[0], nil
}

func deviceNames(devices []Device) string {
	names := make([]string, 0, len(devices))
	for _, device := range devices {
		names = append(names, fmt.Sprintf("%s (%s)", device.Name, device.ID))
	}
	return strings.Join(names, ", ")
}

func (s *Store) deviceDir(deviceID string) (string, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" || deviceID != filepath.Base(deviceID) || strings.HasPrefix(deviceID, ".") {
		return "", fmt.Errorf("invalid phone device id %q", deviceID)
	}
	dir := filepath.Join(s.root, deviceID)
	return dir, os.MkdirAll(dir, 0o700)
}

func (s *Store) loadLocked(dir string) (deviceState, error) {
	var state deviceState
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	return state, json.Unmarshal(data, &state)
}

func (s *Store) saveLocked(dir string, state deviceState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "state.json"), data)
}

func writeFileAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, err = temp.Write(data)
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(temp.Name())
	}
	return err
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "…"
}
