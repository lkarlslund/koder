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
	SearchCallHistory   Action = "search_call_history"
	RecentNotifications Action = "recent_notifications"
	PlaceCall           Action = "place_call"
	SendSMS             Action = "send_sms"
	ComposeEmail        Action = "compose_email"
	CreateContact       Action = "create_contact"
	EditContact         Action = "edit_contact"
	CreateCalendarEvent Action = "create_calendar_event"
	EditCalendarEvent   Action = "edit_calendar_event"
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
	// UserFacing marks actions that visibly or audibly affect the phone. They
	// are withheld unless the current utterance explicitly requests the action.
	UserFacing bool
}

var catalog = []CatalogEntry{
	{DeviceStatus, "Read battery, charging, storage, network, locale, and time-zone status", "none", false, false},
	{GetLocation, "Read and resolve the phone's current location for questions about where the user is or what is happening nearby; this does not display a map", "none", false, false},
	{SearchContacts, "Search device contacts and return matching names, phone numbers, and email addresses", "query; optional limit", false, false},
	{UpcomingCalendar, "Search calendar events in a time range", "optional query, start_time, end_time, limit", false, false},
	{SearchSMS, "Search SMS messages stored on the phone", "optional query, phone_number, since_time, limit", false, false},
	{SearchCallHistory, "Search recent incoming, outgoing, rejected, and missed calls stored on the phone", "optional query, phone_number, since_time, limit", false, false},
	{RecentNotifications, "Search current notifications, including enabled email and messaging previews", "optional query, app, limit", false, false},
	{PlaceCall, "Place a real phone call after confirmation on the phone", "phone_number or contact_name", true, true},
	{SendSMS, "Send an SMS after confirmation on the phone", "phone_number or contact_name; message", true, true},
	{ComposeEmail, "Open a prefilled email draft for the user to review and send", "optional to, subject, body", true, true},
	{CreateContact, "Open a prefilled new-contact screen for the user to save", "name; optional phone_number, email", true, true},
	{EditContact, "Open one existing contact with proposed changes for review; never save directly", "contact_id or contact_name; one or more of phone_number, email, address, note", true, true},
	{CreateCalendarEvent, "Open a prefilled calendar event for the user to review and save", "title, start_time; optional end_time, location, description", true, true},
	{EditCalendarEvent, "Open one existing calendar event for reviewed changes or user-confirmed cancellation; never update or delete it directly", "event_id or query; operation: edit or cancel; for edit, one or more of title, start_time, end_time, location, description", true, true},
	{OpenMap, "Display a place or route in the phone's map app only when the user explicitly asks to see a map, open a place, or navigate; never use this merely to determine or describe where the user is", "query or latitude and longitude", true, true},
	{SetAlarm, "Open a prefilled alarm on the phone", "hour, minute; optional label", true, true},
	{SetTimer, "Open a prefilled timer on the phone", "duration_seconds; optional label", true, true},
	{ReadClipboard, "Read the current clipboard while Koder Voice is in the foreground", "none", false, false},
	{WriteClipboard, "Replace the phone clipboard after confirmation", "text", true, true},
	{OpenURL, "Open an HTTPS URL on the phone", "url", true, true},
	{MediaControl, "Control the active media session", "media_action: play, pause, toggle, next, or previous", true, true},
	{ListApps, "Search launchable apps installed on the phone", "optional query, limit", false, false},
	{OpenApp, "Open an installed app", "package_name", true, true},
	{ShareText, "Open Android's share sheet with text", "text; optional title", true, true},
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
	generation        uint64
	allowedUserFacing map[Action]bool
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
		generation:        generation,
		allowedUserFacing: explicitlyRequestedUserFacingActions(userText),
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

func explicitlyRequestedUserFacingActions(text string) map[Action]bool {
	normalized := strings.ToLower(text)
	allowed := make(map[Action]bool)
	containsAny := func(terms ...string) bool {
		for _, term := range terms {
			if strings.Contains(normalized, term) {
				return true
			}
		}
		return false
	}
	allow := func(action Action, terms ...string) {
		if containsAny(terms...) {
			allowed[action] = true
		}
	}
	if explicitlyRequestsMap(text) {
		allowed[OpenMap] = true
	}
	openVerb := containsAny("open ", "show ", "visit ", "go to ", "launch ", "start ", "åbn ", "vis ", "gå til ", "start ")
	urlTarget := containsAny("url", "link", "website", "web page", "webpage", "browser", "hjemmeside", "webside", "http://", "https://", "www.")
	if openVerb && urlTarget {
		allowed[OpenURL] = true
	}
	if openVerb && !urlTarget && !allowed[OpenMap] {
		allowed[OpenApp] = true
	}
	allow(ShareText, "share ", "share this", "del ", "del dette")
	if containsAny("email", "e-mail", "mail ") && containsAny("compose", "write", "send", "draft", "skriv", "send", "kladde") {
		allowed[ComposeEmail] = true
	}
	contactTarget := containsAny("contact", "contacts", "phone number", "email address", "kontakt", "kontakter", "telefonnummer", "mailadresse")
	if contactTarget && containsAny("add", "create", "save", "tilføj", "opret", "gem") {
		allowed[CreateContact] = true
	}
	if contactTarget && containsAny("edit", "update", "change", "correct", "rediger", "opdater", "ændr", "ret ") {
		allowed[EditContact] = true
	}
	if containsAny("calendar", "appointment", "event", "meeting", "kalender", "aftale", "møde") && containsAny("add", "create", "schedule", "book", "make", "tilføj", "opret", "planlæg", "book", "lav") {
		allowed[CreateCalendarEvent] = true
	}
	if containsAny("calendar", "appointment", "event", "meeting", "kalender", "aftale", "møde") && containsAny("edit", "update", "change", "move", "reschedule", "cancel", "delete", "rediger", "opdater", "ændr", "flyt", "aflys", "slet") {
		allowed[EditCalendarEvent] = true
	}
	allow(PlaceCall, "call ", "phone call", "ring ", "dial ", "ring til", "ring op")
	if containsAny("sms", "text message", "message", "besked") && containsAny("send", "write", "text ", "skriv") {
		allowed[SendSMS] = true
	}
	allow(SetAlarm, "alarm", "vækkeur")
	allow(SetTimer, "timer", "nedtælling")
	if containsAny("clipboard", "udklipsholder", "copy ", "kopier ") && containsAny("write", "put", "save", "copy", "skriv", "sæt", "gem", "kopier") {
		allowed[WriteClipboard] = true
	}
	allow(MediaControl, "play ", "pause ", "resume ", "next track", "previous track", "afspil ", "sæt på pause", "fortsæt ", "næste sang", "forrige sang")
	return allowed
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
		if entry.UserFacing && (h.turn == nil || !h.turn.allowedUserFacing[entry.Action]) {
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
	entry, knownAction := known[action]
	if knownAction && entry.UserFacing && (h.turn == nil || !h.turn.allowedUserFacing[action]) {
		h.mu.RUnlock()
		return Result{}, fmt.Errorf("phone action %s requires an explicit request for that user-facing action in the current voice utterance", action)
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
