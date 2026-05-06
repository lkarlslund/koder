package chatruntime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/appstate"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/store"
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

type Snapshot struct {
	Session          domain.Session
	Chat             domain.Chat
	Messages         []domain.Message
	Parts            map[int64][]domain.Part
	Approvals        []store.Approval
	QueuedInputs     []domain.QueuedInput
	PendingAssistant appstate.PendingAssistantTurn
	Status           Status
	StatusText       string
	Context          domain.ContextUsage
	Active           bool
}

type Update struct {
	Event      *domain.Event
	Status     Status
	StatusText string
	Queue      []domain.QueuedInput
	Context    domain.ContextUsage
	Active     bool
}

type Runtime struct {
	manager *Manager
	store   *store.Store
	engine  promptRunner

	mu         sync.RWMutex
	session    domain.Session
	chat       domain.Chat
	state      *appstate.ChatState
	status     Status
	statusText string
	active     bool
	cancel     context.CancelFunc
	queue      []domain.QueuedInput
	closed     bool

	inbox   chan any
	subsMu  sync.Mutex
	subs    map[int]chan Update
	nextSub int
}

type enqueueCmd struct {
	item QueueItem
}

type interruptCmd struct{}
type closeCmd struct{}
type streamEventCmd struct {
	event domain.Event
}
type streamClosedCmd struct{}

type continueRunner interface {
	RunContinueInChat(context.Context, domain.Session, domain.Chat, string) (<-chan domain.Event, error)
}

func (m *Manager) Runtime(ctx context.Context, session domain.Session, chat domain.Chat) (*Runtime, error) {
	if chat.ID == 0 {
		return nil, fmt.Errorf("chat id is required")
	}
	m.mu.RLock()
	if rt := m.runtimes[chat.ID]; rt != nil {
		m.mu.RUnlock()
		return rt, nil
	}
	m.mu.RUnlock()

	messages, parts, err := m.store.PartsForChat(ctx, chat.ID)
	if err != nil {
		return nil, err
	}
	approvals, err := m.store.PendingApprovalsForChat(ctx, chat.ID)
	if err != nil {
		return nil, err
	}
	rt := &Runtime{
		manager: m,
		store:   m.store,
		engine:  m.engine,
		session: session,
		chat:    chat,
		state:   appstate.NewChatState(chat, messages, parts, approvals),
		status:  StatusIdle,
		queue:   cloneQueuedInputs(chat.QueuedInputs),
		inbox:   make(chan any, 64),
		subs:    map[int]chan Update{},
	}
	m.mu.Lock()
	if existing := m.runtimes[chat.ID]; existing != nil {
		m.mu.Unlock()
		return existing, nil
	}
	m.runtimes[chat.ID] = rt
	m.mu.Unlock()
	go rt.loop()
	rt.inbox <- struct{}{} // trigger initial auto-dispatch
	return rt, nil
}

func (r *Runtime) Enqueue(item QueueItem) {
	r.inbox <- enqueueCmd{item: item}
}

func (r *Runtime) Interrupt() {
	r.inbox <- interruptCmd{}
}

func (r *Runtime) Close() {
	r.inbox <- closeCmd{}
}

func (r *Runtime) Status() (Status, string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status, r.statusText, r.active
}

func (r *Runtime) ContextSize() domain.ContextUsage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return domain.ContextUsage{}
	}
	return r.state.CurrentContextSize()
}

func (r *Runtime) Snapshot() Snapshot {
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

func (r *Runtime) Subscribe() (<-chan Update, func()) {
	ch := make(chan Update, 64)
	r.subsMu.Lock()
	id := r.nextSub
	r.nextSub++
	r.subs[id] = ch
	r.subsMu.Unlock()
	ch <- Update{
		Status:     r.snapshotStatus(),
		StatusText: r.snapshotStatusText(),
		Queue:      cloneQueuedInputs(r.snapshotQueue()),
		Context:    r.ContextSize(),
		Active:     r.snapshotActive(),
	}
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

func (r *Runtime) loop() {
	for cmd := range r.inbox {
		switch typed := cmd.(type) {
		case enqueueCmd:
			r.handleEnqueue(typed.item)
		case interruptCmd:
			r.handleInterrupt()
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

func (r *Runtime) handleEnqueue(item QueueItem) {
	queued := queuedInputFromItem(item)
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
	r.broadcast(Update{
		Status:     r.snapshotStatus(),
		StatusText: r.snapshotStatusText(),
		Queue:      cloneQueuedInputs(r.snapshotQueue()),
		Context:    r.ContextSize(),
		Active:     r.snapshotActive(),
	})
	r.maybeDispatchNext()
}

func (r *Runtime) handleInterrupt() {
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *Runtime) handleStreamEvent(evt domain.Event) {
	r.mu.Lock()
	switch evt.Kind {
	case domain.EventKindMessageDelta:
		if r.state != nil {
			r.state.AppendPendingAssistantText(evt.Text)
		}
		r.status = StatusStreamingResponse
		r.statusText = "Streaming LLM response ..."
	case domain.EventKindReasoning:
		if r.state != nil {
			r.state.AppendPendingAssistantReasoning(evt.Text)
		}
		r.status = StatusStreamingThoughts
		r.statusText = "Streaming thoughts ..."
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
		}
	case domain.EventKindToolStart:
		r.status = StatusRunningTools
		r.statusText = strings.TrimSpace(evt.Text)
		if r.statusText == "" {
			r.statusText = "Running tool"
		}
	case domain.EventKindApprovalAsk:
		r.status = StatusWaitingApproval
		r.statusText = strings.TrimSpace(evt.Text)
		r.active = false
		r.cancel = nil
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
	case domain.EventKindMessageDone:
		if r.state != nil && evt.Message.ID > 0 {
			r.state.ClearPendingAssistant()
		}
	}
	r.mu.Unlock()
	copyEvt := evt
	r.broadcast(Update{
		Event:      &copyEvt,
		Status:     r.snapshotStatus(),
		StatusText: r.snapshotStatusText(),
		Queue:      cloneQueuedInputs(r.snapshotQueue()),
		Context:    r.ContextSize(),
		Active:     r.snapshotActive(),
	})
}

func (r *Runtime) handleStreamClosed() {
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
	r.mu.Unlock()
	r.broadcast(Update{
		Status:     r.snapshotStatus(),
		StatusText: r.snapshotStatusText(),
		Queue:      cloneQueuedInputs(r.snapshotQueue()),
		Context:    r.ContextSize(),
		Active:     r.snapshotActive(),
	})
	if shouldDispatch {
		r.maybeDispatchNext()
	}
}

func (r *Runtime) handleClose() {
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
	r.manager.mu.Lock()
	delete(r.manager.runtimes, r.chat.ID)
	r.manager.mu.Unlock()
	close(r.inbox)
}

func (r *Runtime) maybeDispatchNext() {
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
	r.cancel = cancel
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Waiting for LLM response"
	session := r.session
	chat := r.chat
	r.mu.Unlock()

	_ = r.persistQueue()
	r.broadcast(Update{
		Status:     r.snapshotStatus(),
		StatusText: r.snapshotStatusText(),
		Queue:      cloneQueuedInputs(r.snapshotQueue()),
		Context:    r.ContextSize(),
		Active:     r.snapshotActive(),
	})

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
			events, err = runner.RunContinueInChat(ctx, session, chat, "")
		}
	default:
		events, err = r.engine.RunPromptInChat(ctx, session, chat, item.Text, queuedAttachmentDrafts(item.Attachments), queuedReferenceDrafts(item.References), "")
	}
	if err != nil {
		r.mu.Lock()
		r.active = false
		r.cancel = nil
		r.status = StatusErrored
		r.statusText = err.Error()
		r.mu.Unlock()
		evt := domain.Event{Kind: domain.EventKindError, Err: err}
		r.broadcast(Update{
			Event:      &evt,
			Status:     r.snapshotStatus(),
			StatusText: r.snapshotStatusText(),
			Queue:      cloneQueuedInputs(r.snapshotQueue()),
			Context:    r.ContextSize(),
			Active:     r.snapshotActive(),
		})
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

func (r *Runtime) persistQueue() error {
	if r.store == nil || r.chat.ID == 0 {
		return nil
	}
	r.mu.RLock()
	items := cloneQueuedInputs(r.queue)
	r.mu.RUnlock()
	return r.store.SetChatQueuedInputs(context.Background(), r.chat.ID, items)
}

func (r *Runtime) snapshotStatus() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *Runtime) snapshotStatusText() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.statusText
}

func (r *Runtime) snapshotQueue() []domain.QueuedInput {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneQueuedInputs(r.queue)
}

func (r *Runtime) snapshotActive() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

func (r *Runtime) broadcast(update Update) {
	r.subsMu.Lock()
	defer r.subsMu.Unlock()
	for _, ch := range r.subs {
		select {
		case ch <- update:
		default:
		}
	}
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
