package chat

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/turncontrol"
)

type QueueKind string

const (
	QueueKindSteer    QueueKind = "steer"
	QueueKindQueued   QueueKind = "queue"
	QueueKindContinue QueueKind = "continue"
)

type QueueItem struct {
	Kind        QueueKind
	Text        string
	Attachments []attachment.Draft
	References  []reference.Draft
	Note        string
}

type Status string

const (
	StatusIdle              Status = "idle"
	StatusWaitingLLM        Status = "waiting_llm"
	StatusStreamingThoughts Status = "streaming_thoughts"
	StatusStreamingResponse Status = "streaming_response"
	StatusRunningTools      Status = "running_tools"
	StatusWaitingApproval   Status = "waiting_approval"
	StatusErrored           Status = "error"
)

type CancelState string

const (
	CancelStateNone       CancelState = ""
	CancelStateCancelling CancelState = "cancelling"
)

type Snapshot struct {
	Session          domain.Session
	Chat             domain.Chat
	Timeline         []domain.TimelineItem
	Approvals        []store.Approval
	QueuedInputs     []domain.QueuedInput
	PendingAssistant PendingAssistantTurn
	Status           Status
	StatusText       string
	Context          domain.ContextUsage
	Active           bool
}

type Update struct {
	Event             *domain.Event
	Snapshot          Snapshot
	Status            Status
	StatusText        string
	Queue             []domain.QueuedInput
	Context           domain.ContextUsage
	Active            bool
	TranscriptChanged bool
	QueueChanged      bool
	StatusChanged     bool
	ContextChanged    bool
	ApprovalsChanged  bool
}

type Chat struct {
	store   *store.Store
	engine  Runner
	onClose func(int64)

	mu          sync.RWMutex
	session     domain.Session
	chat        domain.Chat
	state       *ChatState
	status      Status
	statusText  string
	active      bool
	cancel      context.CancelFunc
	queue       []domain.QueuedInput
	queueNotes  map[int64]string
	cancelState CancelState
	running     map[string]struct{}
	closed      bool

	inbox   chan any
	subsMu  sync.Mutex
	subs    map[int]chan Update
	nextSub int
}

type enqueueCmd struct {
	item QueueItem
}

type replaceQueueCmd struct {
	items []domain.QueuedInput
}

type dispatchQueuedCmd struct {
	item      domain.QueuedInput
	remaining []domain.QueuedInput
}

type interruptCmd struct{}
type approveCmd struct {
	approvalID int64
	rule       *domain.PermissionOverride
}
type denyCmd struct {
	approvalID int64
}
type closeCmd struct{}
type streamEventCmd struct {
	event domain.Event
}
type streamClosedCmd struct{}

type continueRunner interface {
	RunContinueInChat(context.Context, domain.Session, domain.Chat, string) (<-chan domain.Event, error)
}

type approvalRunner interface {
	ApproveInChat(context.Context, int64, int64, int64) (<-chan domain.Event, error)
	ApproveInChatWithRule(context.Context, int64, int64, int64, domain.PermissionOverride) (<-chan domain.Event, error)
	DenyInChat(context.Context, int64, int64, int64) (<-chan domain.Event, error)
}

type compactRunner interface {
	CompactChat(context.Context, int64, int64) (<-chan domain.Event, error)
}

type promptRunner interface {
	RunPromptInChat(context.Context, domain.Session, domain.Chat, string, []attachment.Draft, []reference.Draft, string) (<-chan domain.Event, error)
}

// Runner provides the shared execution behavior used by a live Chat.
type Runner interface {
	promptRunner
	continueRunner
	approvalRunner
}

// Load builds a live chat by hydrating its timeline and approval state from store.
func Load(ctx context.Context, st *store.Store, session domain.Session, chatRecord domain.Chat, runner Runner, onClose func(int64)) (*Chat, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	if chatRecord.ID == 0 {
		return nil, fmt.Errorf("chat id is required")
	}
	if chatRecord.SessionID == 0 {
		loaded, err := st.GetChat(ctx, chatRecord.ID)
		if err != nil {
			return nil, err
		}
		chatRecord = loaded
	}
	timeline, err := st.TimelineForChat(ctx, chatRecord.ID)
	if err != nil {
		return nil, err
	}
	approvals, err := st.PendingApprovalsForChat(ctx, chatRecord.ID)
	if err != nil {
		return nil, err
	}
	return New(session, chatRecord, timeline, approvals, runner, st, onClose)
}

// New builds a live chat from hydrated persisted state.
func New(session domain.Session, chatRecord domain.Chat, timeline []domain.TimelineItem, approvals []store.Approval, runner Runner, st *store.Store, onClose func(int64)) (*Chat, error) {
	if chatRecord.ID == 0 {
		return nil, fmt.Errorf("chat id is required")
	}
	c := &Chat{
		store:      st,
		engine:     runner,
		onClose:    onClose,
		session:    session,
		chat:       chatRecord,
		state:      NewTimelineState(chatRecord, timeline, approvals),
		status:     StatusIdle,
		queue:      cloneQueuedInputs(chatRecord.QueuedInputs),
		queueNotes: map[int64]string{},
		inbox:      make(chan any, 64),
		subs:       map[int]chan Update{},
	}
	go c.loop()
	c.inbox <- struct{}{}
	return c, nil
}

// Persist writes the current chat snapshot and remaps optimistic in-memory IDs to durable store IDs.
func (r *Chat) Persist(ctx context.Context, st *store.Store) error {
	if st == nil {
		st = r.store
	}
	if st == nil {
		return nil
	}
	r.mu.RLock()
	chatRecord := r.chat
	if r.state == nil {
		r.mu.RUnlock()
		return st.UpdateChat(ctx, chatRecord)
	}
	timeline := r.state.SnapshotTimeline()
	approvals := r.state.Approvals()
	r.mu.RUnlock()

	persisted, err := st.TimelineForChat(ctx, chatRecord.ID)
	if err != nil {
		return r.markPersistError(err)
	}
	itemIDs := make(map[int64]struct{}, len(persisted))
	for _, item := range persisted {
		itemIDs[item.ID] = struct{}{}
	}

	itemRemap := map[int64]int64{}
	changed := false
	for _, item := range timeline {
		if item.ChatID == 0 {
			item.ChatID = chatRecord.ID
		}
		if _, ok := itemIDs[item.ID]; ok {
			if err := st.PutTimelineItem(ctx, item); err != nil {
				return r.markPersistError(err)
			}
			continue
		}
		oldID := item.ID
		if isTemporaryTimelineID(oldID) {
			item.ID = 0
		}
		durable, err := st.InsertTimelineItem(ctx, item)
		if err != nil {
			return r.markPersistError(err)
		}
		if oldID != durable.ID {
			itemRemap[oldID] = durable.ID
		}
		changed = true
	}
	for _, approval := range approvals {
		if approval.ID <= 0 {
			continue
		}
		if _, err := st.GetApproval(ctx, approval.ID); err == nil {
			if err := st.UpdateApproval(ctx, approval.ID, approval.Status); err != nil {
				return r.markPersistError(err)
			}
		}
	}
	chatRecord.QueuedInputs = cloneQueuedInputs(r.snapshotQueue())
	if err := st.UpdateChat(ctx, chatRecord); err != nil {
		return r.markPersistError(err)
	}
	if changed {
		r.remapTimelineIDs(itemRemap)
		r.broadcast(r.snapshotUpdateFlags(nil, true, false, false, true, false))
	}
	return nil
}

func isTemporaryTimelineID(id int64) bool {
	return id < 0 || id > 1_000_000_000_000
}

func (r *Chat) Enqueue(item QueueItem) {
	r.inbox <- enqueueCmd{item: item}
}

func (r *Chat) ReplaceQueue(items []domain.QueuedInput) {
	r.inbox <- replaceQueueCmd{items: cloneQueuedInputs(items)}
}

func (r *Chat) DispatchQueued(item domain.QueuedInput, remaining []domain.QueuedInput) {
	cloned := item
	cloned.Attachments = append([]domain.QueuedAttachment(nil), item.Attachments...)
	cloned.References = append([]domain.QueuedReference(nil), item.References...)
	r.inbox <- dispatchQueuedCmd{item: cloned, remaining: cloneQueuedInputs(remaining)}
}

func (r *Chat) Cancel() {
	r.mu.RLock()
	cancel := r.cancel
	stagedToolCancel := r.status == StatusRunningTools && len(r.running) > 0 && r.cancelState != CancelStateCancelling
	r.mu.RUnlock()
	if cancel != nil && !stagedToolCancel {
		cancel()
	}
	select {
	case r.inbox <- interruptCmd{}:
	default:
	}
}

func (r *Chat) Interrupt() {
	r.Cancel()
}

func (r *Chat) Approve(approvalID int64) {
	r.inbox <- approveCmd{approvalID: approvalID}
}

func (r *Chat) ApproveWithRule(approvalID int64, rule domain.PermissionOverride) {
	r.inbox <- approveCmd{approvalID: approvalID, rule: &rule}
}

func (r *Chat) Deny(approvalID int64) {
	r.inbox <- denyCmd{approvalID: approvalID}
}

func (r *Chat) Compact() error {
	runner, ok := r.engine.(compactRunner)
	if !ok {
		return fmt.Errorf("compaction is not supported by runner")
	}
	r.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.cancelState = CancelStateNone
	r.running = map[string]struct{}{}
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Compacting session..."
	sessionID := r.session.ID
	chatID := r.chat.ID
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, false, false))
	events, err := runner.CompactChat(ctx, sessionID, chatID)
	r.handleApprovalEventStream(events, err)
	return nil
}

func (r *Chat) Close() {
	r.inbox <- closeCmd{}
}

func (r *Chat) Status() (Status, string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status, r.statusText, r.active
}

func (r *Chat) ContextSize() domain.ContextUsage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return domain.ContextUsage{}
	}
	return r.state.CurrentContextSize()
}

func (r *Chat) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return Snapshot{Session: r.session, Chat: r.chat, Status: r.status, StatusText: r.statusText, Active: r.active}
	}
	return Snapshot{
		Session:          r.session,
		Chat:             r.state.Chat(),
		Timeline:         r.state.SnapshotTimeline(),
		Approvals:        r.state.Approvals(),
		QueuedInputs:     cloneQueuedInputs(r.queue),
		PendingAssistant: r.state.PendingAssistant(),
		Status:           r.status,
		StatusText:       r.statusText,
		Context:          r.state.CurrentContextSize(),
		Active:           r.active,
	}
}

func (r *Chat) Subscribe() (<-chan Update, func()) {
	ch := make(chan Update, 64)
	r.subsMu.Lock()
	id := r.nextSub
	r.nextSub++
	r.subs[id] = ch
	r.subsMu.Unlock()
	ch <- r.snapshotUpdate(nil)
	unsub := func() {
		r.subsMu.Lock()
		if existing, ok := r.subs[id]; ok {
			delete(r.subs, id)
			close(existing)
		}
		r.subsMu.Unlock()
	}
	return ch, unsub
}

func (r *Chat) loop() {
	for cmd := range r.inbox {
		switch typed := cmd.(type) {
		case enqueueCmd:
			r.handleEnqueue(typed.item)
		case replaceQueueCmd:
			r.handleReplaceQueue(typed.items)
		case dispatchQueuedCmd:
			r.handleDispatchQueued(typed.item, typed.remaining)
		case interruptCmd:
			r.handleInterrupt()
		case approveCmd:
			r.handleApprove(typed.approvalID, typed.rule)
		case denyCmd:
			r.handleDeny(typed.approvalID)
		case streamEventCmd:
			r.handleStreamEvent(typed.event)
		case streamClosedCmd:
			r.handleStreamClosed()
		case closeCmd:
			r.handleClose()
			return
		default:
			r.maybeDispatchNext()
		}
	}
}

func (r *Chat) handleEnqueue(item QueueItem) {
	queued := queuedInputFromItem(item)
	if note := strings.TrimSpace(item.Note); note != "" {
		r.mu.Lock()
		r.queueNotes[queued.ID] = note
		r.mu.Unlock()
	}
	r.handleAppendQueuedInput(queued)
}

func (r *Chat) handleAppendQueuedInput(queued domain.QueuedInput) {
	r.mu.Lock()
	r.queue = append(r.queue, queued)
	r.chat.QueuedInputs = cloneQueuedInputs(r.queue)
	if r.state != nil {
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.QueuedInputs = cloneQueuedInputs(r.queue)
		})
	}
	r.mu.Unlock()
	_ = r.persistQueue()
	r.broadcast(r.snapshotUpdateFlags(nil, false, true, false, false, false))
	r.maybeDispatchNext()
}

func (r *Chat) handleReplaceQueue(items []domain.QueuedInput) {
	r.mu.Lock()
	r.queue = cloneQueuedInputs(items)
	r.chat.QueuedInputs = cloneQueuedInputs(r.queue)
	if r.state != nil {
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.QueuedInputs = cloneQueuedInputs(r.queue)
		})
	}
	r.mu.Unlock()
	_ = r.persistQueue()
	r.broadcast(r.snapshotUpdateFlags(nil, false, true, false, false, false))
	r.maybeDispatchNext()
}

func (r *Chat) handleDispatchQueued(item domain.QueuedInput, remaining []domain.QueuedInput) {
	r.mu.Lock()
	if r.active || r.status == StatusWaitingApproval {
		r.queue = cloneQueuedInputs(remaining)
		r.chat.QueuedInputs = cloneQueuedInputs(r.queue)
		if r.state != nil {
			r.state.UpdateChat(func(chat *domain.Chat) {
				chat.QueuedInputs = cloneQueuedInputs(r.queue)
			})
		}
		r.mu.Unlock()
		_ = r.persistQueue()
		r.broadcast(r.snapshotUpdateFlags(nil, false, true, false, false, false))
		return
	}
	r.queue = cloneQueuedInputs(remaining)
	r.chat.QueuedInputs = cloneQueuedInputs(r.queue)
	if r.state != nil {
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.QueuedInputs = cloneQueuedInputs(r.queue)
			chat.LastKnownContextTokens = r.chat.LastKnownContextTokens
			chat.ContextTokensKnown = r.chat.ContextTokensKnown
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = turncontrol.WithShouldStop(ctx, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cancelState == CancelStateCancelling
	})
	r.cancel = cancel
	r.cancelState = CancelStateNone
	r.running = map[string]struct{}{}
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Waiting for LLM response"
	session := r.session
	chat := r.chat
	r.mu.Unlock()

	r.appendOptimisticUserMessage(item, session, chat)
	_ = r.persistQueue()
	r.broadcast(r.snapshotUpdateFlags(nil, item.Kind != domain.QueuedInputKindContinue, true, true, true, false))
	r.runItem(ctx, session, chat, item)
}

func (r *Chat) handleInterrupt() {
	r.mu.Lock()
	if r.status == StatusRunningTools && len(r.running) > 0 {
		if r.cancelState != CancelStateCancelling {
			r.cancelState = CancelStateCancelling
			r.appendRuntimeNoticeLocked("Cancelling. Tool calls running, waiting for completition. Press ESC again to cancel tool calls.", "interrupt_pending", "warning")
			r.statusText = "Cancelling..."
			r.mu.Unlock()
			r.broadcast(r.snapshotUpdateFlags(nil, true, false, true, false, false))
			return
		}
	}
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *Chat) handleApprove(approvalID int64, rule *domain.PermissionOverride) {
	runner, ok := r.engine.(approvalRunner)
	if !ok {
		err := fmt.Errorf("approval is not supported by runner")
		evt := domain.Event{Kind: domain.EventKindError, Err: err}
		r.broadcast(r.snapshotUpdateFlags(&evt, false, false, true, false, false))
		return
	}
	r.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Waiting for LLM response"
	sessionID := r.session.ID
	chatID := r.chat.ID
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, false, false))

	var (
		events <-chan domain.Event
		err    error
	)
	if rule != nil {
		events, err = runner.ApproveInChatWithRule(ctx, sessionID, chatID, approvalID, *rule)
	} else {
		events, err = runner.ApproveInChat(ctx, sessionID, chatID, approvalID)
	}
	r.handleApprovalEventStream(events, err)
}

func (r *Chat) handleDeny(approvalID int64) {
	runner, ok := r.engine.(approvalRunner)
	if !ok {
		err := fmt.Errorf("approval is not supported by runner")
		evt := domain.Event{Kind: domain.EventKindError, Err: err}
		r.broadcast(r.snapshotUpdateFlags(&evt, false, false, true, false, false))
		return
	}
	r.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Waiting for LLM response"
	sessionID := r.session.ID
	chatID := r.chat.ID
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, false, false))

	events, err := runner.DenyInChat(ctx, sessionID, chatID, approvalID)
	r.handleApprovalEventStream(events, err)
}

func (r *Chat) handleApprovalEventStream(events <-chan domain.Event, err error) {
	if err != nil {
		r.mu.Lock()
		r.active = false
		r.cancel = nil
		r.status = StatusErrored
		r.statusText = err.Error()
		r.mu.Unlock()
		evt := domain.Event{Kind: domain.EventKindError, Err: err}
		r.broadcast(r.snapshotUpdateFlags(&evt, false, false, true, false, false))
		return
	}
	go func() {
		for evt := range events {
			r.inbox <- streamEventCmd{event: evt}
		}
		r.inbox <- streamClosedCmd{}
	}()
}

func (r *Chat) handleStreamEvent(evt domain.Event) {
	r.mu.Lock()
	transcriptChanged := false
	contextChanged := false
	if evt.Item.ID != 0 && r.state != nil {
		r.state.UpsertTimelineItem(evt.Item)
		transcriptChanged = true
		if text := timelineItemSummary(evt.Item); text != "" {
			r.chat.LastMessage = text
			r.state.UpdateChat(func(chat *domain.Chat) {
				chat.LastMessage = text
			})
		}
	}
	switch evt.Kind {
	case domain.EventKindMessageDelta:
		if r.state != nil {
			if err := r.state.AppendAssistantText(r.chat.ID, evt.Text); err != nil {
				r.status = StatusErrored
				r.statusText = err.Error()
			}
		}
		r.status = StatusStreamingResponse
		r.statusText = "Streaming LLM response ..."
		transcriptChanged = true
		contextChanged = true
	case domain.EventKindReasoning:
		if r.state != nil {
			if err := r.state.AppendAssistantReasoning(r.chat.ID, evt.Text); err != nil {
				r.status = StatusErrored
				r.statusText = err.Error()
			}
		}
		r.status = StatusStreamingThoughts
		r.statusText = "Streaming thoughts ..."
		transcriptChanged = true
		contextChanged = true
	case domain.EventKindUsage:
		if contextTokens, ok := evt.Usage.ContextTokens(); ok {
			r.chat.LastKnownContextTokens = contextTokens
			r.chat.ContextTokensKnown = true
			if r.state != nil {
				r.state.UpdateChat(func(chat *domain.Chat) {
					chat.LastKnownContextTokens = contextTokens
					chat.ContextTokensKnown = true
				})
			}
			contextChanged = true
		}
	case domain.EventKindToolStart:
		if r.running == nil {
			r.running = map[string]struct{}{}
		}
		if strings.TrimSpace(evt.ToolCallID) != "" {
			r.running[evt.ToolCallID] = struct{}{}
		}
		r.status = StatusRunningTools
		r.statusText = strings.TrimSpace(evt.Text)
		if r.statusText == "" {
			r.statusText = "Running tool"
		}
	case domain.EventKindToolResult:
		if strings.TrimSpace(evt.ToolCallID) != "" {
			delete(r.running, evt.ToolCallID)
		}
	case domain.EventKindApprovalAsk:
		r.status = StatusWaitingApproval
		r.statusText = strings.TrimSpace(evt.Text)
		r.active = false
		r.cancel = nil
		r.cancelState = CancelStateNone
		r.running = nil
	case domain.EventKindError:
		r.status = StatusErrored
		if evt.Err != nil {
			r.statusText = evt.Err.Error()
		} else {
			r.statusText = strings.TrimSpace(evt.Text)
		}
		if r.state != nil {
			r.state.ClearPendingAssistant()
		}
		r.active = false
		r.cancel = nil
		r.cancelState = CancelStateNone
		r.running = nil
	case domain.EventKindMessageDone:
		if r.state != nil {
			r.state.SealActiveAssistant("")
			r.state.ClearPendingAssistant()
			contextChanged = true
		}
		r.cancelState = CancelStateNone
		r.running = nil
	case domain.EventKindStatus:
		if afterTokens, ok := completedCompactionContext(evt.Item, evt.Meta); ok {
			r.chat.LastKnownContextTokens = afterTokens
			r.chat.ContextTokensKnown = false
			if r.state != nil {
				r.state.UpdateChat(func(chat *domain.Chat) {
					chat.LastKnownContextTokens = afterTokens
					chat.ContextTokensKnown = false
				})
			}
			contextChanged = true
		}
	}
	r.mu.Unlock()
	copyEvt := evt
	r.broadcast(r.snapshotUpdateFlags(&copyEvt, transcriptChanged || evt.Kind == domain.EventKindMessageDone, false, true, contextChanged, evt.Kind == domain.EventKindApprovalAsk || evt.Kind == domain.EventKindApprovalReply))
}

func completedCompactionContext(item domain.TimelineItem, meta map[string]string) (int, bool) {
	if meta["compaction"] != "completed" {
		return 0, false
	}
	payload, ok := item.Content.(domain.Compaction)
	if !ok || payload.AfterContextTokens <= 0 {
		return 0, false
	}
	return payload.AfterContextTokens, true
}

func timelineItemSummary(item domain.TimelineItem) string {
	switch payload := item.Content.(type) {
	case domain.UserMessage:
		return strings.TrimSpace(payload.Text)
	case domain.AssistantMessage:
		if text := strings.TrimSpace(payload.Text); text != "" {
			return text
		}
		if len(payload.Tools) == 1 {
			return "tool:" + string(payload.Tools[0].Tool)
		}
		if len(payload.Tools) > 1 {
			return fmt.Sprintf("tools:%d", len(payload.Tools))
		}
	case domain.Notice:
		return strings.TrimSpace(payload.Text)
	case domain.Compaction:
		return strings.TrimSpace(payload.Summary)
	}
	return ""
}

func (r *Chat) handleStreamClosed() {
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel = nil
	}
	shouldDispatch := r.status != StatusWaitingApproval
	if r.status != StatusErrored && r.status != StatusWaitingApproval {
		r.active = false
		r.status = StatusIdle
		r.statusText = "Idle"
		if r.state != nil {
			r.state.ClearPendingAssistant()
		}
	}
	r.cancelState = CancelStateNone
	r.running = nil
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, true, false))
	if shouldDispatch {
		r.maybeDispatchNext()
	}
}

func (r *Chat) handleClose() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	cancel := r.cancel
	r.cancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.subsMu.Lock()
	for id, ch := range r.subs {
		delete(r.subs, id)
		close(ch)
	}
	r.subsMu.Unlock()
	if r.onClose != nil {
		r.onClose(r.chat.ID)
	}
}

func (r *Chat) maybeDispatchNext() {
	r.mu.Lock()
	if r.active || r.status == StatusWaitingApproval {
		r.mu.Unlock()
		return
	}
	idx := nextDispatchableIndex(r.queue)
	if idx < 0 {
		r.mu.Unlock()
		return
	}
	item := r.queue[idx]
	r.queue = append(r.queue[:idx], r.queue[idx+1:]...)
	r.chat.QueuedInputs = cloneQueuedInputs(r.queue)
	if r.state != nil {
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.QueuedInputs = cloneQueuedInputs(r.queue)
			chat.LastKnownContextTokens = r.chat.LastKnownContextTokens
			chat.ContextTokensKnown = r.chat.ContextTokensKnown
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = turncontrol.WithShouldStop(ctx, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cancelState == CancelStateCancelling
	})
	r.cancel = cancel
	r.cancelState = CancelStateNone
	r.running = map[string]struct{}{}
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Waiting for LLM response"
	session := r.session
	chat := r.chat
	r.mu.Unlock()

	r.appendOptimisticUserMessage(item, session, chat)
	_ = r.persistQueue()
	r.broadcast(r.snapshotUpdateFlags(nil, item.Kind != domain.QueuedInputKindContinue, true, true, true, false))
	r.runItem(ctx, session, chat, item)
}

func (r *Chat) runItem(ctx context.Context, session domain.Session, chat domain.Chat, item domain.QueuedInput) {
	r.mu.Lock()
	note := r.queueNotes[item.ID]
	delete(r.queueNotes, item.ID)
	r.mu.Unlock()
	var (
		events <-chan domain.Event
		err    error
	)
	switch item.Kind {
	case domain.QueuedInputKindContinue:
		runner, ok := r.engine.(continueRunner)
		if !ok {
			err = fmt.Errorf("continue is not supported by runner")
		} else {
			events, err = runner.RunContinueInChat(ctx, session, chat, note)
		}
	default:
		events, err = r.engine.RunPromptInChat(ctx, session, chat, item.Text, queuedAttachmentDrafts(item.Attachments), queuedReferenceDrafts(item.References), note)
	}
	if err != nil {
		r.mu.Lock()
		r.active = false
		r.cancel = nil
		r.status = StatusErrored
		r.statusText = err.Error()
		r.mu.Unlock()
		evt := domain.Event{Kind: domain.EventKindError, Err: err}
		r.broadcast(r.snapshotUpdateFlags(&evt, false, false, true, false, false))
		r.maybeDispatchNext()
		return
	}
	go func() {
		for evt := range events {
			r.inbox <- streamEventCmd{event: evt}
		}
		r.inbox <- streamClosedCmd{}
	}()
}

func (r *Chat) persistQueue() error {
	if r.store == nil || r.chat.ID == 0 {
		return nil
	}
	r.mu.RLock()
	items := cloneQueuedInputs(r.queue)
	r.mu.RUnlock()
	return r.store.SetChatQueuedInputs(context.Background(), r.chat.ID, items)
}

func (r *Chat) markPersistError(err error) error {
	r.mu.Lock()
	r.status = StatusErrored
	r.statusText = err.Error()
	r.active = false
	r.mu.Unlock()
	evt := domain.Event{Kind: domain.EventKindError, Err: err}
	r.broadcast(r.snapshotUpdateFlags(&evt, false, false, true, false, false))
	return err
}

func (r *Chat) remapTimelineIDs(items map[int64]int64) {
	if len(items) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == nil {
		return
	}
	for _, record := range r.state.timeline {
		if record == nil {
			continue
		}
		oldID := record.Item.ID
		next, ok := items[oldID]
		if !ok {
			continue
		}
		delete(r.state.byItem, oldID)
		record.Item.ID = next
		r.state.byItem[next] = record
	}
}

func (r *Chat) snapshotStatus() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *Chat) snapshotStatusText() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.statusText
}

func (r *Chat) snapshotQueue() []domain.QueuedInput {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneQueuedInputs(r.queue)
}

func (r *Chat) snapshotActive() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

func (r *Chat) broadcast(update Update) {
	r.subsMu.Lock()
	defer r.subsMu.Unlock()
	for _, ch := range r.subs {
		select {
		case ch <- update:
		default:
		}
	}
}

func (r *Chat) snapshotUpdate(event *domain.Event) Update {
	return r.snapshotUpdateFlags(event, false, false, false, false, false)
}

func (r *Chat) snapshotUpdateFlags(event *domain.Event, transcriptChanged, queueChanged, statusChanged, contextChanged, approvalsChanged bool) Update {
	snapshot := r.Snapshot()
	return Update{
		Event:             event,
		Snapshot:          snapshot,
		Status:            snapshot.Status,
		StatusText:        snapshot.StatusText,
		Queue:             cloneQueuedInputs(snapshot.QueuedInputs),
		Context:           snapshot.Context,
		Active:            snapshot.Active,
		TranscriptChanged: transcriptChanged,
		QueueChanged:      queueChanged,
		StatusChanged:     statusChanged,
		ContextChanged:    contextChanged,
		ApprovalsChanged:  approvalsChanged,
	}
}

func (r *Chat) appendOptimisticUserMessage(item domain.QueuedInput, session domain.Session, chat domain.Chat) {
	_ = session
	if item.Kind == domain.QueuedInputKindContinue || r.state == nil {
		return
	}
	now := time.Now().UTC()
	summary := strings.TrimSpace(item.Text)
	user := domain.UserMessage{Text: summary}
	for _, draft := range item.Attachments {
		user.Attachments = append(user.Attachments, domain.Attachment{
			ID: draft.ID, Name: draft.Name, MIME: draft.MIME, Path: draft.Path, Size: draft.Size, Source: draft.Source, Original: draft.Original,
		})
	}
	for _, ref := range item.References {
		user.References = append(user.References, domain.Reference{
			Kind: ref.Kind, Path: ref.Path, Display: ref.Display, Start: ref.Start, End: ref.End,
		})
	}
	r.mu.Lock()
	timelineItem := domain.TimelineItem{
		ID:        now.UnixNano(),
		ChatID:    chat.ID,
		Seq:       int64(len(r.state.Timeline()) + 1),
		Content:   user,
		CreatedAt: now,
		UpdatedAt: now,
		SealedAt:  now,
	}
	r.state.AppendTimelineItem(timelineItem)
	r.chat.LastMessage = summary
	r.state.UpdateChat(func(chat *domain.Chat) {
		chat.LastMessage = summary
	})
	r.mu.Unlock()
}

func (r *Chat) appendRuntimeNoticeLocked(body, kind, severity string) {
	if r.state == nil {
		return
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	now := time.Now().UTC()
	r.state.AppendTimelineItem(domain.TimelineItem{
		ID:        -now.UnixNano(),
		ChatID:    r.chat.ID,
		Seq:       int64(len(r.state.Timeline()) + 1),
		Content:   domain.Notice{Text: body, Kind: kind, Level: severity},
		CreatedAt: now,
		UpdatedAt: now,
		SealedAt:  now,
	})
}

func nextDispatchableIndex(items []domain.QueuedInput) int {
	priority := []domain.QueuedInputKind{
		domain.QueuedInputKindSteer,
		domain.QueuedInputKindRejectedSteer,
		domain.QueuedInputKindContinue,
		domain.QueuedInputKindQueued,
	}
	for _, kind := range priority {
		for idx, item := range items {
			if item.Held {
				continue
			}
			if item.Kind == kind {
				return idx
			}
		}
	}
	return -1
}

func queuedInputFromItem(item QueueItem) domain.QueuedInput {
	kind := domain.QueuedInputKindQueued
	switch item.Kind {
	case QueueKindSteer:
		kind = domain.QueuedInputKindSteer
	case QueueKindContinue:
		kind = domain.QueuedInputKindContinue
	}
	return domain.QueuedInput{
		ID:          time.Now().UTC().UnixNano(),
		Kind:        kind,
		Text:        strings.TrimSpace(item.Text),
		Attachments: queuedAttachmentsFromDrafts(item.Attachments),
		References:  queuedReferencesFromDrafts(item.References),
		CreatedAt:   time.Now().UTC(),
	}
}

func cloneQueuedInputs(src []domain.QueuedInput) []domain.QueuedInput {
	if len(src) == 0 {
		return nil
	}
	dst := make([]domain.QueuedInput, 0, len(src))
	for _, item := range src {
		cloned := item
		cloned.Attachments = append([]domain.QueuedAttachment(nil), item.Attachments...)
		cloned.References = append([]domain.QueuedReference(nil), item.References...)
		dst = append(dst, cloned)
	}
	return dst
}

func queuedAttachmentsFromDrafts(src []attachment.Draft) []domain.QueuedAttachment {
	if len(src) == 0 {
		return nil
	}
	dst := make([]domain.QueuedAttachment, 0, len(src))
	for _, draft := range src {
		dst = append(dst, domain.QueuedAttachment{
			ID:       draft.ID,
			Name:     draft.Name,
			MIME:     draft.MIME,
			Path:     draft.Path,
			Size:     draft.Size,
			Source:   draft.Source,
			Original: draft.Original,
		})
	}
	return dst
}

func queuedAttachmentDrafts(src []domain.QueuedAttachment) []attachment.Draft {
	if len(src) == 0 {
		return nil
	}
	dst := make([]attachment.Draft, 0, len(src))
	for _, item := range src {
		dst = append(dst, attachment.Draft{
			Metadata: attachment.Metadata{
				ID:       item.ID,
				Name:     item.Name,
				MIME:     item.MIME,
				Path:     item.Path,
				Size:     item.Size,
				Source:   item.Source,
				Original: item.Original,
			},
		})
	}
	return dst
}

func queuedReferencesFromDrafts(src []reference.Draft) []domain.QueuedReference {
	if len(src) == 0 {
		return nil
	}
	dst := make([]domain.QueuedReference, 0, len(src))
	for _, draft := range src {
		dst = append(dst, domain.QueuedReference{
			Kind:    string(draft.Kind),
			Path:    draft.Path,
			Display: draft.Display,
			Start:   draft.Start,
			End:     draft.End,
		})
	}
	return dst
}

func queuedReferenceDrafts(src []domain.QueuedReference) []reference.Draft {
	if len(src) == 0 {
		return nil
	}
	dst := make([]reference.Draft, 0, len(src))
	for _, item := range src {
		dst = append(dst, reference.Draft{
			Kind:    reference.Kind(item.Kind),
			Path:    item.Path,
			Display: item.Display,
			Start:   item.Start,
			End:     item.End,
		})
	}
	return dst
}
