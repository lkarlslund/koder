package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
)

type pebbleBackend struct {
	db     *pebble.DB
	mu     sync.Mutex
	closed bool
}

func openPebbleBackend(stateDir string) (*pebbleBackend, error) {
	dir := filepath.Join(stateDir, "store-pebble-v6")
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
	if reset, err := b.needsSchemaReset(); err != nil {
		_ = db.Close()
		return nil, err
	} else if reset {
		_ = db.Close()
		if err := os.RemoveAll(dir); err != nil {
			return nil, fmt.Errorf("reset pebble store: %w", err)
		}
		if err := ensureDir(dir); err != nil {
			return nil, fmt.Errorf("recreate pebble store: %w", err)
		}
		db, err = pebble.Open(dir, &pebble.Options{Logger: silentPebbleLogger{}})
		if err != nil {
			return nil, fmt.Errorf("reopen pebble after reset: %w", err)
		}
		b = &pebbleBackend{db: db}
		if err := b.init(); err != nil {
			_ = db.Close()
			return nil, err
		}
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

func (b *pebbleBackend) needsSchemaReset() (bool, error) {
	meta, err := b.readMeta()
	if err != nil {
		return false, err
	}
	return meta.SchemaVersion != schemaVersion || meta.Encoding != encodingJSON || meta.Backend != BackendPebble, nil
}

func (b *pebbleBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	return b.db.Close()
}

func (b *pebbleBackend) getCollectionRecord(ctx context.Context, namespace string, id string) ([]byte, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	data, closer, err := b.db.Get([]byte(collectionRecordKey(namespace, id)))
	if err != nil {
		return nil, fmt.Errorf("get %s %s: %w", namespace, id, err)
	}
	defer closer.Close()
	return cloneBytes(data), nil
}

func (b *pebbleBackend) putCollectionRecord(ctx context.Context, namespace string, id string, data []byte, indexes map[string]string) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := batch.Set([]byte(collectionRecordKey(namespace, id)), data, nil); err != nil {
		return fmt.Errorf("put %s %s: %w", namespace, id, err)
	}
	for name, value := range indexes {
		if err := batch.Set([]byte(collectionIndexKey(namespace, name, value, id)), nil, nil); err != nil {
			return fmt.Errorf("index %s %s: %w", namespace, id, err)
		}
	}
	return batch.Commit(pebble.Sync)
}

func (b *pebbleBackend) deleteCollectionRecord(ctx context.Context, namespace string, id string) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := batch.Delete([]byte(collectionRecordKey(namespace, id)), nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (b *pebbleBackend) listCollectionRecords(ctx context.Context, namespace string, _ *indexLookup) ([][]byte, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	prefix := collectionRecordPrefix(namespace)
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefix),
		UpperBound: nextPrefix([]byte(prefix)),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out [][]byte
	for ok := iter.First(); ok; ok = iter.Next() {
		out = append(out, cloneBytes(iter.Value()))
	}
	return out, iter.Error()
}

func (b *pebbleBackend) transaction(ctx context.Context, fn func() error) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	return fn()
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

func (b *pebbleBackend) CreateSession(ctx context.Context, title, providerID, modelID string, parentID *domain.ID) (domain.Session, error) {
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
		ID:                domain.NewID(),
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
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putMeta(batch, meta); err != nil {
		return domain.Session{}, err
	}
	if err := b.putSession(batch, nil, &session); err != nil {
		return domain.Session{}, err
	}
	chat := domain.Chat{
		ID:                domain.NewID(),
		SessionID:         session.ID,
		Title:             "Main",
		WorkflowRole:      chatrole.Orchestrator,
		ProviderID:        providerID,
		ModelID:           modelID,
		PermissionProfile: session.PermissionProfile,
		ToolStates:        map[domain.ToolKind]bool{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
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

func (b *pebbleBackend) CreateChat(ctx context.Context, sessionID domain.ID, title string, role domain.WorkflowRole, parentChatID *domain.ID) (domain.Chat, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Chat{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	meta, err := b.readMeta()
	if err != nil {
		return domain.Chat{}, err
	}
	session, err := b.readSession(sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	if parentChatID != nil && *parentChatID != "" {
		parent, err := b.readChat(*parentChatID)
		if err != nil {
			return domain.Chat{}, err
		}
		if parent.SessionID != sessionID {
			return domain.Chat{}, fmt.Errorf("parent chat %s belongs to session %s, not %s", parent.ID, parent.SessionID, sessionID)
		}
		session.ProviderID = parent.ProviderID
		session.ModelID = parent.ModelID
	}
	existing, err := b.ListChats(ctx, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	now := time.Now().UTC()
	chat := domain.Chat{
		ID:                domain.NewID(),
		SessionID:         sessionID,
		ParentChatID:      parentChatID,
		Title:             strings.TrimSpace(title),
		WorkflowRole:      role,
		ProviderID:        session.ProviderID,
		ModelID:           session.ModelID,
		PermissionProfile: strings.TrimSpace(session.PermissionProfile),
		ToolStates:        cloneToolStates(session.ToolStates),
		Position:          len(existing),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if chat.Title == "" {
		chat.Title = "Chat " + chat.ID
	}
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

func (b *pebbleBackend) ListChats(ctx context.Context, sessionID domain.ID) ([]domain.Chat, error) {
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
	sortChatsForSidebar(chats)
	return chats, iter.Error()
}

func (b *pebbleBackend) GetChat(ctx context.Context, chatID domain.ID) (domain.Chat, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Chat{}, err
	}
	return b.readChat(chatID)
}

func (b *pebbleBackend) DefaultChat(ctx context.Context, sessionID domain.ID) (domain.Chat, error) {
	chats, err := b.ListChats(ctx, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	if len(chats) == 0 {
		return domain.Chat{}, fmt.Errorf("no chat for session %s", sessionID)
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

func (b *pebbleBackend) SetChatModel(ctx context.Context, chatID domain.ID, providerID, modelID string) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	chat, err := b.readChat(chatID)
	if err != nil {
		return err
	}
	chat.ProviderID = providerID
	chat.ModelID = modelID
	chat.UpdatedAt = time.Now().UTC()
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putChat(batch, chat); err != nil {
		return err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("commit set chat model: %w", err)
	}
	return nil
}

func (b *pebbleBackend) DeleteChat(ctx context.Context, chatID domain.ID) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	chat, err := b.readChat(chatID)
	if err != nil {
		return err
	}
	chats, err := b.ListChats(ctx, chat.SessionID)
	if err != nil {
		return err
	}
	if len(chats) <= 1 {
		return fmt.Errorf("cannot delete the only chat in a session")
	}
	approvalIDs, err := b.approvalIDsForChat(chatID)
	if err != nil {
		return err
	}
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := batch.Delete([]byte(chatKey(chatID)), nil); err != nil {
		return fmt.Errorf("delete chat: %w", err)
	}
	if err := batch.Delete([]byte(chatSessionIndexKey(chat.SessionID, chatID)), nil); err != nil {
		return fmt.Errorf("delete chat session index: %w", err)
	}
	if err := batch.Delete([]byte(collectionRecordKey("chats", chatID)), nil); err != nil && !errors.Is(err, pebble.ErrNotFound) {
		return fmt.Errorf("delete generic chat: %w", err)
	}
	for _, approvalID := range approvalIDs {
		if err := batch.Delete([]byte(approvalKey(approvalID)), nil); err != nil {
			return fmt.Errorf("delete approval: %w", err)
		}
		if err := batch.Delete([]byte(approvalChatIndexKey(chatID, approvalID)), nil); err != nil {
			return fmt.Errorf("delete approval chat index: %w", err)
		}
		if err := batch.Delete([]byte(approvalPendingIndexKey(chatID, approvalID)), nil); err != nil {
			return fmt.Errorf("delete approval pending index: %w", err)
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("commit delete chat: %w", err)
	}
	return nil
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

func (b *pebbleBackend) GetSession(ctx context.Context, sessionID domain.ID) (domain.Session, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Session{}, err
	}
	return b.readSession(sessionID)
}

func (b *pebbleBackend) TouchSession(ctx context.Context, sessionID domain.ID) (domain.Session, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Session{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	session, err := b.readSession(sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	previous := session
	session.UpdatedAt = time.Now().UTC()

	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putSession(batch, &previous, &session); err != nil {
		return domain.Session{}, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return domain.Session{}, fmt.Errorf("commit touch session: %w", err)
	}
	return session, nil
}

func (b *pebbleBackend) UpdateSessionWorkspace(ctx context.Context, sessionID domain.ID, cwd, projectRoot string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.CWD = cwd
		session.ProjectRoot = projectRoot
	})
}

func (b *pebbleBackend) SetSessionPermissionProfile(ctx context.Context, sessionID domain.ID, profile string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.PermissionProfile = profile
	})
}

func (b *pebbleBackend) AddSessionPermissionRule(ctx context.Context, sessionID domain.ID, rule domain.PermissionOverride) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.PermissionRules = appendPermissionRule(session.PermissionRules, rule)
	})
}

func (b *pebbleBackend) SetSessionToolStates(ctx context.Context, sessionID domain.ID, states map[domain.ToolKind]bool) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.ToolStates = cloneToolStates(states)
	})
}

func (b *pebbleBackend) UpdateSessionTitle(ctx context.Context, sessionID domain.ID, title string, generatedAt time.Time, refreshCount int) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.Title = title
		session.TitleGeneratedAt = generatedAt
		session.TitleRefreshCount = refreshCount
	})
}

func (b *pebbleBackend) UpdateSessionAgents(
	ctx context.Context,
	sessionID domain.ID,
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

func (b *pebbleBackend) SetSessionModel(ctx context.Context, sessionID domain.ID, providerID, modelID string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.ProviderID = providerID
		session.ModelID = modelID
	})
}

func (b *pebbleBackend) SetChatQueuedInputs(ctx context.Context, chatID domain.ID, items []domain.QueuedInput) error {
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

func (b *pebbleBackend) CreateApproval(ctx context.Context, sessionID domain.ID, tool domain.ToolKind, command string) (Approval, error) {
	chat, err := b.DefaultChat(ctx, sessionID)
	if err != nil {
		return Approval{}, err
	}
	return b.CreateChatApproval(ctx, chat.ID, tool, command)
}

func (b *pebbleBackend) CreateChatApproval(ctx context.Context, chatID domain.ID, tool domain.ToolKind, command string) (Approval, error) {
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
		ID:        domain.NewID(),
		SessionID: chat.SessionID,
		ChatID:    chatID,
		Tool:      tool,
		Command:   command,
		Status:    domain.ApprovalStatusPending,
		CreatedAt: time.Now().UTC(),
	}
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

func (b *pebbleBackend) UpdateApproval(ctx context.Context, approvalID domain.ID, status domain.ApprovalStatus) error {
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

func (b *pebbleBackend) PendingApprovals(ctx context.Context, sessionID domain.ID) ([]Approval, error) {
	chat, err := b.DefaultChat(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return b.PendingApprovalsForChat(ctx, chat.ID)
}

func (b *pebbleBackend) PendingApprovalsForChat(ctx context.Context, chatID domain.ID) ([]Approval, error) {
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

func (b *pebbleBackend) AddTask(ctx context.Context, sessionID domain.ID, body string, status domain.TaskStatus) (Task, error) {
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
		ID:        domain.NewID(),
		SessionID: sessionID,
		Body:      body,
		Status:    status,
		CreatedAt: time.Now().UTC(),
	}
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

func (b *pebbleBackend) UpdateTask(ctx context.Context, taskID domain.ID, status domain.TaskStatus) error {
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

func (b *pebbleBackend) ListTasks(ctx context.Context, sessionID domain.ID) ([]Task, error) {
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

func (b *pebbleBackend) SetMilestonePlan(ctx context.Context, sessionID domain.ID, summary string, milestones []Milestone) (MilestonePlan, error) {
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

func (b *pebbleBackend) GetMilestonePlan(ctx context.Context, sessionID domain.ID) (MilestonePlan, error) {
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

func (b *pebbleBackend) AddTodoItems(ctx context.Context, sessionID domain.ID, milestoneRef string, contents []string) ([]TodoItem, error) {
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
			ID:           domain.NewID(),
			SessionID:    sessionID,
			MilestoneRef: milestoneRef,
			Content:      content,
			Status:       domain.TodoStatusPending,
			Position:     len(existing) + len(created),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
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

func (b *pebbleBackend) UpdateTodoItem(ctx context.Context, todoID domain.ID, status domain.TodoStatus, content string) (TodoItem, error) {
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

func (b *pebbleBackend) ListTodos(ctx context.Context, sessionID domain.ID, milestoneRef string) ([]TodoItem, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	return b.listTodosLocked(sessionID, milestoneRef)
}

func (b *pebbleBackend) GetApproval(ctx context.Context, approvalID domain.ID) (Approval, error) {
	if err := ensureContext(ctx); err != nil {
		return Approval{}, err
	}
	return b.readApproval(approvalID)
}

func (b *pebbleBackend) listTodosLocked(sessionID domain.ID, milestoneRef string) ([]TodoItem, error) {
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
	if err := iter.Error(); err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, c TodoItem) int {
		switch {
		case a.Position < c.Position:
			return -1
		case a.Position > c.Position:
			return 1
		case a.ID < c.ID:
			return -1
		case a.ID > c.ID:
			return 1
		default:
			return 0
		}
	})
	return items, nil
}

func (b *pebbleBackend) updateSession(ctx context.Context, sessionID domain.ID, update func(*domain.Session)) error {
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

func (b *pebbleBackend) readSession(sessionID domain.ID) (domain.Session, error) {
	var session domain.Session
	if err := b.readRecord(sessionKey(sessionID), &session); err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}
	return session, nil
}

func (b *pebbleBackend) readChat(chatID domain.ID) (domain.Chat, error) {
	var chat domain.Chat
	if err := b.readRecord(chatKey(chatID), &chat); err != nil {
		return domain.Chat{}, fmt.Errorf("get chat: %w", err)
	}
	return chat, nil
}

func (b *pebbleBackend) readApproval(approvalID domain.ID) (Approval, error) {
	var approval Approval
	if err := b.readRecord(approvalKey(approvalID), &approval); err != nil {
		return Approval{}, fmt.Errorf("get approval: %w", err)
	}
	return approval, nil
}

func (b *pebbleBackend) approvalIDsForChat(chatID domain.ID) ([]domain.ID, error) {
	prefix := "chat-approval/" + strconvID(chatID)
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefix),
		UpperBound: nextPrefix([]byte(prefix)),
	})
	if err != nil {
		return nil, fmt.Errorf("new chat approvals iterator: %w", err)
	}
	defer iter.Close()
	var ids []domain.ID
	for ok := iter.First(); ok; ok = iter.Next() {
		approvalID, err := approvalIDFromChatIndex(iter.Key())
		if err != nil {
			return nil, err
		}
		ids = append(ids, approvalID)
	}
	return ids, iter.Error()
}

func (b *pebbleBackend) readTask(taskID domain.ID) (Task, error) {
	var task Task
	if err := b.readRecord(taskKey(taskID), &task); err != nil {
		return Task{}, fmt.Errorf("get task: %w", err)
	}
	return task, nil
}

func (b *pebbleBackend) readMilestonePlan(sessionID domain.ID) (MilestonePlan, error) {
	var plan MilestonePlan
	if err := b.readRecord(milestonePlanKey(sessionID), &plan); err != nil {
		return MilestonePlan{}, fmt.Errorf("get milestone plan: %w", err)
	}
	return plan, nil
}

func (b *pebbleBackend) readTodoItem(todoID domain.ID) (TodoItem, error) {
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
				id := strings.TrimPrefix(key, "session/")
				return fmt.Errorf("session %s not found", id)
			}
			if strings.HasPrefix(key, "approval/") {
				id := strings.TrimPrefix(key, "approval/")
				return fmt.Errorf("approval %s not found", id)
			}
			return err
		}
		return err
	}
	defer closer.Close()
	return json.Unmarshal(cloneBytes(data), dst)
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

func sessionKey(id domain.ID) string { return "session/" + strconvID(id) }
func chatKey(id domain.ID) string    { return "chat/" + strconvID(id) }
func sessionUpdatedIndexKey(updatedAt time.Time, id domain.ID) string {
	return "session-updated/" + formatUnixNanos(updatedAt) + "/" + strconvID(id)
}
func chatSessionIndexKey(sessionID, chatID domain.ID) string {
	return chatSessionIndexPrefix(sessionID) + "/" + strconvID(chatID)
}
func chatSessionIndexPrefix(sessionID domain.ID) string {
	return "session-chat/" + strconvID(sessionID)
}
func approvalKey(id domain.ID) string { return "approval/" + strconvID(id) }
func approvalChatIndexKey(chatID, approvalID domain.ID) string {
	return "chat-approval/" + strconvID(chatID) + "/" + strconvID(approvalID)
}
func approvalPendingIndexKey(chatID, approvalID domain.ID) string {
	return approvalPendingIndexPrefix(chatID) + "/" + strconvID(approvalID)
}
func approvalPendingIndexPrefix(chatID domain.ID) string {
	return "approval-pending/" + strconvID(chatID)
}
func taskKey(id domain.ID) string { return "task/" + strconvID(id) }
func taskSessionIndexKey(sessionID, taskID domain.ID) string {
	return taskSessionIndexPrefix(sessionID) + "/" + strconvID(taskID)
}
func taskSessionIndexPrefix(sessionID domain.ID) string {
	return "session-task/" + strconvID(sessionID)
}
func milestonePlanKey(sessionID domain.ID) string { return "milestone-plan/" + strconvID(sessionID) }
func todoItemKey(id domain.ID) string             { return "todo/" + strconvID(id) }
func todoSessionIndexKey(sessionID, todoID domain.ID) string {
	return todoSessionIndexPrefix(sessionID) + "/" + strconvID(todoID)
}
func todoSessionIndexPrefix(sessionID domain.ID) string {
	return "session-todo/" + strconvID(sessionID)
}
func strconvID(id domain.ID) string { return id }

func sessionIDFromUpdatedIndex(key []byte) (domain.ID, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid session updated index key %q", string(key))
	}
	return parseID(parts[2])
}

func chatIDFromSessionIndex(key []byte) (domain.ID, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid session chat index key %q", string(key))
	}
	return parseID(parts[2])
}

func approvalIDFromPendingIndex(key []byte) (domain.ID, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid pending approval index key %q", string(key))
	}
	return parseID(parts[2])
}

func approvalIDFromChatIndex(key []byte) (domain.ID, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid chat approval index key %q", string(key))
	}
	return parseID(parts[2])
}

func taskIDFromSessionIndex(key []byte) (domain.ID, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid session task index key %q", string(key))
	}
	return parseID(parts[2])
}

func todoIDFromSessionIndex(key []byte) (domain.ID, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid session todo index key %q", string(key))
	}
	return parseID(parts[2])
}

func parseID(raw []byte) (domain.ID, error) {
	return string(raw), nil
}
