package store

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
)

func TestSessionMessageRoundTrip(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			chat, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			item := appendTimelineForTest(t, st, chat.ID, domain.UserMessage{Text: "hello"})
			items, err := st.TimelineForChat(context.Background(), chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].ID != item.ID {
				t.Fatalf("unexpected timeline items: %#v", items)
			}
			user, ok := items[0].Content.(domain.UserMessage)
			if !ok || user.Text != "hello" {
				t.Fatalf("unexpected timeline content: %#v", items[0].Content)
			}
		})
	}
}

func appendTimelineForTest(t *testing.T, st *Store, chatID domain.ID, content domain.TimelineContent) domain.TimelineItem {
	t.Helper()
	item, err := st.AppendTimeline(context.Background(), chatID, content)
	if err != nil {
		t.Fatal(err)
	}
	item.Seal(time.Now().UTC())
	if err := st.Timeline().Put(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	return item
}

func chatIDs(chats []domain.Chat) []domain.ID {
	ids := make([]domain.ID, 0, len(chats))
	for _, chat := range chats {
		ids = append(ids, chat.ID)
	}
	return ids
}

func TestConcurrentAttachToolResultsPreservesAllChildren(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)
			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			chat, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			const callCount = 24
			calls := make([]domain.ToolCall, 0, callCount)
			for i := range callCount {
				calls = append(calls, domain.ToolCall{
					ToolCallID: domain.ToolCallID("call_" + strconv.Itoa(i)),
					Tool:       domain.ToolKindBash,
					Args:       map[string]string{"command": "printf " + strconv.Itoa(i)},
					Status:     domain.ToolStatusPending,
				})
			}
			item, err := st.AppendAssistantToolCalls(context.Background(), chat.ID, calls, "", domain.Usage{})
			if err != nil {
				t.Fatal(err)
			}

			var wg sync.WaitGroup
			errs := make(chan error, callCount)
			for i := range callCount {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, err := st.AttachToolResult(context.Background(), chat.ID, "call_"+strconv.Itoa(i), domain.ToolResult{
						Text:   "result " + strconv.Itoa(i),
						Status: domain.ToolResultStatusOK,
						Data:   domain.BashStoredResult{Command: "printf " + strconv.Itoa(i), Output: "result " + strconv.Itoa(i)},
					})
					if err != nil {
						errs <- err
					}
				}(i)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatal(err)
			}

			got, err := st.Timeline().Get(context.Background(), item.ID)
			if err != nil {
				t.Fatal(err)
			}
			assistant, ok := got.Content.(domain.AssistantMessage)
			if !ok {
				t.Fatalf("expected assistant item, got %#v", got.Content)
			}
			if len(assistant.Tools) != callCount {
				t.Fatalf("expected %d tool children, got %d", callCount, len(assistant.Tools))
			}
			for i := range callCount {
				call := assistant.ToolByID(domain.ToolCallID("call_" + strconv.Itoa(i)))
				if call == nil {
					t.Fatalf("missing call_%d in %#v", i, assistant.Tools)
				}
				if call.Status != domain.ToolStatusDone || call.Result == nil || call.Result.Text != "result "+strconv.Itoa(i) {
					t.Fatalf("call_%d not completed in-place: %#v", i, call)
				}
			}
		})
	}
}

func TestGenericCollectionRoundTripAndIndex(t *testing.T) {
	type note struct {
		ID     domain.ID
		ChatID domain.ID
		Body   string
	}
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			dir := t.TempDir()
			st := openTestStoreAt(t, backend, dir)
			notes := NewCollection(st, CollectionSpec[note]{
				Namespace: "test-notes",
				GetID:     func(v note) string { return v.ID },
				SetID:     func(v *note, id string) { v.ID = id },
				Indexes: []IndexSpec[note]{
					{Name: "chat", Value: func(v note) string { return v.ChatID }},
				},
			})
			first, err := notes.Insert(context.Background(), note{ChatID: "chat-7", Body: "first"})
			if err != nil {
				t.Fatal(err)
			}
			second, err := notes.Insert(context.Background(), note{ChatID: "chat-8", Body: "second"})
			if err != nil {
				t.Fatal(err)
			}
			first.Body = "updated"
			if err := notes.Put(context.Background(), first); err != nil {
				t.Fatal(err)
			}
			got, err := notes.Get(context.Background(), first.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Body != "updated" {
				t.Fatalf("body = %q", got.Body)
			}
			indexed, err := notes.List(context.Background(), ByIndex[note]("chat", "chat-7"))
			if err != nil {
				t.Fatal(err)
			}
			if len(indexed) != 1 || indexed[0].ID != first.ID {
				t.Fatalf("indexed = %#v", indexed)
			}
			if err := notes.Delete(context.Background(), second.ID); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}

			reopened := openTestStoreAt(t, backend, dir)
			notes = NewCollection(reopened, notes.spec)
			reloaded, err := notes.List(context.Background(), All[note]())
			if err != nil {
				t.Fatal(err)
			}
			if len(reloaded) != 1 || reloaded[0].Body != "updated" {
				t.Fatalf("reloaded = %#v", reloaded)
			}
		})
	}
}

func TestGenericCollectionTransactionPersistsMultipleCollections(t *testing.T) {
	type note struct {
		ID     domain.ID
		ChatID domain.ID
		Body   string
	}
	type marker struct {
		ID     domain.ID
		NoteID domain.ID
		Label  string
	}
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			dir := t.TempDir()
			st := openTestStoreAt(t, backend, dir)
			notes := NewCollection(st, CollectionSpec[note]{
				Namespace: "tx-notes",
				GetID:     func(v note) string { return v.ID },
				SetID:     func(v *note, id string) { v.ID = id },
				Indexes: []IndexSpec[note]{
					{Name: "chat", Value: func(v note) string { return v.ChatID }},
				},
			})
			markers := NewCollection(st, CollectionSpec[marker]{
				Namespace: "tx-markers",
				GetID:     func(v marker) string { return v.ID },
				SetID:     func(v *marker, id string) { v.ID = id },
				Indexes: []IndexSpec[marker]{
					{Name: "note", Value: func(v marker) string { return v.NoteID }},
				},
			})

			var inserted note
			if err := st.Transaction(context.Background(), func(tx *Tx) error {
				var err error
				inserted, err = notes.InsertTx(tx, context.Background(), note{ChatID: "chat-42", Body: "inside transaction"})
				if err != nil {
					return err
				}
				_, err = markers.InsertTx(tx, context.Background(), marker{NoteID: inserted.ID, Label: "linked"})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}

			reopened := openTestStoreAt(t, backend, dir)
			notes = NewCollection(reopened, notes.spec)
			markers = NewCollection(reopened, markers.spec)
			reloadedNotes, err := notes.List(context.Background(), ByIndex[note]("chat", "chat-42"))
			if err != nil {
				t.Fatal(err)
			}
			if len(reloadedNotes) != 1 || reloadedNotes[0].ID != inserted.ID {
				t.Fatalf("reloaded notes = %#v", reloadedNotes)
			}
			reloadedMarkers, err := markers.List(context.Background(), ByIndex[marker]("note", inserted.ID))
			if err != nil {
				t.Fatal(err)
			}
			if len(reloadedMarkers) != 1 || reloadedMarkers[0].Label != "linked" {
				t.Fatalf("reloaded markers = %#v", reloadedMarkers)
			}
		})
	}
}

func TestGlobalRuntimeStateLastWebBindRoundTrip(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			dir := t.TempDir()
			st := openTestStoreAt(t, backend, dir)
			if err := st.SetLastWebBind(context.Background(), "127.0.0.1:45678"); err != nil {
				t.Fatal(err)
			}
			state, err := st.GlobalRuntimeState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if state.ID == "" || state.LastWebBind != "127.0.0.1:45678" {
				t.Fatalf("unexpected runtime state: %#v", state)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}

			reopened := openTestStoreAt(t, backend, dir)
			state, err = reopened.GlobalRuntimeState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if state.LastWebBind != "127.0.0.1:45678" {
				t.Fatalf("unexpected reloaded runtime state: %#v", state)
			}
		})
	}
}

func TestApprovalAndTask(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionPermissionProfile(context.Background(), session.ID, "readonly"); err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionToolStates(context.Background(), session.ID, map[domain.ToolKind]bool{
				domain.ToolKindRead: true,
				domain.ToolKindBash: false,
			}); err != nil {
				t.Fatal(err)
			}
			task, err := st.AddTask(context.Background(), session.ID, "ship v1", domain.TaskStatusPending)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.UpdateTask(context.Background(), task.ID, domain.TaskStatusCompleted); err != nil {
				t.Fatal(err)
			}
			tasks, err := st.ListTasks(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(tasks) != 1 || tasks[0].Status != domain.TaskStatusCompleted {
				t.Fatalf("unexpected tasks: %#v", tasks)
			}
			sessions, err := st.ListSessions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || sessions[0].PermissionProfile != "readonly" {
				t.Fatalf("unexpected session profile: %#v", sessions)
			}
			if sessions[0].ToolStates[domain.ToolKindBash] {
				t.Fatalf("expected bash tool disabled in stored session, got %#v", sessions[0].ToolStates)
			}
		})
	}
}

func TestUpdateSessionTitle(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			generatedAt := time.Now().UTC().Truncate(time.Second)
			if err := st.UpdateSessionTitle(context.Background(), session.ID, "Short Helpful Session Title", generatedAt, 1); err != nil {
				t.Fatal(err)
			}
			sessions, err := st.ListSessions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := sessions[0].Title; got != "Short Helpful Session Title" {
				t.Fatalf("unexpected title: %q", got)
			}
			if got := sessions[0].TitleRefreshCount; got != 1 {
				t.Fatalf("unexpected title refresh count: %d", got)
			}
			if got := sessions[0].TitleGeneratedAt; !got.Equal(generatedAt) {
				t.Fatalf("unexpected title generated at: %v", got)
			}
		})
	}
}

func TestSetSessionProjectRootPersistsProjectRoot(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionProjectRoot(context.Background(), session.ID, "/repo"); err != nil {
				t.Fatal(err)
			}

			sessions, err := st.ListSessions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := sessions[0].ProjectRoot; got != "/repo" {
				t.Fatalf("expected project root persisted, got %q", got)
			}
		})
	}
}

func TestTouchSessionMarksSessionMostRecent(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			first, err := st.CreateSession(context.Background(), "first", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			second, err := st.CreateSession(context.Background(), "second", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Millisecond)
			touched, err := st.TouchSession(context.Background(), first.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !touched.UpdatedAt.After(second.UpdatedAt) {
				t.Fatalf("expected touched session to be newer than second session")
			}
			sessions, err := st.ListSessions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := sessions[0].ID; got != first.ID {
				t.Fatalf("expected touched session first, got %s", got)
			}
		})
	}
}

func TestCreateChatInheritsSessionPermissions(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionPermissionProfile(context.Background(), session.ID, "readonly"); err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionToolStates(context.Background(), session.ID, map[domain.ToolKind]bool{
				domain.ToolKindRead: true,
				domain.ToolKindBash: false,
			}); err != nil {
				t.Fatal(err)
			}
			mainChat, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			child, err := st.CreateChat(context.Background(), session.ID, "child", chatrole.Execution, &mainChat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if child.PermissionProfile != "readonly" {
				t.Fatalf("expected child chat to inherit session profile, got %q", child.PermissionProfile)
			}
			if enabled, ok := child.ToolStates[domain.ToolKindBash]; !ok || enabled {
				t.Fatalf("expected child chat to inherit disabled bash tool state, got %#v", child.ToolStates)
			}

			mainChat.PermissionProfile = "full-access"
			mainChat.ToolStates = map[domain.ToolKind]bool{
				domain.ToolKindRead: false,
				domain.ToolKindBash: true,
			}
			if err := st.UpdateChat(context.Background(), mainChat); err != nil {
				t.Fatal(err)
			}
			secondChild, err := st.CreateChat(context.Background(), session.ID, "child 2", chatrole.Execution, &mainChat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if secondChild.PermissionProfile != "readonly" {
				t.Fatalf("expected child chat to ignore parent profile and inherit session profile, got %q", secondChild.PermissionProfile)
			}
			if enabled, ok := secondChild.ToolStates[domain.ToolKindBash]; !ok || enabled {
				t.Fatalf("expected child chat to ignore parent tool states and inherit session tool states, got %#v", secondChild.ToolStates)
			}
		})
	}
}

func TestUpdateChatPersistsContextTokens(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			chat, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			chat.LastKnownContextTokens = 1234
			chat.ContextTokensKnown = true
			if err := st.UpdateChat(context.Background(), chat); err != nil {
				t.Fatal(err)
			}

			got, err := st.GetChat(context.Background(), chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.LastKnownContextTokens != 1234 || !got.ContextTokensKnown {
				t.Fatalf("expected persisted context tokens, got %#v", got)
			}
		})
	}
}

func TestChatOrderIsExplicitAndStableAcrossUpdates(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			first, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			second, err := st.CreateChat(context.Background(), session.ID, "second", chatrole.Orchestrator, nil)
			if err != nil {
				t.Fatal(err)
			}
			third, err := st.CreateChat(context.Background(), session.ID, "third", chatrole.Orchestrator, nil)
			if err != nil {
				t.Fatal(err)
			}

			ordered, err := st.ReorderChats(context.Background(), session.ID, []domain.ID{third.ID, first.ID, second.ID})
			if err != nil {
				t.Fatal(err)
			}
			if got := chatIDs(ordered); !slices.Equal(got, []domain.ID{third.ID, first.ID, second.ID}) {
				t.Fatalf("unexpected reordered ids: %#v", got)
			}

			first.Title = "updated after reorder"
			first.UpdatedAt = time.Now().UTC().Add(time.Hour)
			if err := st.UpdateChat(context.Background(), first); err != nil {
				t.Fatal(err)
			}
			listed, err := st.ListChats(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got := chatIDs(listed); !slices.Equal(got, []domain.ID{third.ID, first.ID, second.ID}) {
				t.Fatalf("chat update changed explicit order: %#v", got)
			}
			for idx, chat := range listed {
				if chat.Position != idx {
					t.Fatalf("expected position %d for %s, got %d", idx, chat.ID, chat.Position)
				}
			}
		})
	}
}

func TestDeleteChatRemovesChatTimelineAndApprovals(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			chat, err := st.CreateChat(context.Background(), session.ID, "side", chatrole.Execution, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.AppendTimeline(context.Background(), chat.ID, domain.Notice{Text: "hello"}); err != nil {
				t.Fatal(err)
			}
			if err := st.DeleteChat(context.Background(), chat.ID); err != nil {
				t.Fatalf("delete chat: %v", err)
			}
			if _, err := st.GetChat(context.Background(), chat.ID); err == nil {
				t.Fatal("expected deleted chat to be unavailable")
			}
			chats, err := st.ListChats(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range chats {
				if item.ID == chat.ID {
					t.Fatalf("deleted chat still listed: %#v", chats)
				}
			}
			timeline, err := st.TimelineForChat(context.Background(), chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(timeline) != 0 {
				t.Fatalf("expected timeline to be deleted, got %#v", timeline)
			}
			approvals, err := st.PendingApprovalsForChat(context.Background(), chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(approvals) != 0 {
				t.Fatalf("expected approvals to be deleted, got %#v", approvals)
			}
		})
	}
}

func TestSetChatModelPersistsOnChat(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			chat, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetChatModel(context.Background(), chat.ID, "other", "next"); err != nil {
				t.Fatalf("set chat model: %v", err)
			}
			updated, err := st.GetChat(context.Background(), chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.ProviderID != "other" || updated.ModelID != "next" {
				t.Fatalf("expected chat model other/next, got %s/%s", updated.ProviderID, updated.ModelID)
			}
			if _, err := st.GetSession(context.Background(), session.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeleteSessionRemovesOwnedData(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			st := openTestStore(t, backend)

			session, err := st.CreateSession(ctx, "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			mainChat, err := st.DefaultChat(ctx, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			sideChat, err := st.CreateChat(ctx, session.ID, "side", chatrole.Execution, &mainChat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.AppendTimeline(ctx, mainChat.ID, domain.UserMessage{Text: "hello"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.AppendTimeline(ctx, sideChat.ID, domain.Notice{Text: "side"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CreateChatApproval(ctx, sideChat.ID, domain.ToolKindBash, "echo ok"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.AddTask(ctx, session.ID, "task", domain.TaskStatusPending); err != nil {
				t.Fatal(err)
			}
			if _, err := st.SetMilestonePlan(ctx, session.ID, "summary", []Milestone{{Ref: "m1", Title: "M1"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.AddTodoItems(ctx, session.ID, "m1", []string{"todo"}); err != nil {
				t.Fatal(err)
			}

			if err := st.DeleteSession(ctx, session.ID); err != nil {
				t.Fatalf("delete session: %v", err)
			}
			if _, err := st.GetSession(ctx, session.ID); err == nil {
				t.Fatal("expected session to be deleted")
			}
			for _, chatID := range []domain.ID{mainChat.ID, sideChat.ID} {
				if _, err := st.GetChat(ctx, chatID); err == nil {
					t.Fatalf("expected chat %s to be deleted", chatID)
				}
				timeline, err := st.TimelineForChat(ctx, chatID)
				if err != nil {
					t.Fatal(err)
				}
				if len(timeline) != 0 {
					t.Fatalf("expected chat timeline to be deleted, got %#v", timeline)
				}
				approvals, err := st.Approvals().List(ctx, ByIndex[Approval]("chat", string(chatID)))
				if err != nil {
					t.Fatal(err)
				}
				if len(approvals) != 0 {
					t.Fatalf("expected chat approvals to be deleted, got %#v", approvals)
				}
			}
			tasks, err := st.ListTasks(ctx, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(tasks) != 0 {
				t.Fatalf("expected tasks to be deleted, got %#v", tasks)
			}
			todos, err := st.ListTodos(ctx, session.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(todos) != 0 {
				t.Fatalf("expected todos to be deleted, got %#v", todos)
			}
			plan, err := st.GetMilestonePlan(ctx, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Summary != "" || len(plan.Milestones) != 0 {
				t.Fatalf("expected milestone plan to be deleted, got %#v", plan)
			}
		})
	}
}

func TestToolApprovalStateIsDerivedFromTimeline(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			chat, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.AppendAssistantToolCalls(context.Background(), chat.ID, []domain.ToolCall{{
				ToolCallID: "call_1",
				Tool:       domain.ToolKindBash,
				Args:       map[string]string{"command": "echo hi"},
				Status:     domain.ToolStatusPending,
			}}, "", domain.Usage{}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.AttachToolApproval(context.Background(), chat.ID, "call_1", domain.ApprovalRequest{
				Body: "run echo hi",
			}); err != nil {
				t.Fatal(err)
			}
			pending, err := st.PendingApprovalsForChat(context.Background(), chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 || pending[0].ToolCallID != "call_1" {
				t.Fatalf("pending approvals = %#v", pending)
			}

			item, err := st.AttachToolResult(context.Background(), chat.ID, "call_1", domain.ToolResult{
				Text:   "hi\n",
				Status: domain.ToolResultStatusOK,
				Data:   domain.BashStoredResult{Command: "echo hi", Output: "hi\n"},
			})
			if err != nil {
				t.Fatal(err)
			}
			assistant, ok := item.Content.(domain.AssistantMessage)
			if !ok {
				t.Fatalf("item content = %T", item.Content)
			}
			call := assistant.ToolByID("call_1")
			if call == nil {
				t.Fatal("tool call missing")
			}
			if call.Status != domain.ToolStatusDone {
				t.Fatalf("tool status = %q", call.Status)
			}
			if call.Approval != nil || call.ApprovalID != "" {
				t.Fatalf("approval state = %#v/%q, want cleared", call.Approval, call.ApprovalID)
			}
			pending, err = st.PendingApprovalsForChat(context.Background(), chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 0 {
				t.Fatalf("pending approvals after result = %#v", pending)
			}
		})
	}
}

func TestFailInterruptedToolCallsMarksPendingAndRunningCallsErrored(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			chat, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.AppendAssistantToolCalls(context.Background(), chat.ID, []domain.ToolCall{
				{
					ToolCallID: "running",
					Tool:       domain.ToolKindBash,
					Args:       map[string]string{"command": "sleep 60"},
					Status:     domain.ToolStatusRunning,
				},
				{
					ToolCallID: "pending",
					Tool:       domain.ToolKindRead,
					Args:       map[string]string{"path": "README.md"},
					Status:     domain.ToolStatusPending,
				},
			}, "", domain.Usage{}); err != nil {
				t.Fatal(err)
			}

			count, err := st.FailInterruptedToolCalls(context.Background(), chat.ID, "restarted")
			if err != nil {
				t.Fatal(err)
			}
			if count != 2 {
				t.Fatalf("expected two failed interrupted calls, got %d", count)
			}
			items, err := st.TimelineForChat(context.Background(), chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			assistant, ok := items[len(items)-1].Content.(domain.AssistantMessage)
			if !ok {
				t.Fatalf("expected assistant item, got %T", items[len(items)-1].Content)
			}
			running := assistant.ToolByID("running")
			if running == nil || running.Status != domain.ToolStatusErrored || running.Error == nil || running.Error.Message != "restarted" {
				t.Fatalf("expected running call to be errored, got %#v", running)
			}
			pending := assistant.ToolByID("pending")
			if pending == nil || pending.Status != domain.ToolStatusErrored || pending.Error == nil || pending.Error.Message != "restarted" {
				t.Fatalf("expected pending call to be errored, got %#v", pending)
			}
		})
	}
}

func TestFailRunningToolCallsDoesNotFailPendingCalls(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			chat, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.AppendAssistantToolCalls(context.Background(), chat.ID, []domain.ToolCall{
				{
					ToolCallID: "running",
					Tool:       domain.ToolKindExecCommand,
					Args:       map[string]string{"cmd": "sleep 60"},
					Status:     domain.ToolStatusRunning,
				},
				{
					ToolCallID: "pending",
					Tool:       domain.ToolKindRead,
					Args:       map[string]string{"path": "README.md"},
					Status:     domain.ToolStatusPending,
				},
			}, "", domain.Usage{}); err != nil {
				t.Fatal(err)
			}

			count, err := st.FailRunningToolCalls(context.Background(), chat.ID, "stale running")
			if err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("expected one failed running call, got %d", count)
			}
			items, err := st.TimelineForChat(context.Background(), chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			assistant, ok := items[len(items)-1].Content.(domain.AssistantMessage)
			if !ok {
				t.Fatalf("expected assistant item, got %T", items[len(items)-1].Content)
			}
			running := assistant.ToolByID("running")
			if running == nil || running.Status != domain.ToolStatusErrored || running.Error == nil || running.Error.Message != "stale running" {
				t.Fatalf("expected running call to be errored, got %#v", running)
			}
			pending := assistant.ToolByID("pending")
			if pending == nil || pending.Status != domain.ToolStatusPending || pending.Error != nil {
				t.Fatalf("expected pending call to remain pending, got %#v", pending)
			}
		})
	}
}

func TestAddSessionPermissionRulePersistsAndReplacesByKey(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.AddSessionPermissionRule(context.Background(), session.ID, domain.PermissionOverride{
				Tool:    domain.ToolKindBash,
				Pattern: "git *",
				Action:  domain.PermissionModeAllow,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.AddSessionPermissionRule(context.Background(), session.ID, domain.PermissionOverride{
				Tool:    domain.ToolKindBash,
				Pattern: "git *",
				Action:  domain.PermissionModeAllow,
			}); err != nil {
				t.Fatal(err)
			}

			updated, err := st.GetSession(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(updated.PermissionRules) != 1 {
				t.Fatalf("expected one deduplicated permission rule, got %#v", updated.PermissionRules)
			}
			if updated.PermissionRules[0].Tool != domain.ToolKindBash || updated.PermissionRules[0].Pattern != "git *" {
				t.Fatalf("unexpected stored permission rule: %#v", updated.PermissionRules[0])
			}
		})
	}
}

func TestMilestonePlanAndTodosRoundTrip(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := st.SetMilestonePlan(context.Background(), session.ID, "Ship the feature", []Milestone{
				{Ref: "investigate", Title: "Investigate", Status: domain.MilestoneStatusCompleted, Position: 0},
				{Ref: "implement", Title: "Implement", Status: domain.MilestoneStatusReady, Position: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			if plan.Summary != "Ship the feature" || len(plan.Milestones) != 2 {
				t.Fatalf("unexpected milestone plan: %#v", plan)
			}
			items, err := st.AddTodoItems(context.Background(), session.ID, "implement", []string{"Write tests", "Fix bug"})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 2 || items[0].Position != 0 || items[1].Position != 1 {
				t.Fatalf("unexpected todo items: %#v", items)
			}
			if _, err := st.UpdateTodoItem(context.Background(), items[0].ID, domain.TodoStatusInProgress, "Write focused tests"); err != nil {
				t.Fatal(err)
			}
			gotPlan, err := st.GetMilestonePlan(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if gotPlan.Summary != "Ship the feature" || len(gotPlan.Milestones) != 2 {
				t.Fatalf("unexpected stored plan: %#v", gotPlan)
			}
			todos, err := st.ListTodos(context.Background(), session.ID, "implement")
			if err != nil {
				t.Fatal(err)
			}
			if len(todos) != 2 || todos[0].Status != domain.TodoStatusInProgress || todos[0].Content != "Write focused tests" {
				t.Fatalf("unexpected stored todos: %#v", todos)
			}
		})
	}
}

func TestForkSessionCopiesTranscriptAndParent(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionPermissionProfile(context.Background(), session.ID, "readonly"); err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionToolStates(context.Background(), session.ID, map[domain.ToolKind]bool{
				domain.ToolKindRead: true,
				domain.ToolKindBash: false,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionProjectRoot(context.Background(), session.ID, "/repo"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.SetMilestonePlan(context.Background(), session.ID, "Ship feature", []Milestone{
				{Ref: "implement", Title: "Implement", Status: domain.MilestoneStatusReady, Position: 0},
			}); err != nil {
				t.Fatal(err)
			}
			todos, err := st.AddTodoItems(context.Background(), session.ID, "implement", []string{"Write tests"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.UpdateTodoItem(context.Background(), todos[0].ID, domain.TodoStatusCompleted, "Write tests"); err != nil {
				t.Fatal(err)
			}
			chat, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			appendTimelineForTest(t, st, chat.ID, domain.UserMessage{Text: "hello"})

			forked, err := st.ForkSession(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if forked.ID == session.ID {
				t.Fatal("expected forked session to have distinct id")
			}
			if forked.ParentID == nil || *forked.ParentID != session.ID {
				t.Fatalf("expected parent id %s, got %#v", session.ID, forked.ParentID)
			}
			if forked.PermissionProfile != "readonly" {
				t.Fatalf("expected permission profile copied, got %q", forked.PermissionProfile)
			}
			if forked.ProjectRoot != "/repo" {
				t.Fatalf("expected workspace copied, got %#v", forked)
			}
			if forked.ToolStates[domain.ToolKindBash] {
				t.Fatalf("expected tool states copied, got %#v", forked.ToolStates)
			}
			plan, err := st.GetMilestonePlan(context.Background(), forked.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Milestones) != 1 || plan.Milestones[0].Ref != "implement" {
				t.Fatalf("expected milestone plan copied, got %#v", plan)
			}
			forkTodos, err := st.ListTodos(context.Background(), forked.ID, "implement")
			if err != nil {
				t.Fatal(err)
			}
			if len(forkTodos) != 1 || forkTodos[0].Status != domain.TodoStatusCompleted {
				t.Fatalf("expected todos copied, got %#v", forkTodos)
			}

			forkedChat, err := st.DefaultChat(context.Background(), forked.ID)
			if err != nil {
				t.Fatal(err)
			}
			items, err := st.TimelineForChat(context.Background(), forkedChat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 {
				t.Fatalf("unexpected forked timeline: %#v", items)
			}
			user, ok := items[0].Content.(domain.UserMessage)
			if !ok || user.Text != "hello" {
				t.Fatalf("unexpected forked timeline content: %#v", items[0].Content)
			}
		})
	}
}

func TestTimelinePutUpdatesAttachmentPayload(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			chat, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			item := appendTimelineForTest(t, st, chat.ID, domain.UserMessage{Attachments: []domain.Attachment{{Name: "note.txt", Path: "old"}}})
			user := item.Content.(domain.UserMessage)
			user.Attachments[0].Path = "new"
			item.Content = user
			if err := st.Timeline().Put(context.Background(), item); err != nil {
				t.Fatal(err)
			}
			items, err := st.TimelineForChat(context.Background(), chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			got := items[0].Content.(domain.UserMessage)
			if got.Attachments[0].Path != "new" {
				t.Fatalf("unexpected updated attachment: %#v", got.Attachments[0])
			}
		})
	}
}

func TestJSONFSWritesInspectableFiles(t *testing.T) {
	root := t.TempDir()
	st, err := OpenWithOptions(root, Options{Backend: BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "inspect", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "store-jsonfs-v6", "sessions", session.ID+".json")); err != nil {
		t.Fatalf("expected inspectable session JSON file: %v", err)
	}
}

func TestOpenResetsStoreWhenSchemaVersionChanges(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			st, err := OpenWithOptions(root, Options{Backend: backend})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.CreateSession(context.Background(), "old", "provider", "model", nil); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			writeStoreMetaForTest(t, root, backend, metaRecord{
				SchemaVersion: schemaVersion - 1,
				Encoding:      encodingJSON,
				Backend:       backend,
			})

			st, err = OpenWithOptions(root, Options{Backend: backend})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			sessions, err := st.ListSessions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 0 {
				t.Fatalf("expected old sessions to be cleared after schema reset, got %#v", sessions)
			}
			session, err := st.CreateSession(context.Background(), "new", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			if session.ID == "" {
				t.Fatalf("expected new id after schema reset, got %q", session.ID)
			}
		})
	}
}

func writeStoreMetaForTest(t *testing.T, root, backend string, meta metaRecord) {
	t.Helper()
	switch backend {
	case BackendJSONFS:
		if err := writeJSONFile(filepath.Join(root, "store-jsonfs-v6", "meta.json"), meta); err != nil {
			t.Fatal(err)
		}
	case BackendPebble:
		impl, err := openPebbleBackend(root)
		if err != nil {
			t.Fatal(err)
		}
		batch := impl.db.NewBatch()
		if err := impl.putMeta(batch, meta); err != nil {
			_ = batch.Close()
			_ = impl.Close()
			t.Fatal(err)
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			_ = batch.Close()
			_ = impl.Close()
			t.Fatal(err)
		}
		_ = batch.Close()
		if err := impl.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown backend %q", backend)
	}
}

func openTestStore(t *testing.T, backend string) *Store {
	t.Helper()
	return openTestStoreAt(t, backend, t.TempDir())
}

func openTestStoreAt(t *testing.T, backend, dir string) *Store {
	t.Helper()
	st, err := OpenWithOptions(dir, Options{Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}
