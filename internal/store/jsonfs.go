package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
)

type jsonfsBackend struct {
	root string
	mu   sync.Mutex
}

func openJSONFSBackend(stateDir string) (*jsonfsBackend, error) {
	root := filepath.Join(stateDir, "store-jsonfs-v3")
	if reset, err := jsonfsNeedsSchemaReset(root); err != nil {
		return nil, err
	} else if reset {
		if err := os.RemoveAll(root); err != nil {
			return nil, fmt.Errorf("reset jsonfs store: %w", err)
		}
	}
	for _, dir := range []string{
		root,
		filepath.Join(root, "sessions"),
		filepath.Join(root, "chats"),
		filepath.Join(root, "timeline"),
		filepath.Join(root, "approvals"),
		filepath.Join(root, "tasks"),
		filepath.Join(root, "milestone-plans"),
		filepath.Join(root, "todos"),
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

func jsonfsNeedsSchemaReset(root string) (bool, error) {
	metaPath := filepath.Join(root, "meta.json")
	if !fileExists(metaPath) {
		return false, nil
	}
	var meta metaRecord
	if err := readJSONFile(metaPath, &meta); err != nil {
		return false, fmt.Errorf("read jsonfs metadata before schema check: %w", err)
	}
	return meta.SchemaVersion != schemaVersion || meta.Encoding != encodingJSON || meta.Backend != BackendJSONFS, nil
}

func (b *jsonfsBackend) init() error {
	metaPath := filepath.Join(b.root, "meta.json")
	if fileExists(metaPath) {
		return nil
	}
	return writeJSONFile(metaPath, defaultMeta(BackendJSONFS))
}

func (b *jsonfsBackend) Close() error { return nil }

func (b *jsonfsBackend) allocateCollectionID(ctx context.Context, key string) (int64, error) {
	if err := ensureContext(ctx); err != nil {
		return 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	meta, err := b.readMeta()
	if err != nil {
		return 0, err
	}
	if meta.NextIDs == nil {
		meta.NextIDs = map[string]int64{}
	}
	next := meta.NextIDs[key]
	if next <= 0 {
		next = 1
	}
	meta.NextIDs[key] = next + 1
	if err := b.writeMeta(meta); err != nil {
		return 0, err
	}
	return next, nil
}

func (b *jsonfsBackend) getCollectionRecord(ctx context.Context, namespace string, id int64) ([]byte, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	path := filepath.Join(b.root, "collections", namespace, formatID(id)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("get %s %d: %w", namespace, id, err)
	}
	return data, nil
}

func (b *jsonfsBackend) putCollectionRecord(ctx context.Context, namespace string, id int64, data []byte, indexes map[string]string) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	dir := filepath.Join(b.root, "collections", namespace)
	if err := ensureDir(dir); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, formatID(id)+".json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("put %s %d: %w", namespace, id, err)
	}
	if err := b.rebuildCollectionIndexes(namespace); err != nil {
		return err
	}
	_ = indexes
	return nil
}

func (b *jsonfsBackend) deleteCollectionRecord(ctx context.Context, namespace string, id int64) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := os.Remove(filepath.Join(b.root, "collections", namespace, formatID(id)+".json")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s %d: %w", namespace, id, err)
	}
	return b.rebuildCollectionIndexes(namespace)
}

func (b *jsonfsBackend) listCollectionRecords(ctx context.Context, namespace string, lookup *indexLookup) ([][]byte, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	dir := filepath.Join(b.root, "collections", namespace)
	paths, err := sortedJSONPaths(dir)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, 0, len(paths))
	for _, path := range paths {
		id, err := parseIDFromSuffix(strings.TrimSuffix(filepath.Base(path), ".json"), "")
		if err != nil {
			return nil, err
		}
		if lookup != nil {
			ok, err := b.collectionIndexContains(namespace, lookup.name, lookup.value, id)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, data)
	}
	return out, nil
}

func (b *jsonfsBackend) transaction(ctx context.Context, fn func() error) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}
	return fn()
}

func (b *jsonfsBackend) rebuildCollectionIndexes(namespace string) error {
	_ = os.RemoveAll(filepath.Join(b.root, "collection-indexes", namespace))
	return ensureDir(filepath.Join(b.root, "collection-indexes", namespace))
}

func (b *jsonfsBackend) collectionIndexContains(namespace, name, value string, id int64) (bool, error) {
	_ = namespace
	_ = name
	_ = value
	_ = id
	return true, nil
}

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
		PermissionRules:   nil,
		ToolStates:        map[domain.ToolKind]bool{},
	}
	meta.NextSessionID++
	if err := b.writeMeta(meta); err != nil {
		return domain.Session{}, err
	}
	if err := b.writeSession(session); err != nil {
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
	if err := b.writeMeta(meta); err != nil {
		return domain.Session{}, err
	}
	if err := b.writeChat(chat); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func (b *jsonfsBackend) CreateChat(ctx context.Context, sessionID int64, title string, role domain.WorkflowRole, parentChatID *int64) (domain.Chat, error) {
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
	if err := b.writeMeta(meta); err != nil {
		return domain.Chat{}, err
	}
	if err := b.writeChat(chat); err != nil {
		return domain.Chat{}, err
	}
	return chat, nil
}

func (b *jsonfsBackend) ListChats(ctx context.Context, sessionID int64) ([]domain.Chat, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	if _, err := b.readSession(sessionID); err != nil {
		return nil, err
	}
	paths, err := sortedJSONPaths(filepath.Join(b.root, "chats"))
	if err != nil {
		return nil, err
	}
	var chats []domain.Chat
	for _, path := range paths {
		var chat domain.Chat
		if err := readJSONFile(path, &chat); err != nil {
			return nil, err
		}
		if chat.SessionID == sessionID {
			chats = append(chats, chat)
		}
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
	return chats, nil
}

func (b *jsonfsBackend) GetChat(ctx context.Context, chatID int64) (domain.Chat, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Chat{}, err
	}
	return b.readChat(chatID)
}

func (b *jsonfsBackend) DefaultChat(ctx context.Context, sessionID int64) (domain.Chat, error) {
	chats, err := b.ListChats(ctx, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	if len(chats) == 0 {
		return domain.Chat{}, fmt.Errorf("no chat for session %d", sessionID)
	}
	return chats[0], nil
}

func (b *jsonfsBackend) UpdateChat(ctx context.Context, chat domain.Chat) error {
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
	return b.writeChat(updated)
}

func (b *jsonfsBackend) DeleteChat(ctx context.Context, chatID int64) error {
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
	if err := os.Remove(filepath.Join(b.root, "chats", formatID(chatID)+".json")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete chat: %w", err)
	}
	if err := os.Remove(filepath.Join(b.root, "collections", "chats", formatID(chatID)+".json")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete generic chat: %w", err)
	}
	if err := b.rebuildCollectionIndexes("chats"); err != nil {
		return err
	}
	approvals, err := b.allApprovals()
	if err != nil {
		return err
	}
	for _, approval := range approvals {
		if approval.ChatID != chatID {
			continue
		}
		if err := os.Remove(filepath.Join(b.root, "approvals", formatID(approval.ID)+".json")); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete chat approval: %w", err)
		}
	}
	return nil
}

func (b *jsonfsBackend) SetChatQueuedInputs(ctx context.Context, chatID int64, items []domain.QueuedInput) error {
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
	return b.writeChat(chat)
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

func (b *jsonfsBackend) UpdateSessionWorkspace(ctx context.Context, sessionID int64, cwd, projectRoot string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.CWD = cwd
		session.ProjectRoot = projectRoot
	})
}

func (b *jsonfsBackend) GetSession(ctx context.Context, sessionID int64) (domain.Session, error) {
	if err := ensureContext(ctx); err != nil {
		return domain.Session{}, err
	}
	session, err := b.readSession(sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func (b *jsonfsBackend) SetSessionPermissionProfile(ctx context.Context, sessionID int64, profile string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.PermissionProfile = profile
	})
}

func (b *jsonfsBackend) AddSessionPermissionRule(ctx context.Context, sessionID int64, rule domain.PermissionOverride) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.PermissionRules = appendPermissionRule(session.PermissionRules, rule)
	})
}

func (b *jsonfsBackend) SetSessionToolStates(ctx context.Context, sessionID int64, states map[domain.ToolKind]bool) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.ToolStates = cloneToolStates(states)
	})
}

func (b *jsonfsBackend) UpdateSessionTitle(ctx context.Context, sessionID int64, title string, generatedAt time.Time, refreshCount int) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.Title = title
		session.TitleGeneratedAt = generatedAt
		session.TitleRefreshCount = refreshCount
	})
}

func (b *jsonfsBackend) UpdateSessionAgents(
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

func (b *jsonfsBackend) SetSessionModel(ctx context.Context, sessionID int64, providerID, modelID string) error {
	return b.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.ProviderID = providerID
		session.ModelID = modelID
	})
}

func (b *jsonfsBackend) CreateApproval(ctx context.Context, sessionID int64, tool domain.ToolKind, command string) (Approval, error) {
	chat, err := b.DefaultChat(ctx, sessionID)
	if err != nil {
		return Approval{}, err
	}
	return b.CreateChatApproval(ctx, chat.ID, tool, command)
}

func (b *jsonfsBackend) CreateChatApproval(ctx context.Context, chatID int64, tool domain.ToolKind, command string) (Approval, error) {
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
	chat, err := b.DefaultChat(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return b.PendingApprovalsForChat(ctx, chat.ID)
}

func (b *jsonfsBackend) PendingApprovalsForChat(ctx context.Context, chatID int64) ([]Approval, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	items, err := b.allApprovals()
	if err != nil {
		return nil, err
	}
	var approvals []Approval
	for _, approval := range items {
		if approval.ChatID == chatID && approval.Status == domain.ApprovalStatusPending {
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

func (b *jsonfsBackend) SetMilestonePlan(ctx context.Context, sessionID int64, summary string, milestones []Milestone) (MilestonePlan, error) {
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
	if err := b.writeMilestonePlan(plan); err != nil {
		return MilestonePlan{}, err
	}
	return plan, nil
}

func (b *jsonfsBackend) GetMilestonePlan(ctx context.Context, sessionID int64) (MilestonePlan, error) {
	if err := ensureContext(ctx); err != nil {
		return MilestonePlan{}, err
	}
	plan, err := b.readMilestonePlan(sessionID)
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file or directory") {
			return MilestonePlan{SessionID: sessionID}, nil
		}
		return MilestonePlan{}, err
	}
	return plan, nil
}

func (b *jsonfsBackend) AddTodoItems(ctx context.Context, sessionID int64, milestoneRef string, contents []string) ([]TodoItem, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	meta, err := b.readMeta()
	if err != nil {
		return nil, err
	}
	existing, err := b.listTodosLocked(sessionID, milestoneRef)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	items := make([]TodoItem, 0, len(contents))
	for _, content := range contents {
		item := TodoItem{
			ID:           meta.NextTodoID,
			SessionID:    sessionID,
			MilestoneRef: milestoneRef,
			Content:      content,
			Status:       domain.TodoStatusPending,
			Position:     len(existing) + len(items),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		meta.NextTodoID++
		if err := b.writeTodoItem(item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := b.writeMeta(meta); err != nil {
		return nil, err
	}
	return items, nil
}

func (b *jsonfsBackend) UpdateTodoItem(ctx context.Context, todoID int64, status domain.TodoStatus, content string) (TodoItem, error) {
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
	if err := b.writeTodoItem(item); err != nil {
		return TodoItem{}, err
	}
	return item, nil
}

func (b *jsonfsBackend) ListTodos(ctx context.Context, sessionID int64, milestoneRef string) ([]TodoItem, error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	return b.listTodosLocked(sessionID, milestoneRef)
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
	if meta.NextTaskID <= 0 {
		meta.NextTaskID = 1
	}
	if meta.NextChatID <= 0 {
		meta.NextChatID = 1
	}
	if meta.NextTodoID <= 0 {
		meta.NextTodoID = 1
	}
	if meta.NextIDs == nil {
		meta.NextIDs = map[string]int64{}
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

func (b *jsonfsBackend) readMilestonePlan(sessionID int64) (MilestonePlan, error) {
	var plan MilestonePlan
	path := filepath.Join(b.root, "milestone-plans", formatID(sessionID)+".json")
	if err := readJSONFile(path, &plan); err != nil {
		return MilestonePlan{}, fmt.Errorf("read milestone plan: %w", err)
	}
	return plan, nil
}

func (b *jsonfsBackend) readChat(chatID int64) (domain.Chat, error) {
	var chat domain.Chat
	path := filepath.Join(b.root, "chats", formatID(chatID)+".json")
	if err := readJSONFile(path, &chat); err != nil {
		return domain.Chat{}, fmt.Errorf("read chat: %w", err)
	}
	return chat, nil
}

func (b *jsonfsBackend) writeChat(chat domain.Chat) error {
	if err := writeJSONFile(filepath.Join(b.root, "chats", formatID(chat.ID)+".json"), chat); err != nil {
		return fmt.Errorf("write chat: %w", err)
	}
	return nil
}

func (b *jsonfsBackend) writeMilestonePlan(plan MilestonePlan) error {
	if err := writeJSONFile(filepath.Join(b.root, "milestone-plans", formatID(plan.SessionID)+".json"), plan); err != nil {
		return fmt.Errorf("write milestone plan: %w", err)
	}
	return nil
}

func (b *jsonfsBackend) readTodoItem(todoID int64) (TodoItem, error) {
	var item TodoItem
	path := filepath.Join(b.root, "todos", formatID(todoID)+".json")
	if err := readJSONFile(path, &item); err != nil {
		return TodoItem{}, fmt.Errorf("read todo item: %w", err)
	}
	return item, nil
}

func (b *jsonfsBackend) writeTodoItem(item TodoItem) error {
	if err := writeJSONFile(filepath.Join(b.root, "todos", formatID(item.ID)+".json"), item); err != nil {
		return fmt.Errorf("write todo item: %w", err)
	}
	return nil
}

func (b *jsonfsBackend) allTodoItems() ([]TodoItem, error) {
	paths, err := sortedJSONPaths(filepath.Join(b.root, "todos"))
	if err != nil {
		return nil, err
	}
	items := make([]TodoItem, 0, len(paths))
	for _, path := range paths {
		var item TodoItem
		if err := readJSONFile(path, &item); err != nil {
			return nil, fmt.Errorf("read todo file: %w", err)
		}
		items = append(items, item)
	}
	return items, nil
}

func (b *jsonfsBackend) listTodosLocked(sessionID int64, milestoneRef string) ([]TodoItem, error) {
	items, err := b.allTodoItems()
	if err != nil {
		return nil, err
	}
	var todos []TodoItem
	for _, item := range items {
		if item.SessionID != sessionID {
			continue
		}
		if milestoneRef != "" && item.MilestoneRef != milestoneRef {
			continue
		}
		todos = append(todos, item)
	}
	slices.SortFunc(todos, func(a, c TodoItem) int {
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
	return todos, nil
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
