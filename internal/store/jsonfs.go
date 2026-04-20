package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
)

type jsonfsBackend struct {
	root string
	mu   sync.Mutex
}

func openJSONFSBackend(stateDir string) (*jsonfsBackend, error) {
	root := filepath.Join(stateDir, "store-jsonfs")
	for _, dir := range []string{
		root,
		filepath.Join(root, "sessions"),
		filepath.Join(root, "messages"),
		filepath.Join(root, "parts"),
		filepath.Join(root, "approvals"),
		filepath.Join(root, "tasks"),
	} {
		if err := ensureDir(dir); err != nil {
			return nil, fmt.Errorf("create jsonfs store dir: %w", err)
		}
	}
	b := &jsonfsBackend{root: root}
	if err := b.init(); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *jsonfsBackend) init() error {
	metaPath := filepath.Join(b.root, "meta.json")
	if fileExists(metaPath) {
		return nil
	}
	return writeJSONFile(metaPath, defaultMeta(BackendJSONFS))
}

func (b *jsonfsBackend) Close() error { return nil }

func (b *jsonfsBackend) EnsureSession(ctx context.Context, providerID, modelID string) (domain.Session, error) {
	sessions, err := b.ListSessions(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	if len(sessions) > 0 {
		return sessions[0], nil
	}
	return b.CreateSession(ctx, "New Session", providerID, modelID, nil)
}

func (b *jsonfsBackend) CreateSession(ctx context.Context, title, providerID, modelID string, parentID *int64) (domain.Session, error) {
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
		CreatedAt:         now,
		UpdatedAt:         now,
		PermissionProfile: "",
	}
	meta.NextSessionID++
	if err := b.writeMeta(meta); err != nil {
		return domain.Session{}, err
	}
	if err := b.writeSession(session); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func (b *jsonfsBackend) ListSessions(ctx context.Context) ([]domain.Session, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	paths, err := sortedJSONPaths(filepath.Join(b.root, "sessions"))
	if err != nil {
		return nil, fmt.Errorf("list session files: %w", err)
	}
	sessions := make([]domain.Session, 0, len(paths))
	for _, path := range paths {
		var session domain.Session
		if err := readJSONFile(path, &session); err != nil {
			return nil, fmt.Errorf("read session file: %w", err)
		}
		messages, err := b.sessionMessages(session.ID)
		if err != nil {
			return nil, err
		}
		session.LastMessage = sessionLastMessage(messages)
		sessions = append(sessions, session)
	}
	slices.SortFunc(sessions, func(a, c domain.Session) int {
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
	return sessions, nil
}

func (b *jsonfsBackend) GetSession(ctx context.Context, sessionID int64) (domain.Session, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Session{}, err
	}
	session, err := b.readSession(sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	messages, err := b.sessionMessages(session.ID)
	if err != nil {
		return domain.Session{}, err
	}
	session.LastMessage = sessionLastMessage(messages)
	return session, nil
}

func (b *jsonfsBackend) SetSessionPermissionProfile(ctx context.Context, sessionID int64, profile string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.PermissionProfile = profile
	})
}

func (b *jsonfsBackend) UpdateSessionTitle(ctx context.Context, sessionID int64, title string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.Title = title
	})
}

func (b *jsonfsBackend) CountMessagesByRole(ctx context.Context, sessionID int64, role domain.MessageRole) (int, error) {
	messages, err := b.sessionMessages(sessionID)
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

func (b *jsonfsBackend) SetSessionModel(ctx context.Context, sessionID int64, providerID, modelID string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.ProviderID = providerID
		session.ModelID = modelID
	})
}

func (b *jsonfsBackend) AddMessage(ctx context.Context, sessionID int64, role domain.MessageRole, summary string) (domain.Message, error) {
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

	if err := b.writeMeta(meta); err != nil {
		return domain.Message{}, err
	}
	if err := b.writeMessage(message); err != nil {
		return domain.Message{}, err
	}
	if err := b.writeSession(session); err != nil {
		return domain.Message{}, err
	}
	return message, nil
}

func (b *jsonfsBackend) UpdateMessageSummary(ctx context.Context, messageID int64, summary string) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	message, err := b.readMessage(messageID)
	if err != nil {
		return err
	}
	message.Summary = summary
	return b.writeMessage(message)
}

func (b *jsonfsBackend) AddPart(ctx context.Context, messageID int64, kind domain.PartKind, body, metaJSON string) (domain.Part, error) {
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
	part := domain.Part{
		ID:        meta.NextPartID,
		MessageID: messageID,
		Kind:      kind,
		Body:      body,
		MetaJSON:  metaJSON,
		CreatedAt: time.Now().UTC(),
	}
	meta.NextPartID++
	if err := b.writeMeta(meta); err != nil {
		return domain.Part{}, err
	}
	if err := b.writePart(part); err != nil {
		return domain.Part{}, err
	}
	return part, nil
}

func (b *jsonfsBackend) UpdatePartMetaJSON(ctx context.Context, partID int64, metaJSON string) error {
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
	return b.writePart(part)
}

func (b *jsonfsBackend) PartsForSession(ctx context.Context, sessionID int64) ([]domain.Message, map[int64][]domain.Part, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, nil, err
	}
	messages, err := b.sessionMessages(sessionID)
	if err != nil {
		return nil, nil, err
	}
	allParts, err := b.allParts()
	if err != nil {
		return nil, nil, err
	}
	partsByMessage := make(map[int64][]domain.Part)
	for _, part := range allParts {
		partsByMessage[part.MessageID] = append(partsByMessage[part.MessageID], part)
	}
	for messageID := range partsByMessage {
		slices.SortFunc(partsByMessage[messageID], func(a, c domain.Part) int {
			switch {
			case a.ID < c.ID:
				return -1
			case a.ID > c.ID:
				return 1
			default:
				return 0
			}
		})
	}
	return messages, partsByMessage, nil
}

func (b *jsonfsBackend) CreateApproval(ctx context.Context, sessionID int64, tool domain.ToolKind, command string) (Approval, error) {
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
	if err := b.writeMeta(meta); err != nil {
		return Approval{}, err
	}
	if err := b.writeApproval(approval); err != nil {
		return Approval{}, err
	}
	return approval, nil
}

func (b *jsonfsBackend) UpdateApproval(ctx context.Context, approvalID int64, status domain.ApprovalStatus) error {
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
	return b.writeApproval(approval)
}

func (b *jsonfsBackend) PendingApprovals(ctx context.Context, sessionID int64) ([]Approval, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	items, err := b.allApprovals()
	if err != nil {
		return nil, err
	}
	var approvals []Approval
	for _, approval := range items {
		if approval.SessionID == sessionID && approval.Status == domain.ApprovalStatusPending {
			approvals = append(approvals, approval)
		}
	}
	slices.SortFunc(approvals, func(a, c Approval) int {
		switch {
		case a.ID < c.ID:
			return -1
		case a.ID > c.ID:
			return 1
		default:
			return 0
		}
	})
	return approvals, nil
}

func (b *jsonfsBackend) AddTask(ctx context.Context, sessionID int64, body string, status domain.TaskStatus) (Task, error) {
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
	if err := b.writeMeta(meta); err != nil {
		return Task{}, err
	}
	if err := b.writeTask(task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (b *jsonfsBackend) UpdateTask(ctx context.Context, taskID int64, status domain.TaskStatus) error {
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
	return b.writeTask(task)
}

func (b *jsonfsBackend) ListTasks(ctx context.Context, sessionID int64) ([]Task, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	items, err := b.allTasks()
	if err != nil {
		return nil, err
	}
	var tasks []Task
	for _, task := range items {
		if task.SessionID == sessionID {
			tasks = append(tasks, task)
		}
	}
	slices.SortFunc(tasks, func(a, c Task) int {
		switch {
		case a.ID < c.ID:
			return -1
		case a.ID > c.ID:
			return 1
		default:
			return 0
		}
	})
	return tasks, nil
}

func (b *jsonfsBackend) GetApproval(ctx context.Context, approvalID int64) (Approval, error) {
	if err := ensureContext(ctx); err != nil {
		return Approval{}, err
	}
	return b.readApproval(approvalID)
}

func (b *jsonfsBackend) updateSession(ctx context.Context, sessionID int64, update func(*domain.Session)) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	session, err := b.readSession(sessionID)
	if err != nil {
		return err
	}
	update(&session)
	session.UpdatedAt = time.Now().UTC()
	return b.writeSession(session)
}

func (b *jsonfsBackend) readMeta() (metaRecord, error) {
	var meta metaRecord
	if err := readJSONFile(filepath.Join(b.root, "meta.json"), &meta); err != nil {
		return metaRecord{}, fmt.Errorf("read jsonfs metadata: %w", err)
	}
	return meta, nil
}

func (b *jsonfsBackend) writeMeta(meta metaRecord) error {
	if err := writeJSONFile(filepath.Join(b.root, "meta.json"), meta); err != nil {
		return fmt.Errorf("write jsonfs metadata: %w", err)
	}
	return nil
}

func (b *jsonfsBackend) readSession(sessionID int64) (domain.Session, error) {
	var session domain.Session
	path := filepath.Join(b.root, "sessions", formatID(sessionID)+".json")
	if err := readJSONFile(path, &session); err != nil {
		if os.IsNotExist(err) {
			return domain.Session{}, fmt.Errorf("session %d not found", sessionID)
		}
		return domain.Session{}, fmt.Errorf("read session: %w", err)
	}
	return session, nil
}

func (b *jsonfsBackend) writeSession(session domain.Session) error {
	if err := writeJSONFile(filepath.Join(b.root, "sessions", formatID(session.ID)+".json"), session); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	return nil
}

func (b *jsonfsBackend) readMessage(messageID int64) (domain.Message, error) {
	var message domain.Message
	path := filepath.Join(b.root, "messages", formatID(messageID)+".json")
	if err := readJSONFile(path, &message); err != nil {
		return domain.Message{}, fmt.Errorf("read message: %w", err)
	}
	return message, nil
}

func (b *jsonfsBackend) readPart(partID int64) (domain.Part, error) {
	var part domain.Part
	path := filepath.Join(b.root, "parts", formatID(partID)+".json")
	if err := readJSONFile(path, &part); err != nil {
		return domain.Part{}, fmt.Errorf("read part: %w", err)
	}
	return part, nil
}

func (b *jsonfsBackend) writeMessage(message domain.Message) error {
	if err := writeJSONFile(filepath.Join(b.root, "messages", formatID(message.ID)+".json"), message); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	return nil
}

func (b *jsonfsBackend) writePart(part domain.Part) error {
	if err := writeJSONFile(filepath.Join(b.root, "parts", formatID(part.ID)+".json"), part); err != nil {
		return fmt.Errorf("write part: %w", err)
	}
	return nil
}

func (b *jsonfsBackend) readApproval(approvalID int64) (Approval, error) {
	var approval Approval
	path := filepath.Join(b.root, "approvals", formatID(approvalID)+".json")
	if err := readJSONFile(path, &approval); err != nil {
		if os.IsNotExist(err) {
			return Approval{}, fmt.Errorf("approval %d not found", approvalID)
		}
		return Approval{}, fmt.Errorf("read approval: %w", err)
	}
	return approval, nil
}

func (b *jsonfsBackend) writeApproval(approval Approval) error {
	if err := writeJSONFile(filepath.Join(b.root, "approvals", formatID(approval.ID)+".json"), approval); err != nil {
		return fmt.Errorf("write approval: %w", err)
	}
	return nil
}

func (b *jsonfsBackend) readTask(taskID int64) (Task, error) {
	var task Task
	path := filepath.Join(b.root, "tasks", formatID(taskID)+".json")
	if err := readJSONFile(path, &task); err != nil {
		return Task{}, fmt.Errorf("read task: %w", err)
	}
	return task, nil
}

func (b *jsonfsBackend) writeTask(task Task) error {
	if err := writeJSONFile(filepath.Join(b.root, "tasks", formatID(task.ID)+".json"), task); err != nil {
		return fmt.Errorf("write task: %w", err)
	}
	return nil
}

func (b *jsonfsBackend) sessionMessages(sessionID int64) ([]domain.Message, error) {
	var sessionFound bool
	if _, err := b.readSession(sessionID); err == nil {
		sessionFound = true
	}
	if !sessionFound {
		return nil, fmt.Errorf("session %d not found", sessionID)
	}
	paths, err := sortedJSONPaths(filepath.Join(b.root, "messages"))
	if err != nil {
		return nil, fmt.Errorf("list message files: %w", err)
	}
	var messages []domain.Message
	for _, path := range paths {
		var message domain.Message
		if err := readJSONFile(path, &message); err != nil {
			return nil, fmt.Errorf("read message file: %w", err)
		}
		if message.SessionID == sessionID {
			messages = append(messages, message)
		}
	}
	slices.SortFunc(messages, func(a, c domain.Message) int {
		switch {
		case a.ID < c.ID:
			return -1
		case a.ID > c.ID:
			return 1
		default:
			return 0
		}
	})
	return messages, nil
}

func (b *jsonfsBackend) allParts() ([]domain.Part, error) {
	paths, err := sortedJSONPaths(filepath.Join(b.root, "parts"))
	if err != nil {
		return nil, fmt.Errorf("list part files: %w", err)
	}
	parts := make([]domain.Part, 0, len(paths))
	for _, path := range paths {
		var part domain.Part
		if err := readJSONFile(path, &part); err != nil {
			return nil, fmt.Errorf("read part file: %w", err)
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func (b *jsonfsBackend) allApprovals() ([]Approval, error) {
	paths, err := sortedJSONPaths(filepath.Join(b.root, "approvals"))
	if err != nil {
		return nil, fmt.Errorf("list approval files: %w", err)
	}
	items := make([]Approval, 0, len(paths))
	for _, path := range paths {
		var approval Approval
		if err := readJSONFile(path, &approval); err != nil {
			return nil, fmt.Errorf("read approval file: %w", err)
		}
		items = append(items, approval)
	}
	return items, nil
}

func (b *jsonfsBackend) allTasks() ([]Task, error) {
	paths, err := sortedJSONPaths(filepath.Join(b.root, "tasks"))
	if err != nil {
		return nil, fmt.Errorf("list task files: %w", err)
	}
	items := make([]Task, 0, len(paths))
	for _, path := range paths {
		var task Task
		if err := readJSONFile(path, &task); err != nil {
			return nil, fmt.Errorf("read task file: %w", err)
		}
		items = append(items, task)
	}
	return items, nil
}
