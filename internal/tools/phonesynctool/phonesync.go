// Package phonesynctool reads diagnostic logs uploaded by the Koder Android
// app and schedules alerts on it. Both work outside voice calls, because the
// phone syncs with the server on its own.
package phonesynctool

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/phonesync"
	"github.com/lkarlslund/koder/internal/tools"
)

const serviceKey = "phone_sync"

// RuntimeService exposes the phone sync store to the tools.
func RuntimeService(store *phonesync.Store) map[string]any {
	if store == nil {
		return nil
	}
	return map[string]any{serviceKey: store}
}

// staleSync is when the tools warn that the phone has not synced recently.
const staleSync = 10 * time.Minute

func init() {
	tools.Register(logsTool{}, tools.ToolSpec{
		Title:       "Read phone logs",
		Description: "Read the Koder Android app's diagnostic log as last uploaded by the phone.",
		Usage: "Use to investigate problems with the Koder Android app: audio that stops, dropped connections, crashes, and the short error popups the app showed. " +
			"The log has the app's own events (tags such as popup, call, audio, playback, connection, telecom, crash, sync) and the app process's Android logcat (tag logcat). " +
			"The phone uploads within seconds of a warning or error and every minute while the app is open; check the reported last sync time. " +
			"Narrow with since, level, tag or query; the newest lines are kept when the limit is reached.",
		Parameters: `{"type":"object","properties":{` +
			`"since":{"type":"string","description":"RFC 3339 time, or a duration back from now such as 30m or 2h"},` +
			`"until":{"type":"string","description":"RFC 3339 time or duration back from now; exclusive"},` +
			`"level":{"type":"string","enum":["debug","info","warn","error"],"description":"Minimum level; default debug"},` +
			`"tag":{"type":"string","description":"Exact tag, such as popup, audio or logcat"},` +
			`"query":{"type":"string","description":"Case-insensitive text to find in the tag or message"},` +
			`"limit":{"type":"integer","minimum":1,"maximum":2000,"description":"Newest lines to return; default 300"},` +
			`"device":{"type":"string","description":"Phone name or id; needed only when several phones have synced"}` +
			`},"additionalProperties":false}`,
		ExposeToLLM: true,
	})
	tools.Register(alertsTool{}, tools.ToolSpec{
		Title:       "Phone alerts",
		Description: "Schedule, list or cancel notifications that the Koder Android app shows at a set time.",
		Usage: "Use when the user asks to be reminded or alerted on their phone. schedule needs title and either at or in. " +
			"The phone picks new alerts up when it next syncs (within a minute while the app is open, otherwise about every 15 minutes), then fires them on its own even offline; leave that much lead time and check status with list. " +
			"A status of scheduled with detail inexact means Android may deliver it some minutes late.",
		Parameters: `{"type":"object","properties":{` +
			`"action":{"type":"string","enum":["schedule","list","cancel"]},` +
			`"title":{"type":"string","description":"Notification title"},` +
			`"message":{"type":"string","description":"Optional notification text"},` +
			`"at":{"type":"string","description":"RFC 3339 time with offset, such as 2026-10-21T18:30:00+02:00"},` +
			`"in":{"type":"string","description":"Delay from now, such as 45m or 2h30m"},` +
			`"alert_id":{"type":"string","description":"Alert to cancel"},` +
			`"device":{"type":"string","description":"Phone name or id; needed only when several phones have synced"}` +
			`},"required":["action"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func store(runtime tools.Runtime) (*phonesync.Store, error) {
	value, err := tools.RequireService[*phonesync.Store](runtime, serviceKey)
	if err != nil {
		return nil, fmt.Errorf("phone sync is unavailable: %w", err)
	}
	return value, nil
}

// syncedPhone offers the tools only once a phone has synced, so chats on a
// server without the Android app never see them.
func syncedPhone(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	syncStore, err := store(runtime)
	if err != nil || len(syncStore.Devices()) == 0 {
		return tools.ToolSpec{}, false
	}
	return spec, true
}

type logsTool struct{}

func (logsTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return syncedPhone(runtime, spec)
}

func (logsTool) ID() tools.ID             { return tools.PhoneLogs }
func (logsTool) BypassesPermission() bool { return true }
func (logsTool) Preview(req tools.Request) string {
	parts := []string{"phone logs"}
	for _, key := range []string{"since", "level", "tag", "query"} {
		if value := req.Args[key]; value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	return strings.Join(parts, " ")
}

func (logsTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := trimmed(args, "since", "until", "level", "tag", "query", "limit", "device")
	for _, key := range []string{"since", "until"} {
		if _, err := parseWhen(out[key], time.Now()); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
	}
	if level := out["level"]; level != "" {
		if _, ok := levelCodes[strings.ToLower(level)]; !ok {
			return nil, fmt.Errorf("level must be debug, info, warn or error")
		}
	}
	if raw := out["limit"]; raw != "" {
		if limit, err := strconv.Atoi(raw); err != nil || limit < 1 || limit > 2000 {
			return nil, fmt.Errorf("limit must be between 1 and 2000")
		}
	}
	return out, nil
}

var levelCodes = map[string]string{"debug": "D", "info": "I", "warn": "W", "error": "E"}

func (logsTool) Call(_ context.Context, options tools.Options) (tools.Result, error) {
	syncStore, err := store(options.Runtime)
	if err != nil {
		return tools.Result{}, err
	}
	args, now := options.Request.Args, time.Now()
	since, _ := parseWhen(args["since"], now)
	until, _ := parseWhen(args["until"], now)
	limit := 300
	if raw := args["limit"]; raw != "" {
		limit, _ = strconv.Atoi(raw)
	}
	device, entries, err := syncStore.Logs(phonesync.LogQuery{
		DeviceID: args["device"], Since: since, Until: until, MinLevel: levelCodes[strings.ToLower(args["level"])],
		Tag: args["tag"], Contains: args["query"], Limit: limit,
	})
	if err != nil {
		return tools.Result{}, err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Phone %s (%s), %s.\n", device.Name, device.ID, syncAge(device.LastSync, now))
	if len(entries) == 0 {
		out.WriteString("No matching log lines.")
		if device.LogCursor == 0 {
			out.WriteString(" The phone has uploaded no logs yet; it starts once phone_logs is enabled and the app next syncs.")
		}
	}
	for _, entry := range entries {
		fmt.Fprintf(&out, "%s %s %s: %s\n", entry.Time.UTC().Format("2006-01-02T15:04:05.000Z"), entry.Level, entry.Tag, entry.Message)
	}
	return tools.Result{
		Output: strings.TrimRight(out.String(), "\n"),
		Meta:   map[string]string{"device": device.ID, "lines": strconv.Itoa(len(entries))},
	}, nil
}

type alertsTool struct{}

func (alertsTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return syncedPhone(runtime, spec)
}

func (alertsTool) ID() tools.ID             { return tools.PhoneAlerts }
func (alertsTool) BypassesPermission() bool { return true }
func (alertsTool) Preview(req tools.Request) string {
	switch req.Args["action"] {
	case "schedule":
		when := req.Args["at"]
		if when == "" {
			when = "in " + req.Args["in"]
		}
		return fmt.Sprintf("phone alert %q %s", req.Args["title"], when)
	case "cancel":
		return "cancel phone alert " + req.Args["alert_id"]
	}
	return "list phone alerts"
}

func (alertsTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := trimmed(args, "action", "title", "message", "at", "in", "alert_id", "device")
	switch out["action"] {
	case "schedule":
		if out["title"] == "" {
			return nil, fmt.Errorf("title is required")
		}
		if (out["at"] == "") == (out["in"] == "") {
			return nil, fmt.Errorf("pass exactly one of at or in")
		}
		if _, err := fireTime(out, time.Now()); err != nil {
			return nil, err
		}
	case "cancel":
		if out["alert_id"] == "" {
			return nil, fmt.Errorf("alert_id is required")
		}
	case "list":
	default:
		return nil, fmt.Errorf("action must be schedule, list or cancel")
	}
	return out, nil
}

func (alertsTool) Call(_ context.Context, options tools.Options) (tools.Result, error) {
	syncStore, err := store(options.Runtime)
	if err != nil {
		return tools.Result{}, err
	}
	args, now := options.Request.Args, time.Now()
	switch args["action"] {
	case "schedule":
		at, err := fireTime(args, now)
		if err != nil {
			return tools.Result{}, err
		}
		device, alert, err := syncStore.ScheduleAlert(args["device"], args["title"], args["message"], at)
		if err != nil {
			return tools.Result{}, err
		}
		return tools.Result{
			Output: fmt.Sprintf("Queued alert %s %q for %s (in %s) on %s; %s. It is scheduled on the phone at its next sync.",
				alert.ID, alert.Title, at.Format(time.RFC3339), at.Sub(now).Round(time.Minute), device.Name, syncAge(device.LastSync, now)),
			Meta: map[string]string{"alert_id": alert.ID, "fire_at": alert.FireAt.Format(time.RFC3339)},
		}, nil
	case "cancel":
		alert, err := syncStore.CancelAlert(args["alert_id"])
		if err != nil {
			return tools.Result{}, err
		}
		if alert.Status == phonesync.AlertCancelled {
			return tools.Result{Output: fmt.Sprintf("Cancelled alert %s before it reached the phone.", alert.ID)}, nil
		}
		return tools.Result{Output: fmt.Sprintf("Alert %s will be cancelled on the phone at its next sync.", alert.ID)}, nil
	}
	device, alerts, err := syncStore.Alerts(args["device"])
	if err != nil {
		return tools.Result{}, err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Phone %s (%s), %s.\n", device.Name, device.ID, syncAge(device.LastSync, now))
	if len(alerts) == 0 {
		out.WriteString("No alerts.")
	}
	for _, alert := range alerts {
		status := string(alert.Status)
		if alert.Detail != "" {
			status += " (" + alert.Detail + ")"
		}
		if alert.CancelRequested && alert.Status != phonesync.AlertCancelled {
			status += ", cancel requested"
		}
		fmt.Fprintf(&out, "%s %s %q: %s\n", alert.ID, alert.FireAt.Format(time.RFC3339), alert.Title, status)
	}
	return tools.Result{Output: strings.TrimRight(out.String(), "\n")}, nil
}

func fireTime(args map[string]string, now time.Time) (time.Time, error) {
	if raw := args["in"]; raw != "" {
		delay, err := time.ParseDuration(raw)
		if err != nil || delay <= 0 {
			return time.Time{}, fmt.Errorf("in must be a positive duration such as 45m")
		}
		return now.Add(delay), nil
	}
	at, err := time.Parse(time.RFC3339, args["at"])
	if err != nil {
		return time.Time{}, fmt.Errorf("at must be an RFC 3339 time with offset")
	}
	if !at.After(now) {
		return time.Time{}, fmt.Errorf("at %s is not in the future", args["at"])
	}
	return at, nil
}

// parseWhen reads an RFC 3339 time or a duration back from now.
func parseWhen(raw string, now time.Time) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if at, err := time.Parse(time.RFC3339, raw); err == nil {
		return at, nil
	}
	ago, err := time.ParseDuration(raw)
	if err != nil || ago < 0 {
		return time.Time{}, fmt.Errorf("use an RFC 3339 time or a duration such as 2h")
	}
	return now.Add(-ago), nil
}

func syncAge(lastSync, now time.Time) string {
	age := now.Sub(lastSync).Round(time.Second)
	text := fmt.Sprintf("last synced %s ago", age)
	if age > staleSync {
		text += " (open the Koder app on the phone to sync)"
	}
	return text
}

func trimmed(args map[string]string, keys ...string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value := strings.TrimSpace(args[key]); value != "" {
			out[key] = value
		}
	}
	return out
}
