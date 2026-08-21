package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/mcp"
	"github.com/lkarlslund/koder/internal/modeloverlay"
	"github.com/lkarlslund/koder/internal/modeltest"
	"github.com/lkarlslund/koder/internal/phonedevice"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/provider"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools/chattool"
	"github.com/lkarlslund/koder/internal/voice"
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

func TestMCPRuntimeStateReflectsManagerSnapshots(t *testing.T) {
	states := mcpRuntimeStateFromServers([]mcp.ServerState{
		{ID: "zeta", Status: mcp.ServerStatusError, LastError: "connection refused"},
		{ID: "alpha", Status: mcp.ServerStatusConnected, SessionID: "session-1", ToolCount: 3, ResourceCount: 2, ResourceTemplateCount: 1, PromptCount: 4},
	})
	if len(states) != 2 || states[0].ID != "alpha" || states[0].Status != "connected" || states[0].ToolCount != 3 || states[0].ResourceTemplateCount != 1 {
		t.Fatalf("unexpected connected MCP runtime state: %#v", states)
	}
	if states[1].ID != "zeta" || states[1].Status != "error" || states[1].LastError != "connection refused" {
		t.Fatalf("unexpected failed MCP runtime state: %#v", states[1])
	}
}

func TestPreferencesSerializeBrowserRuntimeSeparately(t *testing.T) {
	state := PreferencesState{
		Browser:        NativeBrowserPreferences{Enabled: true, OperationTimeout: 30, MaxTabsPerChat: 8, MaxTabsGlobal: 32},
		BrowserRuntime: browserapi.Status{State: "ready"},
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(payload, &root); err != nil {
		t.Fatal(err)
	}
	var browser map[string]any
	if err := json.Unmarshal(root["browser"], &browser); err != nil {
		t.Fatal(err)
	}
	if _, exists := browser["status"]; exists {
		t.Fatalf("browser runtime leaked into editable preferences: %s", payload)
	}
	var runtime browserapi.Status
	if err := json.Unmarshal(root["browser_runtime"], &runtime); err != nil || runtime.State != "ready" {
		t.Fatalf("missing separate browser runtime state: runtime=%#v err=%v payload=%s", runtime, err, payload)
	}
}

func TestProviderDraftCannotOverwritePromptProgressObservation(t *testing.T) {
	checkedAt := time.Now().UTC()
	next := config.WithPromptProgressObservation(config.Provider{Kind: "openai-compatible", BaseURL: "http://provider.test/v1", PromptProgressMode: "auto"}, true, checkedAt)
	applyProviderDraftPreferences(&next, ProviderDraft{PromptProgressMode: "auto", PromptProgressProbed: false, PromptProgressSupported: false})
	if !config.PromptProgressObservationValid(next) || !next.PromptProgressSupported || !next.PromptProgressCheckedAt.Equal(checkedAt) {
		t.Fatalf("editable provider draft overwrote runtime observation: %#v", next)
	}
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

func setSessionProjectRoot(ctx context.Context, st *store.Store, sessionID id.ID, root string) error {
	return modeltest.UpdateSession(ctx, st, sessionID, func(session *domain.Session) {
		session.ProjectRoot = root
	})
}

func controllerSelection(ctrl *Controller) Selection {
	ctx := context.Background()
	sessions, err := ctrl.Sessions(ctx)
	if err != nil || len(sessions.Sessions) == 0 {
		return Selection{}
	}
	session := newestSession(sessions.Sessions)
	state, err := ctrl.StateForSelection(ctx, Selection{SessionID: session.ID})
	if err != nil {
		return Selection{SessionID: session.ID}
	}
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

func eventStateProjectRoot(t *testing.T, ctrl *Controller, selection Selection) string {
	t.Helper()
	state, err := ctrl.StateForSelection(context.Background(), selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	return state.Session.ProjectRoot
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

func TestChatBackendsExposeConfiguredCreationPolicy(t *testing.T) {
	cfg := config.Default()
	cfg.Permissions.Profiles["careful"] = config.PermissionProfile{Network: false, Root: "readonly", Workspace: "readonly"}
	controller := &Controller{cfg: cfg}
	backends := controller.ChatBackends(context.Background())
	if len(backends) != 2 {
		t.Fatalf("expected two backend options, got %#v", backends)
	}
	var foundProfile bool
	for _, profile := range backends[0].PermissionProfiles {
		if profile.ID == "careful" && profile.Description != "" {
			foundProfile = true
		}
	}
	if !foundProfile {
		t.Fatalf("custom permission profile missing from backend: %#v", backends[0].PermissionProfiles)
	}
	if len(backends[1].AdditionalTools) < 10 {
		t.Fatalf("expected complete Codex addition catalog, got %#v", backends[1].AdditionalTools)
	}
	for _, tool := range backends[1].AdditionalTools {
		if tool.ID == "" || tool.Label == "" {
			t.Fatalf("tool option lacks usable metadata: %#v", tool)
		}
	}
}

func TestControllerSelectedStateHydratesAndKicksStoredChat(t *testing.T) {
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	first := New(cfg, agent.New(cfg, st, nil, nil))
	if err := first.Start(context.Background(), StartupModeNew, t.TempDir()); err != nil {
		t.Fatalf("start first controller: %v", err)
	}
	session := activateTestSession(t, first, t.TempDir())
	selection := controllerSelection(first)
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown first controller: %v", err)
	}
	if err := testSetChatQueuedInputs(context.Background(), st, selection.ChatID, []domain.QueuedInput{{
		ID:       id.New(),
		Kind:     domain.QueuedInputKindContinue,
		Delivery: domain.QueuedInputDeliveryContinue,
		Origin:   domain.QueuedInputOriginAutoResume,
		Source:   domain.UserMessageSourceAutoResume,
	}}); err != nil {
		t.Fatalf("persist queued continuation: %v", err)
	}

	engine := agent.New(cfg, st, nil, nil)
	next := New(cfg, engine)
	if err := next.Start(context.Background(), StartupModeNew, session.ProjectRoot); err != nil {
		t.Fatalf("start next controller: %v", err)
	}
	t.Cleanup(func() { _ = next.Shutdown(context.Background()) })
	owner, err := engine.LoadSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("load session owner: %v", err)
	}
	if got := len(owner.Snapshot().Snapshots); got != 0 {
		t.Fatalf("expected stored chats before browser selection, got %d live runtimes", got)
	}

	if _, err := next.StateForSelection(context.Background(), selection); err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		snapshot, ok := owner.Snapshot().Snapshots[selection.ChatID]
		if ok && snapshot.TimelineLoadedAll {
			return
		}
		select {
		case <-deadline:
			t.Fatal("selected stored chat was not hydrated and kicked")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestControllerStateIncludesCurrentChatExecProcesses(t *testing.T) {
	ctrl, _, execManager := newTestControllerWithExec(t)
	selection := controllerSelection(ctrl)
	state, err := ctrl.StateForSelection(context.Background(), selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	events, unsub := ctrl.Subscribe()
	defer unsub()
	snap, err := execManager.Start(context.Background(), execruntime.StartRequest{
		SessionID: selection.SessionID,
		ChatID:    selection.ChatID,
		Command:   "printf hi",
		Workdir:   state.Session.ProjectRoot,
		YieldTime: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start exec: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		selectedState, err := ctrl.StateForSelection(context.Background(), selection)
		if err != nil {
			t.Fatalf("state for selection: %v", err)
		}
		current := selectedState.Snapshots[selection.ChatID].ExecProcesses
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
		Workdir:   eventStateProjectRoot(t, ctrl, selection),
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

func TestControllerHardStopCancelsActiveProviderRequest(t *testing.T) {
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	recorder := debugsrv.NewRecorder()
	engine := agent.New(cfg, st, recorder, nil)
	ctrl := New(cfg, engine)
	projectRoot := t.TempDir()
	if err := ctrl.Start(context.Background(), StartupModeNew, projectRoot); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	activateTestSession(t, ctrl, projectRoot)
	t.Cleanup(func() { _ = ctrl.ShutdownWithCancelReason(context.Background(), chat.CancelReasonShutdownInterrupt) })

	selection := controllerSelection(ctrl)
	activeCtx, _, finish := recorder.StartHTTP(context.Background(), debugsrv.HTTPTrace{
		SessionID: selection.SessionID,
		ChatID:    selection.ChatID,
		Method:    "POST",
		Path:      "/chat/completions",
	})
	defer finish()

	if err := ctrl.StopForSelection(context.Background(), selection); err != nil {
		t.Fatalf("stop selection: %v", err)
	}
	select {
	case <-activeCtx.Done():
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected hard stop to cancel active provider request")
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
	if err := ctrl.ArchiveChatForSelection(ctx, selection, side); err != nil {
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
	if err := ctrl.ArchiveChatForSelection(ctx, Selection{SessionID: selection.SessionID, ChatID: side}, side); err != nil {
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
	selection := controllerSelection(ctrl)
	state, err := ctrl.StateForSelection(ctx, selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
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
	selection := controllerSelection(ctrl)
	state, err := ctrl.StateForSelection(ctx, selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	if state.Session.ID == "" || state.ActiveChatID == "" {
		t.Fatal("expected active session and chat")
	}
	if _, err := ctrl.SetMilestonePlan(ctx, state.Session.ID, "Ship it", []planning.Milestone{
		{Key: "M001", Title: "Alpha", Status: planning.MilestoneStatusReady, Position: 0},
	}); err != nil {
		t.Fatalf("set milestone plan: %v", err)
	}
	if _, err := ctrl.AddTasks(ctx, state.Session.ID, "M001", []string{"Implement alpha"}); err != nil {
		t.Fatalf("add task: %v", err)
	}

	status, err := ctrl.StartChat(ctx, state.Session.ID, state.ActiveChatID, chattool.StartRequest{
		Profile:      chatrole.Execution,
		Objective:    "Implement the milestone",
		MilestoneKey: "M001",
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
		if item.ID == status.ID && item.ActiveMilestoneKey == "M001" && item.AssignedTaskRef == "" {
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
		if r.URL.Path == "/slots" || r.URL.Path == "/props" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"data":[{"id":"z-model","owned_by":"remote","max_model_len":65536,"architecture":{"input_modalities":["text","image","pdf"]},"supported_parameters":["tools","structured_outputs","reasoning"]},{"id":"a-model","status":{"args":["llama-server","--ctx-size","49152"]}}]}`)
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

	options, err := ctrl.ModelOptionsForSelection(context.Background(), Selection{})
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
	if custom.Health.Status != "healthy" || custom.Health.CheckedAt == nil || custom.Health.CheckedAt.IsZero() {
		t.Fatalf("expected custom model runtime health from backing model, got %#v", custom.Health)
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
	if !options[2].SupportsTools || !options[2].SupportsImages || !options[2].SupportsPDFs || !options[2].SupportsJSON || !options[2].SupportsReasoning || !options[2].CapabilitiesKnown {
		t.Fatalf("expected detected model capabilities, got %#v", options[2])
	}
	if !custom.SupportsTools || !custom.SupportsImages || !custom.SupportsPDFs || !custom.SupportsJSON || !custom.SupportsReasoning || !custom.CapabilitiesKnown {
		t.Fatalf("expected custom model to inherit source capabilities, got %#v", custom)
	}
	providerState := ctrl.Providers()
	if len(providerState.Providers) != 1 || providerState.Providers[0].Health.Status != "healthy" || providerState.Providers[0].Health.ModelCount != 2 || providerState.Providers[0].Health.CheckedAt == nil || providerState.Providers[0].Health.CheckedAt.IsZero() {
		t.Fatalf("expected provider runtime health after discovery, got %#v", providerState.Providers)
	}
}

func TestValidateChatModelAvailableReturnsRecoverableErrorForRemovedModel(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[{"id":"current-model","supported_parameters":["tools"]}]}`)
	}))
	defer modelServer.Close()
	cfg := config.Default()
	cfg.Providers = map[string]config.Provider{"test": {Name: "Test Provider", BaseURL: modelServer.URL + "/v1"}}
	ctrl := &Controller{cfg: cfg, providerHealth: provider.NewHealthTracker()}
	err := ctrl.validateChatModelAvailable(context.Background(), domain.Chat{Backend: domain.ChatBackendKoder, ProviderID: "test", ModelID: "removed-model"})
	var unavailable *ModelUnavailableError
	if !errors.As(err, &unavailable) || unavailable.ClientErrorCode() != "model_unavailable" {
		t.Fatalf("expected recoverable unavailable-model error, got %v", err)
	}
}

func TestMissingCustomModelBackingIsVisibleButNeverSelectable(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slots", "/props":
			http.NotFound(w, r)
		case "/models", "/v1/models":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"live-model"}]}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer modelServer.Close()

	ctrl, _ := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Name: "Test Provider", BaseURL: modelServer.URL + "/v1", Timeout: time.Second},
		}
		cfg.Defaults.ProviderID = "test"
		cfg.Defaults.ModelID = "missing alias"
		cfg.SetModelConfig(config.ModelConfig{
			ProviderID:       "test",
			ModelID:          "missing alias",
			SourceProviderID: "test",
			SourceModelID:    "removed-provider-model",
		})
	})

	options, err := ctrl.ModelOptionsForSelection(context.Background(), Selection{})
	if err != nil {
		t.Fatalf("model options: %v", err)
	}
	var missingOption *ModelOption
	for idx := range options {
		if options[idx].ProviderID == "test" && options[idx].ModelID == "missing alias" {
			missingOption = &options[idx]
			break
		}
	}
	if missingOption == nil || !missingOption.Custom || missingOption.BackingDetected {
		t.Fatalf("expected settings catalog to retain missing custom model, got %#v", options)
	}

	backends := ctrl.ChatBackends(context.Background())
	var koder ChatBackendState
	for _, backend := range backends {
		if backend.ID == domain.ChatBackendKoder {
			koder = backend
			break
		}
	}
	if len(koder.Models) != 1 || koder.Models[0].ID != "live-model" || !koder.Models[0].Default {
		t.Fatalf("expected only live fallback model in chat picker, got models=%#v catalog=%#v detail=%q", koder.Models, options, koder.Detail)
	}

	err = ctrl.SetModelForSelection(context.Background(), controllerSelection(ctrl), "test", "missing alias")
	var unavailable *ModelUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("expected missing custom backing to be rejected as unavailable, got %v", err)
	}
	if _, err := ctrl.SetDefaultModel(context.Background(), "test", "missing alias"); err == nil || !strings.Contains(err.Error(), "not currently available") {
		t.Fatalf("expected missing custom backing to be rejected as default, got %v", err)
	}
}

func TestControllerSetDefaultAndDeleteCustomModelConfig(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slots" || r.URL.Path == "/props" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"data":[{"id":"base-model"}]}`)
	}))
	defer modelServer.Close()

	ctrl, _ := newPersistentTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Name: "Test Provider", BaseURL: modelServer.URL + "/v1"},
		}
		cfg.Defaults.ProviderID = "test"
		cfg.Defaults.ModelID = "base-model"
		cfg.SetModelConfig(config.ModelConfig{
			ProviderID:       "test",
			ModelID:          "base-model custom",
			SourceProviderID: "test",
			SourceModelID:    "base-model",
			ContextWindow:    65536,
		})
		cfg.UI.TTS.ProviderID = "test"
		cfg.UI.TTS.ModelID = "base-model custom"
		cfg.Compaction.ProviderID = "test"
		cfg.Compaction.ModelID = "base-model custom"
		cfg.Thinking.CavemanProviderID = "test"
		cfg.Thinking.CavemanModelID = "base-model custom"
	})

	prefs, err := ctrl.SetDefaultModel(context.Background(), "test", "base-model custom")
	if err != nil {
		t.Fatalf("set default model: %v", err)
	}
	if prefs.General.DefaultProvider != "test" || prefs.General.DefaultModel != "base-model custom" {
		t.Fatalf("expected custom default, got %#v", prefs.General)
	}
	options, err := ctrl.ModelOptionsForSelection(context.Background(), Selection{})
	if err != nil {
		t.Fatalf("model options: %v", err)
	}
	var customDefault bool
	for _, option := range options {
		if option.ProviderID == "test" && option.ModelID == "base-model custom" {
			customDefault = option.Default
		}
	}
	if !customDefault {
		t.Fatalf("expected custom model option to be marked default: %#v", options)
	}

	prefs, err = ctrl.DeleteModelConfig(context.Background(), "test", "base-model custom")
	if err != nil {
		t.Fatalf("delete custom model: %v", err)
	}
	if prefs.General.DefaultProvider != "test" || prefs.General.DefaultModel != "base-model" {
		t.Fatalf("expected default to fall back to base model, got %#v", prefs.General)
	}
	if prefs.UI.TTS.ProviderID != "" || prefs.UI.TTS.ModelID != "" {
		t.Fatalf("expected tts custom model reference to clear, got %#v", prefs.UI.TTS)
	}
	if !prefs.Compaction.UseChatModel || prefs.Compaction.ProviderID != "" || prefs.Compaction.ModelID != "" {
		t.Fatalf("expected compaction custom model reference to clear to chat model, got %#v", prefs.Compaction)
	}
	if !prefs.Thinking.UseChatModel || prefs.Thinking.ProviderID != "" || prefs.Thinking.ModelID != "" {
		t.Fatalf("expected thinking custom model reference to clear to chat model, got %#v", prefs.Thinking)
	}
	if len(prefs.ModelConfigs) != 0 {
		t.Fatalf("expected custom model config to be deleted, got %#v", prefs.ModelConfigs)
	}
}

func TestControllerSaveModelConfigValidatesResolvedOverlayOptions(t *testing.T) {
	ctrl, _ := newPersistentTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"llamacpp": {Name: "llama.cpp", BaseURL: "http://127.0.0.1:8000/v1"},
		}
	})
	pref := ModelConfigPreference{
		ProviderID:       "llamacpp",
		ModelID:          "qwen custom",
		SourceProviderID: "llamacpp",
		SourceModelID:    "qwen3.8-27b-q8-mtp",
		ContextWindow:    524288,
		ModelPreset:      "auto",
		Options: map[string]any{
			"reasoning_effort": "medium",
			"temperature":      1.0,
		},
	}

	saved, err := ctrl.SaveModelConfig(context.Background(), pref)
	if err != nil {
		t.Fatalf("save valid overlay settings: %v", err)
	}
	if saved.ResolvedOverlay.Title != "Qwen3.8" || saved.Options["reasoning_effort"] != "medium" || saved.ModelOverlays == nil {
		t.Fatalf("expected resolved Qwen3.8 overlay metadata, got %#v", saved)
	}
	stored, ok := ctrl.cfg.ModelConfig("llamacpp", "qwen custom")
	if !ok || stored.Options["reasoning_effort"] != "medium" {
		t.Fatalf("expected arbitrary overlay options to persist, got %#v", stored)
	}
	reloaded, err := config.LoadWithOptions(config.LoadOptions{DataDir: filepath.Dir(ctrl.cfg.Path())})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	stored, ok = reloaded.ModelConfig("llamacpp", "qwen custom")
	if !ok || stored.Options["reasoning_effort"] != "medium" || stored.Options["temperature"] != 1.0 {
		t.Fatalf("expected overlay options to survive config reload, got %#v", stored)
	}

	pref.Options["reasoning_effort"] = "high"
	if _, err := ctrl.SaveModelConfig(context.Background(), pref); err == nil || !strings.Contains(err.Error(), "unsupported value high") {
		t.Fatalf("expected unsupported model option to be rejected, got %v", err)
	}
}

func TestControllerSaveModelConfigRemovesLegacyFieldsShadowedByOverlayOptions(t *testing.T) {
	ctrl, _ := newPersistentTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"llamacpp": {Name: "llama.cpp", BaseURL: "http://127.0.0.1:8000/v1"},
		}
	})
	legacyTemperature := 1.0
	pref := ModelConfigPreference{
		ProviderID:       "llamacpp",
		ModelID:          "qwen custom",
		SourceProviderID: "llamacpp",
		SourceModelID:    "qwen3.8-27b-q8-mtp",
		ContextWindow:    262144,
		ModelPreset:      "qwen3.8",
		Temperature:      &legacyTemperature,
		ThinkingMode:     "enabled",
		ReasoningEffort:  "medium",
		Options: map[string]any{
			"temperature":      0.8,
			"thinking_mode":    "disabled",
			"reasoning_effort": "none",
		},
	}

	if _, err := ctrl.SaveModelConfig(context.Background(), pref); err != nil {
		t.Fatal(err)
	}
	stored, ok := ctrl.cfg.ModelConfig("llamacpp", "qwen custom")
	if !ok {
		t.Fatal("saved model is missing")
	}
	if stored.Temperature != nil || stored.ThinkingMode != "auto" || stored.ReasoningEffort != "" {
		t.Fatalf("shadowed legacy fields were retained: %#v", stored)
	}
	values := provider.ModelOptionValues(stored)
	if values["temperature"] != 0.8 || values["thinking_mode"] != "disabled" || values["reasoning_effort"] != "none" {
		t.Fatalf("canonical overlay values changed: %#v", values)
	}
	body := provider.RequestExtraBody(ctrl.cfg.Providers["llamacpp"], stored, modeloverlay.Load(ctrl.cfg.ManagedAssetsDir()))
	if body["temperature"] != 0.8 || body["reasoning_effort"] != "none" {
		t.Fatalf("request does not use canonical overlay values: %#v", body)
	}
	kwargs, ok := body["chat_template_kwargs"].(map[string]any)
	if !ok || kwargs["enable_thinking"] != false {
		t.Fatalf("request did not disable thinking: %#v", body)
	}
}

func TestControllerDeleteModelConfigRejectsNonCustomModel(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slots" || r.URL.Path == "/props" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"data":[{"id":"base-model"}]}`)
	}))
	defer modelServer.Close()

	ctrl, _ := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Name: "Test Provider", BaseURL: modelServer.URL + "/v1"},
		}
		cfg.SetModelConfig(config.ModelConfig{
			ProviderID:    "test",
			ModelID:       "base-model",
			ContextWindow: 65536,
		})
	})

	if _, err := ctrl.DeleteModelConfig(context.Background(), "test", "base-model"); err == nil || !strings.Contains(err.Error(), "only custom") {
		t.Fatalf("expected non-custom delete to fail, got %v", err)
	}
}

func TestControllerModelOptionsSignalsTTSOnlyModel(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"omnivoice-base-Q8_0.gguf","owned_by":"local"}]}`)
		case "/v1/audio/speech":
			w.Header().Set("Content-Type", "audio/pcm")
			_, _ = w.Write([]byte{0, 1, 2, 3})
		case "/v1/chat/completions":
			http.NotFound(w, r)
		case "/slots", "/props":
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

	options, err := ctrl.ModelOptionsForSelection(context.Background(), Selection{})
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
		if r.URL.Path == "/slots" || r.URL.Path == "/props" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"data":[{"id":"live-model"}]}`)
	}))
	defer modelServer.Close()

	ctrl, st := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Name: "Test Provider", BaseURL: modelServer.URL + "/v1"},
		}
	})
	_ = st

	options, err := ctrl.ModelOptionsForSelection(context.Background(), Selection{})
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

	_, err := ctrl.ModelOptionsForSelection(context.Background(), Selection{})
	if err == nil || !strings.Contains(err.Error(), "failed to load models from test") {
		t.Fatalf("expected provider failure, got %v", err)
	}
	providerState := ctrl.Providers()
	if len(providerState.Providers) != 1 || providerState.Providers[0].Health.Status != "unhealthy" || providerState.Providers[0].Health.CheckedAt == nil || providerState.Providers[0].Health.CheckedAt.IsZero() || !strings.Contains(providerState.Providers[0].Health.Detail, "list models") {
		t.Fatalf("expected provider runtime failure, got %#v", providerState.Providers)
	}
}

func TestProviderProbeSummaryReportsAggregateOfferings(t *testing.T) {
	models := []domain.Model{
		{ID: "chat", ContextWindow: 65536, SupportsChat: true, SupportsTools: true, SupportsImages: true, SupportsJSON: true, CapabilitiesKnown: true},
		{ID: "speech", MaxContextWindow: 131072, SupportsSTT: true, SupportsTTS: true, SupportsPDFs: true, SupportsReasoning: true, CapabilitiesKnown: true},
	}
	want := []string{"Chat", "STT", "Tools", "Vision", "PDF", "JSON", "Reasoning", "TTS"}
	if got := providerProbeCapabilities(models); !slices.Equal(got, want) {
		t.Fatalf("provider capabilities = %v, want %v", got, want)
	}
	if got := providerProbeMaxContextWindow(models); got != 131072 {
		t.Fatalf("provider max context = %d, want 131072", got)
	}
}

func TestProviderProbeSummaryTreatsMissingMetadataAsChatCapable(t *testing.T) {
	if got := providerProbeCapabilities([]domain.Model{{ID: "compatible-model"}}); !slices.Equal(got, []string{"Chat"}) {
		t.Fatalf("provider capabilities = %v, want Chat fallback", got)
	}
	if got := providerProbeCapabilities([]domain.Model{{ID: "tts", SupportsTTS: true, CapabilitiesKnown: true}}); !slices.Equal(got, []string{"TTS"}) {
		t.Fatalf("provider capabilities = %v, want TTS-only", got)
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
	prefs.General.MaxChildChats = 3
	prefs.UI.Theme = "dark"
	prefs.Codex.Enabled = true
	prefs.Codex.Executable = "/opt/codex/bin/codex"
	prefs.Codex.Home = "/var/lib/koder-codex"
	prefs.Compaction.UseChatModel = false
	prefs.Compaction.ProviderID = "test"
	prefs.Compaction.ModelID = "compact-model"
	prefs.Compaction.AutoCompactAt = 66
	prefs.Thinking.CavemanEnabled = true
	prefs.Thinking.UseChatModel = false
	prefs.Thinking.ProviderID = "test"
	prefs.Thinking.ModelID = "model"
	prefs.Thinking.CavemanPrompt = "rewrite thinking:\n{{thinking}}"
	prefs.Thinking.CavemanMinTokens = 96
	temperature := 0.7
	topP := 0.9
	prefs.ModelConfigs = []ModelConfigPreference{{
		ProviderID:      "test",
		ModelID:         "model",
		ContextWindow:   12345,
		ModelPreset:     provider.ModelPresetDefault,
		Temperature:     &temperature,
		TopP:            &topP,
		ThinkingMode:    "enabled",
		ThinkingBudget:  4096,
		ReasoningEffort: "xhigh",
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
	if loaded.MaxToolLoopSteps != 77 || loaded.MaxChildChats != 3 || loaded.UI.Theme != "dark" || loaded.Compaction.ModelID != "compact-model" {
		t.Fatalf("expected saved config, got max=%d child=%d theme=%q compact=%q/%q", loaded.MaxToolLoopSteps, loaded.MaxChildChats, loaded.UI.Theme, loaded.Compaction.ProviderID, loaded.Compaction.ModelID)
	}
	if !loaded.Codex.Enabled || loaded.Codex.Executable != "/opt/codex/bin/codex" || loaded.Codex.Home != "/var/lib/koder-codex" {
		t.Fatalf("expected saved codex settings, got %#v", loaded.Codex)
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
	if !ok || modelCfg.Temperature == nil || *modelCfg.Temperature != 0.7 || modelCfg.TopP == nil || *modelCfg.TopP != 0.9 || modelCfg.ThinkingMode != "enabled" || modelCfg.ThinkingBudget != 4096 || modelCfg.ReasoningEffort != "xhigh" {
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
		_, _ = fmt.Fprint(w, `{"data":[{"id":"new-model"}]}`)
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
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slots", "/props":
			http.NotFound(w, r)
		case "/models", "/v1/models":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"base-model"}]}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer modelServer.Close()

	ctrl, st := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {BaseURL: modelServer.URL + "/v1"},
		}
		cfg.SetModelConfig(config.ModelConfig{
			ProviderID:       "test",
			ModelID:          "next-model",
			SourceProviderID: "test",
			SourceModelID:    "base-model",
			ContextWindow:    12345,
		})
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

func TestControllerSetModelRejectsModelOutsideCatalog(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slots" || r.URL.Path == "/props" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"data":[{"id":"catalog-model"}]}`)
	}))
	defer modelServer.Close()

	ctrl, _ := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {BaseURL: modelServer.URL + "/v1"},
		}
	})
	before := len(ctrl.cfg.Models)
	err := ctrl.SetModelForSelection(context.Background(), controllerSelection(ctrl), "test", "chat-only-model")
	if err == nil || !strings.Contains(err.Error(), "not detected or customized") {
		t.Fatalf("expected uncatalogued model to be rejected, got %v", err)
	}
	if got := len(ctrl.cfg.Models); got != before {
		t.Fatalf("expected rejected chat selection not to create a model definition, got %d configs from %d", got, before)
	}
}

func TestControllerSetModelRejectsImageDependentChatOnTextModel(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slots", "/props":
			http.NotFound(w, r)
		case "/models", "/v1/models":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"text-model"}]}`)
		case "/v1/chat/completions":
			http.Error(w, "image vision unsupported content part", http.StatusBadRequest)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer modelServer.Close()

	ctrl, st := newTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Kind: provider.ProviderKindCompatible, BaseURL: modelServer.URL + "/v1", Timeout: time.Second},
		}
		cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "text-model", ContextWindow: 32768})
	})
	ctx := context.Background()
	selection := controllerSelection(ctrl)
	_, _, chatRecord, rt, err := ctrl.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		t.Fatalf("resolve runtime: %v", err)
	}
	chatRecord.RequiresImages = true
	if err := testUpdateChat(ctx, st, chatRecord); err != nil {
		t.Fatalf("update chat: %v", err)
	}
	rt.SetChat(chatRecord)

	err = ctrl.SetModelForSelection(ctx, selection, "test", "text-model")
	if err == nil || !strings.Contains(err.Error(), "image context") {
		t.Fatalf("expected image capability error, got %v", err)
	}
}

func TestControllerSetModelRefreshesDetectedContextWindow(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"live-model"}]}`))
		case "/slots":
			http.NotFound(w, r)
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
		case "/slots", "/props":
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

	selection := controllerSelection(ctrl)
	state, err := ctrl.StateForSelection(context.Background(), selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	if state.ContextWindow != 262144 {
		t.Fatalf("expected detected context window 262144, got %d", state.ContextWindow)
	}
	if state.ModelInfo.ContextWindow != 262144 {
		t.Fatalf("expected model info context window 262144, got %#v", state.ModelInfo)
	}
}

func TestControllerStartWarmsDefaultModelContextWindow(t *testing.T) {
	var propsCalls atomic.Int32
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"live-model"}]}`))
		case "/slots":
			http.NotFound(w, r)
		case "/props":
			if got := r.URL.Query().Get("model"); got != "live-model" {
				t.Fatalf("unexpected model query: %q", got)
			}
			propsCalls.Add(1)
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
	})

	ctrl.mu.RLock()
	model, ok := ctrl.cfg.ModelConfig("test", "live-model")
	ctrl.mu.RUnlock()
	if !ok {
		t.Fatalf("expected default model config to be warmed")
	}
	if model.ContextWindow != 65536 {
		t.Fatalf("expected warmed context window 65536, got %#v", model)
	}
	if propsCalls.Load() == 0 {
		t.Fatalf("expected startup to probe model props")
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
	selection := controllerSelection(ctrl)
	state, err := ctrl.StateForSelection(context.Background(), selection)
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}

	var got []string
	for _, preset := range state.Access.Presets {
		got = append(got, preset.ID)
	}
	want := []string{"locked-down", "normal-coding", "allow-all"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected access presets, got %v", got)
	}
}

func TestControllerGlobalMountPreferencesApplyToAllSessions(t *testing.T) {
	ctrl, _ := newPersistentTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {BaseURL: "https://example.invalid/v1"},
		}
	})
	ctx := context.Background()
	shared := t.TempDir()
	prefs, err := ctrl.Preferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Access.GlobalMounts = []accesssettings.Mount{{Path: shared, Mode: accesssettings.ModeReadWrite}}
	saved, err := ctrl.SavePreferences(ctx, prefs)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Access.GlobalMounts) != 1 || saved.Access.GlobalMounts[0].Path != shared {
		t.Fatalf("saved global mounts = %#v", saved.Access.GlobalMounts)
	}
	selection := controllerSelection(ctrl)
	state, err := ctrl.StateForSelection(ctx, selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Access.GlobalMounts) != 1 || state.Access.GlobalMounts[0].Path != shared {
		t.Fatalf("session state global mounts = %#v", state.Access.GlobalMounts)
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

func TestControllerNewSessionUsesSavedAccessDefaults(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)
	t.Setenv("HOME", temp)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: "https://example.invalid/v1"},
	}
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	cfg.Permissions.Profile = "full-access"
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	projectRoot := t.TempDir()
	ctrl := New(cfg, agent.New(cfg, st, nil, nil))
	if err := ctrl.Start(context.Background(), StartupModeNew, projectRoot); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	activateTestSession(t, ctrl, projectRoot)
	t.Cleanup(func() { _ = ctrl.ShutdownWithCancelReason(context.Background(), chat.CancelReasonShutdownInterrupt) })
	ctx := context.Background()
	prefs, err := ctrl.Preferences(ctx)
	if err != nil {
		t.Fatalf("preferences: %v", err)
	}
	prefs.Access.Settings = accesssettings.AllowAll()
	updated, err := ctrl.SavePreferences(ctx, prefs)
	if err != nil {
		t.Fatalf("save preferences: %v", err)
	}
	if !updated.Access.Settings.Network || updated.Access.Settings.Root != accesssettings.ModeReadWrite {
		t.Fatalf("expected saved access defaults to be allow-all, got %#v", updated.Access.Settings)
	}

	newProjectRoot := t.TempDir()
	session, err := ctrl.CreateSession(ctx, "Default Access", newProjectRoot, false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	stored, err := modeltest.GetSession(ctx, st, session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !stored.AccessSettings.Network || stored.AccessSettings.Root != accesssettings.ModeReadWrite || stored.AccessSettings.Project != accesssettings.ModeReadWrite {
		t.Fatalf("expected new session to use saved access defaults, got %#v", stored.AccessSettings)
	}
	if stored.PermissionProfile != "full-access" {
		t.Fatalf("expected new session to use configured permission profile, got %q", stored.PermissionProfile)
	}
	chats, err := testChatCollection(st).List(ctx, store.ByIndex[domain.Chat]("session", string(session.ID)))
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if len(chats) != 1 || chats[0].PermissionProfile != "full-access" {
		t.Fatalf("expected initial chat to inherit permission profile, got %#v", chats)
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
	if err := ctrl.Start(ctx, StartupModeNew, workspaceA); err != nil {
		t.Fatalf("start: %v", err)
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
	ctrl, st := newTestController(t)
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
	storedSessionBefore, err := modeltest.GetSession(ctx, st, secondSession.ID)
	if err != nil {
		t.Fatalf("get stored session: %v", err)
	}
	storedChatBefore, err := testGetChat(ctx, st, secondState.ActiveChatID)
	if err != nil {
		t.Fatalf("get stored chat: %v", err)
	}
	time.Sleep(time.Millisecond)
	if _, err := ctrl.StateForSelection(ctx, Selection{SessionID: secondSession.ID, ChatID: secondState.ActiveChatID}); err != nil {
		t.Fatalf("state for selected chat: %v", err)
	}
	storedSessionAfter, err := modeltest.GetSession(ctx, st, secondSession.ID)
	if err != nil {
		t.Fatalf("get stored session after state read: %v", err)
	}
	storedChatAfter, err := testGetChat(ctx, st, secondState.ActiveChatID)
	if err != nil {
		t.Fatalf("get stored chat after state read: %v", err)
	}
	if !storedSessionAfter.UpdatedAt.Equal(storedSessionBefore.UpdatedAt) {
		t.Fatalf("state read touched session updated_at: before=%s after=%s", storedSessionBefore.UpdatedAt, storedSessionAfter.UpdatedAt)
	}
	if !storedChatAfter.UpdatedAt.Equal(storedChatBefore.UpdatedAt) {
		t.Fatalf("state read touched chat updated_at: before=%s after=%s", storedChatBefore.UpdatedAt, storedChatAfter.UpdatedAt)
	}
	if got := ctrl.State().Session.ID; got != firstSessionID {
		t.Fatalf("expected controller state to remain %s after second selection state, got %s", firstSessionID, got)
	}
}

func TestControllerQueueMutationDoesNotTouchSessionMetadata(t *testing.T) {
	ctrl, st := newTestController(t)
	ctx := context.Background()
	selection := controllerSelection(ctrl)
	owner, err := ctrl.agent.LoadSession(ctx, selection.SessionID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	rt, err := owner.Chat(ctx, selection.ChatID)
	if err != nil {
		t.Fatalf("load chat: %v", err)
	}
	queuedID := id.New()
	rt.ReplaceQueue([]domain.QueuedInput{{
		ID:        queuedID,
		Kind:      domain.QueuedInputKindQueued,
		Delivery:  domain.QueuedInputDeliveryNextTurn,
		Origin:    domain.QueuedInputOriginUser,
		Source:    domain.UserMessageSourceUser,
		Text:      "later",
		Held:      true,
		CreatedAt: time.Now().UTC(),
	}})
	deadline := time.Now().Add(2 * time.Second)
	for len(rt.Snapshot().QueuedInputs) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("timed out preparing queued input")
		}
		time.Sleep(time.Millisecond)
	}
	storedBefore, err := modeltest.GetSession(ctx, st, selection.SessionID)
	if err != nil {
		t.Fatalf("get session before queue mutation: %v", err)
	}
	time.Sleep(time.Millisecond)
	if err := ctrl.ToggleQueueItemKindForSelection(ctx, selection, queuedID); err != nil {
		t.Fatalf("toggle queued input: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		queued := rt.Snapshot().QueuedInputs
		if len(queued) == 1 && domain.DeliveryForQueuedInput(queued[0]) == domain.QueuedInputDeliveryTurnBoundary {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out toggling queued input")
		}
		time.Sleep(time.Millisecond)
	}
	storedAfter, err := modeltest.GetSession(ctx, st, selection.SessionID)
	if err != nil {
		t.Fatalf("get session after queue mutation: %v", err)
	}
	if !storedAfter.UpdatedAt.Equal(storedBefore.UpdatedAt) {
		t.Fatalf("queue mutation touched session updated_at: before=%s after=%s", storedBefore.UpdatedAt, storedAfter.UpdatedAt)
	}
}

func TestControllerStateForSelectionDoesNotPersistLastUsedChat(t *testing.T) {
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
	side := newSelectedChat(t, ctrl, selection, "Side")
	if _, err := ctrl.StateForSelection(ctx, selection); err != nil {
		t.Fatalf("read first chat state: %v", err)
	}

	next := New(cfg, agent.New(cfg, st, nil, nil))
	if err := next.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("restart controller: %v", err)
	}
	state, err := next.StateForSelection(ctx, Selection{SessionID: selection.SessionID})
	if err != nil {
		t.Fatalf("state after restart: %v", err)
	}
	if got := state.ActiveChatID; got != side.ID {
		t.Fatalf("expected state read not to persist viewed chat %s; restarted controller focused %s, want newest chat %s", first, got, side.ID)
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

	state, err := ctrl.StateForSelection(ctx, Selection{SessionID: session.ID})
	if err != nil {
		t.Fatalf("state for selection: %v", err)
	}
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
	_ = activateTestSession(t, ctrl, workspaceB)

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
	ctrl.workspaceSnapshot = func(_ context.Context, projectRoot string) (workspacepkg.SnapshotResult, error) {
		select {
		case <-snapshotStarted:
		default:
			close(snapshotStarted)
		}
		<-releaseSnapshot
		return workspacepkg.SnapshotResult{Status: workspacepkg.Status{Available: true, ProjectRoot: projectRoot, RefreshedAt: time.Now().UTC()}}, nil
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

func TestControllerStateForSelectionDoesNotStartWorkspaceSnapshot(t *testing.T) {
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
	session, err := modeltest.CreateSession(ctx, st, "Workspace", "test", "model", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := setSessionProjectRoot(ctx, st, session.ID, workdir); err != nil {
		t.Fatalf("set project root: %v", err)
	}
	ctrl := New(cfg, agent.New(cfg, st, nil, nil))
	t.Cleanup(func() { _ = ctrl.ShutdownWithCancelReason(context.Background(), chat.CancelReasonShutdownInterrupt) })
	snapshotStarted := make(chan struct{})
	ctrl.workspaceSnapshot = func(_ context.Context, projectRoot string) (workspacepkg.SnapshotResult, error) {
		select {
		case <-snapshotStarted:
		default:
			close(snapshotStarted)
		}
		return workspacepkg.SnapshotResult{Status: workspacepkg.Status{Available: true, ProjectRoot: projectRoot, RefreshedAt: time.Now().UTC()}}, nil
	}
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	if _, err := ctrl.StateForSelection(ctx, Selection{SessionID: session.ID}); err != nil {
		t.Fatalf("state for selection: %v", err)
	}
	select {
	case <-snapshotStarted:
		t.Fatal("state selection should not start workspace snapshot")
	case <-time.After(100 * time.Millisecond):
	}
	if err := ctrl.EnsureSessionWorkspace(ctx, session.ID); err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}
	select {
	case <-snapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("expected explicit workspace activation to start snapshot")
	}
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

func TestControllerWorkspaceWatcherThrottlesRefreshes(t *testing.T) {
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
	t.Cleanup(func() { _ = ctrl.ShutdownWithCancelReason(context.Background(), chat.CancelReasonShutdownInterrupt) })
	ctrl.workspaceRefreshMinInterval = time.Hour
	var snapshots atomic.Int32
	ctrl.workspaceSnapshot = func(_ context.Context, projectRoot string) (workspacepkg.SnapshotResult, error) {
		snapshots.Add(1)
		return workspacepkg.SnapshotResult{Status: workspacepkg.Status{Available: true, ProjectRoot: projectRoot, RefreshedAt: time.Now().UTC()}}, nil
	}
	if err := ctrl.Start(ctx, StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	session := activateTestSession(t, ctrl, workdir)
	if _, err := ctrl.RefreshWorkspaceForSelection(ctx, Selection{SessionID: session.ID}); err != nil {
		t.Fatalf("initial refresh workspace: %v", err)
	}
	baseline := snapshots.Load()
	if baseline < 1 {
		t.Fatalf("expected at least one workspace snapshot, got %d", baseline)
	}
	for {
		time.Sleep(10 * time.Millisecond)
		next := snapshots.Load()
		if next == baseline {
			break
		}
		baseline = next
	}
	events, unsub := ctrl.Subscribe()
	defer unsub()

	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type != "workspace_delta" {
				continue
			}
			status := event.Payload.(map[string]any)["workspace_status"].(workspacepkg.Status)
			if status.Stale {
				if got := snapshots.Load(); got != baseline {
					t.Fatalf("watcher bypassed refresh throttle, snapshots=%d baseline=%d", got, baseline)
				}
				return
			}
		case <-deadline:
			t.Fatalf("expected stale workspace delta, snapshots=%d baseline=%d", snapshots.Load(), baseline)
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

func newPersistentTestControllerWithConfig(t *testing.T, edit func(*config.Config)) (*Controller, *store.Store) {
	t.Helper()
	cfg, err := config.LoadWithOptions(config.LoadOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	if edit != nil {
		edit(&cfg)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
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

func TestQuickChatLifecycleSeparatesListsAndPromotes(t *testing.T) {
	ctrl, _ := newPersistentTestControllerWithConfig(t, nil)
	ctx := context.Background()
	quick, chatRecord, err := ctrl.CreateQuickChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if quick.Kind != domain.SessionKindQuick || chatRecord.WorkflowRole != chatrole.Standalone {
		t.Fatalf("unexpected quick chat: %#v %#v", quick, chatRecord)
	}
	state, err := ctrl.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.QuickChats) != 1 || state.QuickChats[0].ID != quick.ID {
		t.Fatalf("unexpected quick chat list: %#v", state.QuickChats)
	}
	selected, err := ctrl.StateForSelection(ctx, Selection{SessionID: quick.ID})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Session.ID != quick.ID || selected.Session.Kind != domain.SessionKindQuick || selected.ActiveChatID != chatRecord.ID {
		t.Fatalf("unexpected selected quick chat state: %#v", selected)
	}
	if len(selected.QuickChats) != 1 || selected.QuickChats[0].ID != quick.ID {
		t.Fatalf("selected state lost quick chat list: %#v", selected.QuickChats)
	}
	updated, err := ctrl.PromoteQuickChat(ctx, quick.ID, agent.PromoteQuickRequest{Mode: agent.QuickPromotionAssign})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Kind != domain.SessionKindRegular {
		t.Fatalf("promoted kind = %v", updated.Kind)
	}
	state, err = ctrl.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.QuickChats) != 0 {
		t.Fatalf("promoted chat remained quick: %#v", state.QuickChats)
	}
}

func TestVoiceChatLifecycleIsDurableAndSelectable(t *testing.T) {
	ctrl, _ := newPersistentTestControllerWithConfig(t, nil)
	ctx := context.Background()
	voiceSession, chatRecord, err := ctrl.CreateVoiceChat(ctx, "Car call")
	if err != nil {
		t.Fatal(err)
	}
	if voiceSession.Kind != domain.SessionKindVoice || voiceSession.ProjectRoot != "" || chatRecord.WorkflowRole != chatrole.Orchestrator || chatRecord.InteractionMode != domain.InteractionModeVoice {
		t.Fatalf("unexpected voice chat: %#v %#v", voiceSession, chatRecord)
	}
	state, err := ctrl.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, session := range state.Sessions {
		found = found || session.ID == voiceSession.ID
	}
	if !found {
		t.Fatalf("voice session missing from durable sessions: %#v", state.Sessions)
	}
	selected, err := ctrl.StateForSelection(ctx, Selection{SessionID: voiceSession.ID})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Session.ID != voiceSession.ID || selected.ActiveChatID != chatRecord.ID {
		t.Fatalf("voice session not selectable: %#v", selected)
	}
	targets, err := ctrl.ListVoiceSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var listed voice.Session
	for _, target := range targets {
		if target.ID == string(voiceSession.ID) {
			listed = target
		}
	}
	if listed.ID == "" || listed.Kind != domain.SessionKindVoice.String() || listed.ChatCount != 1 || listed.VoiceChats != 1 {
		t.Fatalf("voice session missing from native hierarchy: %#v", targets)
	}
	ensured, err := ctrl.EnsureVoiceSession(ctx, string(voiceSession.ID))
	if err != nil {
		t.Fatal(err)
	}
	if ensured.ID != string(voiceSession.ID) {
		t.Fatalf("ensured voice session = %#v", ensured)
	}
}

func TestClientSessionSummaryUsesLatestChatActivity(t *testing.T) {
	created := time.Now().UTC().Add(-2 * time.Hour)
	latest := created.Add(90 * time.Minute)
	summary := clientSessionSummary(domain.Session{
		ID: "session-activity", Kind: domain.SessionKindRegular, Title: "Activity", CreatedAt: created, UpdatedAt: created,
	}, []domain.Chat{
		{ID: "chat-old", SessionID: "session-activity", UpdatedAt: created.Add(time.Minute)},
		{ID: "chat-latest", SessionID: "session-activity", UpdatedAt: latest},
	})
	if !summary.UpdatedAt.Equal(latest) {
		t.Fatalf("session summary updated_at = %s, want latest chat activity %s", summary.UpdatedAt, latest)
	}
}

func TestVoiceChatOrganizationIsDurableAndDeletedChatsAreOmitted(t *testing.T) {
	ctrl, _ := newPersistentTestControllerWithConfig(t, nil)
	ctx := context.Background()
	created, _, err := ctrl.CreateVoiceChat(ctx, "Organize me")
	if err != nil {
		t.Fatal(err)
	}
	originalUpdatedAt := created.UpdatedAt
	value := true
	organized, err := ctrl.UpdateVoiceSession(ctx, string(created.ID), voice.SessionUpdate{Pinned: &value, Favorite: &value})
	if err != nil {
		t.Fatal(err)
	}
	if !organized.Pinned || !organized.Favorite || !organized.UpdatedAt.Equal(originalUpdatedAt) {
		t.Fatalf("organized voice session = %#v, original updated_at=%s", organized, originalUpdatedAt)
	}
	archived, err := ctrl.UpdateVoiceSession(ctx, string(created.ID), voice.SessionUpdate{Archived: &value})
	if err != nil {
		t.Fatal(err)
	}
	if !archived.Archived || archived.Pinned {
		t.Fatalf("archived voice session = %#v", archived)
	}
	if _, err := ctrl.EnsureVoiceSession(ctx, string(created.ID)); err == nil {
		t.Fatal("archived voice session remained selectable")
	}
	visible, err := ctrl.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sessionIDIn(visible.Sessions, created.ID) {
		t.Fatal("archived voice session remained visible in the active browser list")
	}
	listed, err := ctrl.ListVoiceChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || !listed[0].Archived {
		t.Fatalf("archived voice session was not manageable: %#v", listed)
	}
	if err := ctrl.DeleteVoiceSession(ctx, string(created.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.EnsureVoiceSession(ctx, string(created.ID)); err == nil {
		t.Fatal("deleted voice session remained selectable")
	}
	listed, err = ctrl.ListVoiceChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("deleted voice session remained listed: %#v", listed)
	}
	value = false
	restored, err := ctrl.UpdateVoiceSession(ctx, string(created.ID), voice.SessionUpdate{Deleted: &value, Archived: &value})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Deleted || restored.Archived {
		t.Fatalf("restored voice session = %#v", restored)
	}
	if _, err := ctrl.EnsureVoiceSession(ctx, string(created.ID)); err != nil {
		t.Fatalf("restored voice session is not selectable: %v", err)
	}
}

func TestNativeClientManagesSessionsAndChats(t *testing.T) {
	ctrl, _ := newPersistentTestControllerWithConfig(t, nil)
	ctx := context.Background()
	created, err := ctrl.CreateSession(ctx, "Original", "", false)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ctrl.StateForSelection(ctx, Selection{SessionID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	side, err := ctrl.NewChatForSelection(ctx, Selection{SessionID: created.ID, ChatID: state.ActiveChatID}, "Side chat")
	if err != nil {
		t.Fatal(err)
	}
	archived := true
	title := "Renamed session"
	updated, err := ctrl.UpdateClientSession(ctx, string(created.ID), voice.SessionUpdate{Title: &title, Archived: &archived})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != title || !updated.Archived {
		t.Fatalf("updated session = %#v", updated)
	}
	listed, err := ctrl.ListVoiceSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listedSession := slices.IndexFunc(listed, func(item voice.Session) bool { return item.ID == string(created.ID) })
	if listedSession < 0 || !listed[listedSession].Archived {
		t.Fatalf("archived session was not manageable: %#v", listed)
	}
	archived = false
	if _, err := ctrl.UpdateClientSession(ctx, string(created.ID), voice.SessionUpdate{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	archived = true
	chatTitle := "Renamed side chat"
	updatedChat, err := ctrl.UpdateClientChat(ctx, string(created.ID), string(side.ID), voice.ChatUpdate{Title: &chatTitle, Archived: &archived})
	if err != nil {
		t.Fatal(err)
	}
	if updatedChat.Title != chatTitle || !updatedChat.Archived {
		t.Fatalf("updated chat = %#v", updatedChat)
	}
	if err := ctrl.DeleteClientChat(ctx, string(created.ID), string(side.ID)); err != nil {
		t.Fatal(err)
	}
	chats, err := ctrl.ListSessionChats(ctx, string(created.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range chats {
		if item.ID == string(side.ID) {
			t.Fatalf("deleted chat remained listed: %#v", chats)
		}
	}
	if err := ctrl.DeleteClientSession(ctx, string(created.ID)); err != nil {
		t.Fatal(err)
	}
	listed, err = ctrl.ListVoiceSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(listed, func(item voice.Session) bool { return item.ID == string(created.ID) }) {
		t.Fatalf("deleted session remained listed: %#v", listed)
	}
}

func TestNewTypedChatForSelectionCreatesTopLevelVoiceChat(t *testing.T) {
	ctrl, _ := newPersistentTestControllerWithConfig(t, nil)
	ctx := context.Background()
	created, err := ctrl.CreateSession(ctx, "Project", "", false)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ctrl.StateForSelection(ctx, Selection{SessionID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	voiceChat, err := ctrl.NewTypedChatForSelection(ctx, Selection{SessionID: created.ID, ChatID: state.ActiveChatID}, "Voice conversation", domain.WorkflowRoleVoice)
	if err != nil {
		t.Fatal(err)
	}
	if voiceChat.WorkflowRole != domain.WorkflowRoleOrchestrator || voiceChat.InteractionMode != domain.InteractionModeVoice || voiceChat.ParentChatID != nil {
		t.Fatalf("voice chat = %#v", voiceChat)
	}
	if _, err := ctrl.NewTypedChatForSelection(ctx, Selection{SessionID: created.ID, ChatID: state.ActiveChatID}, "Future", domain.WorkflowRole("codex")); err == nil {
		t.Fatal("unsupported chat type was accepted")
	}
	codexChat, err := ctrl.CreateChatForSelection(ctx, Selection{SessionID: created.ID, ChatID: state.ActiveChatID}, domain.ChatCreateSpec{
		Title: "Codex implementation", Backend: domain.ChatBackendCodex, WorkflowRole: domain.WorkflowRoleExecution,
		ModelID: "gpt-test", TaskRef: "M001T008", ToolStates: domain.ToolStates{domain.ToolKindChatStart: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if codexChat.Backend != domain.ChatBackendCodex || codexChat.WorkflowRole != domain.WorkflowRoleExecution || codexChat.InteractionMode != domain.InteractionModeText || codexChat.ModelID != "gpt-test" || codexChat.AssignedTaskRef != "M001T008" || codexChat.ToolStates[domain.ToolKindChatStart] {
		t.Fatalf("codex chat = %#v", codexChat)
	}
}

func TestRunVoiceTurnUsesNormalVoiceChatAndSessionTools(t *testing.T) {
	var targetChatID string
	var requestsMu sync.Mutex
	var requests []string
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		requestBody := string(body)
		requestsMu.Lock()
		requests = append(requests, requestBody)
		requestsMu.Unlock()
		switch {
		case strings.Contains(requestBody, "voice orchestrator") && strings.Contains(requestBody, `"role":"tool"`):
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"We found that the laptop now boots normally."},"finish_reason":"stop"}],"usage":{"total_tokens":2}}`)
		case strings.Contains(requestBody, "voice orchestrator"):
			arguments := fmt.Sprintf(`{"chat_id":%q,"message":"Check whether the laptop fix still works","wait":true}`, targetChatID)
			_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"tool_calls":[{"id":"send-1","type":"function","function":{"name":"chat_send","arguments":%q}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":2}}`, arguments)
		default:
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"The laptop now boots normally after the firmware fix."},"finish_reason":"stop"}],"usage":{"total_tokens":2}}`)
		}
	}))
	defer modelServer.Close()

	ctrl, _ := newPersistentTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Kind: provider.ProviderKindCompatible, BaseURL: modelServer.URL + "/v1", Timeout: 5 * time.Second},
		}
	})
	state, err := ctrl.Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sessions) == 0 {
		t.Fatal("expected ordinary target session")
	}
	target := state.Sessions[0]
	selected, err := ctrl.StateForSelection(context.Background(), Selection{SessionID: target.ID})
	if err != nil {
		t.Fatal(err)
	}
	targetChatID = string(selected.ActiveChatID)
	target.Title = "Laptop repair"
	if err := ctrl.RenameSession(context.Background(), target.ID, target.Title); err != nil {
		t.Fatal(err)
	}
	voiceChat, err := ctrl.CreateVoiceChatInSession(context.Background(), string(target.ID), domain.ChatCreateSpec{Title: "Phone assistant", InteractionMode: domain.InteractionModeVoice})
	if err != nil {
		t.Fatal(err)
	}
	chats, err := ctrl.ListSessionChats(context.Background(), string(target.ID))
	if err != nil || len(chats) < 2 {
		t.Fatalf("session chats = %#v, %v", chats, err)
	}

	var working voice.Session
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	message, err := ctrl.RunVoiceChatTurn(ctx, string(target.ID), voiceChat.ID, "Does the laptop fix still work?", voice.TurnOptions{ResponsePacing: voice.ResponsePacingDetailed}, func(session voice.Session) error {
		working = session
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.SpokenText != "We found that the laptop now boots normally." {
		t.Fatalf("voice response = %#v", message)
	}
	if message.TranscriptID == "" {
		t.Fatalf("voice response omitted durable transcript id: %#v", message)
	}
	owner, err := ctrl.agent.LoadSession(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterTurn := owner.Snapshot()
	if afterTurn.Session.Title != "Laptop repair" || !afterTurn.Session.TitleUserDefined {
		t.Fatalf("voice turn changed user-defined session title: %#v", afterTurn.Session)
	}
	voiceIndex := slices.IndexFunc(afterTurn.Chats, func(chat domain.Chat) bool { return string(chat.ID) == voiceChat.ID })
	if voiceIndex < 0 || afterTurn.Chats[voiceIndex].Title != "Phone assistant" || !afterTurn.Chats[voiceIndex].TitleUserDefined {
		t.Fatalf("voice turn changed user-defined chat title: %#v", afterTurn.Chats)
	}
	if working.ID != targetChatID || working.Kind != "chat" {
		t.Fatalf("working target = %#v", working)
	}
	voiceChats, err := ctrl.ListVoiceSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var parent voice.Session
	for _, item := range voiceChats {
		if item.ID == string(target.ID) {
			parent = item
		}
	}
	if parent.ID == "" || parent.LastMessage != message.SpokenText || parent.VoiceChats != 1 {
		t.Fatalf("voice conversation preview = %#v", voiceChats)
	}

	_, _, _, runtime, err := ctrl.resolveSelectedRuntimeWithoutTouch(ctx, Selection{SessionID: target.ID, ChatID: id.ID(voiceChat.ID)}, true)
	if err != nil {
		t.Fatal(err)
	}
	timeline := runtime.SnapshotTimeline()
	userTurns := 0
	for _, item := range timeline {
		if user, ok := item.Content.(domain.UserMessage); ok {
			userTurns++
			if user.Text != "Does the laptop fix still work?" || user.Source != "voice" {
				t.Fatalf("voice user turn = %#v", user)
			}
		}
	}
	if userTurns != 1 {
		t.Fatalf("voice transcript has %d user turns: %#v", userTurns, timeline)
	}
	searchResults, err := ctrl.SearchVoiceChatHistory(ctx, string(target.ID), voiceChat.ID, "boots normally", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(searchResults) != 1 || !strings.Contains(searchResults[0].Match.Text, "boots normally") || len(searchResults[0].Context) < 2 {
		t.Fatalf("voice transcript search = %#v", searchResults)
	}
	requestsMu.Lock()
	joinedRequests := strings.Join(requests, "\n")
	requestsMu.Unlock()
	if !strings.Contains(joinedRequests, `"name":"chat_send"`) || strings.Contains(joinedRequests, `"name":"session_delegate"`) {
		t.Fatalf("voice chat did not receive native chat coordination tools: %s", joinedRequests)
	}
	if !strings.Contains(joinedRequests, "Response pacing for this call is detailed") {
		t.Fatalf("voice pacing instruction was not offered transiently: %s", joinedRequests)
	}
	if strings.Contains(chatrole.SpecFor(chatrole.Voice).SystemPrompt, "exact session_id") {
		t.Fatalf("voice prompt contains tool mechanics: %s", chatrole.SpecFor(chatrole.Voice).SystemPrompt)
	}
}

func TestRunVoiceTurnResolvesCustomModelForProjectInstructions(t *testing.T) {
	const (
		aliasModel   = "friendly medium"
		backingModel = "provider-model"
	)
	var modelsMu sync.Mutex
	var receivedModels []string
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":[{"id":%q,"object":"model"}]}`, backingModel)
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			var request struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Error(err)
				return
			}
			modelsMu.Lock()
			receivedModels = append(receivedModels, request.Model)
			modelsMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(string(body), "Resolve project instruction files") {
				_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"resolved_agents_md\":\"Run focused tests.\",\"conflict_summary\":\"No conflicts\"}"},"finish_reason":"stop"}]}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"The resumed conversation works."},"finish_reason":"stop"}],"usage":{"total_tokens":2}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer modelServer.Close()

	ctrl, _ := newPersistentTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Kind: provider.ProviderKindCompatible, BaseURL: modelServer.URL + "/v1", Timeout: 5 * time.Second},
		}
		cfg.Defaults.ModelID = aliasModel
		cfg.SetModelConfig(config.ModelConfig{
			ProviderID: "test", ModelID: aliasModel,
			SourceProviderID: "test", SourceModelID: backingModel,
			ContextWindow: 32768,
		})
	})
	state, err := ctrl.Sessions(context.Background())
	if err != nil || len(state.Sessions) == 0 {
		t.Fatalf("sessions = %#v, %v", state.Sessions, err)
	}
	target := state.Sessions[0]
	if err := os.WriteFile(filepath.Join(target.ProjectRoot, "AGENTS.md"), []byte("Run focused tests.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	voiceChat, err := ctrl.CreateVoiceChatInSession(context.Background(), string(target.ID), domain.ChatCreateSpec{
		Title: "Alias voice", InteractionMode: domain.InteractionModeVoice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if voiceChat.ModelID != aliasModel {
		t.Fatalf("voice chat model = %q, want alias %q", voiceChat.ModelID, aliasModel)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	message, err := ctrl.RunVoiceChatTurn(ctx, string(target.ID), voiceChat.ID, "Resume this conversation", voice.TurnOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if message.SpokenText != "The resumed conversation works." {
		t.Fatalf("voice response = %#v", message)
	}
	modelsMu.Lock()
	defer modelsMu.Unlock()
	if len(receivedModels) < 2 {
		t.Fatalf("provider requests = %v, want instruction resolution and voice turn", receivedModels)
	}
	for _, modelID := range receivedModels {
		if modelID != backingModel {
			t.Fatalf("provider received model %q through custom alias; all requests = %v", modelID, receivedModels)
		}
	}
}

func TestApplyVoiceRuntimeSummaryUsesPersistedPreviewAndLiveWorkState(t *testing.T) {
	chatID := id.ID("voice-chat")
	summary := voice.Session{}
	applyVoiceRuntimeSummary(&summary, sessionpkg.SessionSnapshot{
		Session: domain.Session{ID: "voice-session"},
		Chats:   []domain.Chat{{ID: chatID, WorkflowRole: domain.WorkflowRoleVoice, LastMessage: "The concise result."}},
		Snapshots: map[id.ID]chat.Snapshot{
			chatID: {Status: chat.StatusRunningTools, Active: true},
		},
	})
	if summary.LastMessage != "The concise result." || !summary.Busy || summary.Status != string(chat.StatusRunningTools) {
		t.Fatalf("runtime summary = %#v", summary)
	}
}

func TestRunVoiceTurnResearchesCurrentLocationWithoutOpeningMap(t *testing.T) {
	var voiceStep atomic.Int32
	var requestsMu sync.Mutex
	var requests []string
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		requestBody := string(body)
		requestsMu.Lock()
		requests = append(requests, requestBody)
		requestsMu.Unlock()
		if !strings.Contains(requestBody, "voice orchestrator") {
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"Aarhus has DHL Stafetten today."},"finish_reason":"stop"}],"usage":{"total_tokens":2}}`)
			return
		}

		switch voiceStep.Add(1) {
		case 1:
			_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"tool_calls":[{"id":"location-1","type":"function","function":{"name":"phone","arguments":%q}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":2}}`, `{"action":"get_location"}`)
		case 2:
			// Reproduce the observed failure: a model may fabricate a phone action
			// that was absent from the turn-specific schema, then retry it.
			_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"tool_calls":[{"id":"map-1","type":"function","function":{"name":"phone","arguments":%q}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":2}}`, `{"action":"open_map","query":"Aarhus"}`)
		case 3:
			_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"tool_calls":[{"id":"map-2","type":"function","function":{"name":"phone","arguments":%q}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":2}}`, `{"action":"open_map","query":"Aarhus events"}`)
		case 4:
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"You're in Aarhus, and DHL Stafetten is happening today."},"finish_reason":"stop"}],"usage":{"total_tokens":2}}`)
		default:
			t.Errorf("unexpected voice model step %d", voiceStep.Load())
		}
	}))
	defer modelServer.Close()

	ctrl, _ := newPersistentTestControllerWithConfig(t, func(cfg *config.Config) {
		cfg.Providers = map[string]config.Provider{
			"test": {Kind: provider.ProviderKindCompatible, BaseURL: modelServer.URL + "/v1", Timeout: 5 * time.Second},
		}
	})
	var actionsMu sync.Mutex
	var actions []phonedevice.Action
	release, err := ctrl.PhoneDeviceHub().Attach("local-context-test", []string{"get_location", "open_map"}, func(_ context.Context, _ string, action phonedevice.Action, _ map[string]string) (phonedevice.Result, error) {
		actionsMu.Lock()
		actions = append(actions, action)
		actionsMu.Unlock()
		if action != phonedevice.GetLocation {
			return phonedevice.Result{}, fmt.Errorf("unexpected phone action %q", action)
		}
		return phonedevice.Result{
			Text: "Current location resolved to Aarhus, Central Denmark Region, Denmark with 12 meter accuracy",
			Data: map[string]any{"place_name": "Aarhus, Central Denmark Region, Denmark", "latitude": 56.1629, "longitude": 10.2039},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	voiceSession, _, err := ctrl.CreateVoiceChat(context.Background(), "Phone assistant")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var workingStates []voice.Session
	message, err := ctrl.RunVoiceTurn(ctx, string(voiceSession.ID), "What's happening where I am?", voice.TurnOptions{}, func(session voice.Session) error {
		workingStates = append(workingStates, session)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.SpokenText != "You're in Aarhus, and DHL Stafetten is happening today." {
		t.Fatalf("voice response = %#v", message)
	}
	actionsMu.Lock()
	gotActions := slices.Clone(actions)
	actionsMu.Unlock()
	if !slices.Equal(gotActions, []phonedevice.Action{phonedevice.GetLocation}) {
		t.Fatalf("phone actions = %v, want only get_location", gotActions)
	}
	if len(workingStates) != 1 || workingStates[0].ID != "" {
		t.Fatalf("working states = %#v, want one generic tool-work state", workingStates)
	}
	requestsMu.Lock()
	joinedRequests := strings.Join(requests, "\n")
	firstRequest := requests[0]
	requestsMu.Unlock()
	for _, expected := range []string{
		"phone action open_map requires an explicit request for that user-facing action in the current voice utterance",
		`"name":"web_search"`,
	} {
		if !strings.Contains(joinedRequests, expected) {
			t.Fatalf("voice routing did not contain %q: %s", expected, joinedRequests)
		}
	}
	if strings.Contains(firstRequest, `"open_map"`) {
		t.Fatalf("implicit local-information turn offered open_map to the voice model: %s", firstRequest)
	}
}

func TestEnsureVoiceSessionCreatesFirstDurableChat(t *testing.T) {
	ctrl, _ := newPersistentTestControllerWithConfig(t, nil)
	created, err := ctrl.EnsureVoiceSession(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Title != "Voice Chat" {
		t.Fatalf("created voice session = %#v", created)
	}
	again, err := ctrl.EnsureVoiceSession(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != created.ID {
		t.Fatalf("expected newest voice session reuse, got %#v after %#v", again, created)
	}
}

func TestCreateVoiceTargetSupportsTemporaryAndPersistentSessions(t *testing.T) {
	ctrl, _ := newTestControllerWithConfig(t, nil)
	temporary, err := ctrl.CreateVoiceTarget(context.Background(), "One-off calendar entry", false)
	if err != nil {
		t.Fatal(err)
	}
	persistent, err := ctrl.CreateVoiceTarget(context.Background(), "Ongoing travel plan", true)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ctrl.Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !sessionIDIn(state.QuickChats, id.ID(temporary.ID)) {
		t.Fatalf("temporary target %s missing from quick chats", temporary.ID)
	}
	if !sessionIDIn(state.Sessions, id.ID(persistent.ID)) {
		t.Fatalf("persistent target %s missing from sessions", persistent.ID)
	}
}

func sessionIDIn(sessions []domain.Session, target id.ID) bool {
	for _, session := range sessions {
		if session.ID == target {
			return true
		}
	}
	return false
}

func TestCloseQuickChatRemovesItFromQuickList(t *testing.T) {
	ctrl, _ := newPersistentTestControllerWithConfig(t, nil)
	ctx := context.Background()
	quick, _, err := ctrl.CreateQuickChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ctrl.CloseQuickChat(ctx, quick.ID, false); err != nil {
		t.Fatal(err)
	}
	state, err := ctrl.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.QuickChats) != 0 {
		t.Fatalf("closed quick chat remained listed: %#v", state.QuickChats)
	}
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
