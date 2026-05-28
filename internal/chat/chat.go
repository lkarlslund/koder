package chat

import (
	"context"
	"fmt"
	"slices"
	"strconv"
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
	ExecProcesses    []domain.ExecProcess
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
	onClose func(domain.ID)

	mu          sync.RWMutex
	session     domain.Session
	chat        domain.Chat
	state       *ChatState
	status      Status
	statusText  string
	active      bool
	cancel      context.CancelFunc
	queue       []domain.QueuedInput
	queueNotes  map[domain.ID]string
	cancelState CancelState
	running     map[string]struct{}
	draining    bool
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

type reorderQueueCmd struct {
	ids []domain.ID
}

type deleteQueueItemCmd struct {
	id domain.ID
}

type sendQueueItemNowCmd struct {
	id domain.ID
}

type dispatchQueuedCmd struct {
	item      domain.QueuedInput
	remaining []domain.QueuedInput
}

type interruptCmd struct{}
type resumePendingToolsCmd struct{}
type approveCmd struct {
	toolCallID string
	rule       *domain.PermissionOverride
}
type denyCmd struct {
	toolCallID string
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
	ApproveToolInChat(context.Context, domain.ID, domain.ID, string) (<-chan domain.Event, error)
	ApproveToolInChatWithRule(context.Context, domain.ID, domain.ID, string, domain.PermissionOverride) (<-chan domain.Event, error)
	DenyToolInChat(context.Context, domain.ID, domain.ID, string) (<-chan domain.Event, error)
}

type compactRunner interface {
	CompactChat(context.Context, domain.ID, domain.ID) (<-chan domain.Event, error)
}

type pendingToolRunner interface {
	ResumePendingToolCallsInChat(context.Context, domain.Session, domain.Chat) (<-chan domain.Event, error)
}

type promptRunner interface {
	RunPromptInChat(context.Context, domain.Session, domain.Chat, string, []attachment.Draft, []reference.Draft, string) (<-chan domain.Event, error)
}

type turnPromptRunner interface {
	RunPromptTurn(context.Context, *TurnState, string, []attachment.Draft, []reference.Draft, string) (<-chan domain.Event, error)
}

type turnContinueRunner interface {
	RunContinueTurn(context.Context, *TurnState, string) (<-chan domain.Event, error)
}

type turnApprovalRunner interface {
	ApproveToolTurn(context.Context, *TurnState, string) (<-chan domain.Event, error)
	ApproveToolTurnWithRule(context.Context, *TurnState, string, domain.PermissionOverride) (<-chan domain.Event, error)
	DenyToolTurn(context.Context, *TurnState, string) (<-chan domain.Event, error)
}

// Runner provides the shared execution behavior used by a live Chat.
type Runner interface {
	promptRunner
	continueRunner
	approvalRunner
}

// Load builds a live chat by hydrating its timeline and approval state from store.
func Load(ctx context.Context, st *store.Store, session domain.Session, chatRecord domain.Chat, runner Runner, onClose func(domain.ID)) (*Chat, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	if chatRecord.ID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	if chatRecord.SessionID == "" {
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
func New(session domain.Session, chatRecord domain.Chat, timeline []domain.TimelineItem, approvals []store.Approval, runner Runner, st *store.Store, onClose func(domain.ID)) (*Chat, error) {
	if chatRecord.ID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	status := StatusIdle
	statusText := ""
	if len(approvals) > 0 {
		status = StatusWaitingApproval
		statusText = "Waiting for approval"
	}
	c := &Chat{
		store:      st,
		engine:     runner,
		onClose:    onClose,
		session:    session,
		chat:       chatRecord,
		state:      NewTimelineState(chatRecord, timeline, approvals),
		status:     status,
		statusText: statusText,
		queue:      cloneQueuedInputs(chatRecord.QueuedInputs),
		queueNotes: map[domain.ID]string{},
		inbox:      make(chan any, 64),
		subs:       map[int]chan Update{},
	}
	go c.loop()
	c.inbox <- resumePendingToolsCmd{}
	c.inbox <- struct{}{}
	return c, nil
}

// TurnState exposes the live chat state for an active model turn.
type TurnState struct {
	chat *Chat
}

func (r *Chat) turnState() *TurnState {
	return &TurnState{chat: r}
}

// Session returns the live session metadata for the turn.
func (t *TurnState) Session() domain.Session {
	if t == nil || t.chat == nil {
		return domain.Session{}
	}
	t.chat.mu.RLock()
	defer t.chat.mu.RUnlock()
	return t.chat.session
}

// Chat returns the live chat metadata for the turn.
func (t *TurnState) Chat() domain.Chat {
	if t == nil || t.chat == nil {
		return domain.Chat{}
	}
	t.chat.mu.RLock()
	defer t.chat.mu.RUnlock()
	return t.chat.chat
}

// Timeline returns the live transcript snapshot for the turn.
func (t *TurnState) Timeline() []domain.TimelineItem {
	if t == nil || t.chat == nil {
		return nil
	}
	t.chat.mu.RLock()
	defer t.chat.mu.RUnlock()
	if t.chat.state == nil {
		return nil
	}
	return t.chat.state.SnapshotTimeline()
}

// AppendUserMessage records a user message in the live transcript.
func (t *TurnState) AppendUserMessage(ctx context.Context, user domain.UserMessage) (domain.TimelineItem, error) {
	if t == nil || t.chat == nil {
		return domain.TimelineItem{}, fmt.Errorf("turn state is required")
	}
	r := t.chat
	now := time.Now().UTC()
	text := strings.TrimSpace(user.Text)
	r.mu.Lock()
	seq := int64(1)
	if r.state != nil {
		seq = int64(len(r.state.Timeline()) + 1)
	}
	item := domain.TimelineItem{
		ID:        domain.NewTimelineID(now),
		ChatID:    r.chat.ID,
		Seq:       seq,
		Content:   user,
		CreatedAt: now,
		UpdatedAt: now,
		SealedAt:  now,
	}
	if r.state != nil {
		r.state.AppendTimelineItem(item)
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.LastMessage = text
		})
	}
	r.chat.LastMessage = text
	chatRecord := r.chat
	r.mu.Unlock()

	if r.store != nil {
		if _, err := r.store.InsertTimelineItem(ctx, item); err != nil {
			return domain.TimelineItem{}, err
		}
		if err := r.store.UpdateChat(ctx, chatRecord); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	return item, nil
}

// NextAssistantItem returns the next live assistant timeline identity.
func (t *TurnState) NextAssistantItem() domain.TimelineItem {
	if t == nil || t.chat == nil {
		return domain.TimelineItem{}
	}
	now := time.Now().UTC()
	t.chat.mu.RLock()
	defer t.chat.mu.RUnlock()
	seq := int64(1)
	if t.chat.state != nil {
		seq = int64(len(t.chat.state.Timeline()) + 1)
	}
	return domain.TimelineItem{
		ID:        domain.NewTimelineID(now),
		ChatID:    t.chat.chat.ID,
		Seq:       seq,
		Content:   domain.AssistantMessage{},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// UpsertTimelineItem records a timeline item in live memory and storage.
func (t *TurnState) UpsertTimelineItem(ctx context.Context, item domain.TimelineItem) (domain.TimelineItem, error) {
	if t == nil || t.chat == nil {
		return domain.TimelineItem{}, fmt.Errorf("turn state is required")
	}
	r := t.chat
	if item.ChatID == "" {
		r.mu.RLock()
		item.ChatID = r.chat.ID
		r.mu.RUnlock()
	}
	r.mu.Lock()
	if item.Seq == 0 && r.state != nil {
		item.Seq = int64(len(r.state.Timeline()) + 1)
	}
	if r.state != nil {
		r.state.UpsertTimelineItem(item)
		if text := timelineItemSummary(item); text != "" {
			r.chat.LastMessage = text
			r.state.UpdateChat(func(chat *domain.Chat) {
				chat.LastMessage = text
			})
		}
	}
	chatRecord := r.chat
	r.mu.Unlock()

	if r.store != nil {
		if err := r.store.PutTimelineItem(ctx, item); err != nil {
			return domain.TimelineItem{}, err
		}
		if err := r.store.UpdateChat(ctx, chatRecord); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	return item, nil
}

// ApplyNextSteer removes and records the next queued steer message.
func (t *TurnState) ApplyNextSteer(ctx context.Context) (domain.TimelineItem, bool, error) {
	if t == nil || t.chat == nil {
		return domain.TimelineItem{}, false, nil
	}
	r := t.chat
	now := time.Now().UTC()
	r.mu.Lock()
	idx := -1
	for i, item := range r.queue {
		if item.Held || item.Kind != domain.QueuedInputKindSteer {
			continue
		}
		idx = i
		break
	}
	if idx < 0 {
		r.mu.Unlock()
		return domain.TimelineItem{}, false, nil
	}
	queued := r.queue[idx]
	r.queue = append(slices.Clone(r.queue[:idx]), slices.Clone(r.queue[idx+1:])...)
	r.chat.QueuedInputs = cloneQueuedInputs(r.queue)
	user := domain.UserMessage{Text: strings.TrimSpace(queued.Text)}
	for _, draft := range queued.Attachments {
		user.Attachments = append(user.Attachments, domain.Attachment(draft))
	}
	for _, ref := range queued.References {
		user.References = append(user.References, domain.Reference(ref))
	}
	seq := int64(1)
	if r.state != nil {
		seq = int64(len(r.state.Timeline()) + 1)
	}
	item := domain.TimelineItem{
		ID:        domain.NewTimelineID(now),
		ChatID:    r.chat.ID,
		Seq:       seq,
		Content:   user,
		CreatedAt: now,
		UpdatedAt: now,
		SealedAt:  now,
	}
	if r.state != nil {
		r.state.AppendTimelineItem(item)
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.QueuedInputs = cloneQueuedInputs(r.queue)
			chat.LastMessage = user.Text
		})
	}
	r.chat.LastMessage = user.Text
	chatRecord := r.chat
	queue := cloneQueuedInputs(r.queue)
	r.mu.Unlock()

	if r.store != nil {
		if _, err := r.store.InsertTimelineItem(ctx, item); err != nil {
			return domain.TimelineItem{}, false, err
		}
		if err := r.store.SetChatQueuedInputs(ctx, r.chat.ID, queue); err != nil {
			return domain.TimelineItem{}, false, err
		}
		if err := r.store.UpdateChat(ctx, chatRecord); err != nil {
			return domain.TimelineItem{}, false, err
		}
	}
	return item, true, nil
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
	r.mu.RUnlock()

	persisted, err := st.TimelineForChat(ctx, chatRecord.ID)
	if err != nil {
		return r.markPersistError(err)
	}
	itemIDs := make(map[string]struct{}, len(persisted))
	for _, item := range persisted {
		itemIDs[item.ID] = struct{}{}
	}

	changed := false
	for _, item := range timeline {
		if item.ChatID == "" {
			item.ChatID = chatRecord.ID
		}
		if _, ok := itemIDs[item.ID]; ok {
			if err := st.PutTimelineItem(ctx, item); err != nil {
				return r.markPersistError(err)
			}
			continue
		}
		if _, err := st.InsertTimelineItem(ctx, item); err != nil {
			return r.markPersistError(err)
		}
		changed = true
	}
	chatRecord.QueuedInputs = cloneQueuedInputs(r.snapshotQueue())
	if err := st.UpdateChat(ctx, chatRecord); err != nil {
		return r.markPersistError(err)
	}
	if changed {
		r.broadcast(r.snapshotUpdateFlags(nil, true, false, false, true, false))
	}
	return nil
}

func (r *Chat) Enqueue(item QueueItem) {
	r.inbox <- enqueueCmd{item: item}
}

func (r *Chat) ReorderQueue(ids []domain.ID) {
	r.inbox <- reorderQueueCmd{ids: slices.Clone(ids)}
}

func (r *Chat) DeleteQueueItem(id domain.ID) {
	r.inbox <- deleteQueueItemCmd{id: id}
}

func (r *Chat) SendQueueItemNow(id domain.ID) {
	r.inbox <- sendQueueItemNowCmd{id: id}
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

func (r *Chat) StopAfterCurrentTurn() {
	r.mu.Lock()
	if r.closed || !r.active {
		r.mu.Unlock()
		return
	}
	r.draining = true
	r.statusText = "Stopping after current turn"
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, false, false))
}

func (r *Chat) Interrupt() {
	r.Cancel()
}

func (r *Chat) Approve(approvalID domain.ID) {
	r.inbox <- approveCmd{toolCallID: fmt.Sprint(approvalID)}
}

func (r *Chat) ApproveTool(toolCallID string) {
	r.inbox <- approveCmd{toolCallID: strings.TrimSpace(toolCallID)}
}

func (r *Chat) ApproveWithRule(approvalID domain.ID, rule domain.PermissionOverride) {
	r.inbox <- approveCmd{toolCallID: fmt.Sprint(approvalID), rule: &rule}
}

func (r *Chat) Deny(approvalID domain.ID) {
	r.inbox <- denyCmd{toolCallID: fmt.Sprint(approvalID)}
}

func (r *Chat) DenyTool(toolCallID string) {
	r.inbox <- denyCmd{toolCallID: strings.TrimSpace(toolCallID)}
}

func (r *Chat) Compact() error {
	runner, ok := r.engine.(compactRunner)
	if !ok {
		return fmt.Errorf("compaction is not supported by runner")
	}
	r.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = turncontrol.WithShouldStop(ctx, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cancelState == CancelStateCancelling || r.draining
	})
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

// DrainAndClose waits for the active turn to reach a persisted boundary, then closes the chat.
func (r *Chat) DrainAndClose(ctx context.Context) error {
	return r.closeAfterDrain(ctx, "")
}

// InterruptAndClose cancels active work, records why it was interrupted, and closes the chat.
func (r *Chat) InterruptAndClose(ctx context.Context, reason string) error {
	return r.closeAfterDrain(ctx, strings.TrimSpace(reason))
}

func (r *Chat) closeAfterDrain(ctx context.Context, interruptReason string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	wasActive := r.active
	r.draining = true
	if r.active {
		if interruptReason == "" {
			r.statusText = "Stopping after current turn"
		} else {
			r.statusText = "Interrupting..."
			r.cancelState = CancelStateCancelling
		}
	}
	cancel := r.cancel
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, false, false))
	if interruptReason != "" && cancel != nil {
		cancel()
	}

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		r.mu.RLock()
		active := r.active
		r.mu.RUnlock()
		if !active {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	if wasActive && interruptReason != "" {
		if err := r.appendPersistedInterruptNotice(context.Background(), interruptReason); err != nil {
			return err
		}
	}
	if err := r.Persist(context.Background(), nil); err != nil {
		return err
	}
	r.Close()
	return nil
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

// SetSession replaces the live session metadata used for future chat runs.
func (r *Chat) SetSession(session domain.Session) {
	r.mu.Lock()
	r.session = session
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, false, false, false))
}

// SetChat replaces the live chat metadata used for future snapshots.
func (r *Chat) SetChat(chat domain.Chat) {
	r.mu.Lock()
	r.chat = chat
	if r.state != nil {
		r.state.SetChat(chat)
	}
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, false, false, false))
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
		case reorderQueueCmd:
			r.handleReorderQueue(typed.ids)
		case deleteQueueItemCmd:
			r.handleDeleteQueueItem(typed.id)
		case sendQueueItemNowCmd:
			r.handleSendQueueItemNow(typed.id)
		case dispatchQueuedCmd:
			r.handleDispatchQueued(typed.item, typed.remaining)
		case interruptCmd:
			r.handleInterrupt()
		case resumePendingToolsCmd:
			r.handleResumePendingTools()
		case approveCmd:
			r.handleApprove(typed.toolCallID, typed.rule)
		case denyCmd:
			r.handleDeny(typed.toolCallID)
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

func (r *Chat) handleReorderQueue(ids []domain.ID) {
	if len(ids) == 0 {
		return
	}
	r.mu.Lock()
	queueMap := make(map[domain.ID]domain.QueuedInput, len(r.queue))
	for _, item := range r.queue {
		queueMap[item.ID] = item
	}
	reordered := make([]domain.QueuedInput, 0, len(ids))
	for _, id := range ids {
		if item, ok := queueMap[id]; ok {
			reordered = append(reordered, item)
		}
	}
	r.queue = reordered
	r.chat.QueuedInputs = cloneQueuedInputs(r.queue)
	if r.state != nil {
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.QueuedInputs = cloneQueuedInputs(r.queue)
		})
	}
	r.mu.Unlock()
	_ = r.persistQueue()
	r.broadcast(r.snapshotUpdateFlags(nil, false, true, false, false, false))
}

func (r *Chat) handleDeleteQueueItem(id domain.ID) {
	if id == "" {
		return
	}
	r.mu.Lock()
	found := false
	r.queue = slices.DeleteFunc(r.queue, func(item domain.QueuedInput) bool {
		if item.ID == id {
			found = true
			return true
		}
		return false
	})
	r.chat.QueuedInputs = cloneQueuedInputs(r.queue)
	if r.state != nil && found {
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.QueuedInputs = cloneQueuedInputs(r.queue)
		})
	}
	r.mu.Unlock()
	if found {
		_ = r.persistQueue()
		r.broadcast(r.snapshotUpdateFlags(nil, false, true, false, false, false))
	}
}

func (r *Chat) handleSendQueueItemNow(id domain.ID) {
	if id == "" {
		return
	}
	r.mu.Lock()
	idx := slices.IndexFunc(r.queue, func(item domain.QueuedInput) bool {
		return item.ID == id
	})
	if idx < 0 {
		r.mu.Unlock()
		return
	}
	item := r.queue[idx]
	item.Kind = domain.QueuedInputKindSteer
	item.Held = false
	item.CreatedAt = time.Now().UTC()
	remaining := append(slices.Clone(r.queue[:idx]), r.queue[idx+1:]...)
	r.queue = append([]domain.QueuedInput{item}, remaining...)
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
	if r.draining {
		r.queue = append([]domain.QueuedInput{item}, cloneQueuedInputs(remaining)...)
		r.chat.QueuedInputs = cloneQueuedInputs(r.queue)
		if r.state != nil {
			r.state.UpdateChat(func(chat *domain.Chat) {
				chat.QueuedInputs = cloneQueuedInputs(r.queue)
			})
		}
		r.mu.Unlock()
		_ = r.persistQueue()
		r.broadcast(r.snapshotUpdateFlags(nil, false, true, true, false, false))
		return
	}
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
		return r.cancelState == CancelStateCancelling || r.draining
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

	if r.shouldAppendOptimisticUserMessage(item) {
		r.appendOptimisticUserMessage(item, session, chat)
	}
	_ = r.persistQueue()
	r.broadcast(r.snapshotUpdateFlags(nil, item.Kind != domain.QueuedInputKindContinue, true, true, true, false))
	r.runItem(ctx, session, chat, item)
}

func (r *Chat) handleInterrupt() {
	r.mu.Lock()
	if r.status == StatusRunningTools && len(r.running) > 0 {
		if r.cancelState != CancelStateCancelling {
			r.cancelState = CancelStateCancelling
			r.appendRuntimeNoticeLocked("Cancelling. Tool calls running, waiting for completion. Press ESC again to cancel tool calls.", "interrupt_pending", "warning")
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

func (r *Chat) handleApprove(toolCallID string, rule *domain.PermissionOverride) {
	runner, ok := r.engine.(approvalRunner)
	if !ok {
		err := fmt.Errorf("approval is not supported by runner")
		evt := domain.Event{Kind: domain.EventKindError, Err: err}
		r.broadcast(r.snapshotUpdateFlags(&evt, false, false, true, false, false))
		return
	}
	r.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = turncontrol.WithShouldStop(ctx, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cancelState == CancelStateCancelling || r.draining
	})
	r.cancel = cancel
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Waiting for LLM response"
	sessionID := r.session.ID
	chatID := r.chat.ID
	toolCallID = r.resolveApprovalToolCallIDLocked(toolCallID)
	r.removeApprovalLocked(toolCallID)
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, true, false, true, false, true))

	var (
		events <-chan domain.Event
		err    error
	)
	if turnRunner, ok := r.engine.(turnApprovalRunner); ok && rule != nil {
		events, err = turnRunner.ApproveToolTurnWithRule(ctx, r.turnState(), toolCallID, *rule)
	} else if turnRunner, ok := r.engine.(turnApprovalRunner); ok {
		events, err = turnRunner.ApproveToolTurn(ctx, r.turnState(), toolCallID)
	} else if rule != nil {
		events, err = runner.ApproveToolInChatWithRule(ctx, sessionID, chatID, toolCallID, *rule)
	} else {
		events, err = runner.ApproveToolInChat(ctx, sessionID, chatID, toolCallID)
	}
	r.handleApprovalEventStream(events, err)
}

func (r *Chat) handleDeny(toolCallID string) {
	runner, ok := r.engine.(approvalRunner)
	if !ok {
		err := fmt.Errorf("approval is not supported by runner")
		evt := domain.Event{Kind: domain.EventKindError, Err: err}
		r.broadcast(r.snapshotUpdateFlags(&evt, false, false, true, false, false))
		return
	}
	r.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = turncontrol.WithShouldStop(ctx, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cancelState == CancelStateCancelling || r.draining
	})
	r.cancel = cancel
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Waiting for LLM response"
	sessionID := r.session.ID
	chatID := r.chat.ID
	toolCallID = r.resolveApprovalToolCallIDLocked(toolCallID)
	r.removeApprovalLocked(toolCallID)
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, true, false, true, false, true))

	var events <-chan domain.Event
	var err error
	if turnRunner, ok := r.engine.(turnApprovalRunner); ok {
		events, err = turnRunner.DenyToolTurn(ctx, r.turnState(), toolCallID)
	} else {
		events, err = runner.DenyToolInChat(ctx, sessionID, chatID, toolCallID)
	}
	r.handleApprovalEventStream(events, err)
}

func (r *Chat) resolveApprovalToolCallIDLocked(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, approval := range r.state.Approvals() {
		if approval.ID == raw && strings.TrimSpace(approval.ToolCallID) != "" {
			return approval.ToolCallID
		}
	}
	return raw
}

func (r *Chat) removeApprovalLocked(toolCallID string) {
	if r.state == nil {
		return
	}
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	for _, approval := range r.state.Approvals() {
		if strings.TrimSpace(approval.ToolCallID) == toolCallID {
			r.state.RemoveApproval(approval.ID)
			return
		}
	}
	r.state.RemoveApproval(domain.ID(toolCallID))
}

func (r *Chat) handleResumePendingTools() {
	runner, ok := r.engine.(pendingToolRunner)
	if !ok {
		return
	}
	r.mu.Lock()
	if r.active || r.status == StatusWaitingApproval || r.draining {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = turncontrol.WithShouldStop(ctx, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cancelState == CancelStateCancelling || r.draining
	})
	session := r.session
	chat := r.chat
	r.mu.Unlock()
	events, err := runner.ResumePendingToolCallsInChat(ctx, session, chat)
	if err != nil {
		cancel()
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
	if events == nil {
		cancel()
		return
	}
	r.mu.Lock()
	if r.active || r.status == StatusWaitingApproval || r.draining {
		r.mu.Unlock()
		cancel()
		return
	}
	r.cancel = cancel
	r.cancelState = CancelStateNone
	r.running = map[string]struct{}{}
	r.active = true
	r.status = StatusRunningTools
	r.statusText = "Resuming tool calls"
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, false, false))
	r.handleApprovalEventStream(events, nil)
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
	refreshedQueue, queueChanged, queueErr := r.queueRefreshForEvent(evt)
	r.mu.Lock()
	transcriptChanged := false
	contextChanged := false
	if queueErr != nil {
		r.status = StatusErrored
		r.statusText = queueErr.Error()
		r.active = false
		r.cancel = nil
		r.cancelState = CancelStateNone
		r.running = nil
	} else if queueChanged {
		r.queue = refreshedQueue
		r.chat.QueuedInputs = cloneQueuedInputs(r.queue)
		if r.state != nil {
			r.state.UpdateChat(func(chat *domain.Chat) {
				chat.QueuedInputs = cloneQueuedInputs(r.queue)
			})
		}
	}
	if evt.Item.ID != "" && r.state != nil {
		switch evt.Kind {
		case domain.EventKindMessageDelta, domain.EventKindReasoning:
			r.state.EnsureTimelineItem(evt.Item)
		default:
			r.state.UpsertTimelineItem(evt.Item)
		}
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
		r.statusText = runningToolStatusText(evt.Tool)
	case domain.EventKindToolCallDelta:
		r.status = StatusWaitingLLM
		r.statusText = toolCallDeltaStatusText(evt)
	case domain.EventKindToolResult:
		if strings.TrimSpace(evt.ToolCallID) != "" {
			delete(r.running, evt.ToolCallID)
		}
		if len(r.running) == 0 && r.active {
			r.status = StatusWaitingLLM
			r.statusText = "Waiting for LLM response"
		}
	case domain.EventKindChatTitle:
		title := strings.TrimSpace(evt.Text)
		if title != "" {
			r.chat.Title = title
			if r.state != nil {
				r.state.UpdateChat(func(chat *domain.Chat) {
					chat.Title = title
				})
				contextChanged = true
			}
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
		if r.state != nil && evt.Item.ID != "" {
			r.state.SealActiveAssistant("")
			r.state.ClearPendingAssistant()
			contextChanged = true
		}
		r.cancelState = CancelStateNone
		r.running = nil
	case domain.EventKindStatus:
		if text, ok := promptProgressStatusText(evt.Meta); ok {
			r.status = StatusWaitingLLM
			r.statusText = text
		}
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
	if queueErr != nil {
		copyEvt = domain.Event{Kind: domain.EventKindError, Err: queueErr}
	}
	r.broadcast(r.snapshotUpdateFlags(&copyEvt, transcriptChanged || evt.Kind == domain.EventKindMessageDone, queueChanged, true, contextChanged, evt.Kind == domain.EventKindApprovalAsk || evt.Kind == domain.EventKindApprovalReply))
}

func (r *Chat) queueRefreshForEvent(evt domain.Event) ([]domain.QueuedInput, bool, error) {
	if evt.Meta[domain.EventMetaRefresh] != domain.EventRefreshQueue || r.store == nil {
		return nil, false, nil
	}
	chat, err := r.store.GetChat(context.Background(), r.chat.ID)
	if err != nil {
		return nil, false, fmt.Errorf("refresh queued inputs: %w", err)
	}
	return cloneQueuedInputs(chat.QueuedInputs), true, nil
}

func promptProgressStatusText(meta map[string]string) (string, bool) {
	if meta[domain.EventMetaPromptProgress] != "true" {
		return "", false
	}
	total, totalErr := strconv.Atoi(strings.TrimSpace(meta["total"]))
	processed, processedErr := strconv.Atoi(strings.TrimSpace(meta["processed"]))
	if totalErr == nil && processedErr == nil && total > 0 {
		percent := processed * 100 / total
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100
		}
		return fmt.Sprintf("LLM preprocessing %d%%", percent), true
	}
	return "LLM preprocessing", true
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
			r.state.SealActiveAssistant("")
			r.state.ClearPendingAssistant()
		}
	}
	r.cancelState = CancelStateNone
	r.running = nil
	draining := r.draining
	r.draining = false
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, true, false))
	if shouldDispatch && !draining {
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
	if r.draining || r.active || r.status == StatusWaitingApproval {
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
		return r.cancelState == CancelStateCancelling || r.draining
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

	if r.shouldAppendOptimisticUserMessage(item) {
		r.appendOptimisticUserMessage(item, session, chat)
	}
	_ = r.persistQueue()
	r.broadcast(r.snapshotUpdateFlags(nil, item.Kind != domain.QueuedInputKindContinue, true, true, true, false))
	r.runItem(ctx, session, chat, item)
}

func (r *Chat) shouldAppendOptimisticUserMessage(item domain.QueuedInput) bool {
	if item.Kind == domain.QueuedInputKindContinue {
		return false
	}
	_, ok := r.engine.(turnPromptRunner)
	return !ok
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
		if runner, ok := r.engine.(turnContinueRunner); ok {
			events, err = runner.RunContinueTurn(ctx, r.turnState(), note)
		} else if runner, ok := r.engine.(continueRunner); ok {
			events, err = runner.RunContinueInChat(ctx, session, chat, note)
		} else {
			err = fmt.Errorf("continue is not supported by runner")
		}
	default:
		if runner, ok := r.engine.(turnPromptRunner); ok {
			events, err = runner.RunPromptTurn(ctx, r.turnState(), item.Text, queuedAttachmentDrafts(item.Attachments), queuedReferenceDrafts(item.References), note)
		} else {
			events, err = r.engine.RunPromptInChat(ctx, session, chat, item.Text, queuedAttachmentDrafts(item.Attachments), queuedReferenceDrafts(item.References), note)
		}
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
	if r.store == nil || r.chat.ID == "" {
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

func runningToolStatusText(tool domain.ToolKind) string {
	toolName := strings.TrimSpace(string(tool))
	if toolName == "" {
		return "Running tool"
	}
	return "Running " + toolName
}

func toolCallDeltaStatusText(evt domain.Event) string {
	toolName := strings.TrimSpace(string(evt.Tool))
	if toolName == "" {
		toolName = "tool"
	} else {
		toolName += " tool"
	}
	if args := evt.Meta["arguments"]; args != "" {
		return fmt.Sprintf("Receiving %s call (%s arguments)", toolName, formatBytes(len(args)))
	}
	return fmt.Sprintf("Receiving %s call...", toolName)
}

func formatBytes(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	const unit = 1024
	if size < unit*unit {
		return fmt.Sprintf("%.1f KB", float64(size)/unit)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(unit*unit))
}

func (r *Chat) snapshotQueue() []domain.QueuedInput {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneQueuedInputs(r.queue)
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
		user.Attachments = append(user.Attachments, domain.Attachment(draft))
	}
	for _, ref := range item.References {
		user.References = append(user.References, domain.Reference(ref))
	}
	r.mu.Lock()
	timelineItem := domain.TimelineItem{
		ID:        domain.NewTimelineID(now),
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
		ID:        domain.NewTimelineID(now),
		ChatID:    r.chat.ID,
		Seq:       int64(len(r.state.Timeline()) + 1),
		Content:   domain.Notice{Text: body, Kind: kind, Level: severity},
		CreatedAt: now,
		UpdatedAt: now,
		SealedAt:  now,
	})
}

func (r *Chat) appendPersistedInterruptNotice(ctx context.Context, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil
	}
	now := time.Now().UTC()
	notice := domain.Notice{
		Level:  "warning",
		Text:   "Interrupted",
		Kind:   domain.NoticeKindInterrupted,
		Reason: reason,
	}
	var item domain.TimelineItem
	var err error
	if r.store != nil {
		item, err = r.store.AppendTimeline(ctx, r.chat.ID, notice)
		if err != nil {
			return err
		}
		item.Seal(now)
		if err := r.store.Timeline().Put(ctx, item); err != nil {
			return err
		}
	} else {
		item = domain.TimelineItem{
			ID:        domain.NewTimelineID(now),
			ChatID:    r.chat.ID,
			Content:   notice,
			CreatedAt: now,
			UpdatedAt: now,
			SealedAt:  now,
		}
	}
	r.mu.Lock()
	if r.state != nil {
		r.state.UpsertTimelineItem(item)
	}
	r.chat.LastMessage = notice.Text
	if r.state != nil {
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.LastMessage = notice.Text
		})
	}
	r.status = StatusIdle
	r.statusText = ""
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, true, false, true, false, false))
	return nil
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
		ID:          domain.NewID(),
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
