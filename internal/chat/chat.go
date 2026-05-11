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
	Messages         []domain.Message
	Parts            map[int64][]domain.Part
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

// Load builds a live chat by hydrating its transcript and approval state from store.
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
	messages, parts, err := st.PartsForChat(ctx, chatRecord.ID)
	if err != nil {
		return nil, err
	}
	approvals, err := st.PendingApprovalsForChat(ctx, chatRecord.ID)
	if err != nil {
		return nil, err
	}
	return New(session, chatRecord, messages, parts, approvals, runner, st, onClose)
}

// New builds a live chat from hydrated persisted state.
func New(session domain.Session, chatRecord domain.Chat, messages []domain.Message, parts map[int64][]domain.Part, approvals []store.Approval, runner Runner, st *store.Store, onClose func(int64)) (*Chat, error) {
	if chatRecord.ID == 0 {
		return nil, fmt.Errorf("chat id is required")
	}
	c := &Chat{
		store:      st,
		engine:     runner,
		onClose:    onClose,
		session:    session,
		chat:       chatRecord,
		state:      NewChatState(chatRecord, messages, parts, approvals),
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
	messages := r.state.SnapshotMessages()
	parts := r.state.SnapshotParts()
	approvals := r.state.Approvals()
	r.mu.RUnlock()

	persistedMessages, persistedParts, err := st.PartsForChat(ctx, chatRecord.ID)
	if err != nil {
		return r.markPersistError(err)
	}
	messageIDs := make(map[int64]struct{}, len(persistedMessages))
	partIDs := map[int64]struct{}{}
	for _, msg := range persistedMessages {
		messageIDs[msg.ID] = struct{}{}
		for _, part := range persistedParts[msg.ID] {
			partIDs[part.ID] = struct{}{}
		}
	}

	messageRemap := map[int64]int64{}
	partRemap := map[int64]int64{}
	changed := false
	for _, msg := range messages {
		if _, ok := messageIDs[msg.ID]; ok {
			if err := st.UpdateMessageSummary(ctx, msg.ID, msg.Summary); err != nil {
				return r.markPersistError(err)
			}
			for _, part := range parts[msg.ID] {
				if _, ok := partIDs[part.ID]; ok {
					if err := st.UpdatePartPayload(ctx, part.ID, part.Payload); err != nil {
						return r.markPersistError(err)
					}
				}
			}
			continue
		}
		durable, err := st.AddChatMessage(ctx, chatRecord.ID, msg.Role, msg.Summary)
		if err != nil {
			return r.markPersistError(err)
		}
		messageRemap[msg.ID] = durable.ID
		changed = true
		for _, part := range parts[msg.ID] {
			durablePart, err := st.AddPart(ctx, durable.ID, part.Payload)
			if err != nil {
				return r.markPersistError(err)
			}
			partRemap[part.ID] = durablePart.ID
		}
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
		r.remapStateIDs(messageRemap, partRemap)
		r.broadcast(r.snapshotUpdateFlags(nil, true, false, false, true, false))
	}
	return nil
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
		Messages:         r.state.SnapshotMessages(),
		Parts:            r.state.SnapshotParts(),
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
	if evt.Message.ID > 0 && r.state != nil {
		r.state.UpsertMessageParts(evt.Message, evt.Parts)
		transcriptChanged = true
		if strings.TrimSpace(evt.Message.Summary) != "" {
			r.chat.LastMessage = evt.Message.Summary
			r.state.UpdateChat(func(chat *domain.Chat) {
				chat.LastMessage = evt.Message.Summary
			})
		}
	}
	switch evt.Kind {
	case domain.EventKindMessageDelta:
		if r.state != nil {
			r.state.AppendPendingAssistantText(evt.Text)
		}
		r.status = StatusStreamingResponse
		r.statusText = "Streaming LLM response ..."
		transcriptChanged = true
		contextChanged = true
	case domain.EventKindReasoning:
		if r.state != nil {
			r.state.AppendPendingAssistantReasoning(evt.Text)
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
		if r.state != nil && evt.Message.ID > 0 {
			r.state.ClearPendingAssistant()
			contextChanged = true
		}
		r.cancelState = CancelStateNone
		r.running = nil
	case domain.EventKindStatus:
		if afterTokens, ok := completedCompactionContext(evt.Parts, evt.Meta); ok {
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

func completedCompactionContext(parts []domain.Part, meta map[string]string) (int, bool) {
	if meta["compaction"] != "completed" {
		return 0, false
	}
	for _, part := range parts {
		payload, ok := part.Payload.(domain.CompactionPayload)
		if !ok {
			continue
		}
		if payload.AfterContextTokens <= 0 {
			return 0, false
		}
		return payload.AfterContextTokens, true
	}
	return 0, false
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

func (r *Chat) remapStateIDs(messages map[int64]int64, parts map[int64]int64) {
	if len(messages) == 0 && len(parts) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == nil {
		return
	}
	for _, record := range r.state.messages {
		if record == nil {
			continue
		}
		oldMessageID := record.Message.ID
		if next, ok := messages[oldMessageID]; ok {
			delete(r.state.byMessage, oldMessageID)
			record.Message.ID = next
			r.state.byMessage[next] = record
		}
		for _, partRecord := range record.Parts {
			if partRecord == nil {
				continue
			}
			oldPartID := partRecord.Part.ID
			if next, ok := parts[oldPartID]; ok {
				delete(r.state.byPart, oldPartID)
				partRecord.Part.ID = next
				partRecord.Part.MessageID = record.Message.ID
				r.state.byPart[next] = partRecord
			}
		}
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
	if item.Kind == domain.QueuedInputKindContinue || r.state == nil {
		return
	}
	now := time.Now().UTC()
	messageID := now.UnixNano()
	summary := strings.TrimSpace(item.Text)
	message := domain.Message{
		ID:        messageID,
		SessionID: session.ID,
		ChatID:    chat.ID,
		Role:      domain.MessageRoleUser,
		Summary:   summary,
		CreatedAt: now,
	}
	parts := make([]domain.Part, 0, 1+len(item.Attachments)+len(item.References))
	if summary != "" {
		parts = append(parts, domain.Part{
			ID:        messageID + 1,
			MessageID: messageID,
			Kind:      domain.PartKindText,
			Payload:   domain.TextPayload{Text: summary},
			Body:      summary,
			CreatedAt: now,
		})
	}
	for idx, draft := range item.Attachments {
		parts = append(parts, domain.Part{
			ID:        messageID + int64(2+idx),
			MessageID: messageID,
			Kind:      domain.PartKindAttachment,
			Payload: domain.AttachmentPayload{
				ID: draft.ID, Name: draft.Name, MIME: draft.MIME, Path: draft.Path, Size: draft.Size, Source: draft.Source, Original: draft.Original,
			},
			Body:      draft.Name,
			CreatedAt: now,
		})
	}
	for idx, ref := range item.References {
		parts = append(parts, domain.Part{
			ID:        messageID + int64(2+len(item.Attachments)+idx),
			MessageID: messageID,
			Kind:      domain.PartKindReference,
			Payload: domain.ReferencePayload{
				Kind: ref.Kind, Path: ref.Path, Display: ref.Display, Start: ref.Start, End: ref.End,
			},
			Body:      ref.Display,
			CreatedAt: now,
		})
	}
	r.mu.Lock()
	r.state.AppendMessage(message, parts)
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
	messageID := now.UnixNano()
	message := domain.Message{
		ID:        messageID,
		SessionID: r.session.ID,
		ChatID:    r.chat.ID,
		Role:      domain.MessageRoleAssistant,
		Summary:   body,
		CreatedAt: now,
	}
	parts := []domain.Part{{
		ID:        messageID + 1,
		MessageID: messageID,
		Kind:      domain.PartKindEventNotice,
		Payload:   domain.EventNoticePayload{Text: body, Kind: kind, Severity: severity},
		Body:      body,
		CreatedAt: now,
	}}
	r.state.AppendMessage(message, parts)
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
