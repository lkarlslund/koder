package chatstatustool

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type fakeControl struct{ activity domain.ChatActivity }

func (f *fakeControl) SetChatActivity(_ context.Context, activity domain.ChatActivity) (domain.Chat, error) {
	f.activity = activity
	return domain.Chat{ID: "chat-1", Activity: activity}, nil
}

func TestStatusToolPublishesDescriptiveActivity(t *testing.T) {
	control := &fakeControl{}
	result, err := tools.Call(context.Background(), tools.Options{
		Runtime: tools.Runtime{SessionID: "session-1", ChatID: "chat-1", ChatStatusControl: control},
		Request: tools.Request{Tool: tools.ChatStatus, Args: map[string]string{
			"summary": " Verifying Android voice routing ", "phase": "verifying", "progress_percent": "70", "blocked": "false",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if control.activity.Summary != "Verifying Android voice routing" || control.activity.ProgressPercent == nil || *control.activity.ProgressPercent != 70 {
		t.Fatalf("activity = %#v", control.activity)
	}
	if result.Output != "Chat status updated: Verifying Android voice routing" {
		t.Fatalf("output = %q", result.Output)
	}
}

func TestStatusToolRejectsInvalidProgress(t *testing.T) {
	_, err := (statusTool{}).NormalizeArgs(map[string]string{"summary": "Working", "progress_percent": "101"})
	if err == nil {
		t.Fatal("expected invalid progress error")
	}
}
