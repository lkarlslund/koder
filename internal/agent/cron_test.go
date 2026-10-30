package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/cron"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/modeltest"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestCronJobReachesItsChatAsANotification(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	engine := New(cfg, st, nil)
	defer func() { _ = engine.Shutdown(context.Background(), "") }()
	ctx := context.Background()
	session, err := modeltest.CreateSession(ctx, st, "test", cfg.Defaults.ProviderID, cfg.Defaults.ModelID, nil)
	if err != nil {
		t.Fatal(err)
	}
	chat := defaultChatForSession(t, st, session.ID)

	// The cron tool reaches the engine's scheduler.
	evt, err := engine.handleModelToolCall(ctx, session, chat, tools.Request{Tool: tools.Cron, Args: map[string]string{
		"action": "create", "schedule": "7 * * * *", "prompt": "Check the build queue",
	}})
	if err != nil || !strings.Contains(evt.Text, "Scheduled recurring job") {
		t.Fatalf("create = %#v, %v", evt, err)
	}
	jobs, err := engine.cron.List(ctx, chat.ID)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %+v, %v", jobs, err)
	}

	if err := engine.deliverCronJob(ctx, jobs[0], cron.Due); err != nil {
		t.Fatal(err)
	}
	owner, err := engine.LoadSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := owner.Chat(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot := rt.Snapshot()
		found := false
		for _, item := range snapshot.Timeline {
			if user, ok := item.Content.(domain.UserMessage); ok && user.Source == domain.UserMessageSourceCron && strings.Contains(user.Text, "Check the build queue") {
				found = true
			}
		}
		for _, queued := range snapshot.Chat.QueuedInputs {
			if queued.Origin == domain.QueuedInputOriginCron {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cron prompt never reached the chat: queue %+v", snapshot.Chat.QueuedInputs)
		}
		time.Sleep(10 * time.Millisecond)
	}

	missing := jobs[0]
	missing.SessionID = "no-such-session"
	if err := engine.deliverCronJob(ctx, missing, cron.Due); !errors.Is(err, cron.ErrChatGone) {
		t.Fatalf("job of a deleted session: %v", err)
	}
	missing = jobs[0]
	missing.ChatID = "no-such-chat"
	if err := engine.deliverCronJob(ctx, missing, cron.Due); !errors.Is(err, cron.ErrChatGone) {
		t.Fatalf("job of a deleted chat: %v", err)
	}
}

func TestCronJobTextSaysWhetherToRunIt(t *testing.T) {
	job := cron.Job{ID: "a1b2c3d4", Cron: "17 9 30 10 *", Prompt: "Check the deploy", NextRunAt: time.Now()}
	if text := cronJobText(job, cron.Due); !strings.Contains(text, "Carry out its prompt:\n\nCheck the deploy") {
		t.Fatalf("due text = %q", text)
	}
	if text := cronJobText(job, cron.Missed); !strings.Contains(text, "Do not carry it out unless the user asks") {
		t.Fatalf("missed text = %q", text)
	}
}
