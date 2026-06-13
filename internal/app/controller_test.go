package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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
	"github.com/lkarlslund/koder/internal/modeltest"
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
	return modeltest.UpdateSession(ctx, st, sessionID, func(session *domain.Session) {
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

func TestControllerStartDoesNotActivateSession(t *testing.T) {
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctrl := New(cfg, agent.New(cfg, st, nil, nil))
	events, unsub := ctrl.Subscribe()
	defer unsub()
	if err := ctrl.Start(context.Background(), StartupModeNew, t.TempDir()); err != nil {
		t.Fatalf("start controller: %v", err)
	}

	state := ctrl.State()
	if state.Session.ID != "" {
		t.Fatalf("expected no active session on startup, got %s", state.Session.ID)
	}
	if state.ActiveChatID != "" {
		t.Fatalf("expected no active chat on startup, got %s", state.ActiveChatID)
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

func TestControllerSelectionStateReflectsChatMetadataChanges(t *testing.T) {
	ctrl, _ := newTestController(t)
	ctx := context.Background()
	state := ctrl.State()
	if state.ActiveChatID == "" {
		t.Fatal("expected active chat")
	}
	owner, err := ctrl.agent.LoadSession(ctx, state.Session.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if _, _, err := owner.UpdateChat(ctx, state.ActiveChatID, chattool.UpdateRequest{Title: "Generated Chat Title"}); err != nil {
		t.Fatalf("update chat: %v", err)
	}
	got, err := ctrl.StateForSelection(ctx, Selection{SessionID: state.Session.ID, ChatID: state.ActiveChatID})
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
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
}

func TestControllerSelectionStateReflectsInactiveChatMetadataChanges(t *testing.T) {
	ctrl, _ := newTestController(t)
	ctx := context.Background()
	selection := controllerSelection(ctrl)
	first := selection.ChatID
	if first == "" {
		t.Fatal("expected first chat")
	}
	side := newSelectedChat(t, ctrl, selection, "side chat").ID
	if side == "" || side == first {
		t.Fatalf("expected side chat, first=%s side=%s", first, side)
	}
	owner, err := ctrl.agent.LoadSession(ctx, selection.SessionID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if _, _, err := owner.UpdateChat(ctx, side, chattool.UpdateRequest{Title: "Generated Side Title"}); err != nil {
		t.Fatalf("update side chat: %v", err)
	}
	got, err := ctrl.StateForSelection(ctx, selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
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
}

func TestControllerSelectedStateIncludesStartedChat(t *testing.T) {
	ctrl, _ := newTestController(t)
	ctx := context.Background()
	state := ctrl.State()
	if state.Session.ID == "" || state.ActiveChatID == "" {
		t.Fatal("expected active session and chat")
	}
	if _, err := ctrl.SetMilestonePlan(ctx, state.Session.ID, "Ship it", []planning.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusReady, Position: 0},
	}); err != nil {
		t.Fatalf("set milestone plan: %v", err)
	}
	tasks, err := ctrl.AddTasks(ctx, state.Session.ID, "M001", []string{"Implement alpha"})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}

	status, err := ctrl.StartChat(ctx, state.Session.ID, state.ActiveChatID, chattool.StartRequest{
		Profile:   chatrole.Execution,
		Objective: "Implement only the assigned task",
		TaskRef:   id.ID(planning.TaskKey(tasks[0])),
	})
	if err != nil {
		t.Fatalf("start chat: %v", err)
	}

	next, err := ctrl.StateForSelection(ctx, Selection{SessionID: state.Session.ID, ChatID: state.ActiveChatID})
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	found := false
	for _, item := range next.Chats {
		if item.ID == status.ID && item.ActiveMilestoneRef == "M001" && item.AssignedTaskRef == planning.TaskKey(tasks[0]) {
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
		fmt.Fprint(w, `{"data":[{"id":"z-model","owned_by":"remote","max_model_len":65536},{"id":"a-model","status":{"args":["llama-server","--ctx-size","49152"]}}]}`)
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
	if custom.ContextWindow != 65536 {
		t.Fatalf("expected custom context window 65536, got %#v", custom)
	}
	if options[1].Editable || !options[1].Detected {
		t.Fatalf("expected detected model to be read-only, got %#v", options[1])
	}
	if options[1].ContextWindow != 49152 || options[2].ContextWindow != 65536 {
		t.Fatalf("expected detected model context windows, got %#v", options)
	}
}

func TestControllerModelOptionsSignalsTTSOnlyModel(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"omnivoice-base-Q8_0.gguf","owned_by":"local"}]}`)
		case "/v1/audio/speech":
			w.Header().Set("Content-Type", "audio/pcm")
			_, _ = w.Write([]byte{0, 1, 2, 3})
		case "/v1/chat/completions":
			http.NotFound(w, r)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer modelServer.Close()

	ctrl, _ := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"local-tts": {Name: "Local TTS", BaseURL: modelServer.URL + "/v1", Timeout: time.Second},
		}
	})

	options, err := ctrl.ModelOptions(context.Background())
	if err != nil {
		t.Fatalf("model options: %v", err)
	}
	if len(options) != 1 {
		t.Fatalf("expected one model option, got %#v", options)
	}
	if !options[0].SupportsTTS || options[0].SupportsChat {
		t.Fatalf("expected tts-only model option, got %#v", options[0])
	}

	speech, err := ctrl.SynthesizeSpeech(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("synthesize speech: %v", err)
	}
	if speech.ProviderID != "local-tts" || speech.ModelID != "omnivoice-base-Q8_0.gguf" || speech.ContentType != "audio/wav" {
		t.Fatalf("unexpected speech metadata: %#v", speech)
	}
	if len(speech.Audio) <= 44 || string(speech.Audio[:4]) != "RIFF" || string(speech.Audio[8:12]) != "WAVE" {
		t.Fatalf("unexpected speech audio: %#v", speech.Audio)
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
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
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
	ctrl := New(cfg, engine)
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
	if loaded.MaxToolLoopSteps != 77 || loaded.UI.Theme != "dark" || loaded.Compaction.ModelID != "compact-model" {
		t.Fatalf("expected saved config, got max=%d theme=%q compact=%q/%q", loaded.MaxToolLoopSteps, loaded.UI.Theme, loaded.Compaction.ProviderID, loaded.Compaction.ModelID)
	}
	if !loaded.Thinking.CavemanEnabled || loaded.Thinking.CavemanProviderID != "test" || loaded.Thinking.CavemanModelID != "model" || loaded.Thinking.CavemanPrompt != "rewrite thinking:\n{{thinking}}" || loaded.Thinking.CavemanMinTokens != 96 {
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
	restarted := New(loaded, agent.New(loaded, st, nil, nil))
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
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "old-model"
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
	ctrl := New(cfg, agent.New(cfg, st, nil, nil))
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
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "old-model"
	cfg.Providers = map[string]config.Provider{"test": {Name: "Test", BaseURL: modelServer.URL + "/v1"}}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "old-model", ContextWindow: 32768})
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctrl := New(cfg, agent.New(cfg, st, nil, nil))
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

func TestControllerSetModelRefreshesDetectedContextWindow(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			if got := r.URL.Query().Get("model"); got != "live-model" {
				t.Fatalf("unexpected model query: %q", got)
			}
			_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":65536}}`))
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"live-model","status":{"args":["llama-server","--ctx-size","131072"]}}]}`))
		default:
			t.Fatalf("unexpected model server path: %s", r.URL.Path)
		}
	}))
	defer modelServer.Close()

	ctrl, _ := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Kind: provider.ProviderKindCompatible, BaseURL: modelServer.URL + "/v1", Timeout: time.Second},
		}
		cfg.Defaults.ProviderID = "test"
		cfg.Defaults.ModelID = "live-model"
		cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "live-model", ContextWindow: 131072})
	})
	ctx := context.Background()
	selection := controllerSelection(ctrl)
	if err := ctrl.SetModelForSelection(ctx, selection, "test", "live-model"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	state, err := ctrl.StateForSelection(ctx, selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	if state.ContextWindow != 65536 || state.ModelInfo.ContextWindow != 65536 {
		t.Fatalf("expected refreshed live context window 65536, got state=%d info=%#v", state.ContextWindow, state.ModelInfo)
	}
}

func TestControllerStartDetectsActiveModelContextWindow(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			http.NotFound(w, r)
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
	session, err := modeltest.GetSession(ctx, st, sessionID)
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
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctrl := New(cfg, agent.New(cfg, st, nil, nil))
	if err := ctrl.Start(context.Background(), StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	activateTestSession(t, ctrl, workdir)
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
	next := New(cfg, engine)
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
	state, err := ctrl.StateForSelection(ctx, selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	rt.SetSession(state.Session)
	state, err = ctrl.StateForSelection(ctx, selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	if got := state.Access.Settings.Network; got {
		t.Fatalf("expected runtime update to preserve network disabled")
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
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	sessionA, err := modeltest.CreateSession(ctx, st, "Workspace A", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session a: %v", err)
	}
	if err := setSessionProjectRoot(ctx, st, sessionA.ID, workspaceA); err != nil {
		t.Fatalf("workspace a: %v", err)
	}
	sessionB, err := modeltest.CreateSession(ctx, st, "Workspace B", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session b: %v", err)
	}
	if err := setSessionProjectRoot(ctx, st, sessionB.ID, workspaceB); err != nil {
		t.Fatalf("workspace b: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, engine)
	if err := ctrl.Start(ctx, StartupModeResume, workspaceA); err != nil {
		t.Fatalf("start resume: %v", err)
	}
	if got := ctrl.State().Session.ID; got != "" {
		t.Fatalf("expected no active session at startup, got %s", got)
	}
	if _, err := ctrl.StateForSelection(ctx, Selection{SessionID: sessionB.ID}); err != nil {
		t.Fatalf("state for workspace b session: %v", err)
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

func TestControllerSelectedChatPersistsLastUsedChat(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workdir := t.TempDir()
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	activateTestSession(t, ctrl, workdir)
	selection := controllerSelection(ctrl)
	first := selection.ChatID
	_ = newSelectedChat(t, ctrl, selection, "Side")
	if _, err := ctrl.StateForSelection(ctx, selection); err != nil {
		t.Fatalf("touch first chat: %v", err)
	}

	next := New(cfg, agent.New(cfg, st, nil, nil))
	if err := next.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("restart controller: %v", err)
	}
	state, err := next.StateForSelection(ctx, Selection{SessionID: selection.SessionID})
	if err != nil {
		t.Fatalf("state after restart: %v", err)
	}
	if got := state.ActiveChatID; got != first {
		t.Fatalf("expected restarted controller to focus chat %s, got %s", first, got)
	}
}

func TestControllerSelectedSessionPersistsLastUsedSession(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workdir := t.TempDir()
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, engine)
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	first := activateTestSession(t, ctrl, workdir).ID
	if _, err := ctrl.CreateSession(ctx, "Second", workdir, false); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := ctrl.StateForSelection(ctx, Selection{SessionID: first}); err != nil {
		t.Fatalf("touch first session: %v", err)
	}

	next := New(cfg, agent.New(cfg, st, nil, nil))
	if err := next.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("restart controller: %v", err)
	}
	if _, err := next.StateForSelection(ctx, Selection{SessionID: first}); err != nil {
		t.Fatalf("state after restart: %v", err)
	}
	if got := next.State().Session.ID; got != "" {
		t.Fatalf("expected restarted controller to avoid startup session activation, got %s", got)
	}
	if got := first; got == "" {
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
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	engine := agent.New(cfg, st, nil, nil)
	ctrl := New(cfg, engine)
	t.Cleanup(func() { _ = ctrl.ShutdownWithCancelReason(context.Background(), chat.CancelReasonShutdownInterrupt) })
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	session := activateTestSession(t, ctrl, workdir)
	if _, err := ctrl.RefreshWorkspaceForSelection(ctx, Selection{SessionID: session.ID}); err != nil {
		t.Fatalf("refresh workspace: %v", err)
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
	if _, err := ctrl.RefreshWorkspaceForSelection(ctx, Selection{SessionID: session.ID}); err != nil {
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

func TestControllerRefreshWorkspaceUsesSelectedSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	for _, workdir := range []string{workspaceA, workspaceB} {
		runGit(t, workdir, "init")
		runGit(t, workdir, "config", "user.email", "test@example.com")
		runGit(t, workdir, "config", "user.name", "Test User")
		if err := os.WriteFile(filepath.Join(workdir, "tracked.txt"), []byte("one\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, workdir, "add", "tracked.txt")
		runGit(t, workdir, "commit", "-m", "initial")
	}
	if err := os.WriteFile(filepath.Join(workspaceA, "tracked.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctrl := New(cfg, agent.New(cfg, st, nil, nil))
	t.Cleanup(func() { _ = ctrl.ShutdownWithCancelReason(context.Background(), chat.CancelReasonShutdownInterrupt) })
	if err := ctrl.Start(ctx, StartupModeNew, workspaceA); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	sessionA := activateTestSession(t, ctrl, workspaceA)
	sessionB := activateTestSession(t, ctrl, workspaceB)
	if ctrl.State().Session.ID != sessionB.ID {
		t.Fatalf("expected controller mirror to point at session b")
	}

	if _, err := ctrl.RefreshWorkspaceForSelection(ctx, Selection{SessionID: sessionA.ID}); err != nil {
		t.Fatalf("refresh selected workspace: %v", err)
	}
	state, err := ctrl.StateForSelection(ctx, Selection{SessionID: sessionA.ID})
	if err != nil {
		t.Fatalf("state for session a: %v", err)
	}
	if !state.Workspace.Available || state.Workspace.ProjectRoot != workspaceA || state.Workspace.Modified != 1 {
		t.Fatalf("expected session a git status, got %#v", state.Workspace)
	}
}

func TestControllerStartDoesNotWaitForWorkspaceSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workdir := t.TempDir()

	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctrl := New(cfg, agent.New(cfg, st, nil, nil))

	snapshotStarted := make(chan struct{})
	releaseSnapshot := make(chan struct{})
	ctrl.workspaceSnapshot = func(_ context.Context, projectRoot string) (workspacepkg.Status, error) {
		select {
		case <-snapshotStarted:
		default:
			close(snapshotStarted)
		}
		<-releaseSnapshot
		return workspacepkg.Status{Available: true, ProjectRoot: projectRoot, RefreshedAt: time.Now().UTC()}, nil
	}

	startDone := make(chan error, 1)
	go func() {
		startDone <- ctrl.Start(ctx, StartupModeNew, workdir)
	}()
	select {
	case err := <-startDone:
		if err != nil {
			t.Fatalf("start controller: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("controller start waited for workspace snapshot")
	}
	select {
	case <-snapshotStarted:
		t.Fatal("workspace snapshot should not start until a session is activated")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseSnapshot)
}

func TestControllerWorkspaceWatcherRefreshesChangedStatus(t *testing.T) {
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
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctrl := New(cfg, agent.New(cfg, st, nil, nil))
	ctrl.workspaceRefreshMinInterval = 500 * time.Millisecond
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	session := activateTestSession(t, ctrl, workdir)
	if _, err := ctrl.RefreshWorkspaceForSelection(ctx, Selection{SessionID: session.ID}); err != nil {
		t.Fatalf("initial refresh workspace: %v", err)
	}
	events, unsub := ctrl.Subscribe()
	defer unsub()

	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type != "workspace_delta" {
				continue
			}
			status := event.Payload.(map[string]any)["workspace_status"].(workspacepkg.Status)
			if !status.Stale && status.Modified == 1 {
				return
			}
		case <-deadline:
			t.Fatalf("expected refreshed workspace delta, state=%#v", ctrl.State().Workspace)
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
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workdir := t.TempDir()
	execManager := execruntime.NewManager()
	engine := agent.New(cfg, st, nil)
	engine.SetExecManager(execManager)
	ctrl := New(cfg, engine)
	if err := ctrl.Start(context.Background(), StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	activateTestSession(t, ctrl, workdir)
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
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
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
	ctrl := New(cfg, engine)
	if err := ctrl.Start(context.Background(), StartupModeNew, projectRoot); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	activateTestSession(t, ctrl, projectRoot)
	t.Cleanup(func() { _ = ctrl.ShutdownWithCancelReason(context.Background(), chat.CancelReasonShutdownInterrupt) })
	return ctrl, st
}

func activateTestSession(t *testing.T, ctrl *Controller, projectRoot string) domain.Session {
	t.Helper()
	session, err := ctrl.CreateSession(context.Background(), "New Session", projectRoot, false)
	if err != nil {
		t.Fatalf("create test session: %v", err)
	}
	if err := ctrl.loadSession(context.Background(), session.ID, ""); err != nil {
		t.Fatalf("activate test session: %v", err)
	}
	return session
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
