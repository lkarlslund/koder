package phonedevice

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestHubFiltersCapabilitiesAndExecutesEnabledAction(t *testing.T) {
	hub := &Hub{}
	release, err := hub.Attach("call-1", []string{"search_contacts", "invented_action"}, func(_ context.Context, callID string, action Action, args map[string]string) (Result, error) {
		if callID != "call-1" || action != SearchContacts || args["query"] != "Steen" {
			t.Fatalf("unexpected request: call=%q action=%q args=%v", callID, action, args)
		}
		return Result{Text: " Found Steen ", Data: map[string]any{"count": 1}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	capabilities := hub.Capabilities()
	if len(capabilities) != 1 || capabilities[0].Action != SearchContacts {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	result, err := hub.Execute(context.Background(), SearchContacts, map[string]string{"query": "Steen"})
	if err != nil || result.Text != "Found Steen" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if _, err := hub.Execute(context.Background(), SendSMS, nil); err == nil {
		t.Fatal("disabled action was executable")
	}
}

func TestHubAppliesPhoneConfirmationPolicies(t *testing.T) {
	hub := &Hub{}
	release, err := hub.AttachWithPolicies("call-1",
		[]string{"device_status", "search_contacts", "send_sms", "open_app"},
		map[string]string{"device_status": "ask", "search_contacts": "on", "send_sms": "off", "open_app": "invalid"},
		func(context.Context, string, Action, map[string]string) (Result, error) {
			return Result{Text: "done"}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	releaseTurn := hub.BeginVoiceTurn("Open Spotify")
	defer releaseTurn()
	capabilities := hub.Capabilities()
	got := make(map[Action]bool, len(capabilities))
	for _, capability := range capabilities {
		got[capability.Action] = capability.Confirmation
	}
	if !got[DeviceStatus] {
		t.Fatal("device_status did not honor ask policy")
	}
	if got[SearchContacts] {
		t.Fatal("search_contacts did not honor on policy")
	}
	if _, exists := got[SendSMS]; exists {
		t.Fatal("send_sms with off policy remained advertised")
	}
	if !got[OpenApp] {
		t.Fatal("invalid open_app policy did not retain safe catalog default")
	}
}

func TestCatalogPublishesCallHistoryAsReadOnlySearch(t *testing.T) {
	entry, ok := known[SearchCallHistory]
	if !ok {
		t.Fatal("search_call_history is missing from phone catalog")
	}
	if entry.Confirmation || !strings.Contains(entry.Arguments, "since_time") || !strings.Contains(entry.Summary, "missed calls") {
		t.Fatalf("call history catalog entry = %#v", entry)
	}
}

func TestCatalogPublishesContactEditAsReviewedMutation(t *testing.T) {
	entry, ok := known[EditContact]
	if !ok || !entry.Confirmation {
		t.Fatalf("edit_contact catalog entry = %#v, exists=%v", entry, ok)
	}
	for _, required := range []string{"contact_id", "address", "note", "never save directly"} {
		if !strings.Contains(entry.Arguments+" "+entry.Summary, required) {
			t.Fatalf("edit_contact catalog entry lacks %q: %#v", required, entry)
		}
	}
}

func TestCatalogPublishesCalendarEditAsReviewedMutation(t *testing.T) {
	entry, ok := known[EditCalendarEvent]
	if !ok || !entry.Confirmation || !entry.UserFacing {
		t.Fatalf("edit_calendar_event catalog entry = %#v, exists=%v", entry, ok)
	}
	for _, required := range []string{"event_id", "operation", "cancel", "never update or delete it directly"} {
		if !strings.Contains(entry.Arguments+" "+entry.Summary, required) {
			t.Fatalf("edit_calendar_event catalog entry lacks %q: %#v", required, entry)
		}
	}
}

func TestHubOwnershipAndReplacementRelease(t *testing.T) {
	hub := &Hub{}
	firstRelease, err := hub.Attach("call-1", []string{"device_status"}, func(context.Context, string, Action, map[string]string) (Result, error) {
		return Result{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	secondRelease, err := hub.Attach("call-1", []string{"get_location"}, func(context.Context, string, Action, map[string]string) (Result, error) {
		return Result{}, errors.New("second")
	})
	if err != nil {
		t.Fatal(err)
	}
	thirdRelease, err := hub.Attach("call-2", []string{"device_status"}, func(context.Context, string, Action, map[string]string) (Result, error) { return Result{}, nil })
	if err != nil {
		t.Fatalf("parallel call provider: %v", err)
	}
	firstRelease()
	if capabilities := hub.CapabilitiesForCall("call-1"); len(capabilities) != 1 || capabilities[0].Action != GetLocation {
		t.Fatalf("stale release detached replacement: %#v", capabilities)
	}
	secondRelease()
	if capabilities := hub.Capabilities(); len(capabilities) != 1 || capabilities[0].Action != DeviceStatus {
		t.Fatalf("release disturbed other call = %#v", capabilities)
	}
	thirdRelease()
	if capabilities := hub.Capabilities(); len(capabilities) != 0 {
		t.Fatalf("all released capabilities = %#v", capabilities)
	}
}

func TestHubRoutesParallelVoiceChatsToTheirOwnPhones(t *testing.T) {
	hub := &Hub{}
	var routed []string
	for _, callID := range []string{"call-1", "call-2"} {
		callID := callID
		release, err := hub.Attach(callID, []string{"device_status"}, func(_ context.Context, gotCallID string, _ Action, _ map[string]string) (Result, error) {
			routed = append(routed, gotCallID)
			return Result{Text: gotCallID}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	releaseOne := hub.BeginVoiceTurnForChat("call-1", "chat-1", "status")
	defer releaseOne()
	releaseTwo := hub.BeginVoiceTurnForChat("call-2", "chat-2", "status")
	defer releaseTwo()
	if _, err := hub.ForChat("chat-1").Execute(context.Background(), DeviceStatus, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.ForChat("chat-2").Execute(context.Background(), DeviceStatus, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(routed, []string{"call-1", "call-2"}) {
		t.Fatalf("routed calls = %v", routed)
	}
}

func TestHubVoiceTurnRequiresExplicitMapIntent(t *testing.T) {
	hub := &Hub{}
	var executed []Action
	releasePhone, err := hub.Attach("call-1", []string{"get_location", "open_map"}, func(_ context.Context, _ string, action Action, _ map[string]string) (Result, error) {
		executed = append(executed, action)
		return Result{Text: "done"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer releasePhone()
	if actions := capabilityActions(hub.Capabilities()); !slices.Equal(actions, []Action{GetLocation}) {
		t.Fatalf("between-turn capabilities = %v, want open_map default-denied", actions)
	}
	if _, err := hub.Execute(context.Background(), OpenMap, nil); err == nil {
		t.Fatal("open_map executed without an active voice-turn policy")
	}

	releaseTurn := hub.BeginVoiceTurn("What's happening where I am?")
	if actions := capabilityActions(hub.Capabilities()); !slices.Equal(actions, []Action{GetLocation}) {
		t.Fatalf("implicit-location capabilities = %v, want only get_location", actions)
	}
	if _, err := hub.Execute(context.Background(), OpenMap, nil); err == nil {
		t.Fatal("open_map executed without explicit map intent")
	}
	releaseTurn()

	releaseTurn = hub.BeginVoiceTurn("Show me the route on a map")
	defer releaseTurn()
	if actions := capabilityActions(hub.Capabilities()); !slices.Equal(actions, []Action{GetLocation, OpenMap}) {
		t.Fatalf("explicit-map capabilities = %v", actions)
	}
	if _, err := hub.Execute(context.Background(), OpenMap, map[string]string{"query": "Aarhus"}); err != nil {
		t.Fatal(err)
	}
	releaseTurn()
	if actions := capabilityActions(hub.Capabilities()); !slices.Equal(actions, []Action{GetLocation}) {
		t.Fatalf("post-turn capabilities = %v, want open_map default-denied", actions)
	}
	if _, err := hub.Execute(context.Background(), OpenMap, nil); err == nil {
		t.Fatal("open_map remained executable after explicit voice turn ended")
	}
	if !slices.Equal(executed, []Action{OpenMap}) {
		t.Fatalf("executed actions = %v", executed)
	}
}

func TestExplicitlyRequestsMap(t *testing.T) {
	tests := map[string]bool{
		"What's happening where I am?":      false,
		"Where am I right now?":             false,
		"Show me this area":                 false,
		"Open it on a map":                  true,
		"Give me directions home":           true,
		"Navigate to Steen":                 true,
		"Vis mig ruten på kortet":           true,
		"Hvad sker der i nærheden af mig?":  false,
		"Giv mig en kørselsvejledning hjem": true,
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := explicitlyRequestsMap(input); got != want {
				t.Fatalf("explicitlyRequestsMap(%q) = %v, want %v", input, got, want)
			}
		})
	}
}

func TestUserFacingPhoneActionsRequireMatchingExplicitRequest(t *testing.T) {
	tests := []struct {
		utterance string
		want      []Action
	}{
		{"Check my email", nil},
		{"What's on my calendar?", nil},
		{"What is happening where I am?", nil},
		{"Open this link in the browser", []Action{OpenURL}},
		{"Open Spotify", []Action{OpenApp}},
		{"Show me the route on a map", []Action{OpenMap}},
		{"Write and send an email to Steen", []Action{ComposeEmail}},
		{"Create a calendar appointment tomorrow", []Action{CreateCalendarEvent}},
		{"Move my calendar meeting to eleven", []Action{EditCalendarEvent}},
		{"Cancel the dentist appointment", []Action{EditCalendarEvent}},
		{"Update Steen's phone number", []Action{EditContact}},
		{"Add Steen to my contacts", []Action{CreateContact}},
		{"Ring til Steen", []Action{PlaceCall}},
	}
	for _, test := range tests {
		t.Run(test.utterance, func(t *testing.T) {
			allowed := explicitlyRequestedUserFacingActions(test.utterance)
			got := make([]Action, 0, len(allowed))
			for _, entry := range catalog {
				if allowed[entry.Action] {
					got = append(got, entry.Action)
				}
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("allowed actions = %v, want %v", got, test.want)
			}
		})
	}
}

func TestHubDefaultDeniesAllUserFacingPhoneActions(t *testing.T) {
	hub := &Hub{}
	release, err := hub.Attach("call-1", []string{"device_status", "open_url", "open_app"}, func(context.Context, string, Action, map[string]string) (Result, error) {
		return Result{Text: "done"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := capabilityActions(hub.Capabilities()); !slices.Equal(got, []Action{DeviceStatus}) {
		t.Fatalf("between-turn capabilities = %v", got)
	}
	if _, err := hub.Execute(context.Background(), OpenURL, map[string]string{"url": "https://example.com"}); err == nil {
		t.Fatal("open_url executed without a matching explicit request")
	}
	releaseTurn := hub.BeginVoiceTurn("Open this link in my browser")
	defer releaseTurn()
	if got := capabilityActions(hub.Capabilities()); !slices.Equal(got, []Action{DeviceStatus, OpenURL}) {
		t.Fatalf("explicit URL capabilities = %v", got)
	}
}

func capabilityActions(capabilities []CatalogEntry) []Action {
	actions := make([]Action, len(capabilities))
	for index, capability := range capabilities {
		actions[index] = capability.Action
	}
	return actions
}
