package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
)

// A model request that fails on the way waits to be retried, says so in the
// chat's state, and the person can retry at once or give up.
func TestModelRetryCanBeHurriedOrAbandoned(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	var requests atomic.Int32
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		if failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"loading model"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Back again."},"finish_reason":"stop"}],"usage":{"total_tokens":2}}`)
	}))
	defer modelServer.Close()

	ctrl, _ := newPersistentTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Kind: provider.ProviderKindCompatible, BaseURL: modelServer.URL + "/v1", Timeout: 5 * time.Second},
		}
	})
	selection := controllerSelection(ctrl)
	ctx := context.Background()
	waitRetry := func(attempt int) *chat.RetryWait {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			state, err := ctrl.StateForSelection(ctx, selection)
			if err == nil && state.Snapshot.Retry != nil && state.Snapshot.Retry.Attempt == attempt {
				return state.Snapshot.Retry
			}
			if time.Now().After(deadline) {
				t.Fatalf("chat never waited for retry %d: %+v %v", attempt, state.Snapshot.Retry, err)
			}
		}
	}

	// Try now: the retry goes out at once instead of in about two seconds.
	if err := ctrl.SendPromptWithKindSelection(ctx, selection, chat.QueueKindUser, "Hello", nil); err != nil {
		t.Fatal(err)
	}
	retry := waitRetry(1)
	if retry.Reason != "Server error 503" || retry.Max != 10 || !strings.Contains(retry.Error, "loading model") || time.Until(retry.At) < time.Second {
		t.Fatalf("retry wait = %+v", retry)
	}
	waitRetry(2)
	failing.Store(false)
	before := requests.Load()
	hurried := time.Now()
	if err := ctrl.RetryNowForSelection(ctx, selection); err != nil {
		t.Fatal(err)
	}
	waitChatIdle(t, ctrl, selection)
	if elapsed := time.Since(hurried); elapsed > time.Second || requests.Load() <= before {
		t.Fatalf("retry now took %s and sent %d more requests", elapsed, requests.Load()-before)
	}
	state, err := ctrl.StateForSelection(ctx, selection)
	if err != nil {
		t.Fatal(err)
	}
	if state.Snapshot.Retry != nil || !timelineHasAssistantText(state.Snapshot.Timeline, "Back again.") {
		t.Fatalf("after retrying: retry %+v", state.Snapshot.Retry)
	}
	if err := ctrl.RetryNowForSelection(ctx, selection); err == nil {
		t.Fatal("retry now with nothing waiting should fail")
	}

	// Cancel: the turn ends at once with the error that caused the retry.
	failing.Store(true)
	if err := ctrl.SendPromptWithKindSelection(ctx, selection, chat.QueueKindUser, "Again", nil); err != nil {
		t.Fatal(err)
	}
	waitRetry(1)
	before = requests.Load()
	if err := ctrl.GiveUpRetryForSelection(ctx, selection); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); ; time.Sleep(10 * time.Millisecond) {
		state, err = ctrl.StateForSelection(ctx, selection)
		if err == nil && !state.Snapshot.Active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("turn still running after giving up: %v %v", state.Snapshot.Status, err)
		}
	}
	if requests.Load() != before || state.Snapshot.Retry != nil || state.Snapshot.Status != chat.StatusErrored || !timelineHasNotice(state.Snapshot.Timeline, "503") {
		t.Fatalf("after giving up: %d more requests, status %v, retry %+v", requests.Load()-before, state.Snapshot.Status, state.Snapshot.Retry)
	}
}

func timelineHasAssistantText(items []domain.TimelineItem, text string) bool {
	for _, item := range items {
		if msg, ok := item.Content.(domain.AssistantMessage); ok && strings.Contains(msg.Text, text) {
			return true
		}
	}
	return false
}

func timelineHasNotice(items []domain.TimelineItem, text string) bool {
	for _, item := range items {
		if notice, ok := item.Content.(domain.Notice); ok && strings.Contains(notice.Text, text) {
			return true
		}
	}
	return false
}
