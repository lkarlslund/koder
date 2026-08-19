package phonedevice

import (
	"context"
	"errors"
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
