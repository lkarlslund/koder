package phonedevice

import (
	"context"
	"errors"
	"slices"
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
	if _, err := hub.Attach("call-2", nil, func(context.Context, string, Action, map[string]string) (Result, error) { return Result{}, nil }); err == nil {
		t.Fatal("different call replaced active provider")
	}
	firstRelease()
	if capabilities := hub.Capabilities(); len(capabilities) != 1 || capabilities[0].Action != GetLocation {
		t.Fatalf("stale release detached replacement: %#v", capabilities)
	}
	secondRelease()
	if capabilities := hub.Capabilities(); len(capabilities) != 0 {
		t.Fatalf("released capabilities = %#v", capabilities)
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

func capabilityActions(capabilities []CatalogEntry) []Action {
	actions := make([]Action, len(capabilities))
	for index, capability := range capabilities {
		actions[index] = capability.Action
	}
	return actions
}
