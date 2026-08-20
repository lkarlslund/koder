// Package phonedevice owns permission-gated Android tool providers connected
// to active voice conversations.
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
	PhotosSearch        Action = "phone_photos_search"
	PhotosThumbs        Action = "phone_photos_thumbs"
	PhotoView           Action = "phone_photo_view"
	PhotoTransfer       Action = "phone_photo_transfer"
)

const MaxArtifactBytes = 25 << 20

// Artifact is bounded binary content returned by a phone tool. JSON transports
// encode Data as base64; callers must discard it after materializing the file.
type Artifact struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"data"`
}

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
	{PhotosSearch, "Search photo metadata by capture time or file name without transferring image bytes", "optional start_time, end_time, query, limit", false, false},
	{PhotosThumbs, "Copy a bounded batch of low-resolution photo thumbnails for visual triage", "optional start_time, end_time, query, limit", false, false},
	{PhotoView, "Copy one selected photo at inspection resolution into temporary session storage", "photo_id", false, false},
	{PhotoTransfer, "Copy one selected original photo so Koder can place it at a requested project path and edit it", "photo_id", false, false},
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
	Text      string     `json:"text"`
	Data      any        `json:"data,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

// Executor sends an action to the active Android client.
type Executor func(context.Context, string, Action, map[string]string) (Result, error)

// Control is consumed by the voice-only phone tool.
type Control interface {
	Capabilities() []CatalogEntry
	Execute(context.Context, Action, map[string]string) (Result, error)
}

type callIDContextKey struct{}

// WithCallID binds backend work to the Android call that submitted it.
func WithCallID(ctx context.Context, callID string) context.Context {
	return context.WithValue(ctx, callIDContextKey{}, strings.TrimSpace(callID))
}

// CallIDFromContext returns the Android call bound by the voice transport.
func CallIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(callIDContextKey{}).(string)
	return strings.TrimSpace(value)
}

type connection struct {
	callID               string
	actions              map[Action]bool
	confirmationPolicies map[Action]string
	execute              Executor
	generation           uint64
}

// Hub owns one Android tool provider per active call. Its zero value is usable.
type Hub struct {
	mu             sync.RWMutex
	connections    map[string]*connection
	generation     uint64
	turns          map[string]*voiceTurnPolicy
	turnGeneration uint64
}

type voiceTurnPolicy struct {
	generation        uint64
	callID            string
	allowedUserFacing map[Action]bool
}

// BeginVoiceTurn supports legacy single-provider callers.
func (h *Hub) BeginVoiceTurn(userText string) func() {
	return h.BeginVoiceTurnForChat("", "legacy", userText)
}

// BeginVoiceTurnForChat binds a voice chat's current turn to the phone call
// that submitted it and limits user-facing actions to explicit requests.
func (h *Hub) BeginVoiceTurnForChat(callID, chatID, userText string) func() {
	if h == nil {
		return func() {}
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return func() {}
	}
	h.mu.Lock()
	if callID = strings.TrimSpace(callID); callID == "" && len(h.connections) == 1 {
		for connectedCallID := range h.connections {
			callID = connectedCallID
		}
	}
	h.turnGeneration++
	generation := h.turnGeneration
	if h.turns == nil {
		h.turns = make(map[string]*voiceTurnPolicy)
	}
	h.turns[chatID] = &voiceTurnPolicy{
		generation:        generation,
		callID:            callID,
		allowedUserFacing: explicitlyRequestedUserFacingActions(userText),
	}
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if turn := h.turns[chatID]; turn != nil && turn.generation == generation {
				delete(h.turns, chatID)
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
	if h.connections == nil {
		h.connections = make(map[string]*connection)
	}
	h.generation++
	generation := h.generation
	h.connections[callID] = &connection{callID: callID, actions: actions, confirmationPolicies: confirmationPolicies, execute: execute, generation: generation}
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if active := h.connections[callID]; active != nil && active.generation == generation {
				delete(h.connections, callID)
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
	callID = strings.TrimSpace(callID)
	delete(h.connections, callID)
	for chatID, turn := range h.turns {
		if turn.callID == callID {
			delete(h.turns, chatID)
		}
	}
	h.generation++
}

// ForChat resolves the phone provider bound to a running voice chat.
func (h *Hub) ForChat(chatID string) Control {
	return scopedControl{hub: h, chatID: strings.TrimSpace(chatID)}
}

type scopedControl struct {
	hub    *Hub
	chatID string
}

func (c scopedControl) Capabilities() []CatalogEntry {
	return c.hub.capabilities(c.chatID)
}

func (c scopedControl) Execute(ctx context.Context, action Action, args map[string]string) (Result, error) {
	return c.hub.execute(ctx, c.chatID, action, args)
}

// Capabilities supports legacy callers when exactly one phone is connected.
func (h *Hub) Capabilities() []CatalogEntry {
	return h.capabilities("legacy")
}

func (h *Hub) capabilities(chatID string) []CatalogEntry {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	active, turn := h.connectionForChatLocked(chatID)
	if active == nil {
		return nil
	}
	out := make([]CatalogEntry, 0, len(active.actions))
	for _, entry := range catalog {
		if entry.UserFacing && (turn == nil || !turn.allowedUserFacing[entry.Action]) {
			continue
		}
		if active.actions[entry.Action] {
			switch active.confirmationPolicies[entry.Action] {
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

// CapabilitiesForCall returns every server-known action advertised by one
// sidecar, without applying utterance-specific visibility.
func (h *Hub) CapabilitiesForCall(callID string) []CatalogEntry {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	active := h.connections[strings.TrimSpace(callID)]
	if active == nil {
		return nil
	}
	out := make([]CatalogEntry, 0, len(active.actions))
	for _, entry := range catalog {
		if active.actions[entry.Action] {
			out = append(out, entry)
		}
	}
	return out
}

// Execute supports legacy callers when exactly one phone is connected.
func (h *Hub) Execute(ctx context.Context, action Action, args map[string]string) (Result, error) {
	return h.execute(ctx, "legacy", action, args)
}

func (h *Hub) execute(ctx context.Context, chatID string, action Action, args map[string]string) (Result, error) {
	if h == nil {
		return Result{}, errors.New("phone device provider is unavailable")
	}
	h.mu.RLock()
	active, turn := h.connectionForChatLocked(chatID)
	if active == nil || !active.actions[action] {
		h.mu.RUnlock()
		return Result{}, fmt.Errorf("phone action %q is not enabled or the phone is disconnected", action)
	}
	entry, knownAction := known[action]
	if knownAction && entry.UserFacing && (turn == nil || !turn.allowedUserFacing[action]) {
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
	if err := validateArtifacts(result.Artifacts); err != nil {
		return Result{}, err
	}
	return result, nil
}

func validateArtifacts(artifacts []Artifact) error {
	if len(artifacts) > 12 {
		return errors.New("phone result contains too many artifacts")
	}
	total := 0
	for _, artifact := range artifacts {
		if len(artifact.Data) == 0 {
			return errors.New("phone artifact is empty")
		}
		total += len(artifact.Data)
		if total > MaxArtifactBytes {
			return fmt.Errorf("phone artifacts exceed %d bytes", MaxArtifactBytes)
		}
		if strings.TrimSpace(artifact.MIMEType) == "" {
			return errors.New("phone artifact MIME type is required")
		}
	}
	return nil
}

func (h *Hub) connectionForChatLocked(chatID string) (*connection, *voiceTurnPolicy) {
	if turn := h.turns[strings.TrimSpace(chatID)]; turn != nil {
		return h.connections[turn.callID], turn
	}
	if len(h.connections) == 1 {
		for _, active := range h.connections {
			return active, h.turns["legacy"]
		}
	}
	return nil, nil
}
