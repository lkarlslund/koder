package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
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
		ToolStates:        map[domain.ToolKind]bool{},
		CreatedAt:         now,
		UpdatedAt:         now,
		LastMessage:       "",
	}
	meta.NextSessionID++

	if err := b.writeSessionBatch(meta, nil, session); err != nil {
		return domain.Session{}, err
	}
	return session, nil
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

func (b *pebbleBackend) SetSessionPermissionProfile(ctx context.Context, sessionID int64, profile string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.PermissionProfile = profile
	})
}

func (b *pebbleBackend) SetSessionToolStates(ctx context.Context, sessionID int64, states map[domain.ToolKind]bool) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.ToolStates = cloneToolStates(states)
	})
}

func (b *pebbleBackend) UpdateSessionTitle(ctx context.Context, sessionID int64, title string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.Title = title
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

func (b *pebbleBackend) AddMessage(ctx context.Context, sessionID int64, role domain.MessageRole, summary string) (domain.Message, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Message{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	meta, err := b.readMeta()
	if err != nil {
		return domain.Message{}, err
	}
	session, err := b.readSession(sessionID)
	if err != nil {
		return domain.Message{}, err
	}
	previousSession := session
	now := time.Now().UTC()
	message := domain.Message{
		ID:        meta.NextMessageID,
		SessionID: sessionID,
		Role:      role,
		Summary:   summary,
		CreatedAt: now,
	}
	meta.NextMessageID++

	session.UpdatedAt = now
	session.LastMessage = summary

	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putMeta(batch, meta); err != nil {
		return domain.Message{}, err
	}
	if err := b.putMessage(batch, message); err != nil {
		return domain.Message{}, err
	}
	if err := batch.Set([]byte(messageSessionIndexKey(sessionID, message.ID)), nil, nil); err != nil {
		return domain.Message{}, fmt.Errorf("index message by session: %w", err)
	}
	if err := b.putSession(batch, &previousSession, &session); err != nil {
		return domain.Message{}, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return domain.Message{}, fmt.Errorf("commit add message: %w", err)
	}
	return message, nil
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
	if err := ensureContext(ctx); err != nil {
		return nil, nil, err
	}
	if _, err := b.readSession(sessionID); err != nil {
		return nil, nil, err
	}

	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(messageSessionIndexPrefix(sessionID)),
		UpperBound: nextPrefix([]byte(messageSessionIndexPrefix(sessionID))),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("new message iterator: %w", err)
	}
	defer iter.Close()

	var messages []domain.Message
	partsByMessage := make(map[int64][]domain.Part)
	for ok := iter.First(); ok; ok = iter.Next() {
		messageID, err := messageIDFromSessionIndex(iter.Key())
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
	if err := ensureContext(ctx); err != nil {
		return Approval{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	meta, err := b.readMeta()
	if err != nil {
		return Approval{}, err
	}
	if _, err := b.readSession(sessionID); err != nil {
		return Approval{}, err
	}
	approval := Approval{
		ID:        meta.NextApprovalID,
		SessionID: sessionID,
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
	if err := batch.Set([]byte(approvalSessionIndexKey(sessionID, approval.ID)), nil, nil); err != nil {
		return Approval{}, fmt.Errorf("index approval by session: %w", err)
	}
	if err := batch.Set([]byte(approvalPendingIndexKey(sessionID, approval.ID)), nil, nil); err != nil {
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
	pendingKey := []byte(approvalPendingIndexKey(approval.SessionID, approval.ID))
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
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(approvalPendingIndexPrefix(sessionID)),
		UpperBound: nextPrefix([]byte(approvalPendingIndexPrefix(sessionID))),
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

func (b *pebbleBackend) GetApproval(ctx context.Context, approvalID int64) (Approval, error) {
	if err := ensureContext(ctx); err != nil {
		return Approval{}, err
	}
	return b.readApproval(approvalID)
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
	if err := decodeJSON(cloneBytes(data), &meta); err != nil {
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

func (b *pebbleBackend) writeSessionBatch(meta metaRecord, previous *domain.Session, session domain.Session) error {
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.putMeta(batch, meta); err != nil {
		return err
	}
	if err := b.putSession(batch, previous, &session); err != nil {
		return err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("commit session batch: %w", err)
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

func (b *pebbleBackend) readSession(sessionID int64) (domain.Session, error) {
	var session domain.Session
	if err := b.readRecord(sessionKey(sessionID), &session); err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}
	return session, nil
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
	return decodeJSON(cloneBytes(data), dst)
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
func sessionUpdatedIndexKey(updatedAt time.Time, id int64) string {
	return "session-updated/" + formatUnixNanos(updatedAt) + "/" + strconvID(id)
}
func messageKey(id int64) string { return "message/" + strconvID(id) }
func messageSessionIndexKey(sessionID, messageID int64) string {
	return messageSessionIndexPrefix(sessionID) + "/" + strconvID(messageID)
}
func messageSessionIndexPrefix(sessionID int64) string {
	return "session-message/" + strconvID(sessionID)
}
func partKey(id int64) string { return "part/" + strconvID(id) }
func partMessageIndexKey(messageID, partID int64) string {
	return partMessageIndexPrefix(messageID) + "/" + strconvID(partID)
}
func partMessageIndexPrefix(messageID int64) string { return "message-part/" + strconvID(messageID) }
func approvalKey(id int64) string                   { return "approval/" + strconvID(id) }
func approvalSessionIndexKey(sessionID, approvalID int64) string {
	return "session-approval/" + strconvID(sessionID) + "/" + strconvID(approvalID)
}
func approvalPendingIndexKey(sessionID, approvalID int64) string {
	return approvalPendingIndexPrefix(sessionID) + "/" + strconvID(approvalID)
}
func approvalPendingIndexPrefix(sessionID int64) string {
	return "approval-pending/" + strconvID(sessionID)
}
func taskKey(id int64) string { return "task/" + strconvID(id) }
func taskSessionIndexKey(sessionID, taskID int64) string {
	return taskSessionIndexPrefix(sessionID) + "/" + strconvID(taskID)
}
func taskSessionIndexPrefix(sessionID int64) string { return "session-task/" + strconvID(sessionID) }
func strconvID(id int64) string                     { return formatID(id) }

func sessionIDFromUpdatedIndex(key []byte) (int64, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid session updated index key %q", string(key))
	}
	return parseID(parts[2])
}

func messageIDFromSessionIndex(key []byte) (int64, error) {
	parts := bytes.Split(key, []byte("/"))
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid session message index key %q", string(key))
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

func parseID(raw []byte) (int64, error) {
	return parseIDFromSuffix(string(raw), "")
}
