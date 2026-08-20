// Package phonedevice owns the process-wide, permission-gated Android tool
// provider connected to the active voice conversation.
package phonedevice

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Action is a server-known operation that an Android client may advertise.
type Action string

const (
	DeviceStatus        Action = "device_status"
	GetLocation         Action = "get_location"
	SearchContacts      Action = "search_contacts"
	UpcomingCalendar    Action = "upcoming_calendar"
	SearchSMS           Action = "search_sms"
	RecentNotifications Action = "recent_notifications"
	PlaceCall           Action = "place_call"
	SendSMS             Action = "send_sms"
	ComposeEmail        Action = "compose_email"
	CreateContact       Action = "create_contact"
	CreateCalendarEvent Action = "create_calendar_event"
	OpenMap             Action = "open_map"
	SetAlarm            Action = "set_alarm"
	SetTimer            Action = "set_timer"
	ReadClipboard       Action = "read_clipboard"
	WriteClipboard      Action = "write_clipboard"
	OpenURL             Action = "open_url"
	MediaControl        Action = "media_control"
	ListApps            Action = "list_apps"
	OpenApp             Action = "open_app"
	ShareText           Action = "share_text"
)

// CatalogEntry is server-owned prompt and risk metadata. Android only
// advertises IDs, preventing a connected client from injecting tool text.
type CatalogEntry struct {
	Action       Action
	Summary      string
	Arguments    string
	Confirmation bool
}

var catalog = []CatalogEntry{
	{DeviceStatus, "Read battery, charging, storage, network, locale, and time-zone status", "none", false},
	{GetLocation, "Read and resolve the phone's current location for questions about where the user is or what is happening nearby; this does not display a map", "none", false},
	{SearchContacts, "Search device contacts and return matching names, phone numbers, and email addresses", "query; optional limit", false},
	{UpcomingCalendar, "Search calendar events in a time range", "optional query, start_time, end_time, limit", false},
	{SearchSMS, "Search SMS messages stored on the phone", "optional query, phone_number, since_time, limit", false},
	{RecentNotifications, "Search current notifications, including enabled email and messaging previews", "optional query, app, limit", false},
	{PlaceCall, "Place a real phone call after confirmation on the phone", "phone_number or contact_name", true},
	{SendSMS, "Send an SMS after confirmation on the phone", "phone_number or contact_name; message", true},
	{ComposeEmail, "Open a prefilled email draft for the user to review and send", "optional to, subject, body", true},
	{CreateContact, "Open a prefilled new-contact screen for the user to save", "name; optional phone_number, email", true},
	{CreateCalendarEvent, "Create a calendar event after confirmation on the phone", "title, start_time; optional end_time, location, description", true},
	{OpenMap, "Display a place or route in the phone's map app only when the user explicitly asks to see a map, open a place, or navigate; never use this merely to determine or describe where the user is", "query or latitude and longitude", true},
	{SetAlarm, "Open a prefilled alarm on the phone", "hour, minute; optional label", true},
	{SetTimer, "Open a prefilled timer on the phone", "duration_seconds; optional label", true},
	{ReadClipboard, "Read the current clipboard while Koder Voice is in the foreground", "none", false},
	{WriteClipboard, "Replace the phone clipboard after confirmation", "text", true},
	{OpenURL, "Open an HTTPS URL on the phone", "url", true},
	{MediaControl, "Control the active media session", "media_action: play, pause, toggle, next, or previous", true},
	{ListApps, "Search launchable apps installed on the phone", "optional query, limit", false},
	{OpenApp, "Open an installed app", "package_name", true},
	{ShareText, "Open Android's share sheet with text", "text; optional title", true},
}

var known = func() map[Action]CatalogEntry {
	out := make(map[Action]CatalogEntry, len(catalog))
	for _, entry := range catalog {
		out[entry.Action] = entry
	}
	return out
}()

// Catalog returns the immutable server-owned action catalog.
func Catalog() []CatalogEntry { return slices.Clone(catalog) }

// Result is the bounded response returned by Android.
type Result struct {
	Text string `json:"text"`
	Data any    `json:"data,omitempty"`
}

// Executor sends an action to the active Android client.
type Executor func(context.Context, string, Action, map[string]string) (Result, error)

// Control is consumed by the voice-only phone tool.
type Control interface {
	Capabilities() []CatalogEntry
	Execute(context.Context, Action, map[string]string) (Result, error)
}

type connection struct {
	callID               string
	actions              map[Action]bool
	confirmationPolicies map[Action]string
	execute              Executor
	generation           uint64
}

// Hub owns at most one Android tool provider, matching the one-active-call
// invariant. Its zero value is usable.
type Hub struct {
	mu             sync.RWMutex
	active         *connection
	generation     uint64
	turn           *voiceTurnPolicy
	turnGeneration uint64
}

type voiceTurnPolicy struct {
	generation   uint64
	allowOpenMap bool
}

// BeginVoiceTurn limits intent-sensitive phone actions to those explicitly
// requested in the current utterance. The returned release must be called when
// the turn finishes. The one-active-voice-conversation invariant means this
// policy is process-wide in the same way as the attached phone provider.
func (h *Hub) BeginVoiceTurn(userText string) func() {
	if h == nil {
		return func() {}
	}
	h.mu.Lock()
	h.turnGeneration++
	generation := h.turnGeneration
	h.turn = &voiceTurnPolicy{
		generation:   generation,
		allowOpenMap: explicitlyRequestsMap(userText),
	}
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.turn != nil && h.turn.generation == generation {
				h.turn = nil
			}
		})
	}
}

func explicitlyRequestsMap(text string) bool {
	text = strings.ToLower(text)
	replacer := strings.NewReplacer(
		".", " ", ",", " ", "?", " ", "!", " ", ":", " ", ";", " ",
		"/", " ", "-", " ", "_", " ", "'", " ", "\"", " ",
	)
	words := strings.Fields(replacer.Replace(text))
	explicit := map[string]bool{
		"map": true, "maps": true, "route": true, "routes": true,
		"navigate": true, "navigation": true, "directions": true,
		"kort": true, "kortet": true, "rute": true, "ruten": true,
		"naviger": true, "navigationen": true, "vejvisning": true,
		"kørselsvejledning": true,
	}
	for _, word := range words {
		if explicit[word] {
			return true
		}
	}
	return false
}

// Attach installs or replaces the provider for callID.
func (h *Hub) Attach(callID string, advertised []string, execute Executor) (func(), error) {
	return h.AttachWithPolicies(callID, advertised, nil, execute)
}

// AttachWithPolicies installs a provider and its per-action local confirmation
// policy. Unknown policy values retain the server catalog's safe default.
func (h *Hub) AttachWithPolicies(callID string, advertised []string, policies map[string]string, execute Executor) (func(), error) {
	if h == nil || execute == nil {
		return nil, errors.New("phone device provider is unavailable")
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil, errors.New("phone device call id is required")
	}
	actions := make(map[Action]bool, len(advertised))
	confirmationPolicies := make(map[Action]string, len(policies))
	for _, raw := range advertised {
		action := Action(strings.TrimSpace(raw))
		policy := strings.ToLower(strings.TrimSpace(policies[string(action)]))
		if _, ok := known[action]; ok && policy != "off" {
			actions[action] = true
			if policy == "ask" || policy == "on" {
				confirmationPolicies[action] = policy
			}
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active != nil && h.active.callID != callID {
		return nil, errors.New("another phone device provider is active")
	}
	h.generation++
	generation := h.generation
	h.active = &connection{callID: callID, actions: actions, confirmationPolicies: confirmationPolicies, execute: execute, generation: generation}
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.active != nil && h.active.generation == generation {
				h.active = nil
			}
		})
	}, nil
}

// DetachCall removes a provider belonging to a voice call that ended.
func (h *Hub) DetachCall(callID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active != nil && h.active.callID == strings.TrimSpace(callID) {
		h.active = nil
		h.generation++
	}
}

// Capabilities returns only server-known actions advertised by Android.
func (h *Hub) Capabilities() []CatalogEntry {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.active == nil {
		return nil
	}
	out := make([]CatalogEntry, 0, len(h.active.actions))
	for _, entry := range catalog {
		if entry.Action == OpenMap && (h.turn == nil || !h.turn.allowOpenMap) {
			continue
		}
		if h.active.actions[entry.Action] {
			switch h.active.confirmationPolicies[entry.Action] {
			case "ask":
				entry.Confirmation = true
			case "on":
				entry.Confirmation = false
			}
			out = append(out, entry)
		}
	}
	return out
}

// Execute invokes one currently advertised action.
func (h *Hub) Execute(ctx context.Context, action Action, args map[string]string) (Result, error) {
	if h == nil {
		return Result{}, errors.New("phone device provider is unavailable")
	}
	h.mu.RLock()
	active := h.active
	if active == nil || !active.actions[action] {
		h.mu.RUnlock()
		return Result{}, fmt.Errorf("phone action %q is not enabled or the phone is disconnected", action)
	}
	if action == OpenMap && (h.turn == nil || !h.turn.allowOpenMap) {
		h.mu.RUnlock()
		return Result{}, errors.New("phone action open_map requires an explicit request in the current voice utterance to view a map or navigate")
	}
	execute := active.execute
	callID := active.callID
	h.mu.RUnlock()
	result, err := execute(ctx, callID, action, args)
	if err != nil {
		return Result{}, fmt.Errorf("phone action %s: %w", action, err)
	}
	result.Text = strings.TrimSpace(result.Text)
	if result.Text == "" {
		result.Text = strings.ReplaceAll(string(action), "_", " ") + " completed"
	}
	if len(result.Text) > 64*1024 {
		return Result{}, errors.New("phone result exceeded 64 KiB")
	}
	return result, nil
}
