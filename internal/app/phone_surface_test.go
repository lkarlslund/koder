package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/phonedevice"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/voice"
)

// Any chat can be reached from the Web UI and from a phone, typed or spoken.
// Each turn carries the instructions and tools for where it came from, and
// none of it is a property of the chat.
func TestAnyChatTakesWebPhoneTextAndVoiceTurns(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, string(body))
		mu.Unlock()
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"**Done.** The tests pass."},"finish_reason":"stop"}],"usage":{"total_tokens":2}}`)
	}))
	defer modelServer.Close()
	lastRequest := func(count int) string {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			mu.Lock()
			n := len(requests)
			var last string
			if n > 0 {
				last = requests[n-1]
			}
			mu.Unlock()
			if n >= count {
				return last
			}
			if time.Now().After(deadline) {
				t.Fatalf("model got %d requests, want %d", n, count)
			}
		}
	}

	ctrl, _ := newPersistentTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Kind: provider.ProviderKindCompatible, BaseURL: modelServer.URL + "/v1", Timeout: 5 * time.Second},
		}
	})
	release, err := ctrl.PhoneDeviceHub().Attach("phone-1", []string{"get_location"}, func(context.Context, string, phonedevice.Action, map[string]string) (phonedevice.Result, error) {
		return phonedevice.Result{Text: "Aarhus"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// An ordinary text chat, not one created for voice.
	selection := controllerSelection(ctrl)
	ctx, cancel := context.WithTimeout(phonedevice.WithCallID(context.Background(), "phone-1"), 10*time.Second)
	defer cancel()

	// A Web UI turn: normal output, Web UI tools, no phone tools even though
	// a phone is connected.
	if err := ctrl.SendPromptWithKindSelection(ctx, selection, chat.QueueKindUser, "Run the tests", nil); err != nil {
		t.Fatal(err)
	}
	web := lastRequest(1)
	waitChatIdle(t, ctrl, selection)
	if strings.Contains(web, "spoken aloud") || strings.Contains(web, "small screen") || !strings.Contains(web, `"name":"request_user_input"`) || strings.Contains(web, `"name":"phone_`) {
		t.Fatalf("web turn request:\n%s", web)
	}

	// The phone app in text mode: short written replies, the phone's tools.
	message, err := ctrl.RunVoiceChatTurn(ctx, string(selection.SessionID), string(selection.ChatID), "Did they pass?", voice.TurnOptions{ReplyMode: voice.ReplyModeText}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := lastRequest(2)
	if !strings.Contains(text, "small screen") || strings.Contains(text, "spoken aloud") || strings.Contains(text, `"name":"request_user_input"`) || !strings.Contains(text, `"name":"phone_`) {
		t.Fatalf("phone text turn request: small=%v spoken=%v input=%v phone=%v", strings.Contains(text, "small screen"), strings.Contains(text, "spoken aloud"), strings.Contains(text, `"name":"request_user_input"`), strings.Contains(text, `"name":"phone_`))
	}
	if message.SpokenText != "" || len(message.Parts) == 0 || message.Parts[0].MIMEType != "text/markdown" || message.Parts[0].Data != "**Done.** The tests pass." {
		t.Fatalf("phone text reply = %#v", message)
	}

	// A voice call to the same chat: spoken style and pacing, read aloud.
	message, err = ctrl.RunVoiceChatTurn(ctx, string(selection.SessionID), string(selection.ChatID), "And the build?", voice.TurnOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	spoken := lastRequest(3)
	if !strings.Contains(spoken, "spoken aloud") || !strings.Contains(spoken, "Response pacing") || !strings.Contains(spoken, `"name":"phone_`) {
		t.Fatalf("voice turn request:\n%s", spoken)
	}
	if message.SpokenText != "Done. The tests pass." {
		t.Fatalf("voice reply = %#v", message)
	}

	// None of it stuck to the chat: the next Web UI turn is plain again.
	if err := ctrl.SendPromptWithKindSelection(ctx, selection, chat.QueueKindUser, "Thanks", nil); err != nil {
		t.Fatal(err)
	}
	if again := lastRequest(4); strings.Contains(again, "spoken aloud") || strings.Contains(again, "small screen") || strings.Contains(again, `"name":"phone_`) {
		t.Fatalf("web turn after phone turns:\n%s", again)
	}
}

func waitChatIdle(t *testing.T, ctrl *Controller, selection Selection) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		state, err := ctrl.StateForSelection(context.Background(), selection)
		if err == nil && !state.Snapshot.Active && state.Snapshot.Status == chat.StatusIdle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("chat did not go idle: %v %v", state.Snapshot.Status, err)
		}
	}
}
