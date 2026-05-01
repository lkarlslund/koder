package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/domain"
)

type pebbleBackend struct {
	db *pebble.DB
	mu sync.Mutex
}

func openPebbleBackend(stateDir string) (*pebbleBackend, error) {
	dir := filepath.Join(stateDir, "store-pebble")
	if err := ensureDir(dir); err != nil {
		return nil, fmt.Errorf("create pebble store dir: %w", err)
	}
	db, err := pebble.Open(dir, &pebble.Options{Logger: silentPebbleLogger{}})
	if err != nil {
		return nil, fmt.Errorf("open pebble: %w", err)
	}
	b := &pebbleBackend{db: db}
	if err := b.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return b, nil
}

type silentPebbleLogger struct{}

func (silentPebbleLogger) Infof(string, ...interface{}) {}

func (silentPebbleLogger) Fatalf(format string, args ...interface{}) {
	log.Printf("pebble fatal: "+format, args...)
}

func (b *pebbleBackend) init() error {
	_, closer, err := b.db.Get([]byte("meta/store"))
	if err == nil {
		return closer.Close()
	}
	if !errors.Is(err, pebble.ErrNotFound) {
		return fmt.Errorf("read pebble metadata: %w", err)
	}
	metaBytes, err := encodeJSON(defaultMeta(BackendPebble))
	if err != nil {
		return fmt.Errorf("encode pebble metadata: %w", err)
	}
	return b.db.Set([]byte("meta/store"), metaBytes, pebble.Sync)
}

func (b *pebbleBackend) Close() error {
	return b.db.Close()
}

func (b *pebbleBackend) EnsureSession(ctx context.Context, providerID, modelID string) (domain.Session, error) {
	sessions, err := b.ListSessions(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	if len(sessions) > 0 {
		return sessions[0], nil
	}
	return b.CreateSession(ctx, "New Session", providerID, modelID, nil)
}

func (b *pebbleBackend) CreateSession(ctx context.Context, title, providerID, modelID string, parentID *int64) (domain.Session, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Session{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	meta, err := b.readMeta()
	if err != nil {
		return domain.Session{}, err
	}
	now := time.Now().UTC()
	session := domain.Session{
		ID:                meta.NextSessionID,
		ParentID:          parentID,
		Title:             title,
		ProviderID:        providerID,
		ModelID:           modelID,
		PermissionProfile: "",
		PermissionRules:   nil,
		ToolStates:        map[domain.ToolKind]bool{},
		CreatedAt:         now,
		UpdatedAt:         now,
		LastMessage:       "",
	}
	meta.NextSessionID++

	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putMeta(batch, meta); err != nil {
		return domain.Session{}, err
	}
	if err := b.putSession(batch, nil, &session); err != nil {
		return domain.Session{}, err
	}
	chat := domain.Chat{
		ID:                meta.NextChatID,
		SessionID:         session.ID,
		Title:             "Main",
		WorkflowRole:      domain.WorkflowRoleOrchestrator,
		PermissionProfile: session.PermissionProfile,
		ToolStates:        map[domain.ToolKind]bool{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	meta.NextChatID++
	if err := b.putMeta(batch, meta); err != nil {
		return domain.Session{}, err
	}
	if err := b.putChat(batch, chat); err != nil {
		return domain.Session{}, err
	}
	if err := batch.Set([]byte(chatSessionIndexKey(session.ID, chat.ID)), nil, nil); err != nil {
		return domain.Session{}, fmt.Errorf("index chat by session: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return domain.Session{}, fmt.Errorf("commit create session: %w", err)
	}
	return session, nil
}

func (b *pebbleBackend) CreateChat(ctx context.Context, sessionID int64, title string, role domain.WorkflowRole, parentChatID *int64) (domain.Chat, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Chat{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	meta, err := b.readMeta()
	if err != nil {
		return domain.Chat{}, err
	}
	if _, err := b.readSession(sessionID); err != nil {
		return domain.Chat{}, err
	}
	parentProfile := ""
	if parentChatID != nil && *parentChatID > 0 {
		parent, err := b.readChat(*parentChatID)
		if err != nil {
			return domain.Chat{}, err
		}
		parentProfile = strings.TrimSpace(parent.PermissionProfile)
		if parentProfile == "" {
			session, err := b.readSession(sessionID)
			if err != nil {
				return domain.Chat{}, err
			}
			parentProfile = strings.TrimSpace(session.PermissionProfile)
		}
	}
	now := time.Now().UTC()
	chat := domain.Chat{
		ID:                meta.NextChatID,
		SessionID:         sessionID,
		ParentChatID:      parentChatID,
		Title:             strings.TrimSpace(title),
		WorkflowRole:      role,
		PermissionProfile: parentProfile,
		ToolStates:        map[domain.ToolKind]bool{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if chat.Title == "" {
		chat.Title = "Chat " + strconv.FormatInt(chat.ID, 10)
	}
	meta.NextChatID++
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putMeta(batch, meta); err != nil {
		return domain.Chat{}, err
	}
	if err := b.putChat(batch, chat); err != nil {
		return domain.Chat{}, err
	}
	if err := batch.Set([]byte(chatSessionIndexKey(sessionID, chat.ID)), nil, nil); err != nil {
		return domain.Chat{}, fmt.Errorf("index chat by session: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return domain.Chat{}, fmt.Errorf("commit create chat: %w", err)
	}
	return chat, nil
}

func (b *pebbleBackend) ListChats(ctx context.Context, sessionID int64) ([]domain.Chat, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	if _, err := b.readSession(sessionID); err != nil {
		return nil, err
	}
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(chatSessionIndexPrefix(sessionID)),
		UpperBound: nextPrefix([]byte(chatSessionIndexPrefix(sessionID))),
	})
	if err != nil {
		return nil, fmt.Errorf("new chat iterator: %w", err)
	}
	defer iter.Close()
	var chats []domain.Chat
	for ok := iter.First(); ok; ok = iter.Next() {
		chatID, err := chatIDFromSessionIndex(iter.Key())
		if err != nil {
			return nil, err
		}
		chat, err := b.readChat(chatID)
		if err != nil {
			return nil, err
		}
		chats = append(chats, chat)
	}
	slices.SortFunc(chats, func(a, c domain.Chat) int {
		if a.UpdatedAt.Equal(c.UpdatedAt) {
			switch {
			case a.ID > c.ID:
				return -1
			case a.ID < c.ID:
				return 1
			default:
				return 0
			}
		}
		if a.UpdatedAt.After(c.UpdatedAt) {
			return -1
		}
		return 1
	})
	return chats, iter.Error()
}

func (b *pebbleBackend) GetChat(ctx context.Context, chatID int64) (domain.Chat, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Chat{}, err
	}
	return b.readChat(chatID)
}

func (b *pebbleBackend) DefaultChat(ctx context.Context, sessionID int64) (domain.Chat, error) {
	chats, err := b.ListChats(ctx, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	if len(chats) == 0 {
		return domain.Chat{}, fmt.Errorf("no chat for session %d", sessionID)
	}
	return chats[0], nil
}

func (b *pebbleBackend) UpdateChat(ctx context.Context, chat domain.Chat) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	existing, err := b.readChat(chat.ID)
	if err != nil {
		return err
	}
	updated := chat
	updated.SessionID = existing.SessionID
	updated.CreatedAt = existing.CreatedAt
	if updated.UpdatedAt.IsZero() {
		updated.UpdatedAt = time.Now().UTC()
	}
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putChat(batch, updated); err != nil {
		return err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("commit update chat: %w", err)
	}
	return nil
}

func (b *pebbleBackend) AddMessage(ctx context.Context, sessionID int64, role domain.MessageRole, summary string) (domain.Message, error) {
	chat, err := b.DefaultChat(ctx, sessionID)
	if err != nil {
		return domain.Message{}, err
	}
	return b.AddChatMessage(ctx, chat.ID, role, summary)
}

func (b *pebbleBackend) AddChatMessage(ctx context.Context, chatID int64, role domain.MessageRole, summary string) (domain.Message, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Message{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	meta, err := b.readMeta()
	if err != nil {
		return domain.Message{}, err
	}
	chat, err := b.readChat(chatID)
	if err != nil {
		return domain.Message{}, err
	}
	session, err := b.readSession(chat.SessionID)
	if err != nil {
		return domain.Message{}, err
	}
	previousSession := session
	now := time.Now().UTC()
	message := domain.Message{
		ID:        meta.NextMessageID,
		SessionID: chat.SessionID,
		ChatID:    chatID,
		Role:      role,
		Summary:   summary,
		CreatedAt: now,
	}
	meta.NextMessageID++

	session.UpdatedAt = now
	session.LastMessage = summary
	chat.UpdatedAt = now
	chat.LastMessage = summary

	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putMeta(batch, meta); err != nil {
		return domain.Message{}, err
	}
	if err := b.putMessage(batch, message); err != nil {
		return domain.Message{}, err
	}
	if err := batch.Set([]byte(messageChatIndexKey(chatID, message.ID)), nil, nil); err != nil {
		return domain.Message{}, fmt.Errorf("index message by chat: %w", err)
	}
	if err := b.putSession(batch, &previousSession, &session); err != nil {
		return domain.Message{}, err
	}
	if err := b.putChat(batch, chat); err != nil {
		return domain.Message{}, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return domain.Message{}, fmt.Errorf("commit add message: %w", err)
	}
	return message, nil
}

func (b *pebbleBackend) ListSessions(ctx context.Context) ([]domain.Session, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte("session-updated/"),
		UpperBound: nextPrefix([]byte("session-updated/")),
	})
	if err != nil {
		return nil, fmt.Errorf("new session iterator: %w", err)
	}
	defer iter.Close()

	var sessions []domain.Session
	for ok := iter.Last(); ok; ok = iter.Prev() {
		sessionID, err := sessionIDFromUpdatedIndex(iter.Key())
		if err != nil {
			return nil, err
		}
		session, err := b.readSession(sessionID)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, iter.Error()
}

func (b *pebbleBackend) GetSession(ctx context.Context, sessionID int64) (domain.Session, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Session{}, err
	}
	return b.readSession(sessionID)
}

func (b *pebbleBackend) UpdateSessionWorkspace(ctx context.Context, sessionID int64, cwd, projectRoot string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.CWD = cwd
		session.ProjectRoot = projectRoot
	})
}

func (b *pebbleBackend) SetSessionPermissionProfile(ctx context.Context, sessionID int64, profile string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.PermissionProfile = profile
	})
}

func (b *pebbleBackend) AddSessionPermissionRule(ctx context.Context, sessionID int64, rule domain.PermissionOverride) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.PermissionRules = appendPermissionRule(session.PermissionRules, rule)
	})
}

func (b *pebbleBackend) SetSessionToolStates(ctx context.Context, sessionID int64, states map[domain.ToolKind]bool) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.ToolStates = cloneToolStates(states)
	})
}

func (b *pebbleBackend) UpdateSessionTitle(ctx context.Context, sessionID int64, title string, generatedAt time.Time, refreshCount int) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.Title = title
		session.TitleGeneratedAt = generatedAt
		session.TitleRefreshCount = refreshCount
	})
}

func (b *pebbleBackend) UpdateSessionAgents(
	ctx context.Context,
	sessionID int64,
	projectRoot string,
	projectChecksum string,
	resolved string,
	summary string,
	files []domain.AgentsFile,
	generatedAt time.Time,
) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.ProjectRoot = projectRoot
		session.ProjectChecksum = projectChecksum
		session.AgentsResolved = resolved
		session.AgentsSummary = summary
		session.AgentsFiles = append([]domain.AgentsFile(nil), files...)
		session.AgentsGeneratedAt = generatedAt
	})
}

func (b *pebbleBackend) CountMessagesByRole(ctx context.Context, sessionID int64, role domain.MessageRole) (int, error) {
	messages, _, err := b.PartsForSession(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, message := range messages {
		if message.Role == role {
			count++
		}
	}
	return count, nil
}

func (b *pebbleBackend) SetSessionModel(ctx context.Context, sessionID int64, providerID, modelID string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.ProviderID = providerID
		session.ModelID = modelID
	})
}

func (b *pebbleBackend) UpdateMessageSummary(ctx context.Context, messageID int64, summary string) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	message, err := b.readMessage(messageID)
	if err != nil {
		return err
	}
	session, err := b.readSession(message.SessionID)
	if err != nil {
		return err
	}
	chat, err := b.readChat(message.ChatID)
	if err != nil {
		return err
	}

	oldSummary := message.Summary
	message.Summary = summary
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putMessage(batch, message); err != nil {
		return err
	}
	if session.LastMessage == "" || session.LastMessage == oldSummary {
		session.LastMessage = summary
		if err := b.putSession(batch, &session, &session); err != nil {
			return err
		}
	}
	if chat.LastMessage == "" || chat.LastMessage == oldSummary {
		chat.LastMessage = summary
		if err := b.putChat(batch, chat); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

func (b *pebbleBackend) SetChatQueuedInputs(ctx context.Context, chatID int64, items []domain.QueuedInput) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	chat, err := b.readChat(chatID)
	if err != nil {
		return err
	}
	chat.QueuedInputs = cloneQueuedInputs(items)
	chat.UpdatedAt = time.Now().UTC()
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putChat(batch, chat); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (b *pebbleBackend) AddPart(ctx context.Context, messageID int64, kind domain.PartKind, body, metaJSON string) (domain.Part, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Part{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	meta, err := b.readMeta()
	if err != nil {
		return domain.Part{}, err
	}
	if _, err := b.readMessage(messageID); err != nil {
		return domain.Part{}, err
	}
	now := time.Now().UTC()
	part := domain.Part{
		ID:        meta.NextPartID,
		MessageID: messageID,
		Kind:      kind,
		Body:      body,
		MetaJSON:  metaJSON,
		CreatedAt: now,
	}
	meta.NextPartID++

	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putMeta(batch, meta); err != nil {
		return domain.Part{}, err
	}
	if err := b.putPart(batch, part); err != nil {
		return domain.Part{}, err
	}
	if err := batch.Set([]byte(partMessageIndexKey(messageID, part.ID)), nil, nil); err != nil {
		return domain.Part{}, fmt.Errorf("index part by message: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return domain.Part{}, fmt.Errorf("commit add part: %w", err)
	}
	return part, nil
}

func (b *pebbleBackend) UpdatePartMetaJSON(ctx context.Context, partID int64, metaJSON string) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	part, err := b.readPart(partID)
	if err != nil {
		return err
	}
	part.MetaJSON = metaJSON

	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putPart(batch, part); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (b *pebbleBackend) PartsForSession(ctx context.Context, sessionID int64) ([]domain.Message, map[int64][]domain.Part, error) {
	chat, err := b.DefaultChat(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	return b.PartsForChat(ctx, chat.ID)
}

func (b *pebbleBackend) PartsForChat(ctx context.Context, chatID int64) ([]domain.Message, map[int64][]domain.Part, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, nil, err
	}
	if _, err := b.readChat(chatID); err != nil {
		return nil, nil, err
	}
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(messageChatIndexPrefix(chatID)),
		UpperBound: nextPrefix([]byte(messageChatIndexPrefix(chatID))),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("new message iterator: %w", err)
	}
	defer iter.Close()
	var messages []domain.Message
	partsByMessage := make(map[int64][]domain.Part)
	for ok := iter.First(); ok; ok = iter.Next() {
		messageID, err := messageIDFromChatIndex(iter.Key())
		if err != nil {
			return nil, nil, err
		}
		message, err := b.readMessage(messageID)
		if err != nil {
			return nil, nil, err
		}
		messages = append(messages, message)
		parts, err := b.partsForMessage(messageID)
		if err != nil {
			return nil, nil, err
		}
		partsByMessage[message.ID] = parts
	}
	return messages, partsByMessage, iter.Error()
}

func (b *pebbleBackend) CreateApproval(ctx context.Context, sessionID int64, tool domain.ToolKind, command string) (Approval, error) {
	chat, err := b.DefaultChat(ctx, sessionID)
	if err != nil {
		return Approval{}, err
	}
	return b.CreateChatApproval(ctx, chat.ID, tool, command)
}

func (b *pebbleBackend) CreateChatApproval(ctx context.Context, chatID int64, tool domain.ToolKind, command string) (Approval, error) {
	if err := ensureContext(ctx); err != nil {
		return Approval{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	meta, err := b.readMeta()
	if err != nil {
		return Approval{}, err
	}
	chat, err := b.readChat(chatID)
	if err != nil {
		return Approval{}, err
	}
	approval := Approval{
		ID:        meta.NextApprovalID,
		SessionID: chat.SessionID,
		ChatID:    chatID,
		Tool:      tool,
		Command:   command,
		Status:    domain.ApprovalStatusPending,
		CreatedAt: time.Now().UTC(),
	}
	meta.NextApprovalID++

	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putMeta(batch, meta); err != nil {
		return Approval{}, err
	}
	if err := b.putApproval(batch, approval); err != nil {
		return Approval{}, err
	}
	if err := batch.Set([]byte(approvalChatIndexKey(chatID, approval.ID)), nil, nil); err != nil {
		return Approval{}, fmt.Errorf("index approval by chat: %w", err)
	}
	if err := batch.Set([]byte(approvalPendingIndexKey(chatID, approval.ID)), nil, nil); err != nil {
		return Approval{}, fmt.Errorf("index pending approval: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return Approval{}, fmt.Errorf("commit add approval: %w", err)
	}
	return approval, nil
}

func (b *pebbleBackend) UpdateApproval(ctx context.Context, approvalID int64, status domain.ApprovalStatus) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	approval, err := b.readApproval(approvalID)
	if err != nil {
		return err
	}
	approval.Status = status

	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putApproval(batch, approval); err != nil {
		return err
	}
	pendingKey := []byte(approvalPendingIndexKey(approval.ChatID, approval.ID))
	if status == domain.ApprovalStatusPending {
		if err := batch.Set(pendingKey, nil, nil); err != nil {
			return fmt.Errorf("restore pending approval index: %w", err)
		}
	} else if err := batch.Delete(pendingKey, nil); err != nil {
		return fmt.Errorf("delete pending approval index: %w", err)
	}
	return batch.Commit(pebble.Sync)
}

func (b *pebbleBackend) PendingApprovals(ctx context.Context, sessionID int64) ([]Approval, error) {
	chat, err := b.DefaultChat(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return b.PendingApprovalsForChat(ctx, chat.ID)
}

func (b *pebbleBackend) PendingApprovalsForChat(ctx context.Context, chatID int64) ([]Approval, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(approvalPendingIndexPrefix(chatID)),
		UpperBound: nextPrefix([]byte(approvalPendingIndexPrefix(chatID))),
	})
	if err != nil {
		return nil, fmt.Errorf("new pending approvals iterator: %w", err)
	}
	defer iter.Close()

	var approvals []Approval
	for ok := iter.First(); ok; ok = iter.Next() {
		approvalID, err := approvalIDFromPendingIndex(iter.Key())
		if err != nil {
			return nil, err
		}
		approval, err := b.readApproval(approvalID)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	return approvals, iter.Error()
}

func (b *pebbleBackend) AddTask(ctx context.Context, sessionID int64, body string, status domain.TaskStatus) (Task, error) {
	if err := ensureContext(ctx); err != nil {
		return Task{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	meta, err := b.readMeta()
	if err != nil {
		return Task{}, err
	}
	if _, err := b.readSession(sessionID); err != nil {
		return Task{}, err
	}
	task := Task{
		ID:        meta.NextTaskID,
		SessionID: sessionID,
		Body:      body,
		Status:    status,
		CreatedAt: time.Now().UTC(),
	}
	meta.NextTaskID++

	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putMeta(batch, meta); err != nil {
		return Task{}, err
	}
	if err := b.putTask(batch, task); err != nil {
		return Task{}, err
	}
	if err := batch.Set([]byte(taskSessionIndexKey(sessionID, task.ID)), nil, nil); err != nil {
		return Task{}, fmt.Errorf("index task by session: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return Task{}, fmt.Errorf("commit add task: %w", err)
	}
	return task, nil
}

func (b *pebbleBackend) UpdateTask(ctx context.Context, taskID int64, status domain.TaskStatus) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	task, err := b.readTask(taskID)
	if err != nil {
		return err
	}
	task.Status = status

	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putTask(batch, task); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (b *pebbleBackend) ListTasks(ctx context.Context, sessionID int64) ([]Task, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(taskSessionIndexPrefix(sessionID)),
		UpperBound: nextPrefix([]byte(taskSessionIndexPrefix(sessionID))),
	})
	if err != nil {
		return nil, fmt.Errorf("new task iterator: %w", err)
	}
	defer iter.Close()

	var tasks []Task
	for ok := iter.First(); ok; ok = iter.Next() {
		taskID, err := taskIDFromSessionIndex(iter.Key())
		if err != nil {
			return nil, err
		}
		task, err := b.readTask(taskID)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, iter.Error()
}

func (b *pebbleBackend) SetMilestonePlan(ctx context.Context, sessionID int64, summary string, milestones []Milestone) (MilestonePlan, error) {
	if err := ensureContext(ctx); err != nil {
		return MilestonePlan{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.readSession(sessionID); err != nil {
		return MilestonePlan{}, err
	}
	plan := MilestonePlan{
		SessionID:  sessionID,
		Summary:    summary,
		Milestones: append([]Milestone(nil), milestones...),
		UpdatedAt:  time.Now().UTC(),
	}
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putMilestonePlan(batch, plan); err != nil {
		return MilestonePlan{}, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return MilestonePlan{}, fmt.Errorf("commit milestone plan: %w", err)
	}
	return plan, nil
}

func (b *pebbleBackend) GetMilestonePlan(ctx context.Context, sessionID int64) (MilestonePlan, error) {
	if err := ensureContext(ctx); err != nil {
		return MilestonePlan{}, err
	}
	plan, err := b.readMilestonePlan(sessionID)
	if err == nil {
		return plan, nil
	}
	if errors.Is(err, pebble.ErrNotFound) {
		return MilestonePlan{SessionID: sessionID}, nil
	}
	if strings.Contains(err.Error(), "not found") {
		return MilestonePlan{SessionID: sessionID}, nil
	}
	return MilestonePlan{}, err
}

func (b *pebbleBackend) AddTodoItems(ctx context.Context, sessionID int64, milestoneRef string, contents []string) ([]TodoItem, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.readSession(sessionID); err != nil {
		return nil, err
	}
	existing, err := b.listTodosLocked(sessionID, milestoneRef)
	if err != nil {
		return nil, err
	}
	meta, err := b.readMeta()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	created := make([]TodoItem, 0, len(contents))
	batch := b.db.NewBatch()
	defer batch.Close()
	for _, content := range contents {
		item := TodoItem{
			ID:           meta.NextTodoID,
			SessionID:    sessionID,
			MilestoneRef: milestoneRef,
			Content:      content,
			Status:       domain.TodoStatusPending,
			Position:     len(existing) + len(created),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		meta.NextTodoID++
		if err := b.putTodoItem(batch, item); err != nil {
			return nil, err
		}
		if err := batch.Set([]byte(todoSessionIndexKey(sessionID, item.ID)), nil, nil); err != nil {
			return nil, fmt.Errorf("index todo by session: %w", err)
		}
		created = append(created, item)
	}
	if err := b.putMeta(batch, meta); err != nil {
		return nil, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return nil, fmt.Errorf("commit add todos: %w", err)
	}
	return created, nil
}

func (b *pebbleBackend) UpdateTodoItem(ctx context.Context, todoID int64, status domain.TodoStatus, content string) (TodoItem, error) {
	if err := ensureContext(ctx); err != nil {
		return TodoItem{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	item, err := b.readTodoItem(todoID)
	if err != nil {
		return TodoItem{}, err
	}
	item.Status = status
	if strings.TrimSpace(content) != "" {
		item.Content = content
	}
	item.UpdatedAt = time.Now().UTC()
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putTodoItem(batch, item); err != nil {
		return TodoItem{}, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return TodoItem{}, fmt.Errorf("commit todo update: %w", err)
	}
	return item, nil
}

func (b *pebbleBackend) ListTodos(ctx context.Context, sessionID int64, milestoneRef string) ([]TodoItem, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	return b.listTodosLocked(sessionID, milestoneRef)
}

func (b *pebbleBackend) GetApproval(ctx context.Context, approvalID int64) (Approval, error) {
	if err := ensureContext(ctx); err != nil {
		return Approval{}, err
	}
	return b.readApproval(approvalID)
}

func (b *pebbleBackend) listTodosLocked(sessionID int64, milestoneRef string) ([]TodoItem, error) {
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(todoSessionIndexPrefix(sessionID)),
		UpperBound: nextPrefix([]byte(todoSessionIndexPrefix(sessionID))),
	})
	if err != nil {
		return nil, fmt.Errorf("new todo iterator: %w", err)
	}
	defer iter.Close()
	var items []TodoItem
	for ok := iter.First(); ok; ok = iter.Next() {
		todoID, err := todoIDFromSessionIndex(iter.Key())
		if err != nil {
			return nil, err
		}
		item, err := b.readTodoItem(todoID)
		if err != nil {
			return nil, err
		}
		if milestoneRef != "" && item.MilestoneRef != milestoneRef {
			continue
		}
		items = append(items, item)
	}
	return items, iter.Error()
}

func (b *pebbleBackend) updateSession(ctx context.Context, sessionID int64, update func(*domain.Session)) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	session, err := b.readSession(sessionID)
	if err != nil {
		return err
	}
	previous := session
	update(&session)
	session.UpdatedAt = time.Now().UTC()

	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putSession(batch, &previous, &session); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (b *pebbleBackend) readMeta() (metaRecord, error) {
	data, closer, err := b.db.Get([]byte("meta/store"))
	if err != nil {
		return metaRecord{}, fmt.Errorf("get pebble metadata: %w", err)
	}
	defer closer.Close()
	var meta metaRecord
	if err := json.Unmarshal(cloneBytes(data), &meta); err != nil {
		return metaRecord{}, fmt.Errorf("decode pebble metadata: %w", err)
	}
	if meta.NextTaskID <= 0 {
		meta.NextTaskID = 1
	}
	if meta.NextChatID <= 0 {
		meta.NextChatID = 1
	}
	if meta.NextTodoID <= 0 {
		meta.NextTodoID = 1
	}
	return meta, nil
}

func (b *pebbleBackend) putMeta(batch *pebble.Batch, meta metaRecord) error {
	data, err := encodeJSON(meta)
	if err != nil {
		return fmt.Errorf("encode pebble metadata: %w", err)
	}
	if err := batch.Set([]byte("meta/store"), data, nil); err != nil {
		return fmt.Errorf("write pebble metadata: %w", err)
	}
	return nil
}

func (b *pebbleBackend) putSession(batch *pebble.Batch, previous, session *domain.Session) error {
	if previous != nil {
		if err := batch.Delete([]byte(sessionUpdatedIndexKey(previous.UpdatedAt, previous.ID)), nil); err != nil {
			return fmt.Errorf("delete old session index: %w", err)
		}
	}
	data, err := encodeJSON(session)
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	if err := batch.Set([]byte(sessionKey(session.ID)), data, nil); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	if err := batch.Set([]byte(sessionUpdatedIndexKey(session.UpdatedAt, session.ID)), nil, nil); err != nil {
		return fmt.Errorf("write session index: %w", err)
	}
	return nil
}

func (b *pebbleBackend) putChat(batch *pebble.Batch, chat domain.Chat) error {
	data, err := encodeJSON(chat)
	if err != nil {
		return fmt.Errorf("encode chat: %w", err)
	}
	if err := batch.Set([]byte(chatKey(chat.ID)), data, nil); err != nil {
		return fmt.Errorf("write chat: %w", err)
	}
	return nil
}

func (b *pebbleBackend) putMessage(batch *pebble.Batch, message domain.Message) error {
	data, err := encodeJSON(message)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}
	if err := batch.Set([]byte(messageKey(message.ID)), data, nil); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	return nil
}

func (b *pebbleBackend) putPart(batch *pebble.Batch, part domain.Part) error {
	data, err := encodeJSON(part)
	if err != nil {
		return fmt.Errorf("encode part: %w", err)
	}
	if err := batch.Set([]byte(partKey(part.ID)), data, nil); err != nil {
		return fmt.Errorf("write part: %w", err)
	}
	return nil
}

func (b *pebbleBackend) putApproval(batch *pebble.Batch, approval Approval) error {
	data, err := encodeJSON(approval)
	if err != nil {
		return fmt.Errorf("encode approval: %w", err)
	}
	if err := batch.Set([]byte(approvalKey(approval.ID)), data, nil); err != nil {
		return fmt.Errorf("write approval: %w", err)
	}
	return nil
}

func (b *pebbleBackend) putTask(batch *pebble.Batch, task Task) error {
	data, err := encodeJSON(task)
	if err != nil {
		return fmt.Errorf("encode task: %w", err)
	}
	if err := batch.Set([]byte(taskKey(task.ID)), data, nil); err != nil {
		return fmt.Errorf("write task: %w", err)
	}
	return nil
}

func (b *pebbleBackend) putMilestonePlan(batch *pebble.Batch, plan MilestonePlan) error {
	data, err := encodeJSON(plan)
	if err != nil {
		return fmt.Errorf("encode milestone plan: %w", err)
	}
	if err := batch.Set([]byte(milestonePlanKey(plan.SessionID)), data, nil); err != nil {
		return fmt.Errorf("write milestone plan: %w", err)
	}
	return nil
}

func (b *pebbleBackend) putTodoItem(batch *pebble.Batch, item TodoItem) error {
	data, err := encodeJSON(item)
	if err != nil {
		return fmt.Errorf("encode todo item: %w", err)
	}
	if err := batch.Set([]byte(todoItemKey(item.ID)), data, nil); err != nil {
		return fmt.Errorf("write todo item: %w", err)
	}
	return nil
}

func (b *pebbleBackend) readSession(sessionID int64) (domain.Session, error) {
	var session domain.Session
	if err := b.readRecord(sessionKey(sessionID), &session); err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}
	return session, nil
}

func (b *pebbleBackend) readChat(chatID int64) (domain.Chat, error) {
	var chat domain.Chat
	if err := b.readRecord(chatKey(chatID), &chat); err != nil {
		return domain.Chat{}, fmt.Errorf("get chat: %w", err)
	}
	return chat, nil
}

func (b *pebbleBackend) readMessage(messageID int64) (domain.Message, error) {
	var message domain.Message
	if err := b.readRecord(messageKey(messageID), &message); err != nil {
		return domain.Message{}, fmt.Errorf("get message: %w", err)
	}
	return message, nil
}

func (b *pebbleBackend) readPart(partID int64) (domain.Part, error) {
	var part domain.Part
	if err := b.readRecord(partKey(partID), &part); err != nil {
		return domain.Part{}, fmt.Errorf("get part: %w", err)
	}
	return part, nil
}

func (b *pebbleBackend) readApproval(approvalID int64) (Approval, error) {
	var approval Approval
	if err := b.readRecord(approvalKey(approvalID), &approval); err != nil {
		return Approval{}, fmt.Errorf("get approval: %w", err)
	}
	return approval, nil
}

func (b *pebbleBackend) readTask(taskID int64) (Task, error) {
	var task Task
	if err := b.readRecord(taskKey(taskID), &task); err != nil {
		return Task{}, fmt.Errorf("get task: %w", err)
	}
	return task, nil
}

func (b *pebbleBackend) readMilestonePlan(sessionID int64) (MilestonePlan, error) {
	var plan MilestonePlan
	if err := b.readRecord(milestonePlanKey(sessionID), &plan); err != nil {
		return MilestonePlan{}, fmt.Errorf("get milestone plan: %w", err)
	}
	return plan, nil
}

func (b *pebbleBackend) readTodoItem(todoID int64) (TodoItem, error) {
	var item TodoItem
	if err := b.readRecord(todoItemKey(todoID), &item); err != nil {
		return TodoItem{}, fmt.Errorf("get todo item: %w", err)
	}
	return item, nil
}

func (b *pebbleBackend) readRecord(key string, dst any) error {
	data, closer, err := b.db.Get([]byte(key))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			if strings.HasPrefix(key, "session/") {
				id, _ := parseIDFromSuffix(key, "session/")
				return fmt.Errorf("session %d not found", id)
			}
			if strings.HasPrefix(key, "approval/") {
				id, _ := parseIDFromSuffix(key, "approval/")
				return fmt.Errorf("approval %d not found", id)
			}
			return err
		}
		return err
	}
	defer closer.Close()
	return json.Unmarshal(cloneBytes(data), dst)
}

func (b *pebbleBackend) partsForMessage(messageID int64) ([]domain.Part, error) {
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(partMessageIndexPrefix(messageID)),
		UpperBound: nextPrefix([]byte(partMessageIndexPrefix(messageID))),
	})
	if err != nil {
		return nil, fmt.Errorf("new part iterator: %w", err)
	}
	defer iter.Close()

	var parts []domain.Part
	for ok := iter.First(); ok; ok = iter.Next() {
		partID, err := partIDFromMessageIndex(iter.Key())
		if err != nil {
			return nil, err
		}
		var part domain.Part
		if err := b.readRecord(partKey(partID), &part); err != nil {
			return nil, fmt.Errorf("get part: %w", err)
		}
		parts = append(parts, part)
	}
	return parts, iter.Error()
}

func nextPrefix(prefix []byte) []byte {
	if len(prefix) == 0 {
		return nil
	}
	out := append([]byte(nil), prefix...)
	for i := len(out) - 1; i >= 0; i-- {
		out[i]++
		if out[i] != 0 {
			return out[:i+1]
		}
	}
	return nil
}

func sessionKey(id int64) string { return "session/" + strconvID(id) }
func chatKey(id int64) string    { return "chat/" + strconvID(id) }
func sessionUpdatedIndexKey(updatedAt time.Time, id int64) string {
	return "session-updated/" + formatUnixNanos(updatedAt) + "/" + strconvID(id)
}
func messageKey(id int64) string { return "message/" + strconvID(id) }
func chatSessionIndexKey(sessionID, chatID int64) string {
	return chatSessionIndexPrefix(sessionID) + "/" + strconvID(chatID)
}
func chatSessionIndexPrefix(sessionID int64) string { return "session-chat/" + strconvID(sessionID) }
func messageChatIndexKey(chatID, messageID int64) string {
	return messageChatIndexPrefix(chatID) + "/" + strconvID(messageID)
}
func messageChatIndexPrefix(chatID int64) string { return "chat-message/" + strconvID(chatID) }
func partKey(id int64) string                    { return "part/" + strconvID(id) }
func partMessageIndexKey(messageID, partID int64) string {
	return partMessageIndexPrefix(messageID) + "/" + strconvID(partID)
}
func partMessageIndexPrefix(messageID int64) string { return "message-part/" + strconvID(messageID) }
func approvalKey(id int64) string                   { return "approval/" + strconvID(id) }
func approvalChatIndexKey(chatID, approvalID int64) string {
	return "chat-approval/" + strconvID(chatID) + "/" + strconvID(approvalID)
}
func approvalPendingIndexKey(chatID, approvalID int64) string {
	return approvalPendingIndexPrefix(chatID) + "/" + strconvID(approvalID)
}
func approvalPendingIndexPrefix(chatID int64) string {
	return "approval-pending/" + strconvID(chatID)
}
func taskKey(id int64) string { return "task/" + strconvID(id) }
func taskSessionIndexKey(sessionID, taskID int64) string {
	return taskSessionIndexPrefix(sessionID) + "/" + strconvID(taskID)
}
func taskSessionIndexPrefix(sessionID int64) string { return "session-task/" + strconvID(sessionID) }
func milestonePlanKey(sessionID int64) string       { return "milestone-plan/" + strconvID(sessionID) }
func todoItemKey(id int64) string                   { return "todo/" + strconvID(id) }
func todoSessionIndexKey(sessionID, todoID int64) string {
	return todoSessionIndexPrefix(sessionID) + "/" + strconvID(todoID)
}
func todoSessionIndexPrefix(sessionID int64) string { return "session-todo/" + strconvID(sessionID) }
func strconvID(id int64) string                     { return formatID(id) }

func sessionIDFromUpdatedIndex(key []byte) (int64, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid session updated index key %q", string(key))
	}
	return parseID(parts[2])
}

func chatIDFromSessionIndex(key []byte) (int64, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid session chat index key %q", string(key))
	}
	return parseID(parts[2])
}

func messageIDFromChatIndex(key []byte) (int64, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid chat message index key %q", string(key))
	}
	return parseID(parts[2])
}

func partIDFromMessageIndex(key []byte) (int64, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid message part index key %q", string(key))
	}
	return parseID(parts[2])
}

func approvalIDFromPendingIndex(key []byte) (int64, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid pending approval index key %q", string(key))
	}
	return parseID(parts[2])
}

func taskIDFromSessionIndex(key []byte) (int64, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid session task index key %q", string(key))
	}
	return parseID(parts[2])
}

func todoIDFromSessionIndex(key []byte) (int64, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid session todo index key %q", string(key))
	}
	return parseID(parts[2])
}

func parseID(raw []byte) (int64, error) {
	return parseIDFromSuffix(string(raw), "")
}
