package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type QueueKind string

const (
	QueueKindUser     QueueKind = "user"
	QueueKindSteer    QueueKind = "steer"
	QueueKindQueued   QueueKind = "queue"
	QueueKindContinue QueueKind = "continue"
)

type QueueItem struct {
	Kind        QueueKind
	Text        string
	Source      string
	Attachments []attachment.Draft
	References  []reference.Draft
	Note        string
}

type MetadataUpdate struct {
	Archived *bool
	Title    string
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
	Session           domain.Session
	Chat              domain.Chat
	Timeline          []domain.TimelineItem
	TimelineHasMore   bool
	TimelineLoadedAll bool
	TimelineBefore    id.ID
	Approvals         []Approval
	QueuedInputs      []domain.QueuedInput
	ExecProcesses     []tools.ExecProcess
	PendingAssistant  PendingAssistantTurn
	Turn              Turn
	Status            Status
	StatusText        string
	Context           domain.ContextUsage
	TokenUsage        domain.Usage
	Active            bool
}

type Update struct {
	Event             *domain.Event
	Snapshot          Snapshot
	Turn              Turn
	Status            Status
	StatusText        string
	Queue             []domain.QueuedInput
	Context           domain.ContextUsage
	TokenUsage        domain.Usage
	Active            bool
	TranscriptChanged bool
	ReplaceTimeline   bool
	QueueChanged      bool
	StatusChanged     bool
	ContextChanged    bool
	ApprovalsChanged  bool
}

type Chat struct {
	deps    Deps
	onClose func(id.ID)

	mu               sync.RWMutex
	session          domain.Session
	chat             domain.Chat
	state            *ChatState
	status           Status
	statusText       string
	active           bool
	cancel           context.CancelFunc
	queue            []domain.QueuedInput
	queueNotes       map[id.ID]string
	activeUserSource string
	cancelState      CancelState
	running          map[string]struct{}
	draining         bool
	closed           bool
	timelineLoaded   bool

	inbox   chan any
	done    chan struct{}
	subsMu  sync.Mutex
	subs    map[int]chan Update
	nextSub int
}

type Deps struct {
	Store   *store.Store
	Model   ModelOps
	Tools   ToolTurnService
	Runtime ToolRuntimeService
	Life    ToolLifecycleService
	Pending PendingToolService
	Compact CompactService
}

type enqueueCmd struct {
	item QueueItem
}

type replaceQueueCmd struct {
	items []domain.QueuedInput
}

type reorderQueueCmd struct {
	ids []id.ID
}

type deleteQueueItemCmd struct {
	id id.ID
}

type sendQueueItemNowCmd struct {
	id id.ID
}

type dispatchQueuedCmd struct {
	item      domain.QueuedInput
	remaining []domain.QueuedInput
}

type interruptCmd struct {
	reason CancelReason
}
type resumePendingToolsCmd struct{}
type approveCmd struct {
	toolCallID string
	rule       *accesssettings.PermissionOverride
}
type denyCmd struct {
	toolCallID string
}
type closeCmd struct{}
type streamEventCmd struct {
	event domain.Event
}
type streamClosedCmd struct{}

type TurnStepResult struct {
	Continue         bool
	WaitingApproval  bool
	Done             bool
	TurnInstructions []provider.InstructionBlock
}

type TurnRequest struct {
	Session  domain.Session
	Chat     domain.Chat
	Timeline []domain.TimelineItem
}

type ToolTurnService interface {
	ApproveToolForTurn(context.Context, *Chat, string, *accesssettings.PermissionOverride, chan<- domain.Event) (bool, error)
	DenyToolForTurn(context.Context, *Chat, string, chan<- domain.Event) error
}

type PendingToolService interface {
	ResumePendingToolsForTurn(context.Context, *Chat, chan<- domain.Event) (bool, error)
}

type ToolRuntimeService interface {
	ToolRuntime(context.Context, *Chat) (tools.Runtime, error)
}

type ToolLifecycleService interface {
	ToolExecutionStarted(context.Context, *Chat, tools.Request)
	ToolExecutionFinished(context.Context, *Chat, tools.Request)
	ToolExecutionFailed(context.Context, *Chat, tools.Request, error)
}

type CompactService interface {
	CompactChat(context.Context, *Chat, string, chan<- domain.Event) error
}

type ModelResponse struct {
	Text           string
	RawReasoning   string
	Reasoning      domain.ReasoningContent
	Usage          domain.Usage
	ToolCalls      []provider.ToolCall
	ToolCallErrors []provider.ToolCallError
	Streamed       bool
}

type ToolCallParseResult struct {
	Requests  []tools.Request
	ToolCalls []domain.ToolCall
	Err       error
}

type ModelOps interface {
	PreparePromptTurn(context.Context, *Chat, domain.QueuedInput, string, []attachment.Draft, []reference.Draft, string, chan<- domain.Event) ([]provider.InstructionBlock, error)
	PrepareContinueTurn(context.Context, *Chat, string, chan<- domain.Event) ([]provider.InstructionBlock, error)
	MaxToolLoopSteps() int
	ClientForChat(domain.Chat) (*provider.Client, error)
	BeginModelTurn(context.Context, id.ID, id.ID, int, chan<- domain.Event) error
	BuildConversationForTurn(context.Context, TurnRequest) ([]provider.Message, error)
	AutoCompactAtTurnBoundary(context.Context, *Chat, *provider.Client, []provider.Message, chan<- domain.Event) (bool, error)
	ProviderStreamingEnabled(domain.Chat) bool
	ChatRequest(domain.Session, domain.Chat, []provider.Message, bool) provider.ChatRequest
	NextAssistantTimelineItemForTurn(context.Context, *Chat) (domain.TimelineItem, error)
	CompleteModelRequest(context.Context, domain.Session, domain.Chat, *provider.Client, chan<- domain.Event, provider.ChatRequest, domain.TimelineItem) (ModelResponse, error)
	ParseProviderToolCallsForTranscript([]provider.ToolCall, id.ID) ToolCallParseResult
	FailedStreamedProviderToolCall(provider.ToolCallError) domain.ToolCall
	RecordLifecycle(id.ID, string, string, map[string]string)
	MaybeUpdateChatTitle(context.Context, id.ID) (string, error)
	MaybeUpdateSessionTitle(context.Context, domain.Session, domain.Chat, *provider.Client) (string, error)
	AutoContinueBadStopEnabled() bool
}

// Load builds a live chat by hydrating its timeline and approval state from store.
func Load(ctx context.Context, session domain.Session, chatRecord domain.Chat, deps Deps, onClose func(id.ID)) (*Chat, error) {
	return load(ctx, session, chatRecord, deps, onClose, true)
}

// LoadMetadata builds a live chat with chat metadata and pending approvals, but without transcript items.
func LoadMetadata(ctx context.Context, session domain.Session, chatRecord domain.Chat, deps Deps, onClose func(id.ID)) (*Chat, error) {
	return load(ctx, session, chatRecord, deps, onClose, false)
}

func load(ctx context.Context, session domain.Session, chatRecord domain.Chat, deps Deps, onClose func(id.ID), loadTimeline bool) (*Chat, error) {
	if deps.Store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if chatRecord.ID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	if chatRecord.SessionID == "" {
		loaded, err := getChat(ctx, deps.Store, chatRecord.ID)
		if err != nil {
			return nil, err
		}
		chatRecord = loaded
	}
	var timeline []domain.TimelineItem
	if loadTimeline {
		var err error
		timeline, err = timelineForChat(ctx, deps.Store, chatRecord.ID)
		if err != nil {
			return nil, err
		}
		chatRecord, err = repairContextCacheFromTimeline(ctx, deps.Store, chatRecord, timeline)
		if err != nil {
			return nil, err
		}
	}
	approvals, err := pendingApprovalsForChat(ctx, deps.Store, chatRecord.ID)
	if err != nil {
		return nil, err
	}
	return newChat(session, chatRecord, timeline, approvals, deps, onClose, loadTimeline)
}

func repairContextCacheFromTimeline(ctx context.Context, st *store.Store, chatRecord domain.Chat, timeline []domain.TimelineItem) (domain.Chat, error) {
	anchorTokens, ok := timelineContextAnchorTokens(timeline)
	if !ok || anchorTokens <= 0 {
		if !chatRecord.ContextTokensKnown && chatRecord.LastKnownContextTokens != 0 {
			chatRecord.LastKnownContextTokens = 0
			if st != nil {
				if err := updateChat(ctx, st, chatRecord); err != nil {
					return domain.Chat{}, err
				}
			}
		}
		return chatRecord, nil
	}
	if chatRecord.LastKnownContextTokens == anchorTokens && chatRecord.ContextTokensKnown {
		return chatRecord, nil
	}
	chatRecord.LastKnownContextTokens = anchorTokens
	chatRecord.ContextTokensKnown = true
	if st != nil {
		if err := updateChat(ctx, st, chatRecord); err != nil {
			return domain.Chat{}, err
		}
	}
	return chatRecord, nil
}

// New builds a live chat from hydrated persisted state.
func New(session domain.Session, chatRecord domain.Chat, timeline []domain.TimelineItem, approvals []Approval, deps Deps, onClose func(id.ID)) (*Chat, error) {
	return newChat(session, chatRecord, timeline, approvals, deps, onClose, true)
}

func newChat(session domain.Session, chatRecord domain.Chat, timeline []domain.TimelineItem, approvals []Approval, deps Deps, onClose func(id.ID), timelineLoaded bool) (*Chat, error) {
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
		deps:           deps,
		onClose:        onClose,
		session:        session,
		chat:           chatRecord,
		state:          NewTimelineState(chatRecord, timeline, approvals),
		status:         status,
		statusText:     statusText,
		queue:          cloneQueuedInputs(chatRecord.QueuedInputs),
		queueNotes:     map[id.ID]string{},
		timelineLoaded: timelineLoaded,
		inbox:          make(chan any, 64),
		done:           make(chan struct{}),
		subs:           map[int]chan Update{},
	}
	go c.loop()
	resuming := false
	if c.timelineLoaded && c.state.HasPendingExecutableToolCalls() {
		c.inbox <- resumePendingToolsCmd{}
		resuming = true
	}
	if !resuming && c.timelineLoaded {
		c.inbox <- struct{}{}
	}
	return c, nil
}

func (r *Chat) SnapshotTimeline() []domain.TimelineItem {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return nil
	}
	return r.state.SnapshotTimeline()
}

// PendingExecutableToolCalls returns pending tool calls from the live transcript.
func (r *Chat) PendingExecutableToolCalls(ctx context.Context) ([]domain.ToolCall, error) {
	if r == nil {
		return nil, fmt.Errorf("chat runtime is required")
	}
	if err := r.EnsureTimeline(ctx); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return nil, nil
	}
	items := r.state.SnapshotTimeline()
	for idx := len(items) - 1; idx >= 0; idx-- {
		assistant, ok := items[idx].Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		var calls []domain.ToolCall
		for _, call := range assistant.Tools {
			if call.Status != domain.ToolStatusPending || call.Result != nil || call.Error != nil || call.Approval != nil {
				continue
			}
			calls = append(calls, call)
		}
		return calls, nil
	}
	return nil, nil
}

// AppendUserMessage records a user message in the live transcript.
func (r *Chat) AppendUserMessage(ctx context.Context, user domain.UserMessage) (domain.TimelineItem, error) {
	return r.AppendUserMessageForInput(ctx, domain.QueuedInput{}, user)
}

// AppendUserMessageForInput records a queued user message in the live transcript.
func (r *Chat) AppendUserMessageForInput(ctx context.Context, input domain.QueuedInput, user domain.UserMessage) (domain.TimelineItem, error) {
	if r == nil {
		return domain.TimelineItem{}, fmt.Errorf("chat runtime is required")
	}
	now := time.Now().UTC()
	text := strings.TrimSpace(user.Text)
	r.mu.Lock()
	if strings.TrimSpace(user.Source) == "" {
		user.Source = strings.TrimSpace(r.activeUserSource)
	}
	if strings.TrimSpace(user.Source) == "" {
		user.Source = domain.UserMessageSourceUser
	}
	if input.ID != "" {
		user.QueueID = input.ID
		if !input.CreatedAt.IsZero() {
			user.QueuedAt = input.CreatedAt
		}
	}
	seq := int64(1)
	if r.state != nil {
		seq = int64(len(r.state.Timeline()) + 1)
	}
	item := domain.TimelineItem{
		ID:        NewTimelineID(now),
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

	if r.deps.Store != nil {
		if _, err := insertTimelineItem(ctx, r.deps.Store, item); err != nil {
			return domain.TimelineItem{}, err
		}
		if err := updateChat(ctx, r.deps.Store, chatRecord); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	return item, nil
}

func (r *Chat) NextAssistantItem() domain.TimelineItem {
	if r == nil {
		return domain.TimelineItem{}
	}
	now := time.Now().UTC()
	r.mu.RLock()
	defer r.mu.RUnlock()
	seq := int64(1)
	if r.state != nil {
		seq = int64(len(r.state.Timeline()) + 1)
	}
	return domain.TimelineItem{
		ID:        NewTimelineID(now),
		ChatID:    r.chat.ID,
		Seq:       seq,
		Content:   domain.AssistantMessage{},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// UpsertTimelineItem records a timeline item in live memory and storage.
func (r *Chat) UpsertTimelineItem(ctx context.Context, item domain.TimelineItem) (domain.TimelineItem, error) {
	if r == nil {
		return domain.TimelineItem{}, fmt.Errorf("chat runtime is required")
	}
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

	if r.deps.Store != nil {
		if err := putTimelineItem(ctx, r.deps.Store, item); err != nil {
			return domain.TimelineItem{}, err
		}
		if err := updateChat(ctx, r.deps.Store, chatRecord); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	return item, nil
}

// ApplyNextSteer removes and records queued turn-boundary messages.
func (r *Chat) ApplyNextSteer(ctx context.Context) (domain.TimelineItem, bool, error) {
	if r == nil {
		return domain.TimelineItem{}, false, fmt.Errorf("chat runtime is required")
	}
	now := time.Now().UTC()
	r.mu.Lock()
	var steers []domain.QueuedInput
	remaining := make([]domain.QueuedInput, 0, len(r.queue))
	for _, item := range r.queue {
		if item.Held || domain.DeliveryForQueuedInput(item) != domain.QueuedInputDeliveryTurnBoundary {
			remaining = append(remaining, item)
			continue
		}
		steers = append(steers, item)
	}
	if len(steers) == 0 {
		r.mu.Unlock()
		return domain.TimelineItem{}, false, nil
	}
	r.queue = remaining
	r.chat.QueuedInputs = cloneQueuedInputs(r.queue)
	user := userMessageForSteers(steers)
	seq := int64(1)
	if r.state != nil {
		seq = int64(len(r.state.Timeline()) + 1)
	}
	item := domain.TimelineItem{
		ID:        NewTimelineID(now),
		ChatID:    r.chat.ID,
		Seq:       seq,
		Content:   user,
		CreatedAt: now,
		UpdatedAt: now,
		SealedAt:  now,
	}
	if r.state != nil {
		r.state.AppendTimelineItem(item)
		if strings.TrimSpace(user.Text) != "" {
			r.chat.LastMessage = user.Text
		}
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.QueuedInputs = cloneQueuedInputs(r.queue)
			if strings.TrimSpace(user.Text) != "" {
				chat.LastMessage = user.Text
			}
		})
	} else if strings.TrimSpace(user.Text) != "" {
		r.chat.LastMessage = user.Text
	}
	chatRecord := r.chat
	chatID := r.chat.ID
	queue := cloneQueuedInputs(r.queue)
	r.mu.Unlock()

	if r.deps.Store != nil {
		if _, err := insertTimelineItem(ctx, r.deps.Store, item); err != nil {
			return domain.TimelineItem{}, false, err
		}
		if err := setChatQueuedInputs(ctx, r.deps.Store, chatID, queue); err != nil {
			return domain.TimelineItem{}, false, err
		}
		if err := updateChat(ctx, r.deps.Store, chatRecord); err != nil {
			return domain.TimelineItem{}, false, err
		}
	}
	return item, true, nil
}

func (r *Chat) BuildTurnRequest() (TurnRequest, error) {
	if r == nil {
		return TurnRequest{}, fmt.Errorf("chat runtime is required")
	}
	snapshot := r.Snapshot()
	return TurnRequest{
		Session:  snapshot.Session,
		Chat:     snapshot.Chat,
		Timeline: FilterQueuedTimelineItems(r.SnapshotTimeline()),
	}, nil
}

func FilterQueuedTimelineItems(timeline []domain.TimelineItem) []domain.TimelineItem {
	if len(timeline) == 0 {
		return timeline
	}
	out := make([]domain.TimelineItem, 0, len(timeline))
	waitingToolResult := false
	for _, item := range timeline {
		if _, ok := item.Content.(domain.UserMessage); ok && waitingToolResult {
			continue
		}
		out = append(out, item)
		if assistant, ok := item.Content.(domain.AssistantMessage); ok {
			waitingToolResult = assistantHasUnfinishedToolCall(assistant)
			continue
		}
		if _, ok := item.Content.(domain.ToolExecution); ok {
			waitingToolResult = false
		}
	}
	return out
}

func assistantHasUnfinishedToolCall(assistant domain.AssistantMessage) bool {
	for _, call := range assistant.Tools {
		if call.Result == nil && call.Error == nil {
			return true
		}
	}
	return false
}

func userMessageForSteers(steers []domain.QueuedInput) domain.UserMessage {
	texts := make([]string, 0, len(steers))
	user := domain.UserMessage{Delivery: domain.QueuedInputDeliveryTurnBoundary}
	for idx, queued := range steers {
		if text := strings.TrimSpace(queued.Text); text != "" {
			texts = append(texts, text)
		}
		if idx == 0 {
			user.QueueID = queued.ID
			user.QueuedAt = queued.CreatedAt
		} else if !queued.CreatedAt.IsZero() && (user.QueuedAt.IsZero() || queued.CreatedAt.Before(user.QueuedAt)) {
			user.QueuedAt = queued.CreatedAt
		}
		source := domain.UserMessageSourceForQueuedInput(queued)
		if idx == 0 {
			user.Source = source
		} else if user.Source != source {
			user.Source = domain.UserMessageSourceUser
		}
		for _, draft := range queued.Attachments {
			user.Attachments = append(user.Attachments, domain.Attachment(draft))
		}
		for _, ref := range queued.References {
			user.References = append(user.References, domain.Reference(ref))
		}
	}
	user.Text = strings.Join(texts, "\n\n")
	return user
}

// SetContextUsage records the latest context-token usage on the live chat.
func (r *Chat) SetContextUsage(ctx context.Context, usage domain.Usage) error {
	if r == nil {
		return fmt.Errorf("chat runtime is required")
	}
	contextTokens, ok := usage.ContextTokens()
	if !ok {
		return nil
	}
	r.mu.Lock()
	r.chat.LastKnownContextTokens = contextTokens
	r.chat.ContextTokensKnown = true
	if r.state != nil {
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.LastKnownContextTokens = contextTokens
			chat.ContextTokensKnown = true
		})
	}
	chatRecord := r.chat
	r.mu.Unlock()
	if r.deps.Store != nil {
		if _, err := updateStoredChatUsage(ctx, r.deps.Store, chatRecord.ID, chatRecord); err != nil {
			return err
		}
	}
	return nil
}

// AddTokenUsage records provider token usage accumulated since the latest completed compaction.
func (r *Chat) AddTokenUsage(ctx context.Context, usage domain.Usage) error {
	if r == nil {
		return fmt.Errorf("chat runtime is required")
	}
	usage = usage.Normalized()
	if !usage.HasAnyTokens() {
		return nil
	}
	r.mu.Lock()
	r.chat.TokenUsage = r.chat.TokenUsage.Add(usage)
	if r.state != nil {
		total := r.chat.TokenUsage
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.TokenUsage = total
		})
	}
	chatRecord := r.chat
	r.mu.Unlock()
	if r.deps.Store != nil {
		if _, err := updateStoredChatUsage(ctx, r.deps.Store, chatRecord.ID, chatRecord); err != nil {
			return err
		}
	}
	return nil
}

// ResetTokenUsage clears provider token usage after a completed compaction starts a new burn window.
func (r *Chat) ResetTokenUsage(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("chat runtime is required")
	}
	r.mu.Lock()
	r.chat.TokenUsage = domain.Usage{}
	if r.state != nil {
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.TokenUsage = domain.Usage{}
		})
	}
	chatRecord := r.chat
	r.mu.Unlock()
	if r.deps.Store != nil {
		if _, err := updateStoredChatUsage(ctx, r.deps.Store, chatRecord.ID, chatRecord); err != nil {
			return err
		}
	}
	return nil
}

// ResetContextAndTokenUsage clears cached context and token-burn accounting after compaction.
func (r *Chat) ResetContextAndTokenUsage(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("chat runtime is required")
	}
	r.mu.Lock()
	r.chat.LastKnownContextTokens = 0
	r.chat.ContextTokensKnown = false
	r.chat.TokenUsage = domain.Usage{}
	if r.state != nil {
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.LastKnownContextTokens = 0
			chat.ContextTokensKnown = false
			chat.TokenUsage = domain.Usage{}
		})
	}
	chatRecord := r.chat
	r.mu.Unlock()
	if r.deps.Store != nil {
		if _, err := updateStoredChatUsage(ctx, r.deps.Store, chatRecord.ID, chatRecord); err != nil {
			return err
		}
	}
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, false, true, false))
	return nil
}

// Persist writes the current chat snapshot and remaps optimistic in-memory IDs to durable store IDs.
func (r *Chat) Persist(ctx context.Context, st *store.Store) error {
	if st == nil {
		st = r.deps.Store
	}
	if st == nil {
		return nil
	}
	r.mu.RLock()
	chatRecord := r.chat
	if r.state == nil {
		r.mu.RUnlock()
		slog.Info("chat persist", "chat_id", chatRecord.ID, "session_id", chatRecord.SessionID, "timeline_items", 0, "queued_inputs", len(chatRecord.QueuedInputs), "state_loaded", false)
		return updateChat(ctx, st, chatRecord)
	}
	timeline := r.state.SnapshotTimeline()
	r.mu.RUnlock()

	persisted, err := timelineForChat(ctx, st, chatRecord.ID)
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
			if err := putTimelineItem(ctx, st, item); err != nil {
				return r.markPersistError(err)
			}
			continue
		}
		if _, err := insertTimelineItem(ctx, st, item); err != nil {
			return r.markPersistError(err)
		}
		changed = true
	}
	chatRecord.QueuedInputs = cloneQueuedInputs(r.snapshotQueue())
	if err := updateChat(ctx, st, chatRecord); err != nil {
		return r.markPersistError(err)
	}
	slog.Info("chat persist",
		"chat_id", chatRecord.ID,
		"session_id", chatRecord.SessionID,
		"timeline_items", len(timeline),
		"inserted_items", changed,
		"queued_inputs", len(chatRecord.QueuedInputs),
		"state_loaded", true,
	)
	if changed {
		r.broadcast(r.snapshotUpdateFlags(nil, true, false, false, true, false))
	}
	return nil
}

func (r *Chat) Enqueue(item QueueItem) {
	chatID := id.ID("")
	sessionID := id.ID("")
	if r != nil {
		r.mu.RLock()
		chatID = r.chat.ID
		sessionID = r.chat.SessionID
		r.mu.RUnlock()
	}
	slog.Info("chat queue item received", "chat_id", chatID, "session_id", sessionID, "kind", item.Kind, "text_bytes", len(item.Text), "attachments", len(item.Attachments))
	r.inbox <- enqueueCmd{item: item}
}

// Kick asks the chat loop to dispatch queued work if it is idle.
func (r *Chat) Kick() {
	if r == nil {
		return
	}
	r.inbox <- struct{}{}
}

func (r *Chat) ReorderQueue(ids []id.ID) {
	r.inbox <- reorderQueueCmd{ids: slices.Clone(ids)}
}

func (r *Chat) DeleteQueueItem(id id.ID) {
	r.inbox <- deleteQueueItemCmd{id: id}
}

func (r *Chat) SendQueueItemNow(id id.ID) {
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

func (r *Chat) Cancel(reason CancelReason) {
	hard := reason.Hard()
	r.mu.Lock()
	cancel := r.cancel
	hardUpdated := false
	if hard && r.active && !r.closed {
		r.draining = true
		r.cancelState = CancelStateCancelling
		r.statusText = "Interrupting..."
		hardUpdated = true
		if reason.Restart() {
			r.chat.AutoRestart = true
			if r.state != nil {
				r.state.UpdateChat(func(chat *domain.Chat) {
					chat.AutoRestart = true
				})
			}
		}
		if r.state != nil {
			r.state.DiscardActiveAssistant()
			r.state.ClearPendingAssistant()
		}
	}
	r.mu.Unlock()
	if cancel != nil && hard {
		cancel()
	}
	if hardUpdated {
		r.broadcast(r.snapshotUpdateFlags(nil, true, false, true, true, false))
	}
	select {
	case r.inbox <- interruptCmd{reason: reason}:
	default:
	}
}

func (r *Chat) StopAfterCurrentTurn() {
	r.Cancel(CancelReasonUserInterrupt)
}

func (r *Chat) Interrupt() {
	r.Cancel(CancelReasonUserInterruptHard)
}

func (r *Chat) ApproveTool(toolCallID string) {
	r.inbox <- approveCmd{toolCallID: strings.TrimSpace(toolCallID)}
}

func (r *Chat) DenyTool(toolCallID string) {
	r.inbox <- denyCmd{toolCallID: strings.TrimSpace(toolCallID)}
}

func (r *Chat) Compact(instructions string) error {
	service := r.deps.Compact
	if service == nil {
		return fmt.Errorf("compaction service is not configured")
	}
	if err := r.EnsureTimeline(context.Background()); err != nil {
		return err
	}
	instructions = strings.TrimSpace(instructions)
	r.mu.Lock()
	chatID := r.chat.ID
	sessionID := r.chat.SessionID
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithShouldStop(ctx, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cancelState == CancelStateCancelling
	})
	r.cancel = cancel
	r.cancelState = CancelStateNone
	r.running = map[string]struct{}{}
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Compacting session..."
	r.mu.Unlock()
	slog.Info("chat compaction requested", "chat_id", chatID, "session_id", sessionID, "instructions_bytes", len(instructions))
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, false, false))

	out := make(chan domain.Event, 32)
	go func() {
		defer close(out)
		if err := service.CompactChat(ctx, r, instructions, out); err != nil {
			r.handleTurnError(ctx, out, err)
		}
	}()
	r.forwardTurnEvents(out)
	return nil
}

func (r *Chat) Close() {
	if r == nil {
		return
	}
	done := r.done
	if done == nil {
		r.handleClose()
		return
	}
	select {
	case r.inbox <- closeCmd{}:
	case <-done:
	}
	<-done
}

// DrainAndClose waits for the active turn to reach a persisted boundary, then closes the chat.
func (r *Chat) DrainAndClose(ctx context.Context) error {
	return r.closeAfterDrain(ctx, "")
}

// DrainAndCloseWithInterruptReason waits for a persisted boundary, queues continuation if needed, and closes the chat.
func (r *Chat) DrainAndCloseWithInterruptReason(ctx context.Context, reason string) error {
	return r.closeAfterDrain(ctx, strings.TrimSpace(reason))
}

// InterruptAndClose cancels active work, records why it was interrupted, and closes the chat.
func (r *Chat) InterruptAndClose(ctx context.Context, reason string) error {
	return r.closeAfterDrain(ctx, strings.TrimSpace(reason), true)
}

func (r *Chat) Shutdown(ctx context.Context, reason CancelReason) error {
	return r.closeAfterCancel(ctx, reason)
}

func (r *Chat) closeAfterCancel(ctx context.Context, reason CancelReason) error {
	chatID := id.ID("")
	sessionID := id.ID("")
	if r != nil {
		r.mu.RLock()
		chatID = r.chat.ID
		sessionID = r.chat.SessionID
		r.mu.RUnlock()
	}
	slog.Info("chat cancel received", "chat_id", chatID, "session_id", sessionID, "reason", reason, "hard", reason.Hard(), "restart", reason.Restart())
	noticeReason := reason.NoticeReason()
	if noticeReason == "" {
		return r.closeAfterDrain(ctx, "")
	}
	return r.closeAfterDrain(ctx, noticeReason, reason.Hard(), reason.Restart())
}

func (r *Chat) closeAfterDrain(ctx context.Context, interruptReason string, cancelActive ...bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	shouldCancelActive := len(cancelActive) > 0 && cancelActive[0]
	autoRestart := len(cancelActive) > 1 && cancelActive[1]
	r.mu.Lock()
	if r.closed {
		chatID := r.chat.ID
		sessionID := r.chat.SessionID
		r.mu.Unlock()
		slog.Info("chat close skipped", "chat_id", chatID, "session_id", sessionID, "reason", interruptReason, "already_closed", true)
		return nil
	}
	wasActive := r.active
	wasRunningTool := r.status == StatusRunningTools && len(r.running) > 0
	var autoRestartChat domain.Chat
	if wasActive && autoRestart {
		r.chat.AutoRestart = true
		if r.state != nil {
			r.state.UpdateChat(func(chat *domain.Chat) {
				chat.AutoRestart = true
			})
		}
		autoRestartChat = r.chat
	}
	r.draining = true
	if r.active {
		if interruptReason == "" || !shouldCancelActive {
			r.statusText = "Stopping after current turn"
		} else {
			r.statusText = "Interrupting..."
			r.cancelState = CancelStateCancelling
		}
	}
	cancel := r.cancel
	chatID := r.chat.ID
	sessionID := r.chat.SessionID
	r.mu.Unlock()
	slog.Info("chat close requested",
		"chat_id", chatID,
		"session_id", sessionID,
		"reason", interruptReason,
		"active", wasActive,
		"running_tool", wasRunningTool,
		"cancel_active", shouldCancelActive,
		"auto_restart", autoRestart,
	)
	if autoRestartChat.ID != "" && r.deps.Store != nil {
		if err := updateChat(context.Background(), r.deps.Store, autoRestartChat); err != nil {
			return err
		}
	}
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, false, false))
	if shouldCancelActive && interruptReason != "" && cancel != nil {
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
	if wasActive && interruptReason != "" && !shouldCancelActive {
		r.queueShutdownContinuation()
	}
	if wasActive && interruptReason != "" && shouldCancelActive && wasRunningTool {
		if err := r.appendPersistedInterruptNotice(context.Background(), interruptReason); err != nil {
			return err
		}
	}
	if err := r.Persist(context.Background(), nil); err != nil {
		return err
	}
	r.Close()
	slog.Info("chat close complete", "chat_id", chatID, "session_id", sessionID, "reason", interruptReason)
	return nil
}

func (r *Chat) queueShutdownContinuation() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.queue {
		if domain.DeliveryForQueuedInput(item) == domain.QueuedInputDeliveryContinue {
			return
		}
	}
	item := domain.QueuedInput{
		ID:        id.New(),
		Kind:      domain.QueuedInputKindContinue,
		Delivery:  domain.QueuedInputDeliveryContinue,
		Origin:    domain.QueuedInputOriginAutoResume,
		Source:    domain.UserMessageSourceAutoResume,
		CreatedAt: time.Now().UTC(),
	}
	r.queue = append(r.queue, item)
	r.chat.QueuedInputs = cloneQueuedInputs(r.queue)
	if r.state != nil {
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.QueuedInputs = cloneQueuedInputs(r.queue)
		})
	}
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

// Timeline returns the live transcript, hydrating it from storage if needed.
func (r *Chat) Timeline(ctx context.Context) ([]domain.TimelineItem, error) {
	if r == nil {
		return nil, fmt.Errorf("chat runtime is required")
	}
	if err := r.EnsureTimeline(ctx); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return nil, nil
	}
	return r.state.SnapshotTimeline(), nil
}

// TimelinePage returns a page of the live transcript.
func (r *Chat) TimelinePage(ctx context.Context, before id.ID, limit int, all bool) (TimelinePage, error) {
	timeline, err := r.Timeline(ctx)
	if err != nil {
		return TimelinePage{}, err
	}
	total := len(timeline)
	if all || limit <= 0 || total <= limit {
		return timelinePage(timeline, false, true, total), nil
	}
	end := total
	if before != "" {
		idx := slices.IndexFunc(timeline, func(item domain.TimelineItem) bool {
			return item.ID == before
		})
		if idx >= 0 {
			end = idx
		}
	}
	if end <= 0 {
		return TimelinePage{LoadedAll: true, Total: total}, nil
	}
	start := max(0, end-limit)
	return timelinePage(timeline[start:end], start > 0, false, total), nil
}

// PendingApprovals returns approval requests derived from the live transcript.
func (r *Chat) PendingApprovals(ctx context.Context) ([]Approval, error) {
	if r == nil {
		return nil, fmt.Errorf("chat runtime is required")
	}
	if err := r.EnsureTimeline(ctx); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return nil, nil
	}
	return r.state.Approvals(), nil
}

// EnsureTimeline loads persisted transcript items into memory once.
func (r *Chat) EnsureTimeline(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("chat runtime is required")
	}
	r.mu.RLock()
	loaded := r.timelineLoaded
	chatRecord := r.chat
	session := r.session
	st := r.deps.Store
	r.mu.RUnlock()
	if loaded {
		return nil
	}
	if st == nil {
		return fmt.Errorf("store is required")
	}
	timeline, err := timelineForChat(ctx, st, chatRecord.ID)
	if err != nil {
		return r.markPersistError(err)
	}
	approvals, err := pendingApprovalsForChat(ctx, st, chatRecord.ID)
	if err != nil {
		return r.markPersistError(err)
	}
	r.mu.Lock()
	chatRecord, err = repairContextCacheFromTimeline(ctx, st, chatRecord, timeline)
	if err != nil {
		r.mu.Unlock()
		return r.markPersistError(err)
	}
	r.session = session
	r.chat = chatRecord
	if r.state == nil {
		r.state = NewTimelineState(chatRecord, timeline, approvals)
	} else {
		r.state.MergeTimelineLoaded(chatRecord, timeline, approvals)
	}
	r.timelineLoaded = true
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, false, true, true))
	return nil
}

func (r *Chat) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return Snapshot{Session: r.session, Chat: r.chat, TimelineHasMore: !r.timelineLoaded, TimelineLoadedAll: r.timelineLoaded, Turn: turnForStatus(r.status, r.active, r.cancelState), Status: r.status, StatusText: r.statusText, TokenUsage: r.chat.TokenUsage.Normalized(), Active: r.active}
	}
	chatRecord := r.state.Chat()
	return Snapshot{
		Session:           r.session,
		Chat:              chatRecord,
		Timeline:          r.state.SnapshotTimeline(),
		TimelineHasMore:   !r.timelineLoaded,
		TimelineLoadedAll: r.timelineLoaded,
		Approvals:         r.state.Approvals(),
		QueuedInputs:      visibleQueuedInputs(r.queue),
		PendingAssistant:  r.state.PendingAssistant(),
		Turn:              turnForStatus(r.status, r.active, r.cancelState),
		Status:            r.status,
		StatusText:        r.statusText,
		Context:           r.state.CurrentContextSize(),
		TokenUsage:        chatRecord.TokenUsage.Normalized(),
		Active:            r.active,
	}
}

type LiveRewindResult struct {
	ChatID         id.ID `json:"chat_id"`
	AnchorItemID   id.ID `json:"anchor_item_id"`
	RemovedCount   int   `json:"removed_count"`
	RemainingCount int   `json:"remaining_count"`
}

// RewindLiveTimelineFrom trims the transcript from anchorItemID onward through
// the hydrated chat owner, updating both live state and durable storage.
func (r *Chat) RewindLiveTimelineFrom(ctx context.Context, anchorItemID id.ID) (LiveRewindResult, error) {
	if r == nil {
		return LiveRewindResult{}, fmt.Errorf("chat runtime is required")
	}
	if anchorItemID == "" {
		return LiveRewindResult{}, fmt.Errorf("anchor item id is required")
	}
	if err := r.EnsureTimeline(ctx); err != nil {
		return LiveRewindResult{}, err
	}
	r.mu.Lock()
	if r.status != StatusIdle && r.status != StatusErrored {
		r.mu.Unlock()
		return LiveRewindResult{}, fmt.Errorf("cannot rewind chat %s while status is %s", r.chat.ID, r.status)
	}
	if r.state == nil {
		r.mu.Unlock()
		return LiveRewindResult{}, fmt.Errorf("chat timeline is not loaded")
	}
	timeline := r.state.SnapshotTimeline()
	idx := slices.IndexFunc(timeline, func(item domain.TimelineItem) bool {
		return item.ID == anchorItemID
	})
	if idx < 0 {
		r.mu.Unlock()
		return LiveRewindResult{}, fmt.Errorf("timeline item %s not found in chat %s", anchorItemID, r.chat.ID)
	}
	next := slices.Clone(timeline[:idx])
	removed := slices.Clone(timeline[idx:])
	chatID := r.chat.ID
	st := r.deps.Store
	r.mu.Unlock()

	if st == nil {
		return LiveRewindResult{}, fmt.Errorf("store is required")
	}
	for _, item := range removed {
		if err := deleteTimelineItem(ctx, st, item.ID); err != nil {
			return LiveRewindResult{}, err
		}
	}

	r.mu.Lock()
	r.state.ReplaceTimeline(next)
	contextTokens, contextKnown := timelineContextAnchorTokens(next)
	if !contextKnown {
		contextTokens = 0
	}
	r.chat.LastKnownContextTokens = contextTokens
	r.chat.ContextTokensKnown = contextKnown
	r.chat.TokenUsage = domain.Usage{}
	r.chat.UpdatedAt = time.Now().UTC()
	r.state.UpdateChat(func(chat *domain.Chat) {
		chat.LastKnownContextTokens = contextTokens
		chat.ContextTokensKnown = contextKnown
		chat.TokenUsage = domain.Usage{}
		chat.UpdatedAt = r.chat.UpdatedAt
	})
	chatRecord := r.chat
	r.timelineLoaded = true
	result := LiveRewindResult{
		ChatID:         chatID,
		AnchorItemID:   anchorItemID,
		RemovedCount:   len(removed),
		RemainingCount: len(next),
	}
	r.mu.Unlock()
	if err := updateChat(ctx, st, chatRecord); err != nil {
		return LiveRewindResult{}, err
	}
	update := r.snapshotUpdateFlags(nil, false, false, true, true, true)
	update.ReplaceTimeline = true
	r.broadcast(update)
	return result, nil
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

func (r *Chat) ClearAutoRestart(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("chat runtime is required")
	}
	r.mu.Lock()
	if !r.chat.AutoRestart {
		r.mu.Unlock()
		return nil
	}
	r.chat.AutoRestart = false
	r.chat.UpdatedAt = time.Now().UTC()
	if r.state != nil {
		r.state.UpdateChat(func(chat *domain.Chat) {
			chat.AutoRestart = false
			chat.UpdatedAt = r.chat.UpdatedAt
		})
	}
	chatRecord := r.chat
	r.mu.Unlock()
	if r.deps.Store != nil {
		if err := updateChat(ctx, r.deps.Store, chatRecord); err != nil {
			return err
		}
	}
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, false, false))
	return nil
}

// UpdateMetadata updates chat metadata owned by this chat runtime.
func (r *Chat) UpdateMetadata(ctx context.Context, update MetadataUpdate) (domain.Chat, error) {
	if r == nil {
		return domain.Chat{}, fmt.Errorf("chat runtime is required")
	}
	title := strings.TrimSpace(update.Title)
	r.mu.Lock()
	if update.Archived != nil && *update.Archived && !r.chat.Archived && !r.canArchiveLocked() {
		blockers := strings.Join(r.archiveBlockersLocked(), ", ")
		r.mu.Unlock()
		return domain.Chat{}, fmt.Errorf("cannot archive chat %s while it is not idle: %s", r.chat.ID, blockers)
	}
	shouldDispatchAfterUpdate := update.Archived != nil && !*update.Archived && r.chat.Archived
	if update.Archived != nil {
		r.chat.Archived = *update.Archived
	}
	if title != "" {
		r.chat.Title = title
	}
	r.chat.UpdatedAt = time.Now().UTC()
	if r.state != nil {
		r.state.UpdateChat(func(chat *domain.Chat) {
			if update.Archived != nil {
				chat.Archived = *update.Archived
			}
			if title != "" {
				chat.Title = title
			}
			chat.UpdatedAt = r.chat.UpdatedAt
		})
	}
	chatRecord := r.chat
	r.mu.Unlock()
	if r.deps.Store != nil {
		if err := updateChat(ctx, r.deps.Store, chatRecord); err != nil {
			return domain.Chat{}, err
		}
	}
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, false, false, false))
	if shouldDispatchAfterUpdate {
		r.maybeDispatchNext()
	}
	return chatRecord, nil
}

func (r *Chat) canArchiveLocked() bool {
	return len(r.archiveBlockersLocked()) == 0
}

func (r *Chat) archiveBlockersLocked() []string {
	var blockers []string
	if r.draining {
		blockers = append(blockers, "shutdown or restart is draining")
	}
	if r.active {
		blockers = append(blockers, "turn is active")
	}
	if r.status != StatusIdle {
		blockers = append(blockers, fmt.Sprintf("status is %s", r.status))
	}
	if len(r.queue) > 0 {
		blockers = append(blockers, fmt.Sprintf("%d queued input(s)", len(r.queue)))
	}
	if len(r.running) > 0 {
		blockers = append(blockers, fmt.Sprintf("%d running tool call(s)", len(r.running)))
	}
	return blockers
}

func (r *Chat) RecordToolResult(ctx context.Context, tool domain.ToolKind, toolCallID string, args map[string]string, result domain.ToolResult) (domain.TimelineItem, error) {
	if r == nil {
		return domain.TimelineItem{}, fmt.Errorf("chat runtime is required")
	}
	if strings.TrimSpace(toolCallID) != "" {
		return r.AttachToolResult(ctx, toolCallID, result)
	}
	now := time.Now().UTC()
	item, err := r.AppendTimelineContent(ctx, domain.ToolExecution{
		Tool:      tool,
		Args:      args,
		Result:    &result,
		StartedAt: now,
		EndedAt:   now,
	})
	if err != nil {
		return domain.TimelineItem{}, err
	}
	item.Seal(now)
	return r.UpsertTimelineItem(ctx, item)
}

// AppendTimelineContent appends a timeline entry through the live chat owner.
func (r *Chat) AppendTimelineContent(ctx context.Context, content domain.TimelineContent) (domain.TimelineItem, error) {
	if r == nil {
		return domain.TimelineItem{}, fmt.Errorf("chat runtime is required")
	}
	if content == nil {
		return domain.TimelineItem{}, fmt.Errorf("timeline content is required")
	}
	if err := r.EnsureTimeline(ctx); err != nil {
		return domain.TimelineItem{}, err
	}
	now := time.Now().UTC()
	r.mu.Lock()
	seq := int64(1)
	if r.state != nil {
		seq = int64(len(r.state.Timeline()) + 1)
	}
	item := domain.TimelineItem{
		ID:        NewTimelineID(now),
		ChatID:    r.chat.ID,
		Seq:       seq,
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if r.state != nil {
		r.state.AppendTimelineItem(item)
		if text := timelineItemSummary(item); text != "" {
			r.chat.LastMessage = text
			r.state.UpdateChat(func(chat *domain.Chat) {
				chat.LastMessage = text
			})
		}
	}
	chatRecord := r.chat
	r.mu.Unlock()
	if r.deps.Store != nil {
		if _, err := insertTimelineItem(ctx, r.deps.Store, item); err != nil {
			return domain.TimelineItem{}, err
		}
		if err := updateChat(ctx, r.deps.Store, chatRecord); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	r.broadcast(r.snapshotUpdateFlags(nil, true, false, false, true, false))
	return item, nil
}

// AppendNotice appends a sealed notice through the live chat owner.
func (r *Chat) AppendNotice(ctx context.Context, notice domain.Notice) (domain.TimelineItem, error) {
	if r == nil {
		return domain.TimelineItem{}, fmt.Errorf("chat runtime is required")
	}
	item, err := r.AppendTimelineContent(ctx, notice)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	item.Seal(time.Now().UTC())
	return r.UpsertTimelineItem(ctx, item)
}

// AppendCompaction appends a pending compaction marker through the live chat owner.
func (r *Chat) AppendCompaction(ctx context.Context, compaction domain.Compaction) (domain.TimelineItem, error) {
	if strings.TrimSpace(compaction.Status) == "" {
		compaction.Status = "pending"
	}
	return r.AppendTimelineContent(ctx, compaction)
}

// UpdateCompaction updates and seals a compaction marker when it reaches a final state.
func (r *Chat) UpdateCompaction(ctx context.Context, item domain.TimelineItem, compaction domain.Compaction) (domain.TimelineItem, error) {
	if r == nil {
		return domain.TimelineItem{}, fmt.Errorf("chat runtime is required")
	}
	now := time.Now().UTC()
	item.Content = compaction
	item.UpdatedAt = now
	if compaction.Status == "completed" || compaction.Status == "failed" {
		item.Seal(now)
	}
	return r.UpsertTimelineItem(ctx, item)
}

// AppendAssistantToolCalls appends or updates one sealed assistant message through the live chat owner.
func (r *Chat) AppendAssistantToolCalls(ctx context.Context, item domain.TimelineItem, calls []domain.ToolCall, text string, reasoning domain.ReasoningContent, usage domain.Usage) (domain.TimelineItem, error) {
	if r == nil {
		return domain.TimelineItem{}, fmt.Errorf("chat runtime is required")
	}
	if len(calls) == 0 && strings.TrimSpace(text) == "" {
		return domain.TimelineItem{}, fmt.Errorf("assistant item needs text or tool calls")
	}
	if err := r.EnsureTimeline(ctx); err != nil {
		return domain.TimelineItem{}, err
	}
	assistant := domain.AssistantMessage{Text: text, Reasoning: reasoning}
	for _, call := range calls {
		if err := assistant.AddToolCall(call); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	usage = usage.Normalized()
	if usage.HasAnyTokens() {
		assistant.Usage = &usage
	}
	return r.appendAssistantItem(ctx, item, assistant)
}

// AppendAssistantMessage appends or updates one sealed assistant message through the live chat owner.
func (r *Chat) AppendAssistantMessage(ctx context.Context, item domain.TimelineItem, assistant domain.AssistantMessage) (domain.TimelineItem, error) {
	if r == nil {
		return domain.TimelineItem{}, fmt.Errorf("chat runtime is required")
	}
	if strings.TrimSpace(assistant.Text) == "" && strings.TrimSpace(assistant.Reasoning.Text) == "" {
		return domain.TimelineItem{}, fmt.Errorf("assistant item needs text or reasoning")
	}
	if err := r.EnsureTimeline(ctx); err != nil {
		return domain.TimelineItem{}, err
	}
	return r.appendAssistantItem(ctx, item, assistant)
}

func (r *Chat) appendAssistantItem(ctx context.Context, item domain.TimelineItem, assistant domain.AssistantMessage) (domain.TimelineItem, error) {
	now := time.Now().UTC()
	r.mu.Lock()
	if item.ID == "" {
		item.ID = NewTimelineID(now)
	}
	if item.ChatID == "" {
		item.ChatID = r.chat.ID
	}
	if item.Seq == 0 && r.state != nil {
		item.Seq = int64(len(r.state.Timeline()) + 1)
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	item.Content = assistant
	item.Seal(now)
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
	if r.deps.Store != nil {
		if _, err := insertTimelineItem(ctx, r.deps.Store, item); err != nil {
			if putErr := putTimelineItem(ctx, r.deps.Store, item); putErr != nil {
				return domain.TimelineItem{}, err
			}
		}
		if err := updateChat(ctx, r.deps.Store, chatRecord); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	r.broadcast(r.snapshotUpdateFlags(nil, true, false, false, true, true))
	return item, nil
}

// MarkToolRunning marks one live tool call as running and persists the updated item.
func (r *Chat) MarkToolRunning(ctx context.Context, toolCallID string) (domain.TimelineItem, error) {
	return r.updateToolCall(ctx, toolCallID, func(call *domain.ToolCall) error {
		call.Status = domain.ToolStatusRunning
		call.Approval = nil
		call.ApprovalID = ""
		if call.StartedAt.IsZero() {
			call.StartedAt = time.Now().UTC()
		}
		return nil
	})
}

// AttachToolResult attaches a result to one live tool call and persists the updated item.
func (r *Chat) AttachToolResult(ctx context.Context, toolCallID string, result domain.ToolResult) (domain.TimelineItem, error) {
	return r.updateToolCall(ctx, toolCallID, func(call *domain.ToolCall) error {
		call.Result = &result
		call.Error = nil
		call.Approval = nil
		call.ApprovalID = ""
		if result.Status == domain.ToolResultStatusDenied {
			call.Status = domain.ToolStatusDenied
		} else {
			call.Status = domain.ToolStatusDone
		}
		if call.CompletedAt.IsZero() {
			call.CompletedAt = time.Now().UTC()
		}
		return nil
	})
}

// AttachToolError attaches an error to one live tool call and persists the updated item.
func (r *Chat) AttachToolError(ctx context.Context, toolCallID string, toolErr domain.ToolError) (domain.TimelineItem, error) {
	return r.updateToolCall(ctx, toolCallID, func(call *domain.ToolCall) error {
		call.Error = &toolErr
		call.Result = nil
		call.Approval = nil
		call.ApprovalID = ""
		call.Status = domain.ToolStatusErrored
		if call.CompletedAt.IsZero() {
			call.CompletedAt = time.Now().UTC()
		}
		return nil
	})
}

// AttachToolApproval marks one live tool call as awaiting approval.
func (r *Chat) AttachToolApproval(ctx context.Context, toolCallID string, approval domain.ApprovalRequest) (domain.TimelineItem, error) {
	_ = approval
	return r.updateToolCall(ctx, toolCallID, func(call *domain.ToolCall) error {
		call.Approval = nil
		call.ApprovalID = strings.TrimSpace(toolCallID)
		call.Status = domain.ToolStatusAwaitingApproval
		return nil
	})
}

// RequestForToolCall returns a normalized tool request for an approval-pending tool call.
func (r *Chat) RequestForToolCall(ctx context.Context, toolCallID string) (domain.ToolCall, error) {
	if r == nil {
		return domain.ToolCall{}, fmt.Errorf("chat runtime is required")
	}
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return domain.ToolCall{}, fmt.Errorf("tool call id is required")
	}
	if err := r.EnsureTimeline(ctx); err != nil {
		return domain.ToolCall{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state == nil {
		return domain.ToolCall{}, fmt.Errorf("tool call %q not found", toolCallID)
	}
	items := r.state.SnapshotTimeline()
	for idx := len(items) - 1; idx >= 0; idx-- {
		assistant, ok := items[idx].Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		call := assistant.ToolByID(domain.ToolCallID(toolCallID))
		if call == nil {
			continue
		}
		if call.Status != domain.ToolStatusAwaitingApproval {
			return domain.ToolCall{}, fmt.Errorf("tool call %q is %s, not awaiting approval", toolCallID, call.Status)
		}
		return *call, nil
	}
	return domain.ToolCall{}, fmt.Errorf("tool call %q not found", toolCallID)
}

// FailRunningToolCalls marks currently running tool calls as failed.
func (r *Chat) FailRunningToolCalls(ctx context.Context, message string) (int, error) {
	return r.failToolCallsMatching(ctx, message, func(status domain.ToolStatus) bool {
		return status == domain.ToolStatusRunning
	})
}

// FailInterruptedToolCalls marks pending or running tool calls as failed.
func (r *Chat) FailInterruptedToolCalls(ctx context.Context, message string) (int, error) {
	return r.failToolCallsMatching(ctx, message, interruptedToolStatus)
}

func (r *Chat) failToolCallsMatching(ctx context.Context, message string, match func(domain.ToolStatus) bool) (int, error) {
	if r == nil {
		return 0, fmt.Errorf("chat runtime is required")
	}
	if match == nil {
		return 0, nil
	}
	if err := r.EnsureTimeline(ctx); err != nil {
		return 0, err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Tool execution failed because koder restarted before the tool completed."
	}
	now := time.Now().UTC()
	var changed []domain.TimelineItem
	r.mu.Lock()
	if r.state == nil {
		r.mu.Unlock()
		return 0, nil
	}
	count := 0
	for _, record := range r.state.Timeline() {
		if record == nil {
			continue
		}
		assistant, ok := record.Item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		itemChanged := false
		for idx := range assistant.Tools {
			call := &assistant.Tools[idx]
			if !match(call.Status) || call.Result != nil || call.Error != nil {
				continue
			}
			call.Status = domain.ToolStatusErrored
			call.Error = &domain.ToolError{Message: message, Code: domain.NoticeReasonProcessRestart}
			call.Approval = nil
			call.ApprovalID = ""
			if call.CompletedAt.IsZero() {
				call.CompletedAt = now
			}
			itemChanged = true
			count++
		}
		if !itemChanged {
			continue
		}
		record.Item.Content = assistant
		record.Item.UpdatedAt = now
		changed = append(changed, record.Item)
	}
	chatRecord := r.chat
	r.mu.Unlock()
	if count == 0 {
		return 0, nil
	}
	if r.deps.Store != nil {
		for _, item := range changed {
			if err := putTimelineItem(ctx, r.deps.Store, item); err != nil {
				return count, err
			}
		}
		if err := updateChat(ctx, r.deps.Store, chatRecord); err != nil {
			return count, err
		}
	}
	r.broadcast(r.snapshotUpdateFlags(nil, true, false, false, true, true))
	return count, nil
}

func (r *Chat) updateToolCall(ctx context.Context, toolCallID string, update func(*domain.ToolCall) error) (domain.TimelineItem, error) {
	if r == nil {
		return domain.TimelineItem{}, fmt.Errorf("chat runtime is required")
	}
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return domain.TimelineItem{}, fmt.Errorf("tool call id is required")
	}
	if update == nil {
		return domain.TimelineItem{}, fmt.Errorf("tool call update is required")
	}
	if err := r.EnsureTimeline(ctx); err != nil {
		return domain.TimelineItem{}, err
	}
	now := time.Now().UTC()
	var item domain.TimelineItem
	r.mu.Lock()
	if r.state == nil {
		r.mu.Unlock()
		return domain.TimelineItem{}, fmt.Errorf("chat timeline is not loaded")
	}
	records := r.state.Timeline()
	for idx := len(records) - 1; idx >= 0; idx-- {
		record := records[idx]
		if record == nil {
			continue
		}
		assistant, ok := record.Item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		call := assistant.ToolByID(domain.ToolCallID(toolCallID))
		if call == nil {
			continue
		}
		if err := update(call); err != nil {
			r.mu.Unlock()
			return domain.TimelineItem{}, err
		}
		record.Item.Content = assistant
		record.Item.UpdatedAt = now
		item = record.Item
		if text := timelineItemSummary(item); text != "" {
			r.chat.LastMessage = text
			r.state.UpdateChat(func(chat *domain.Chat) {
				chat.LastMessage = text
			})
		}
		break
	}
	chatRecord := r.chat
	r.mu.Unlock()
	if item.ID == "" {
		return domain.TimelineItem{}, fmt.Errorf("tool call %q has no owning assistant item", toolCallID)
	}
	if r.deps.Store != nil {
		if err := putTimelineItem(ctx, r.deps.Store, item); err != nil {
			return domain.TimelineItem{}, err
		}
		if err := updateChat(ctx, r.deps.Store, chatRecord); err != nil {
			return domain.TimelineItem{}, err
		}
	}
	r.broadcast(r.snapshotUpdateFlags(nil, true, false, false, true, true))
	return item, nil
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
	defer close(r.done)
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
			r.handleInterrupt(typed.reason)
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
	if err := r.persistQueue(); err != nil {
		_ = r.markPersistError(err)
		return
	}
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

func (r *Chat) handleReorderQueue(ids []id.ID) {
	if len(ids) == 0 {
		return
	}
	r.mu.Lock()
	queueMap := make(map[id.ID]domain.QueuedInput, len(r.queue))
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

func (r *Chat) handleDeleteQueueItem(id id.ID) {
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

func (r *Chat) handleSendQueueItemNow(id id.ID) {
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
	item.Delivery = domain.QueuedInputDeliveryNextTurn
	item.Kind = domain.QueuedInputKindQueued
	item.Held = false
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	remaining := append(slices.Clone(r.queue[:idx]), r.queue[idx+1:]...)
	if !r.draining && !r.active && r.status != StatusWaitingApproval {
		r.mu.Unlock()
		r.handleDispatchQueued(item, remaining)
		return
	}
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
	if r.chat.Archived || r.draining {
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
	ctx = WithShouldStop(ctx, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cancelState == CancelStateCancelling
	})
	r.cancel = cancel
	r.cancelState = CancelStateNone
	r.running = map[string]struct{}{}
	if domain.DeliveryForQueuedInput(item) == domain.QueuedInputDeliveryContinue {
		r.activeUserSource = ""
	} else {
		r.activeUserSource = domain.UserMessageSourceForQueuedInput(item)
	}
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Waiting for LLM response"
	r.mu.Unlock()

	_ = r.persistQueue()
	r.broadcast(r.snapshotUpdateFlags(nil, domain.DeliveryForQueuedInput(item) != domain.QueuedInputDeliveryContinue, true, true, true, false))
	r.runItem(ctx, item)
}

func (r *Chat) handleInterrupt(reason CancelReason) {
	r.mu.Lock()
	if r.closed || !r.active {
		r.mu.Unlock()
		return
	}
	r.draining = true
	if reason.Restart() {
		r.chat.AutoRestart = true
		if r.state != nil {
			r.state.UpdateChat(func(chat *domain.Chat) {
				chat.AutoRestart = true
			})
		}
	}
	cancel := r.cancel
	if reason.Hard() {
		r.cancelState = CancelStateCancelling
		r.statusText = "Interrupting..."
		if r.state != nil {
			r.state.DiscardActiveAssistant()
			r.state.ClearPendingAssistant()
		}
	} else {
		r.statusText = "Stopping after current turn"
	}
	r.mu.Unlock()
	if cancel != nil && reason.Hard() {
		cancel()
	}
	_ = r.Persist(context.Background(), nil)
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, false, false))
}

func (r *Chat) handleApprove(toolCallID string, rule *accesssettings.PermissionOverride) {
	if service := r.deps.Tools; service != nil {
		r.handleApproveWithTurnLoop(service, toolCallID, rule)
		return
	}
	err := fmt.Errorf("tool approval service is not configured")
	evt := domain.Event{Kind: domain.EventKindError, Err: err}
	r.broadcast(r.snapshotUpdateFlags(&evt, false, false, true, false, false))
}

func (r *Chat) handleDeny(toolCallID string) {
	if service := r.deps.Tools; service != nil {
		r.handleDenyWithTurnLoop(service, toolCallID)
		return
	}
	err := fmt.Errorf("tool approval service is not configured")
	evt := domain.Event{Kind: domain.EventKindError, Err: err}
	r.broadcast(r.snapshotUpdateFlags(&evt, false, false, true, false, false))
}

func (r *Chat) handleApproveWithTurnLoop(service ToolTurnService, toolCallID string, rule *accesssettings.PermissionOverride) {
	if err := r.EnsureTimeline(context.Background()); err != nil {
		return
	}
	r.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithShouldStop(ctx, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cancelState == CancelStateCancelling
	})
	r.cancel = cancel
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Waiting for LLM response"
	toolCallID = r.resolveApprovalToolCallIDLocked(toolCallID)
	r.removeApprovalLocked(toolCallID)
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, true, false, true, false, true))

	out := make(chan domain.Event, 32)
	go func() {
		defer close(out)
		shouldContinue, err := service.ApproveToolForTurn(ctx, r, toolCallID, rule, out)
		if err != nil {
			r.handleTurnError(ctx, out, err)
			return
		}
		if shouldContinue {
			r.continueTurnLoop(ctx, nil, out)
		}
	}()
	r.forwardTurnEvents(out)
}

func (r *Chat) handleDenyWithTurnLoop(service ToolTurnService, toolCallID string) {
	if err := r.EnsureTimeline(context.Background()); err != nil {
		return
	}
	r.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithShouldStop(ctx, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cancelState == CancelStateCancelling
	})
	r.cancel = cancel
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Waiting for LLM response"
	toolCallID = r.resolveApprovalToolCallIDLocked(toolCallID)
	r.removeApprovalLocked(toolCallID)
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, true, false, true, false, true))

	out := make(chan domain.Event, 32)
	go func() {
		defer close(out)
		if err := service.DenyToolForTurn(ctx, r, toolCallID, out); err != nil {
			r.handleTurnError(ctx, out, err)
		}
	}()
	r.forwardTurnEvents(out)
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
	r.state.RemoveApproval(id.ID(toolCallID))
}

func (r *Chat) handleResumePendingTools() {
	if service := r.deps.Pending; service != nil {
		r.handleResumePendingToolsWithTurnLoop(service)
		return
	}
}

func (r *Chat) handleResumePendingToolsWithTurnLoop(service PendingToolService) {
	if err := r.EnsureTimeline(context.Background()); err != nil {
		return
	}
	r.mu.Lock()
	if r.active || r.status == StatusWaitingApproval || r.draining {
		r.mu.Unlock()
		return
	}
	if !r.state.HasPendingExecutableToolCalls() {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithShouldStop(ctx, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cancelState == CancelStateCancelling
	})
	r.cancel = cancel
	r.cancelState = CancelStateNone
	r.running = map[string]struct{}{}
	r.active = true
	r.status = StatusRunningTools
	r.statusText = "Resuming tool calls"
	r.mu.Unlock()
	r.broadcast(r.snapshotUpdateFlags(nil, false, false, true, false, false))

	out := make(chan domain.Event, 32)
	go func() {
		defer close(out)
		shouldContinue, err := service.ResumePendingToolsForTurn(ctx, r, out)
		if err != nil {
			r.handleTurnError(ctx, out, err)
			return
		}
		if shouldContinue {
			r.continueTurnLoop(ctx, nil, out)
		}
	}()
	r.forwardTurnEvents(out)
}

func (r *Chat) handleStreamEvent(evt domain.Event) {
	refreshedQueue, queueChanged, queueErr := r.queueRefreshForEvent(evt)
	r.mu.Lock()
	if r.cancelState == CancelStateCancelling && discardEventDuringHardCancel(evt) {
		r.mu.Unlock()
		return
	}
	transcriptChanged := false
	contextChanged := false
	tokenUsageChanged := false
	metadataChanged := false
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
		usage := evt.Usage.Normalized()
		if usage.HasAnyTokens() {
			r.chat.TokenUsage = r.chat.TokenUsage.Add(usage)
			if r.state != nil {
				total := r.chat.TokenUsage
				r.state.UpdateChat(func(chat *domain.Chat) {
					chat.TokenUsage = total
				})
			}
			tokenUsageChanged = true
		}
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
			metadataChanged = true
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
		} else if text, ok := compactionStatusText(evt); ok {
			r.status = StatusWaitingLLM
			r.statusText = text
		} else if text, ok := cavemanStatusText(evt); ok {
			r.status = StatusWaitingLLM
			r.statusText = text
		}
		if completedCompaction(evt.Item, evt.Meta) {
			r.chat.LastKnownContextTokens = 0
			r.chat.ContextTokensKnown = false
			r.chat.TokenUsage = domain.Usage{}
			if r.state != nil {
				r.state.UpdateChat(func(chat *domain.Chat) {
					chat.LastKnownContextTokens = 0
					chat.ContextTokensKnown = false
					chat.TokenUsage = domain.Usage{}
				})
			}
			tokenUsageChanged = true
			contextChanged = true
		}
	}
	chatRecord := r.chat
	r.mu.Unlock()
	if metadataChanged && r.deps.Store != nil {
		_ = updateChat(context.Background(), r.deps.Store, chatRecord)
	}
	if tokenUsageChanged && r.deps.Store != nil {
		_, _ = updateStoredChatUsage(context.Background(), r.deps.Store, chatRecord.ID, chatRecord)
	}
	copyEvt := evt
	if queueErr != nil {
		copyEvt = domain.Event{Kind: domain.EventKindError, Err: queueErr}
	}
	r.broadcast(r.snapshotUpdateFlags(&copyEvt, transcriptChanged || evt.Kind == domain.EventKindMessageDone, queueChanged, true, contextChanged, evt.Kind == domain.EventKindApprovalAsk || evt.Kind == domain.EventKindApprovalReply))
}

func discardEventDuringHardCancel(evt domain.Event) bool {
	switch evt.Kind {
	case domain.EventKindMessageDelta,
		domain.EventKindReasoning,
		domain.EventKindToolCallDelta,
		domain.EventKindMessageDone,
		domain.EventKindUsage:
		return true
	default:
		return false
	}
}

func updateStoredChatUsage(ctx context.Context, st *store.Store, chatID id.ID, update domain.Chat) (domain.Chat, error) {
	stored, err := getChat(ctx, st, chatID)
	if err != nil {
		return domain.Chat{}, err
	}
	stored.LastKnownContextTokens = update.LastKnownContextTokens
	stored.ContextTokensKnown = update.ContextTokensKnown
	stored.TokenUsage = update.TokenUsage.Normalized()
	stored.UpdatedAt = time.Now().UTC()
	if err := putChat(ctx, st, stored); err != nil {
		return domain.Chat{}, err
	}
	return stored, nil
}

func (r *Chat) queueRefreshForEvent(evt domain.Event) ([]domain.QueuedInput, bool, error) {
	if evt.Meta[domain.EventMetaRefresh] != domain.EventRefreshQueue || r.deps.Store == nil {
		return nil, false, nil
	}
	chat, err := getChat(context.Background(), r.deps.Store, r.chat.ID)
	if err != nil {
		return nil, false, fmt.Errorf("refresh queued inputs: %w", err)
	}
	return cloneQueuedInputs(chat.QueuedInputs), true, nil
}

func promptProgressStatusText(meta map[string]string) (string, bool) {
	if meta[domain.EventMetaPromptProgress] != "true" {
		return "", false
	}
	prefix := "LLM preprocessing"
	if meta["compaction"] == "progress" {
		prefix = "Compaction pre-processing"
	} else if meta["caveman"] == "progress" {
		prefix = "Caveman pre-processing"
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
		return fmt.Sprintf("%s %d%%", prefix, percent), true
	}
	return prefix, true
}

func compactionStatusText(evt domain.Event) (string, bool) {
	if evt.Meta["compaction"] != "streaming" {
		return "", false
	}
	text := strings.TrimSpace(evt.Text)
	if text == "" {
		return "Streaming compacted results", true
	}
	return text, true
}

func cavemanStatusText(evt domain.Event) (string, bool) {
	switch evt.Meta["caveman"] {
	case "started", "streaming":
	default:
		return "", false
	}
	text := strings.TrimSpace(evt.Text)
	if text == "" {
		return "Converting thinking to caveman", true
	}
	return text, true
}

func completedCompaction(item domain.TimelineItem, meta map[string]string) bool {
	if meta["compaction"] != "completed" {
		return false
	}
	payload, ok := item.Content.(domain.Compaction)
	return ok && payload.Status == "completed"
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
	case domain.LintMessage:
		return strings.TrimSpace(payload.Text)
	}
	return ""
}

func (r *Chat) handleStreamClosed() {
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel = nil
	}
	discardPartialAssistant := r.cancelState == CancelStateCancelling
	shouldDispatch := r.status != StatusWaitingApproval
	if r.status != StatusErrored && r.status != StatusWaitingApproval {
		r.active = false
		r.status = StatusIdle
		r.statusText = "Idle"
		if r.state != nil {
			if discardPartialAssistant {
				r.state.DiscardActiveAssistant()
			} else {
				r.state.SealActiveAssistant("")
			}
			r.state.ClearPendingAssistant()
		}
	}
	r.cancelState = CancelStateNone
	r.running = nil
	r.activeUserSource = ""
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
	if r.chat.Archived || r.draining || r.active || r.status == StatusWaitingApproval {
		r.mu.Unlock()
		return
	}
	idx := nextDispatchableIndex(r.queue)
	if idx < 0 {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	if err := r.EnsureTimeline(context.Background()); err != nil {
		return
	}
	r.mu.Lock()
	if r.chat.Archived || r.draining || r.active || r.status == StatusWaitingApproval {
		r.mu.Unlock()
		return
	}
	if r.state.HasPendingExecutableToolCalls() && r.deps.Pending != nil {
		r.mu.Unlock()
		r.inbox <- resumePendingToolsCmd{}
		return
	}
	idx = nextDispatchableIndex(r.queue)
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
	ctx = WithShouldStop(ctx, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cancelState == CancelStateCancelling
	})
	r.cancel = cancel
	r.cancelState = CancelStateNone
	r.running = map[string]struct{}{}
	if domain.DeliveryForQueuedInput(item) == domain.QueuedInputDeliveryContinue {
		r.activeUserSource = ""
	} else {
		r.activeUserSource = domain.UserMessageSourceForQueuedInput(item)
	}
	r.active = true
	r.status = StatusWaitingLLM
	r.statusText = "Waiting for LLM response"
	r.mu.Unlock()

	_ = r.persistQueue()
	r.broadcast(r.snapshotUpdateFlags(nil, domain.DeliveryForQueuedInput(item) != domain.QueuedInputDeliveryContinue, true, true, true, false))
	r.runItem(ctx, item)
}

func (r *Chat) runItem(ctx context.Context, item domain.QueuedInput) {
	r.mu.Lock()
	note := r.queueNotes[item.ID]
	delete(r.queueNotes, item.ID)
	r.mu.Unlock()
	if r.deps.Model != nil {
		r.runTurnLoop(ctx, item, note)
		return
	}
	err := fmt.Errorf("model service is not configured")
	r.mu.Lock()
	r.active = false
	r.cancel = nil
	r.status = StatusErrored
	r.statusText = err.Error()
	r.mu.Unlock()
	evt := domain.Event{Kind: domain.EventKindError, Err: err}
	r.broadcast(r.snapshotUpdateFlags(&evt, false, false, true, false, false))
	r.maybeDispatchNext()
}

func (r *Chat) runTurnLoop(ctx context.Context, item domain.QueuedInput, note string) {
	out := make(chan domain.Event, 32)
	go func() {
		defer close(out)
		var (
			turnInstructions []provider.InstructionBlock
			err              error
		)
		model := r.deps.Model
		if model == nil {
			r.handleTurnError(ctx, out, fmt.Errorf("model service is not configured"))
			return
		}
		switch domain.DeliveryForQueuedInput(item) {
		case domain.QueuedInputDeliveryContinue:
			turnInstructions, err = model.PrepareContinueTurn(ctx, r, note, out)
		default:
			turnInstructions, err = model.PreparePromptTurn(ctx, r, item, item.Text, queuedAttachmentDrafts(item.Attachments), queuedReferenceDrafts(item.References), note, out)
		}
		if err != nil {
			r.handleTurnError(ctx, out, err)
			return
		}
		r.continueTurnLoop(ctx, turnInstructions, out)
	}()
	r.forwardTurnEvents(out)
}

func (r *Chat) continueTurnLoop(ctx context.Context, turnInstructions []provider.InstructionBlock, out chan<- domain.Event) {
	if r.deps.Model == nil {
		r.handleTurnError(ctx, out, fmt.Errorf("model service is not configured"))
		return
	}
	loop := modelTurnLoop{model: r.deps.Model, session: r.Snapshot().Session}
	for step := 0; step < loop.maxSteps(); step++ {
		if step > 0 {
			item, applied, err := r.ApplyNextSteer(ctx)
			if err != nil {
				r.handleTurnError(ctx, out, err)
				return
			}
			if applied {
				turnInstructions = nil
				out <- domain.Event{
					Kind: domain.EventKindStatus,
					Text: "Applying queued steer...",
					Item: item,
					Meta: map[string]string{domain.EventMetaRefresh: domain.EventRefreshQueue},
				}
			}
		}
		result, err := loop.step(ctx, r, step, turnInstructions, out)
		if err != nil {
			r.handleTurnError(ctx, out, err)
			return
		}
		if result.WaitingApproval || result.Done {
			return
		}
		if !result.Continue {
			return
		}
		turnInstructions = result.TurnInstructions
	}
	loop.pauseLimit(ctx, r, out)
}

func (r *Chat) handleTurnError(ctx context.Context, out chan<- domain.Event, err error) {
	if err == nil {
		return
	}
	sessionID := id.ID("")
	if r != nil {
		sessionID = r.Snapshot().Session.ID
	}
	if errors.Is(err, context.Canceled) {
		if r.deps.Model != nil {
			r.deps.Model.RecordLifecycle(sessionID, "interrupted", "Interrupted", nil)
		}
		out <- domain.Event{Kind: domain.EventKindStatus, Text: "Interrupted"}
		return
	}
	if r.deps.Model != nil {
		r.deps.Model.RecordLifecycle(sessionID, "assistant_error", err.Error(), nil)
	}
	item, appendErr := r.AppendNotice(ctx, domain.Notice{
		Kind:  "model_error",
		Level: "error",
		Text:  "Error: " + strings.TrimSpace(err.Error()),
	})
	evt := domain.Event{Kind: domain.EventKindError, Err: err}
	if appendErr == nil {
		evt.Item = item
	}
	out <- evt
}

func (r *Chat) forwardTurnEvents(events <-chan domain.Event) {
	go func() {
		for evt := range events {
			r.inbox <- streamEventCmd{event: evt}
		}
		r.inbox <- streamClosedCmd{}
	}()
}

func (r *Chat) persistQueue() error {
	if r.deps.Store == nil || r.chat.ID == "" {
		return nil
	}
	r.mu.RLock()
	items := cloneQueuedInputs(r.queue)
	r.mu.RUnlock()
	return setChatQueuedInputs(context.Background(), r.deps.Store, r.chat.ID, items)
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
	toolName := strings.TrimSpace(tool.DisplayName())
	if toolName == "" {
		return "Running tool"
	}
	return "Running " + toolName
}

func toolCallDeltaStatusText(evt domain.Event) string {
	toolName := strings.TrimSpace(evt.Tool.DisplayName())
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

func (r *Chat) hasQueuedModelTurn() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, item := range r.queue {
		if item.Held {
			continue
		}
		switch domain.DeliveryForQueuedInput(item) {
		case domain.QueuedInputDeliveryContinue, domain.QueuedInputDeliveryNextTurn:
			return true
		}
	}
	return false
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
		Turn:              snapshot.Turn,
		Status:            snapshot.Status,
		StatusText:        snapshot.StatusText,
		Queue:             visibleQueuedInputs(snapshot.QueuedInputs),
		Context:           snapshot.Context,
		TokenUsage:        snapshot.TokenUsage,
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
	if domain.DeliveryForQueuedInput(item) == domain.QueuedInputDeliveryContinue || r.state == nil {
		return
	}
	now := time.Now().UTC()
	summary := strings.TrimSpace(item.Text)
	user := domain.UserMessage{Text: summary, Source: domain.UserMessageSourceForQueuedInput(item)}
	for _, draft := range item.Attachments {
		user.Attachments = append(user.Attachments, domain.Attachment(draft))
	}
	for _, ref := range item.References {
		user.References = append(user.References, domain.Reference(ref))
	}
	r.mu.Lock()
	timelineItem := domain.TimelineItem{
		ID:        NewTimelineID(now),
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
		ID:        NewTimelineID(now),
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
	if r.deps.Store != nil {
		item, err = appendTimeline(ctx, r.deps.Store, r.chat.ID, notice)
		if err != nil {
			return err
		}
		item.Seal(now)
		if err := putTimelineItem(ctx, r.deps.Store, item); err != nil {
			return err
		}
	} else {
		item = domain.TimelineItem{
			ID:        NewTimelineID(now),
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
	for idx, item := range items {
		if item.Held || domain.DeliveryForQueuedInput(item) != domain.QueuedInputDeliveryContinue {
			continue
		}
		return idx
	}
	for idx, item := range items {
		if item.Held {
			continue
		}
		return idx
	}
	return -1
}

func queuedInputFromItem(item QueueItem) domain.QueuedInput {
	kind := domain.QueuedInputKindQueued
	delivery := domain.QueuedInputDeliveryNextTurn
	origin := domain.QueuedInputOriginUser
	switch item.Kind {
	case QueueKindSteer:
		kind = domain.QueuedInputKindSteer
		delivery = domain.QueuedInputDeliveryTurnBoundary
	case QueueKindContinue:
		kind = domain.QueuedInputKindContinue
		delivery = domain.QueuedInputDeliveryContinue
		origin = domain.QueuedInputOriginAutoResume
	case QueueKindUser, QueueKindQueued:
		kind = domain.QueuedInputKindQueued
		delivery = domain.QueuedInputDeliveryNextTurn
	}
	switch strings.TrimSpace(item.Source) {
	case domain.UserMessageSourceSubchat:
		origin = domain.QueuedInputOriginSubchat
	case domain.UserMessageSourceAutoGenerated:
		origin = domain.QueuedInputOriginAutoGenerated
	case domain.UserMessageSourceAutoResume:
		origin = domain.QueuedInputOriginAutoResume
	case domain.UserMessageSourceRejectedSteer:
		origin = domain.QueuedInputOriginRejectedSteer
	case domain.UserMessageSourceUser, "":
		if item.Kind == QueueKindSteer {
			origin = domain.QueuedInputOriginUser
		}
	}
	return domain.QueuedInput{
		ID:          id.New(),
		Kind:        kind,
		Delivery:    delivery,
		Origin:      origin,
		Text:        strings.TrimSpace(item.Text),
		Source:      strings.TrimSpace(item.Source),
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

func visibleQueuedInputs(src []domain.QueuedInput) []domain.QueuedInput {
	return cloneQueuedInputs(src)
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
