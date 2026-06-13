package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
)

func (c *Controller) DebugSessions(ctx context.Context, runtime debugsrv.RuntimeDebug) ([]debugsrv.SessionDebug, error) {
	if c == nil || c.agent == nil {
		return nil, fmt.Errorf("no chat agent")
	}
	owners := c.agent.LoadedSessions()
	out := make([]debugsrv.SessionDebug, 0, len(owners))
	for _, owner := range owners {
		debug, err := c.debugSessionFromOwner(ctx, owner, runtime)
		if err != nil {
			return nil, err
		}
		out = append(out, debug)
	}
	slices.SortFunc(out, func(a, b debugsrv.SessionDebug) int {
		if !a.Record.UpdatedAt.Equal(b.Record.UpdatedAt) {
			if a.Record.UpdatedAt.After(b.Record.UpdatedAt) {
				return -1
			}
			return 1
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return out, nil
}

func (c *Controller) DebugSession(ctx context.Context, sessionID id.ID, runtime debugsrv.RuntimeDebug) (debugsrv.SessionDetail, error) {
	owner, err := c.debugOwner(ctx, sessionID)
	if err != nil {
		return debugsrv.SessionDetail{}, err
	}
	debug, err := c.debugSessionFromOwner(ctx, owner, runtime)
	if err != nil {
		return debugsrv.SessionDetail{}, err
	}
	snapshot := owner.Snapshot()
	timeline, err := c.DefaultTranscript(ctx, sessionID, debugsrv.TranscriptOptions{All: true})
	if err != nil {
		timeline = nil
	}
	approvals, err := c.SessionApprovals(ctx, sessionID)
	if err != nil {
		return debugsrv.SessionDetail{}, err
	}
	return debugsrv.SessionDetail{
		Debug:       debug,
		Session:     snapshot.Session,
		Chats:       slices.Clone(snapshot.Chats),
		Timeline:    timeline,
		Approvals:   approvals,
		Plan:        snapshot.Plan,
		Tasks:       slices.Clone(snapshot.Tasks),
		LegacyTasks: slices.Clone(snapshot.LegacyTasks),
	}, nil
}

func (c *Controller) DebugChat(ctx context.Context, sessionID, chatID id.ID, runtime debugsrv.RuntimeDebug) (debugsrv.ChatDetail, error) {
	owner, chatRecord, err := c.debugChatRecord(ctx, sessionID, chatID)
	if err != nil {
		return debugsrv.ChatDetail{}, err
	}
	timeline, err := c.ChatTranscript(ctx, sessionID, chatID, debugsrv.TranscriptOptions{All: true})
	if err != nil {
		return debugsrv.ChatDetail{}, err
	}
	debug := c.debugChatFromOwnerSnapshot(owner.Snapshot(), chatRecord, timeline, runtime)
	return debugsrv.ChatDetail{
		Chat:             debug,
		Timeline:         timeline,
		LatestCompaction: debugsrv.LatestCompactionDebug(timeline),
		LatestUsage:      debugsrv.LatestUsageDebug(timeline),
	}, nil
}

func (c *Controller) ChatTranscript(ctx context.Context, sessionID, chatID id.ID, opts debugsrv.TranscriptOptions) ([]domain.TimelineItem, error) {
	owner, chatRecord, err := c.debugChatRecord(ctx, sessionID, chatID)
	if err != nil {
		return nil, err
	}
	page, err := owner.TimelinePage(ctx, chatRecord.ID, opts.Before, opts.Limit, opts.All || opts.Tail)
	if err != nil {
		return nil, err
	}
	items := slices.Clone(page.Items)
	if opts.Tail || opts.All || opts.Limit <= 0 || len(items) <= opts.Limit {
		return items, nil
	}
	return items[:opts.Limit], nil
}

func (c *Controller) DefaultTranscript(ctx context.Context, sessionID id.ID, opts debugsrv.TranscriptOptions) ([]domain.TimelineItem, error) {
	owner, err := c.debugOwner(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	chatRecord, err := debugDefaultChat(owner.Snapshot().Chats)
	if err != nil {
		return nil, err
	}
	return c.ChatTranscript(ctx, sessionID, chatRecord.ID, opts)
}

func (c *Controller) SessionApprovals(ctx context.Context, sessionID id.ID) ([]debugsrv.DebugApproval, error) {
	owner, err := c.debugOwner(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	snapshot := owner.Snapshot()
	var approvals []debugsrv.DebugApproval
	for _, chatRecord := range snapshot.Chats {
		timeline, err := c.ChatTranscript(ctx, sessionID, chatRecord.ID, debugsrv.TranscriptOptions{All: true})
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, debugsrv.PendingApprovalsFromTimeline(chatRecord, timeline)...)
	}
	return approvals, nil
}

func (c *Controller) Milestones(ctx context.Context, sessionID id.ID) (planning.Plan, error) {
	owner, err := c.debugOwner(ctx, sessionID)
	if err != nil {
		return planning.Plan{}, err
	}
	return owner.Snapshot().Plan, nil
}

func (c *Controller) Tasks(ctx context.Context, sessionID id.ID) ([]planning.Task, error) {
	owner, err := c.debugOwner(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return slices.Clone(owner.Snapshot().Tasks), nil
}

func (c *Controller) LegacyTasks(ctx context.Context, sessionID id.ID) ([]planning.LegacyTask, error) {
	owner, err := c.debugOwner(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return slices.Clone(owner.Snapshot().LegacyTasks), nil
}

func (c *Controller) ResolveRewindAnchor(ctx context.Context, sessionID, chatID id.ID, selector string) (id.ID, error) {
	if strings.TrimSpace(selector) == "" {
		selector = "first_compaction_error"
	}
	if selector != "first_compaction_error" {
		return "", fmt.Errorf("unsupported rewind selector %q", selector)
	}
	timeline, err := c.ChatTranscript(ctx, sessionID, chatID, debugsrv.TranscriptOptions{All: true})
	if err != nil {
		return "", err
	}
	for _, item := range timeline {
		compaction, ok := item.Content.(domain.Compaction)
		if ok && compaction.Status == "failed" {
			return item.ID, nil
		}
	}
	return "", fmt.Errorf("chat %s has no failed compaction item", chatID)
}

func (c *Controller) debugOwner(_ context.Context, sessionID id.ID) (*sessionpkg.Session, error) {
	if c == nil || c.agent == nil {
		return nil, fmt.Errorf("no chat agent")
	}
	for _, owner := range c.agent.LoadedSessions() {
		if owner == nil {
			continue
		}
		if owner.Snapshot().Session.ID == sessionID {
			return owner, nil
		}
	}
	return nil, fmt.Errorf("session %s is not loaded", sessionID)
}

func (c *Controller) debugChatRecord(ctx context.Context, sessionID, chatID id.ID) (*sessionpkg.Session, domain.Chat, error) {
	if chatID == "" {
		return nil, domain.Chat{}, fmt.Errorf("invalid chat id")
	}
	owner, err := c.debugOwner(ctx, sessionID)
	if err != nil {
		return nil, domain.Chat{}, err
	}
	snapshot := owner.Snapshot()
	for _, chatRecord := range snapshot.Chats {
		if chatRecord.ID == chatID {
			return owner, chatRecord, nil
		}
	}
	return nil, domain.Chat{}, fmt.Errorf("chat %s does not belong to session %s", chatID, sessionID)
}

func (c *Controller) debugSessionFromOwner(ctx context.Context, owner *sessionpkg.Session, runtime debugsrv.RuntimeDebug) (debugsrv.SessionDebug, error) {
	snapshot := owner.Snapshot()
	selectedSessions, _ := selectedDebugClientCounts(runtime.Clients)
	out := debugsrv.SessionDebug{
		ID:                  snapshot.Session.ID,
		Title:               snapshot.Session.Title,
		ProjectRoot:         snapshot.Session.ProjectRoot,
		Hydration:           "live",
		Hydrated:            true,
		StoredChatCount:     len(snapshot.Chats),
		HydratedChatCount:   len(snapshot.Snapshots),
		SelectedClientCount: selectedSessions[snapshot.Session.ID],
		Record:              snapshot.Session,
		Chats:               make([]debugsrv.SessionChatDebug, 0, len(snapshot.Chats)),
	}
	for _, chatRecord := range snapshot.Chats {
		if chatRecord.Archived {
			out.ArchivedChatCount++
		} else {
			out.VisibleChatCount++
		}
		timeline, err := c.ChatTranscript(ctx, snapshot.Session.ID, chatRecord.ID, debugsrv.TranscriptOptions{All: true})
		if err != nil {
			timeline = nil
		}
		out.Chats = append(out.Chats, c.debugChatFromOwnerSnapshot(snapshot, chatRecord, timeline, runtime))
	}
	return out, nil
}

func (c *Controller) debugChatFromOwnerSnapshot(snapshot sessionpkg.SessionSnapshot, chatRecord domain.Chat, timeline []domain.TimelineItem, runtime debugsrv.RuntimeDebug) debugsrv.SessionChatDebug {
	_, selectedChats := selectedDebugClientCounts(runtime.Clients)
	runtimeByChat := debugRuntimeChatsByID(runtime)
	queueLen := len(chatRecord.QueuedInputs)
	approvals := debugsrv.PendingApprovalsFromTimeline(chatRecord, timeline)
	chatSnapshot, hydrated := snapshot.Snapshots[chatRecord.ID]
	hydration := "stored"
	if hydrated {
		hydration = "live"
	}
	out := debugsrv.SessionChatDebug{
		ID:                         chatRecord.ID,
		SessionID:                  chatRecord.SessionID,
		Title:                      chatRecord.Title,
		WorkflowRole:               string(chatRecord.WorkflowRole),
		Archived:                   chatRecord.Archived,
		Hydration:                  hydration,
		Hydrated:                   hydrated,
		QueueLen:                   queueLen,
		TimelineCount:              len(timeline),
		PendingApprovals:           len(approvals),
		PendingExecutableToolCalls: pendingExecutableToolCalls(timeline),
		SelectedClientCount:        selectedChats[chatRecord.ID],
		LastKnownContextTokens:     chatRecord.LastKnownContextTokens,
		ContextTokensKnown:         chatRecord.ContextTokensKnown,
		LastMessage:                chatRecord.LastMessage,
	}
	if hydrated {
		out.QueueLen = len(chatSnapshot.QueuedInputs)
		out.PendingApprovals = len(chatSnapshot.Approvals)
	}
	if runtime, ok := runtimeByChat[chatRecord.ID]; ok {
		out.Runtime = &runtime
		out.QueueLen = runtime.QueueLen
		out.PendingApprovals = runtime.PendingApprovals
		if runtime.Busy && runtime.Status == "running_tools" && runtime.RunningToolCalls == 0 {
			out.Diagnostics = append(out.Diagnostics, "live runtime reports running_tools with no running tool calls")
		}
	}
	return out
}

func debugDefaultChat(chats []domain.Chat) (domain.Chat, error) {
	for _, chatRecord := range chats {
		if chatRecord.ParentChatID == nil {
			return chatRecord, nil
		}
	}
	if len(chats) > 0 {
		return chats[0], nil
	}
	return domain.Chat{}, fmt.Errorf("session has no chats")
}

func selectedDebugClientCounts(clients []debugsrv.ClientDebug) (map[id.ID]int, map[id.ID]int) {
	sessions := map[id.ID]int{}
	chats := map[id.ID]int{}
	for _, client := range clients {
		if !client.Connected {
			continue
		}
		if client.SelectedSession != "" {
			sessions[client.SelectedSession]++
		}
		if client.SelectedChat != "" {
			chats[client.SelectedChat]++
		}
	}
	return sessions, chats
}

func debugRuntimeChatsByID(runtime debugsrv.RuntimeDebug) map[id.ID]debugsrv.ChatDebug {
	out := make(map[id.ID]debugsrv.ChatDebug, len(runtime.Chats))
	for _, chat := range runtime.Chats {
		out[chat.ID] = chat
	}
	return out
}

func pendingExecutableToolCalls(timeline []domain.TimelineItem) int {
	for i := len(timeline) - 1; i >= 0; i-- {
		message, ok := timeline[i].Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		var count int
		for _, call := range message.Tools {
			if call.Status == domain.ToolStatusPending && call.Result == nil && call.Error == nil && call.Approval == nil && call.ApprovalID == "" {
				count++
			}
		}
		return count
	}
	return 0
}
