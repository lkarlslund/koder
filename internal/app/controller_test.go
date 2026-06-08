package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/provider"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools/chattool"
	workspacepkg "github.com/lkarlslund/koder/internal/workspace"
)

func testChatCollection(st *store.Store) store.Collection[domain.Chat] {
	return store.NewCollection(st, store.CollectionSpec[domain.Chat]{
		Namespace: "chats",
		GetID:     func(v domain.Chat) string { return v.ID },
		SetID:     func(v *domain.Chat, id string) { v.ID = id },
		Indexes: []store.IndexSpec[domain.Chat]{
			{Name: "session", Value: func(v domain.Chat) string { return v.SessionID }},
		},
	})
}

func testTimelineCollection(st *store.Store) store.Collection[domain.TimelineItem] {
	return store.NewCollection(st, store.CollectionSpec[domain.TimelineItem]{
		Namespace: "timeline",
		GetID:     func(v domain.TimelineItem) string { return v.ID },
		SetID:     func(v *domain.TimelineItem, id string) { v.ID = id },
		Indexes: []store.IndexSpec[domain.TimelineItem]{
			{Name: "chat", Value: func(v domain.TimelineItem) string { return v.ChatID }},
		},
	})
}

func testGetChat(ctx context.Context, st *store.Store, chatID id.ID) (domain.Chat, error) {
	return testChatCollection(st).Get(ctx, chatID)
}

func testUpdateChat(ctx context.Context, st *store.Store, chatRecord domain.Chat) error {
	return testChatCollection(st).Put(ctx, chatRecord)
}

func testSetChatQueuedInputs(ctx context.Context, st *store.Store, chatID id.ID, items []domain.QueuedInput) error {
	chatRecord, err := testGetChat(ctx, st, chatID)
	if err != nil {
		return err
	}
	chatRecord.QueuedInputs = slices.Clone(items)
	chatRecord.UpdatedAt = time.Now().UTC()
	return testUpdateChat(ctx, st, chatRecord)
}

func testTimelineForChat(ctx context.Context, st *store.Store, chatID id.ID) ([]domain.TimelineItem, error) {
	items, err := testTimelineCollection(st).List(ctx, store.ByIndex[domain.TimelineItem]("chat", string(chatID)))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b domain.TimelineItem) int {
		switch {
		case a.Seq < b.Seq:
			return -1
		case a.Seq > b.Seq:
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return items, nil
}

func testAppendTimeline(ctx context.Context, st *store.Store, chatID id.ID, content domain.TimelineContent) (domain.TimelineItem, error) {
	items, err := testTimelineForChat(ctx, st, chatID)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	now := time.Now().UTC()
	return testTimelineCollection(st).Insert(ctx, domain.TimelineItem{
		ChatID:    chatID,
		Seq:       int64(len(items) + 1),
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func testPutTimelineItem(ctx context.Context, st *store.Store, item domain.TimelineItem) error {
	return testTimelineCollection(st).Put(ctx, item)
}

func testAppendAssistantToolCalls(ctx context.Context, st *store.Store, chatID id.ID, calls []domain.ToolCall, text string, usage domain.Usage) (domain.TimelineItem, error) {
	assistant := domain.AssistantMessage{Text: text}
	for _, call := range calls {
		if err := assistant.AddToolCall(call); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	usage = usage.Normalized()
	if usage.HasAnyTokens() {
		assistant.Usage = &usage
	}
	item, err := testAppendTimeline(ctx, st, chatID, assistant)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	item.Seal(time.Now().UTC())
	if err := testPutTimelineItem(ctx, st, item); err != nil {
		return domain.TimelineItem{}, err
	}
	return item, nil
}

func setSessionProjectRoot(ctx context.Context, st *store.Store, sessionID id.ID, root string) error {
	return sessionpkg.UpdateSession(ctx, st, sessionID, func(session *domain.Session) {
		session.ProjectRoot = root
	})
}

func controllerSelection(ctrl *Controller) Selection {
	state := ctrl.State()
	return Selection{SessionID: state.Session.ID, ChatID: state.ActiveChatID}
}

func newSelectedChat(t *testing.T, ctrl *Controller, selection Selection, title string) domain.Chat {
	t.Helper()
	chatRecord, err := ctrl.NewChatForSelection(context.Background(), selection, title)
	if err != nil {
		t.Fatalf("new selected chat: %v", err)
	}
	return chatRecord
}

func TestControllerStartCreatesSessionAndPublishesState(t *testing.T) {
	ctrl, _ := newTestController(t)
	events, unsub := ctrl.Subscribe()
	defer unsub()

	state := ctrl.State()
	if state.Session.ID == "" {
		t.Fatal("expected active session")
	}
	if state.ActiveChatID == "" {
		t.Fatal("expected active chat")
	}
	if len(state.ChatStatuses) == 0 {
		t.Fatal("expected chat sidebar statuses")
	}
	select {
	case event := <-events:
		if event.Type == "snapshot" {
			t.Fatalf("expected subscriptions to avoid unsolicited full snapshots, got %q", event.Type)
		}
	case <-time.After(20 * time.Millisecond):
	}
}

func TestControllerStateIncludesCurrentChatExecProcesses(t *testing.T) {
	ctrl, _, execManager := newTestControllerWithExec(t)
	state := ctrl.State()
	events, unsub := ctrl.Subscribe()
	defer unsub()
	snap, err := execManager.Start(context.Background(), execruntime.StartRequest{
		SessionID: state.Session.ID,
		ChatID:    state.ActiveChatID,
		Command:   "printf hi",
		Workdir:   state.Session.ProjectRoot,
		YieldTime: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start exec: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		current := ctrl.State().Snapshots[state.ActiveChatID].ExecProcesses
		for _, process := range current {
			if process.ProcessID == snap.ProcessID && strings.Contains(process.Output, "hi") {
				return
			}
		}
		select {
		case <-events:
		case <-deadline:
			t.Fatalf("timed out waiting for exec process in state, got %#v", current)
		}
	}
}

func TestControllerSelectionReceivesExecProcessUpdates(t *testing.T) {
	ctrl, _, execManager := newTestControllerWithExec(t)
	ctx := context.Background()
	selection := controllerSelection(ctrl)
	events, unsub, err := ctrl.SubscribeSelection(ctx, selection)
	if err != nil {
		t.Fatalf("subscribe selection: %v", err)
	}
	defer unsub()
	snap, err := execManager.Start(ctx, execruntime.StartRequest{
		SessionID: selection.SessionID,
		ChatID:    selection.ChatID,
		Command:   "sleep 1",
		Workdir:   ctrl.State().Session.ProjectRoot,
	})
	if err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(func() {
		_, _ = execManager.Terminate(context.Background(), execruntime.TerminateRequest{
			SessionID: selection.SessionID,
			ChatID:    selection.ChatID,
			ProcessID: snap.ProcessID,
		})
	})
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("selection subscription closed")
			}
			if event.Type != "chat_delta" {
				continue
			}
			update, ok := event.Payload.(chat.Update)
			if !ok {
				t.Fatalf("expected chat update payload, got %T", event.Payload)
			}
			for _, process := range update.Snapshot.ExecProcesses {
				if process.ProcessID == snap.ProcessID && process.Command == "sleep 1" && process.State == "running" {
					return
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for selected exec process update")
		}
	}
}

func TestControllerSelectionStripsOffscreenTranscriptItems(t *testing.T) {
	ctrl, _, _ := newTestControllerWithExec(t)
	selectedChatID := id.ID("selected-chat")
	offscreenChatID := id.ID("offscreen-chat")
	item := domain.TimelineItem{
		ID:      "item-1",
		ChatID:  offscreenChatID,
		Seq:     1,
		Content: domain.AssistantMessage{Text: "streaming elsewhere"},
	}
	event, ok := ctrl.eventForSelectedSession(sessionpkg.Event{
		Kind:      sessionpkg.EventChatChanged,
		SessionID: "session-1",
		Update: chat.Update{
			Event: &domain.Event{
				Kind: domain.EventKindMessageDelta,
				Item: item,
				Text: "streaming elsewhere",
			},
			Snapshot: chat.Snapshot{
				Chat:   domain.Chat{ID: offscreenChatID, SessionID: "session-1", Title: "Offscreen"},
				Status: chat.StatusStreamingResponse,
				Active: true,
			},
			TranscriptChanged: true,
			ContextChanged:    true,
			StatusChanged:     true,
		},
	}, selectedChatID)
	if !ok {
		t.Fatal("expected chat delta")
	}
	update, ok := event.Payload.(chat.Update)
	if !ok {
		t.Fatalf("expected chat update, got %T", event.Payload)
	}
	if update.Event != nil || update.TranscriptChanged || update.ContextChanged {
		t.Fatalf("expected offscreen transcript payload to be stripped, got %#v", update)
	}
	if update.Snapshot.Chat.ID != offscreenChatID || update.Status != chat.StatusStreamingResponse || !update.Active {
		t.Fatalf("expected offscreen sidebar status to remain, got %#v", update)
	}
}

func TestControllerSelectedStateCanCreateAndSelectChats(t *testing.T) {
	ctrl, _ := newTestController(t)
	ctx := context.Background()
	selection := controllerSelection(ctrl)
	first := selection.ChatID
	if first == "" {
		t.Fatal("expected first chat")
	}
	second := newSelectedChat(t, ctrl, selection, "side chat").ID
	if second == "" || second == first {
		t.Fatalf("expected new active chat, first=%s second=%s", first, second)
	}
	state, err := ctrl.StateForSelection(ctx, Selection{SessionID: selection.SessionID, ChatID: first})
	if err != nil {
		t.Fatalf("state for first chat: %v", err)
	}
	if got := state.ActiveChatID; got != first {
		t.Fatalf("expected active chat %s, got %s", first, got)
	}
}

func TestControllerDeleteInactiveChat(t *testing.T) {
	ctrl, st := newTestController(t)
	ctx := context.Background()
	selection := controllerSelection(ctrl)
	active := selection.ChatID
	side := newSelectedChat(t, ctrl, selection, "side chat").ID
	if err := ctrl.DeleteChatForSelection(ctx, selection, side); err != nil {
		t.Fatalf("delete chat: %v", err)
	}
	state, err := ctrl.StateForSelection(ctx, selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	if got := state.ActiveChatID; got != active {
		t.Fatalf("expected active chat to stay %s, got %s", active, got)
	}
	archived, err := testGetChat(ctx, st, side)
	if err != nil {
		t.Fatal(err)
	}
	if !archived.Archived {
		t.Fatalf("expected side chat to be archived, got %#v", archived)
	}
}

func TestControllerDeleteActiveChatSwitchesToRemainingChat(t *testing.T) {
	ctrl, st := newTestController(t)
	ctx := context.Background()
	selection := controllerSelection(ctrl)
	first := selection.ChatID
	side := newSelectedChat(t, ctrl, selection, "side chat").ID
	if err := ctrl.DeleteChatForSelection(ctx, Selection{SessionID: selection.SessionID, ChatID: side}, side); err != nil {
		t.Fatalf("delete active chat: %v", err)
	}
	state, err := ctrl.StateForSelection(ctx, Selection{SessionID: selection.SessionID})
	if err != nil {
		t.Fatalf("state for session: %v", err)
	}
	if got := state.ActiveChatID; got != first {
		t.Fatalf("expected active chat to switch to %s, got %s", first, got)
	}
	archived, err := testGetChat(ctx, st, side)
	if err != nil {
		t.Fatal(err)
	}
	if !archived.Archived {
		t.Fatalf("expected side chat to be archived, got %#v", archived)
	}
}

func TestControllerForwardRuntimeRefreshesChatListMetadata(t *testing.T) {
	ctrl, _ := newTestController(t)
	state := ctrl.State()
	if state.ActiveChatID == "" {
		t.Fatal("expected active chat")
	}
	ctrl.mu.Lock()
	if ctrl.unsub != nil {
		ctrl.unsub()
		ctrl.unsub = nil
	}
	ctrl.runtime = nil
	ctrl.mu.Unlock()
	updated := state.Snapshot
	updated.Chat.ID = state.ActiveChatID
	updated.Chat.Title = "Generated Chat Title"
	updated.Status = chat.StatusRunningTools
	updated.StatusText = "Running tools"
	updates := make(chan chat.Update, 1)
	updates <- chat.Update{Snapshot: updated, Status: chat.StatusRunningTools, StatusText: "Running tools", Active: true}
	close(updates)

	ctrl.applySessionChatEvent(sessionpkg.Event{Kind: sessionpkg.EventChatChanged, SessionID: state.Session.ID, Chat: updated.Chat, Snapshot: updated, Update: <-updates})

	got := ctrl.State()
	var listed string
	for _, item := range got.Chats {
		if item.ID == state.ActiveChatID {
			listed = item.Title
			break
		}
	}
	if listed != "Generated Chat Title" {
		t.Fatalf("expected chat list title updated, got %q", listed)
	}
	var sidebarStatus ChatSidebarStatus
	for _, item := range got.ChatStatuses {
		if item.ChatID == state.ActiveChatID {
			sidebarStatus = item
			break
		}
	}
	if sidebarStatus.Status != string(chat.StatusRunningTools) || !sidebarStatus.Busy {
		t.Fatalf("expected active chat sidebar status running tools, got %#v", sidebarStatus)
	}
}

func TestControllerForwardRuntimeRefreshesInactiveChatMetadata(t *testing.T) {
	ctrl, _ := newTestController(t)
	selection := controllerSelection(ctrl)
	first := selection.ChatID
	if first == "" {
		t.Fatal("expected first chat")
	}
	side := newSelectedChat(t, ctrl, selection, "side chat").ID
	if side == "" || side == first {
		t.Fatalf("expected side chat, first=%s side=%s", first, side)
	}

	updated := ctrl.State().Snapshots[side]
	updated.Chat.ID = side
	updated.Chat.Title = "Generated Side Title"
	updated.Status = chat.StatusWaitingApproval
	updated.StatusText = "Waiting for approval"
	updates := make(chan chat.Update, 1)
	updates <- chat.Update{Snapshot: updated, Status: chat.StatusWaitingApproval, StatusText: "Waiting for approval", Active: true}
	close(updates)

	ctrl.applySessionChatEvent(sessionpkg.Event{Kind: sessionpkg.EventChatChanged, SessionID: ctrl.State().Session.ID, Chat: updated.Chat, Snapshot: updated, Update: <-updates})

	got := ctrl.State()
	if got.ActiveChatID != first {
		t.Fatalf("expected active chat to remain %s, got %s", first, got.ActiveChatID)
	}
	if got.Snapshots[side].Chat.Title != "Generated Side Title" {
		t.Fatalf("expected inactive snapshot title updated, got %#v", got.Snapshots[side].Chat)
	}
	var listed string
	for _, item := range got.Chats {
		if item.ID == side {
			listed = item.Title
			break
		}
	}
	if listed != "Generated Side Title" {
		t.Fatalf("expected inactive chat list title updated, got %q", listed)
	}
	var sidebarStatus ChatSidebarStatus
	for _, item := range got.ChatStatuses {
		if item.ChatID == side {
			sidebarStatus = item
			break
		}
	}
	if sidebarStatus.Status != string(chat.StatusWaitingApproval) || !sidebarStatus.Busy {
		t.Fatalf("expected inactive chat sidebar status waiting approval, got %#v", sidebarStatus)
	}
}

func TestControllerSessionEventAddsStartedChatToState(t *testing.T) {
	ctrl, _ := newTestController(t)
	state := ctrl.State()
	if state.Session.ID == "" || state.ActiveChatID == "" {
		t.Fatal("expected active session and chat")
	}
	if _, err := ctrl.SetMilestonePlan(context.Background(), state.Session.ID, "Ship it", []planning.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusReady, Position: 0},
	}); err != nil {
		t.Fatalf("set milestone plan: %v", err)
	}
	todos, err := ctrl.AddTodoItems(context.Background(), state.Session.ID, "alpha", []string{"Implement alpha"})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}

	status, err := ctrl.agent.StartChat(context.Background(), state.Session.ID, state.ActiveChatID, chattool.StartRequest{
		Profile:   chatrole.Execution,
		Objective: "Implement only the assigned task",
		TodoRef:   todos[0].ID,
	})
	if err != nil {
		t.Fatalf("start chat: %v", err)
	}

	var next State
	deadline := time.After(2 * time.Second)
	for {
		next = ctrl.State()
		if _, ok := next.Snapshots[status.ID]; ok {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for started chat in state, got %#v", next.Chats)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	found := false
	for _, item := range next.Chats {
		if item.ID == status.ID && item.ActiveMilestoneRef == "alpha" && item.AssignedTodoRef == todos[0].ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected started chat in sidebar state, got %#v", next.Chats)
	}
	if _, ok := next.Snapshots[status.ID]; !ok {
		t.Fatalf("expected started chat snapshot, got %#v", next.Snapshots)
	}
}

func TestControllerModelOptionsLoadsLiveModels(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"z-model","owned_by":"remote"},{"id":"a-model"}]}`)
	}))
	defer modelServer.Close()

	ctrl, _ := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Name: "Test Provider", BaseURL: modelServer.URL + "/v1"},
		}
		cfg.SetModelConfig(config.ModelConfig{
			ProviderID:       "test",
			ModelID:          "fast-qwen",
			SourceProviderID: "test",
			SourceModelID:    "z-model",
			ContextWindow:    65536,
		})
	})

	options, err := ctrl.ModelOptions(context.Background())
	if err != nil {
		t.Fatalf("model options: %v", err)
	}
	got := make([]string, 0, len(options))
	for _, option := range options {
		got = append(got, option.ProviderID+"/"+option.ModelID)
	}
	want := []string{"test/fast-qwen", "test/a-model", "test/z-model"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected options %v, got %v", want, got)
	}
	custom := options[0]
	if !custom.Custom || !custom.Editable || !custom.BackingDetected || custom.SourceModelID != "z-model" {
		t.Fatalf("expected custom option with detected backing model, got %#v", custom)
	}
	if options[1].Editable || !options[1].Detected {
		t.Fatalf("expected detected model to be read-only, got %#v", options[1])
	}
}

func TestControllerModelOptionsDoesNotInventMissingCurrentProvider(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"live-model"}]}`)
	}))
	defer modelServer.Close()

	ctrl, st := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Name: "Test Provider", BaseURL: modelServer.URL + "/v1"},
		}
	})
	_ = st

	options, err := ctrl.ModelOptions(context.Background())
	if err != nil {
		t.Fatalf("model options: %v", err)
	}
	for _, option := range options {
		if option.ProviderID == "ghost" || option.ModelID == "ghost-model" {
			t.Fatalf("unexpected stale current model option: %#v", options)
		}
	}
}

func TestControllerModelOptionsReportsProviderFailureWithoutDefaults(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer modelServer.Close()

	ctrl, _ := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Name: "Test Provider", BaseURL: modelServer.URL + "/v1"},
		}
	})

	_, err := ctrl.ModelOptions(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to load models from test") {
		t.Fatalf("expected provider failure, got %v", err)
	}
}

func TestControllerSavePreferencesPersistsConfigAndPrompts(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)
	t.Setenv("HOME", temp)

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: "https://example.invalid/v1", Stream: true, Timeout: time.Minute},
	}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "model", ContextWindow: 32768})
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	projectRoot := t.TempDir()
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(context.Background(), StartupModeNew, projectRoot); err != nil {
		t.Fatal(err)
	}

	prefs, err := ctrl.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foundWebSearch := false
	for _, item := range prefs.ToolDefaults {
		if item.Tool == domain.ToolKindWebSearch {
			foundWebSearch = true
			if item.Group != "web" || item.GroupLabel != "Web" || item.Label != "web_search" {
				t.Fatalf("expected web_search tool grouping metadata, got %#v", item)
			}
		}
	}
	if !foundWebSearch {
		t.Fatalf("expected web_search tool default in %#v", prefs.ToolDefaults)
	}
	prefs.General.MaxToolLoopSteps = 77
	prefs.UI.Theme = "dark"
	prefs.Compaction.UseChatModel = false
	prefs.Compaction.ProviderID = "test"
	prefs.Compaction.ModelID = "compact-model"
	prefs.Compaction.AutoCompactAt = 66
	prefs.Compaction.KeepToolCalls = 3
	prefs.Thinking.CavemanEnabled = true
	prefs.Thinking.UseChatModel = false
	prefs.Thinking.ProviderID = "test"
	prefs.Thinking.ModelID = "model"
	prefs.Thinking.CavemanPrompt = "rewrite thinking:\n{{thinking}}"
	prefs.Thinking.CavemanMinTokens = 96
	temperature := 0.7
	topP := 0.9
	prefs.ModelConfigs = []ModelConfigPreference{{
		ProviderID:     "test",
		ModelID:        "model",
		ContextWindow:  12345,
		ModelPreset:    provider.ModelPresetDefault,
		Temperature:    &temperature,
		TopP:           &topP,
		ThinkingMode:   "enabled",
		ThinkingBudget: 4096,
	}}
	prefs.MCPServers = []MCPServerPreference{{
		ID:             "docs",
		Name:           "Docs",
		URL:            "https://mcp.example.invalid/sse",
		Headers:        map[string]string{"X-Test": "yes"},
		RequestTimeout: "45s",
	}}
	for idx := range prefs.Prompts {
		if prefs.Prompts[idx].Target == "compaction-prompt.md" {
			prefs.Prompts[idx].Content = "custom compact prompt\n"
		}
	}
	updated, err := ctrl.SavePreferences(context.Background(), prefs)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Compaction.ModelID != "compact-model" {
		t.Fatalf("expected updated preferences response, got %#v", updated.Compaction)
	}
	if !modelOptionsContain(updated.Models, "test", "compact-model") {
		t.Fatalf("expected saved compaction model in preferences options, got %#v", updated.Models)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MaxToolLoopSteps != 77 || loaded.UI.Theme != "dark" || loaded.CompactionModel != "compact-model" {
		t.Fatalf("expected saved config, got max=%d theme=%q compact=%q/%q", loaded.MaxToolLoopSteps, loaded.UI.Theme, loaded.CompactionProvider, loaded.CompactionModel)
	}
	if !loaded.Thinking.CavemanEnabled || loaded.Thinking.CavemanProvider != "test" || loaded.Thinking.CavemanModel != "model" || loaded.Thinking.CavemanPrompt != "rewrite thinking:\n{{thinking}}" || loaded.Thinking.CavemanMinTokens != 96 {
		t.Fatalf("expected saved thinking settings, got %#v", loaded.Thinking)
	}
	if got := loaded.ContextWindow("test", "model"); got != 12345 {
		t.Fatalf("expected saved model context window, got %d", got)
	}
	if got := loaded.ModelPreset("test", "model"); got != provider.ModelPresetDefault {
		t.Fatalf("expected saved model preset, got %q", got)
	}
	modelCfg, ok := loaded.ModelConfig("test", "model")
	if !ok || modelCfg.Temperature == nil || *modelCfg.Temperature != 0.7 || modelCfg.TopP == nil || *modelCfg.TopP != 0.9 || modelCfg.ThinkingMode != "enabled" || modelCfg.ThinkingBudget != 4096 {
		t.Fatalf("expected saved model request settings, got %#v", modelCfg)
	}
	if loaded.MCPServers["docs"].URL != "https://mcp.example.invalid/sse" || loaded.MCPServers["docs"].Headers["X-Test"] != "yes" {
		t.Fatalf("expected saved MCP server, got %#v", loaded.MCPServers["docs"])
	}
	restarted := New(loaded, st, agent.New(loaded, st, nil, nil))
	if err := restarted.Start(context.Background(), StartupModeNew, projectRoot); err != nil {
		t.Fatal(err)
	}
	restartedPrefs, err := restarted.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if restartedPrefs.Compaction.UseChatModel || restartedPrefs.Compaction.ProviderID != "test" || restartedPrefs.Compaction.ModelID != "compact-model" {
		t.Fatalf("expected restart preferences to restore compaction model, got %#v", restartedPrefs.Compaction)
	}
	if !restartedPrefs.Thinking.CavemanEnabled || restartedPrefs.Thinking.UseChatModel || restartedPrefs.Thinking.ProviderID != "test" || restartedPrefs.Thinking.ModelID != "model" || restartedPrefs.Thinking.CavemanMinTokens != 96 {
		t.Fatalf("expected restart preferences to restore thinking model, got %#v", restartedPrefs.Thinking)
	}
	if !modelOptionsContain(restartedPrefs.Models, "test", "compact-model") {
		t.Fatalf("expected restart preferences options to include compaction model, got %#v", restartedPrefs.Models)
	}
	data, err := os.ReadFile(filepath.Join(temp, ".koder", "compaction-prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "custom compact prompt\n" {
		t.Fatalf("expected prompt file update, got %q", string(data))
	}
}

func TestControllerSavePreferencesRepairsDeletedDefaultProvider(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)
	t.Setenv("HOME", temp)

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "old-model"
	cfg.Providers = map[string]config.Provider{
		"test":  {BaseURL: "https://example.invalid/v1"},
		"other": {BaseURL: "https://example.invalid/v1"},
	}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "old-model", ContextWindow: 32768})
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "other", ModelID: "new-model", ContextWindow: 32768})
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	projectRoot := t.TempDir()
	ctrl := New(cfg, st, agent.New(cfg, st, nil, nil))
	if err := ctrl.Start(context.Background(), StartupModeNew, projectRoot); err != nil {
		t.Fatal(err)
	}

	prefs, err := ctrl.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	prefs.General.DefaultProvider = "test"
	prefs.General.DefaultModel = "old-model"
	prefs.Providers.DefaultProvider = "other"
	prefs.Providers.DefaultModel = "new-model"

	if _, err := ctrl.DeleteProvider(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	updated, err := ctrl.SavePreferences(context.Background(), prefs)
	if err != nil {
		t.Fatal(err)
	}
	if updated.General.DefaultProvider != "other" || updated.General.DefaultModel != "new-model" {
		t.Fatalf("expected repaired default provider, got %#v", updated.General)
	}
}

func TestControllerPreferencesRepairsMissingDefaultModel(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)
	t.Setenv("HOME", temp)

	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"new-model"}]}`)
	}))
	defer modelServer.Close()

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "old-model"
	cfg.Providers = map[string]config.Provider{"test": {Name: "Test", BaseURL: modelServer.URL + "/v1"}}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "old-model", ContextWindow: 32768})
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctrl := New(cfg, st, agent.New(cfg, st, nil, nil))
	if err := ctrl.Start(context.Background(), StartupModeNew, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	prefs, err := ctrl.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if prefs.General.DefaultProvider != "test" || prefs.General.DefaultModel != "new-model" {
		t.Fatalf("expected default model to move to detected model, got %#v", prefs.General)
	}
}

func modelOptionsContain(options []ModelOption, providerID, modelID string) bool {
	for _, option := range options {
		if option.ProviderID == providerID && option.ModelID == modelID {
			return true
		}
	}
	return false
}

func TestControllerResetPromptRestoresEmbeddedDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ctrl, _ := newTestController(t)
	path := filepath.Join(home, ".koder", "compaction-prompt.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt, err := ctrl.ResetPrompt("compaction-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.Content, "Summarize this coding session") {
		t.Fatalf("expected embedded compaction prompt, got %q", prompt.Content)
	}
}

func TestControllerSetModelUpdatesStoreStateAndRuntimeSnapshot(t *testing.T) {
	ctrl, st := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {BaseURL: "https://example.invalid/v1"},
		}
		cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "next-model", ContextWindow: 12345})
	})
	ctx := context.Background()
	selection := controllerSelection(ctrl)
	if err := ctrl.SetModelForSelection(ctx, selection, "test", "next-model"); err != nil {
		t.Fatalf("set model: %v", err)
	}

	state, err := ctrl.StateForSelection(ctx, selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	if state.Snapshot.Chat.ProviderID != "test" || state.Snapshot.Chat.ModelID != "next-model" {
		t.Fatalf("expected state chat model test/next-model, got %s/%s", state.Snapshot.Chat.ProviderID, state.Snapshot.Chat.ModelID)
	}
	if state.ContextWindow != 12345 {
		t.Fatalf("expected context window 12345, got %d", state.ContextWindow)
	}
	if state.ModelInfo.ProviderID != "test" || state.ModelInfo.ModelID != "next-model" || state.ModelInfo.ContextWindow != 12345 || !state.ModelInfo.SupportsTools {
		t.Fatalf("unexpected model info: %#v", state.ModelInfo)
	}
	chatRecord, err := testGetChat(ctx, st, state.Snapshot.Chat.ID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chatRecord.ModelID != "next-model" {
		t.Fatalf("expected stored chat model next-model, got %q", chatRecord.ModelID)
	}
}

func TestControllerStartDetectsActiveModelContextWindow(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model","status":{"args":["llama-server","--ctx-size","262144"]}}]}`))
		default:
			t.Fatalf("unexpected model server path: %s", r.URL.Path)
		}
	}))
	defer modelServer.Close()

	ctrl, _ := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Kind: provider.ProviderKindCompatible, BaseURL: modelServer.URL, Timeout: time.Second},
		}
	})

	state := ctrl.State()
	if state.ContextWindow != 262144 {
		t.Fatalf("expected detected context window 262144, got %d", state.ContextWindow)
	}
	if state.ModelInfo.ContextWindow != 262144 {
		t.Fatalf("expected model info context window 262144, got %#v", state.ModelInfo)
	}
}

func TestControllerSetAccessSettingsUpdatesActiveSession(t *testing.T) {
	ctrl, st := newTestController(t)
	ctx := context.Background()
	selection := controllerSelection(ctrl)
	sessionID := selection.SessionID
	settings := accesssettings.AllowAll()
	if err := ctrl.SetAccessSettingsForSelection(ctx, selection, settings); err != nil {
		t.Fatalf("set access settings: %v", err)
	}
	session, err := sessionpkg.GetSession(ctx, st, sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.AccessSettings.Root != accesssettings.ModeReadWrite || !session.AccessSettings.Network {
		t.Fatalf("expected allow-all access settings, got %#v", session.AccessSettings)
	}
	state, err := ctrl.StateForSelection(ctx, selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	if got := state.Access.Settings.Root; got != accesssettings.ModeReadWrite {
		t.Fatalf("expected root readwrite, got %q", got)
	}
}

func TestControllerAccessPresetsAreExposed(t *testing.T) {
	ctrl, _ := newTestController(t)

	var got []string
	for _, preset := range ctrl.State().Access.Presets {
		got = append(got, preset.ID)
	}
	want := []string{"locked-down", "normal-coding", "allow-all"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected access presets, got %v", got)
	}
}

func TestControllerAccessSettingsPersistBySession(t *testing.T) {
	workdir := t.TempDir()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctrl := New(cfg, st, agent.New(cfg, st, nil, nil))
	if err := ctrl.Start(context.Background(), StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	settings := accesssettings.LockedDown()
	selection := controllerSelection(ctrl)
	if err := ctrl.SetAccessSettingsForSelection(context.Background(), selection, settings); err != nil {
		t.Fatalf("set access settings: %v", err)
	}
	sessionID := selection.SessionID
	if err := ctrl.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil)
	next := New(cfg, st, engine)
	if err := next.loadSession(context.Background(), sessionID, ""); err != nil {
		t.Fatalf("start next controller: %v", err)
	}
	if got := next.State().Access.Settings.Network; got {
		t.Fatalf("expected access settings to persist with network disabled")
	}
}

func TestControllerSetAccessSettingsSurvivesRuntimeUpdate(t *testing.T) {
	ctrl, _ := newTestController(t)
	ctx := context.Background()
	selection := controllerSelection(ctrl)
	settings := accesssettings.LockedDown()
	if err := ctrl.SetAccessSettingsForSelection(ctx, selection, settings); err != nil {
		t.Fatalf("set access settings: %v", err)
	}
	owner, err := ctrl.agent.LoadSession(ctx, selection.SessionID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	rt, err := owner.Chat(ctx, selection.ChatID)
	if err != nil {
		t.Fatalf("load chat: %v", err)
	}
	events, unsub := ctrl.Subscribe()
	defer unsub()
	state, err := ctrl.StateForSelection(ctx, selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	rt.SetSession(state.Session)

	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Type != "snapshot" && event.Type != "chat_delta" && event.Type != "session_delta" {
				continue
			}
			state, err := ctrl.StateForSelection(ctx, selection)
			if err != nil {
				t.Fatalf("state for selection: %v", err)
			}
			if got := state.Access.Settings.Network; got {
				t.Fatalf("expected runtime update to preserve network disabled")
			}
			return
		case <-deadline:
			t.Fatalf("expected runtime update to preserve access settings")
		}
	}
}

func TestControllerSetAccessSettingsRejectsRelativeMount(t *testing.T) {
	ctrl, _ := newTestController(t)
	settings := accesssettings.Default()
	settings.Mounts = []accesssettings.Mount{{Path: "relative", Mode: accesssettings.ModeReadOnly}}
	if err := ctrl.SetAccessSettingsForSelection(context.Background(), controllerSelection(ctrl), settings); err == nil {
		t.Fatal("expected relative mount error")
	}
}

func TestControllerSessionsCanUseDifferentProjectRoots(t *testing.T) {
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	sessionA, err := sessionpkg.CreateSession(ctx, st, "Workspace A", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session a: %v", err)
	}
	if err := setSessionProjectRoot(ctx, st, sessionA.ID, workspaceA); err != nil {
		t.Fatalf("workspace a: %v", err)
	}
	sessionB, err := sessionpkg.CreateSession(ctx, st, "Workspace B", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session b: %v", err)
	}
	if err := setSessionProjectRoot(ctx, st, sessionB.ID, workspaceB); err != nil {
		t.Fatalf("workspace b: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeResume, workspaceA); err != nil {
		t.Fatalf("start resume: %v", err)
	}
	if got := ctrl.State().Session.ID; got != sessionB.ID {
		t.Fatalf("expected newest session %s, got %s", sessionB.ID, got)
	}
	sessionState, err := ctrl.Sessions(ctx)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(sessionState.Sessions) != 2 {
		t.Fatalf("expected both project-root sessions, got %#v", sessionState.Sessions)
	}
	created, err := ctrl.CreateSession(ctx, "Second A", workspaceA, false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if got := created.ProjectRoot; got != workspaceA {
		t.Fatalf("expected new session project root %q, got %q", workspaceA, got)
	}
	missingRoot := filepath.Join(t.TempDir(), "missing", "project")
	if _, err := ctrl.CreateSession(ctx, "Missing", missingRoot, false); err == nil || !strings.Contains(err.Error(), "project root does not exist") {
		t.Fatalf("expected missing project root error, got %v", err)
	}
	if _, err := os.Stat(missingRoot); !os.IsNotExist(err) {
		t.Fatalf("expected missing project root to remain absent, got %v", err)
	}
	createdMissing, err := ctrl.CreateSession(ctx, "Created Missing", missingRoot, true)
	if err != nil {
		t.Fatalf("create missing project root session: %v", err)
	}
	if got := createdMissing.ProjectRoot; got != missingRoot {
		t.Fatalf("expected created project root %q, got %q", missingRoot, got)
	}
	if info, err := os.Stat(missingRoot); err != nil || !info.IsDir() {
		t.Fatalf("expected project root directory to be created, info=%#v err=%v", info, err)
	}
	sessionState, err = ctrl.Sessions(ctx)
	if err != nil {
		t.Fatalf("sessions after new: %v", err)
	}
	if len(sessionState.Sessions) != 4 {
		t.Fatalf("expected four sessions, got %#v", sessionState.Sessions)
	}
}

func TestControllerStateForSelectionDoesNotSwitchControllerState(t *testing.T) {
	ctrl, _ := newTestController(t)
	ctx := context.Background()

	firstState := ctrl.State()
	firstSessionID := firstState.Session.ID
	secondSession, err := ctrl.CreateSession(ctx, "Second", firstState.Session.ProjectRoot, false)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if got := ctrl.State().Session.ID; got != firstSessionID {
		t.Fatalf("expected controller selection to remain %s, got %s", firstSessionID, got)
	}
	secondState, err := ctrl.StateForSelection(ctx, Selection{SessionID: secondSession.ID})
	if err != nil {
		t.Fatalf("state for second session: %v", err)
	}
	if secondState.Session.ID != secondSession.ID {
		t.Fatalf("expected selected state for second session %s, got %s", secondSession.ID, secondState.Session.ID)
	}
	if secondState.ActiveChatID == "" {
		t.Fatalf("expected selected state to include active chat")
	}
	if got := ctrl.State().Session.ID; got != firstSessionID {
		t.Fatalf("expected controller state to remain %s after second selection state, got %s", firstSessionID, got)
	}
}

func TestControllerStartupNewResumesRestartInterruptedWorkspaceSession(t *testing.T) {
	var requests atomic.Int32
	var requestBodies atomic.Value
	requestBodies.Store([]string(nil))
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		current := requestBodies.Load().([]string)
		requestBodies.Store(append(current, string(body)))
		requests.Add(1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"resumed"}}],"usage":{"total_tokens":1}}`))
	}))
	defer providerServer.Close()

	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.ToolDefaults[domain.ToolKindBash] = true
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: providerServer.URL + "/v1"},
	}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "model", ContextWindow: 32768})
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	workdir := t.TempDir()
	session, err := sessionpkg.CreateSession(ctx, st, "Interrupted Session", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := setSessionProjectRoot(ctx, st, session.ID, workdir); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	chatRecord, err := sessionpkg.DefaultChat(ctx, st, session.ID)
	if err != nil {
		t.Fatalf("default chat: %v", err)
	}
	chatRecord.AutoRestart = true
	if err := testUpdateChat(ctx, st, chatRecord); err != nil {
		t.Fatalf("mark auto restart: %v", err)
	}
	if _, err := testAppendAssistantToolCalls(ctx, st, chatRecord.ID, []domain.ToolCall{{
		ToolCallID: "call_1",
		Tool:       domain.ToolKindBash,
		Args:       map[string]string{"command": "printf resumed-tool"},
		Status:     domain.ToolStatusPending,
	}}, "", domain.Usage{}); err != nil {
		t.Fatalf("append pending tool call: %v", err)
	}
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	if got := ctrl.State().Session.ID; got != session.ID {
		t.Fatalf("expected restart interrupted session %s, got %s", session.ID, got)
	}
	timeline, err := testTimelineForChat(ctx, st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	assistant, ok := timeline[0].Content.(domain.AssistantMessage)
	if !ok {
		t.Fatalf("expected assistant tool item, got %T", timeline[0].Content)
	}
	call := assistant.ToolByID("call_1")
	if call == nil || call.Status != domain.ToolStatusErrored || call.Error == nil || call.Error.Code != domain.NoticeReasonProcessRestart {
		t.Fatalf("expected pending process-interrupted tool to be marked failed, got %#v", call)
	}
	deadline := time.After(2 * time.Second)
	for requests.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for auto resume provider request; state=%#v", ctrl.State())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	bodies := requestBodies.Load().([]string)
	if len(bodies) == 0 {
		t.Fatal("expected captured provider request")
	}
	if strings.Contains(bodies[0], "Session update:") || strings.Contains(bodies[0], "process was restarting") {
		t.Fatalf("expected restart continuation without interruption note, got %s", bodies[0])
	}
	chatRecord, err = testGetChat(ctx, st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chatRecord.AutoRestart {
		t.Fatalf("expected auto-restart marker to be cleared, got %#v", chatRecord)
	}
}

func TestControllerStartupNewResumesRestartInterruptedChatBeforeQueuedUserInput(t *testing.T) {
	var requestBodies atomic.Value
	requestBodies.Store([]string(nil))
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		current := requestBodies.Load().([]string)
		requestBodies.Store(append(current, string(body)))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"handled user queue"}}],"usage":{"total_tokens":1}}`))
	}))
	defer providerServer.Close()

	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: providerServer.URL + "/v1"},
	}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "model", ContextWindow: 32768})
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	workdir := t.TempDir()
	session, err := sessionpkg.CreateSession(ctx, st, "Interrupted Session", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := setSessionProjectRoot(ctx, st, session.ID, workdir); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	chatRecord, err := sessionpkg.DefaultChat(ctx, st, session.ID)
	if err != nil {
		t.Fatalf("default chat: %v", err)
	}
	chatRecord.AutoRestart = true
	if err := testUpdateChat(ctx, st, chatRecord); err != nil {
		t.Fatalf("mark auto restart: %v", err)
	}
	if err := testSetChatQueuedInputs(ctx, st, chatRecord.ID, []domain.QueuedInput{{
		ID:        id.New(),
		Kind:      domain.QueuedInputKindQueued,
		Delivery:  domain.QueuedInputDeliveryNextTurn,
		Origin:    domain.QueuedInputOriginUser,
		Text:      "run the user request",
		Source:    domain.UserMessageSourceUser,
		CreatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("queue user input: %v", err)
	}
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for len(requestBodies.Load().([]string)) < 2 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for restart continuation and queued user provider requests")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	bodies := requestBodies.Load().([]string)
	if strings.Contains(bodies[0], "process was restarting") {
		t.Fatalf("did not expect restart auto-resume note in provider request, got %s", bodies[0])
	}
	body := bodies[len(bodies)-1]
	if !strings.Contains(body, "run the user request") {
		t.Fatalf("expected queued user request in provider request, got %s", body)
	}
	timeline, err := testTimelineForChat(ctx, st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawAutoResume bool
	for _, item := range timeline {
		user, ok := item.Content.(domain.UserMessage)
		if !ok || user.Source != domain.UserMessageSourceAutoResume {
			continue
		}
		if user.Text != "Continue from where you left off." {
			t.Fatalf("unexpected auto-resume user message, got %#v", user)
		}
		sawAutoResume = true
	}
	if !sawAutoResume {
		t.Fatalf("expected visible auto-resume user message, got %#v", timeline)
	}
	chatRecord, err = testGetChat(ctx, st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chatRecord.AutoRestart {
		t.Fatalf("expected auto-restart marker to be cleared, got %#v", chatRecord)
	}
}

func TestControllerStartupMarksStaleRunningToolCallsFailed(t *testing.T) {
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	workdir := t.TempDir()
	session, err := sessionpkg.CreateSession(ctx, st, "Session", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := setSessionProjectRoot(ctx, st, session.ID, workdir); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	chatRecord, err := sessionpkg.DefaultChat(ctx, st, session.ID)
	if err != nil {
		t.Fatalf("default chat: %v", err)
	}
	if _, err := testAppendAssistantToolCalls(ctx, st, chatRecord.ID, []domain.ToolCall{{
		ToolCallID: "call_exec",
		Tool:       domain.ToolKindExecCommand,
		Args:       map[string]string{"cmd": "sleep 60"},
		Status:     domain.ToolStatusRunning,
	}}, "", domain.Usage{}); err != nil {
		t.Fatalf("append running tool call: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	timeline, err := testTimelineForChat(ctx, st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	assistant, ok := timeline[0].Content.(domain.AssistantMessage)
	if !ok {
		t.Fatalf("expected assistant tool item, got %T", timeline[0].Content)
	}
	call := assistant.ToolByID("call_exec")
	if call == nil || call.Status != domain.ToolStatusErrored || call.Error == nil || !strings.Contains(call.Error.Message, "restarted") {
		t.Fatalf("expected stale running tool to be marked failed, got %#v", call)
	}
}

func TestControllerStartupNewResumesLastWorkspaceSessionAndChat(t *testing.T) {
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	workdir := t.TempDir()
	sessionA, err := sessionpkg.CreateSession(ctx, st, "Older", "test", "model", nil)
	if err != nil {
		t.Fatalf("create older session: %v", err)
	}
	if err := setSessionProjectRoot(ctx, st, sessionA.ID, workdir); err != nil {
		t.Fatalf("workspace older: %v", err)
	}
	sessionB, err := sessionpkg.CreateSession(ctx, st, "Last Used", "test", "model", nil)
	if err != nil {
		t.Fatalf("create last session: %v", err)
	}
	if err := setSessionProjectRoot(ctx, st, sessionB.ID, workdir); err != nil {
		t.Fatalf("workspace last: %v", err)
	}
	if _, err := sessionpkg.TouchSession(ctx, st, sessionB.ID); err != nil {
		t.Fatalf("touch last session: %v", err)
	}
	defaultChat, err := sessionpkg.DefaultChat(ctx, st, sessionB.ID)
	if err != nil {
		t.Fatalf("default chat: %v", err)
	}
	if _, err := sessionpkg.CreateChat(ctx, st, sessionB.ID, "Side", chatrole.Orchestrator, nil); err != nil {
		t.Fatalf("create side chat: %v", err)
	}
	defaultChat.UpdatedAt = time.Now().UTC().Add(time.Hour)
	if err := testUpdateChat(ctx, st, defaultChat); err != nil {
		t.Fatalf("mark default chat last used: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	state := ctrl.State()
	if got := state.Session.ID; got != sessionB.ID {
		t.Fatalf("expected last session %s, got %s", sessionB.ID, got)
	}
	if got := state.ActiveChatID; got != defaultChat.ID {
		t.Fatalf("expected last chat %s, got %s", defaultChat.ID, got)
	}
}

func TestControllerSelectedChatPersistsLastUsedChat(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workdir := t.TempDir()
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	selection := controllerSelection(ctrl)
	first := selection.ChatID
	_ = newSelectedChat(t, ctrl, selection, "Side")
	if _, err := ctrl.StateForSelection(ctx, selection); err != nil {
		t.Fatalf("touch first chat: %v", err)
	}

	next := New(cfg, st, agent.New(cfg, st, nil, nil))
	if err := next.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("restart controller: %v", err)
	}
	if got := next.State().ActiveChatID; got != first {
		t.Fatalf("expected restarted controller to focus chat %s, got %s", first, got)
	}
}

func TestControllerSelectedSessionPersistsLastUsedSession(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workdir := t.TempDir()
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	first := ctrl.State().Session.ID
	if _, err := ctrl.CreateSession(ctx, "Second", workdir, false); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := ctrl.StateForSelection(ctx, Selection{SessionID: first}); err != nil {
		t.Fatalf("touch first session: %v", err)
	}

	next := New(cfg, st, agent.New(cfg, st, nil, nil))
	if err := next.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("restart controller: %v", err)
	}
	if got := next.State().Session.ID; got != first {
		t.Fatalf("expected restarted controller to resume session %s, got %s", first, got)
	}
}

func TestControllerRefreshWorkspacePublishesGitStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workdir := t.TempDir()
	runGit(t, workdir, "init")
	runGit(t, workdir, "config", "user.email", "test@example.com")
	runGit(t, workdir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(workdir, "tracked.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, workdir, "add", "tracked.txt")
	runGit(t, workdir, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(workdir, "tracked.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}

	state := ctrl.State()
	if !state.Workspace.Available {
		t.Fatalf("expected git workspace status, got %#v", state.Workspace)
	}
	if state.Workspace.Modified != 1 || state.Workspace.Untracked != 1 {
		t.Fatalf("expected modified and untracked counts, got %#v", state.Workspace)
	}

	events, unsub := ctrl.Subscribe()
	defer unsub()
	if err := os.WriteFile(filepath.Join(workdir, "tracked.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.RefreshWorkspace(ctx); err != nil {
		t.Fatalf("refresh workspace: %v", err)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Type == "workspace_delta" {
				return
			}
		case <-deadline:
			t.Fatal("expected workspace delta")
		}
	}
}

func TestControllerWorkspaceWatcherMarksStaleThenRefreshes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workdir := t.TempDir()
	runGit(t, workdir, "init")
	runGit(t, workdir, "config", "user.email", "test@example.com")
	runGit(t, workdir, "config", "user.name", "Test User")
	path := filepath.Join(workdir, "tracked.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, workdir, "add", "tracked.txt")
	runGit(t, workdir, "commit", "-m", "initial")

	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctrl := New(cfg, st, agent.New(cfg, st, nil, nil))
	ctrl.workspaceRefreshMinInterval = 500 * time.Millisecond
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	events, unsub := ctrl.Subscribe()
	defer unsub()
	ctrl.mu.Lock()
	ctrl.lastWorkspaceRefresh = time.Now()
	ctrl.mu.Unlock()

	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	staleSeen := false
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type != "workspace_delta" {
				continue
			}
			status := event.Payload.(map[string]any)["workspace_status"].(workspacepkg.Status)
			if status.Stale {
				staleSeen = true
				continue
			}
			if staleSeen && status.Modified == 1 {
				return
			}
		case <-deadline:
			t.Fatalf("expected stale and refreshed workspace deltas, staleSeen=%v state=%#v", staleSeen, ctrl.State().Workspace)
		}
	}
}

func newTestController(t *testing.T) (*Controller, *store.Store) {
	t.Helper()
	return newTestControllerWithConfig(t, nil)
}

func newTestControllerWithExec(t *testing.T) (*Controller, *store.Store, *execruntime.Manager) {
	t.Helper()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workdir := t.TempDir()
	execManager := execruntime.NewManager()
	engine := agent.New(cfg, st, nil)
	engine.SetExecManager(execManager)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(context.Background(), StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	t.Cleanup(func() { _ = ctrl.ShutdownWithCancelReason(context.Background(), chat.CancelReasonShutdownInterrupt) })
	return ctrl, st, execManager
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}

func newTestControllerWithConfig(t *testing.T, edit func(*config.Config)) (*Controller, *store.Store) {
	t.Helper()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	if edit != nil {
		edit(&cfg)
	}
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	projectRoot := t.TempDir()
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, st, engine)
	if err := ctrl.Start(context.Background(), StartupModeNew, projectRoot); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	t.Cleanup(func() { _ = ctrl.ShutdownWithCancelReason(context.Background(), chat.CancelReasonShutdownInterrupt) })
	return ctrl, st
}

func TestNewestSessionUsesUpdatedAtThenID(t *testing.T) {
	now := time.Now()
	got := newestSession([]domain.Session{
		{ID: "session-1", UpdatedAt: now},
		{ID: "session-2", UpdatedAt: now},
		{ID: "session-3", UpdatedAt: now.Add(-time.Second)},
	})
	if got.ID != "session-2" {
		t.Fatalf("expected newest session 2, got %s", got.ID)
	}
}
