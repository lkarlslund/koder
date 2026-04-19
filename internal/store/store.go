package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/lkarlslund/koder/internal/domain"
)

type Store struct {
	db *sql.DB
}

type Approval struct {
	ID        int64
	SessionID int64
	Tool      domain.ToolKind
	Command   string
	Status    domain.ApprovalStatus
	CreatedAt time.Time
}

type Task struct {
	ID        int64
	SessionID int64
	Body      string
	Status    domain.TaskStatus
	CreatedAt time.Time
}

func Open(stateDir string) (*Store, error) {
	db, err := sql.Open("sqlite", filepath.Join(stateDir, "koder.db"))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	schema := `
CREATE TABLE IF NOT EXISTS sessions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	parent_id INTEGER,
	title TEXT NOT NULL,
	provider_id TEXT NOT NULL,
	model_id TEXT NOT NULL,
	permission_profile TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id INTEGER NOT NULL,
	role TEXT NOT NULL,
	summary TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS parts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	message_id INTEGER NOT NULL,
	kind TEXT NOT NULL,
	body TEXT NOT NULL,
	meta_json TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS approvals (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id INTEGER NOT NULL,
	tool TEXT NOT NULL,
	command_text TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id INTEGER NOT NULL,
	body TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at TEXT NOT NULL
);`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE sessions ADD COLUMN permission_profile TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate session permission profile: %w", err)
	}
	return nil
}

func (s *Store) EnsureSession(ctx context.Context, providerID, modelID string) (domain.Session, error) {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	if len(sessions) > 0 {
		return sessions[0], nil
	}
	return s.CreateSession(ctx, "New Session", providerID, modelID, nil)
}

func (s *Store) CreateSession(ctx context.Context, title, providerID, modelID string, parentID *int64) (domain.Session, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
INSERT INTO sessions (parent_id, title, provider_id, model_id, permission_profile, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		parentID, title, providerID, modelID, "", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return domain.Session{}, fmt.Errorf("create session: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.Session{}, fmt.Errorf("create session id: %w", err)
	}
	return domain.Session{
		ID:                id,
		ParentID:          parentID,
		Title:             title,
		ProviderID:        providerID,
		ModelID:           modelID,
		PermissionProfile: "",
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}

func (s *Store) ListSessions(ctx context.Context) ([]domain.Session, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT s.id, s.parent_id, s.title, s.provider_id, s.model_id, s.permission_profile, s.created_at, s.updated_at,
COALESCE((SELECT summary FROM messages m WHERE m.session_id = s.id ORDER BY m.id DESC LIMIT 1), '')
FROM sessions s
ORDER BY s.updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var items []domain.Session
	for rows.Next() {
		var item domain.Session
		var created, updated string
		var parent sql.NullInt64
		if err := rows.Scan(&item.ID, &parent, &item.Title, &item.ProviderID, &item.ModelID, &item.PermissionProfile, &created, &updated, &item.LastMessage); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		if parent.Valid {
			item.ParentID = &parent.Int64
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetSession(ctx context.Context, sessionID int64) (domain.Session, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT s.id, s.parent_id, s.title, s.provider_id, s.model_id, s.permission_profile, s.created_at, s.updated_at,
COALESCE((SELECT summary FROM messages m WHERE m.session_id = s.id ORDER BY m.id DESC LIMIT 1), '')
FROM sessions s
WHERE s.id = ?`, sessionID)

	var item domain.Session
	var created, updated string
	var parent sql.NullInt64
	if err := row.Scan(&item.ID, &parent, &item.Title, &item.ProviderID, &item.ModelID, &item.PermissionProfile, &created, &updated, &item.LastMessage); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Session{}, fmt.Errorf("session %d not found", sessionID)
		}
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}
	if parent.Valid {
		item.ParentID = &parent.Int64
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return item, nil
}

func (s *Store) SetSessionPermissionProfile(ctx context.Context, sessionID int64, profile string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET permission_profile = ?, updated_at = ? WHERE id = ?`,
		profile, time.Now().UTC().Format(time.RFC3339Nano), sessionID)
	if err != nil {
		return fmt.Errorf("update session permission profile: %w", err)
	}
	return nil
}

func (s *Store) UpdateSessionTitle(ctx context.Context, sessionID int64, title string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?`,
		title, time.Now().UTC().Format(time.RFC3339Nano), sessionID)
	if err != nil {
		return fmt.Errorf("update session title: %w", err)
	}
	return nil
}

func (s *Store) CountMessagesByRole(ctx context.Context, sessionID int64, role domain.MessageRole) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE session_id = ? AND role = ?`, sessionID, role)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count messages by role: %w", err)
	}
	return count, nil
}

func (s *Store) SetSessionModel(ctx context.Context, sessionID int64, providerID, modelID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET provider_id = ?, model_id = ?, updated_at = ? WHERE id = ?`,
		providerID, modelID, time.Now().UTC().Format(time.RFC3339Nano), sessionID)
	if err != nil {
		return fmt.Errorf("update session model: %w", err)
	}
	return nil
}

func (s *Store) AddMessage(ctx context.Context, sessionID int64, role domain.MessageRole, summary string) (domain.Message, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO messages (session_id, role, summary, created_at) VALUES (?, ?, ?, ?)`,
		sessionID, role, summary, now.Format(time.RFC3339Nano))
	if err != nil {
		return domain.Message{}, fmt.Errorf("insert message: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.Message{}, fmt.Errorf("message id: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, now.Format(time.RFC3339Nano), sessionID); err != nil {
		return domain.Message{}, fmt.Errorf("touch session: %w", err)
	}
	return domain.Message{ID: id, SessionID: sessionID, Role: role, Summary: summary, CreatedAt: now}, nil
}

func (s *Store) UpdateMessageSummary(ctx context.Context, messageID int64, summary string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET summary = ? WHERE id = ?`, summary, messageID)
	if err != nil {
		return fmt.Errorf("update message summary: %w", err)
	}
	return nil
}

func (s *Store) AddPart(ctx context.Context, messageID int64, kind domain.PartKind, body, metaJSON string) (domain.Part, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO parts (message_id, kind, body, meta_json, created_at) VALUES (?, ?, ?, ?, ?)`,
		messageID, kind, body, metaJSON, now.Format(time.RFC3339Nano))
	if err != nil {
		return domain.Part{}, fmt.Errorf("insert part: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.Part{}, fmt.Errorf("part id: %w", err)
	}
	return domain.Part{ID: id, MessageID: messageID, Kind: kind, Body: body, MetaJSON: metaJSON, CreatedAt: now}, nil
}

func (s *Store) PartsForSession(ctx context.Context, sessionID int64) ([]domain.Message, map[int64][]domain.Part, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_id, role, summary, created_at FROM messages WHERE session_id = ? ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var messages []domain.Message
	partsByMessage := make(map[int64][]domain.Part)
	for rows.Next() {
		var msg domain.Message
		var created string
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Summary, &created); err != nil {
			return nil, nil, fmt.Errorf("scan message: %w", err)
		}
		msg.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	for _, msg := range messages {
		parts, err := s.partsForMessage(ctx, msg.ID)
		if err != nil {
			return nil, nil, err
		}
		partsByMessage[msg.ID] = parts
	}
	return messages, partsByMessage, nil
}

func (s *Store) partsForMessage(ctx context.Context, messageID int64) ([]domain.Part, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, message_id, kind, body, meta_json, created_at FROM parts WHERE message_id = ? ORDER BY id ASC`, messageID)
	if err != nil {
		return nil, fmt.Errorf("query parts: %w", err)
	}
	defer rows.Close()

	var items []domain.Part
	for rows.Next() {
		var part domain.Part
		var created string
		if err := rows.Scan(&part.ID, &part.MessageID, &part.Kind, &part.Body, &part.MetaJSON, &created); err != nil {
			return nil, fmt.Errorf("scan part: %w", err)
		}
		part.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, part)
	}
	return items, rows.Err()
}

func (s *Store) CreateApproval(ctx context.Context, sessionID int64, tool domain.ToolKind, command string) (Approval, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO approvals (session_id, tool, command_text, status, created_at) VALUES (?, ?, ?, ?, ?)`,
		sessionID, tool, command, domain.ApprovalStatusPending, now.Format(time.RFC3339Nano))
	if err != nil {
		return Approval{}, fmt.Errorf("create approval: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Approval{}, fmt.Errorf("approval id: %w", err)
	}
	return Approval{ID: id, SessionID: sessionID, Tool: tool, Command: command, Status: domain.ApprovalStatusPending, CreatedAt: now}, nil
}

func (s *Store) UpdateApproval(ctx context.Context, approvalID int64, status domain.ApprovalStatus) error {
	_, err := s.db.ExecContext(ctx, `UPDATE approvals SET status = ? WHERE id = ?`, status, approvalID)
	if err != nil {
		return fmt.Errorf("update approval: %w", err)
	}
	return nil
}

func (s *Store) PendingApprovals(ctx context.Context, sessionID int64) ([]Approval, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_id, tool, command_text, status, created_at FROM approvals WHERE session_id = ? AND status = ? ORDER BY id ASC`,
		sessionID, domain.ApprovalStatusPending)
	if err != nil {
		return nil, fmt.Errorf("pending approvals: %w", err)
	}
	defer rows.Close()

	var items []Approval
	for rows.Next() {
		var item Approval
		var created string
		if err := rows.Scan(&item.ID, &item.SessionID, &item.Tool, &item.Command, &item.Status, &created); err != nil {
			return nil, fmt.Errorf("scan approval: %w", err)
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) AddTask(ctx context.Context, sessionID int64, body string, status domain.TaskStatus) (Task, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO tasks (session_id, body, status, created_at) VALUES (?, ?, ?, ?)`,
		sessionID, body, status, now.Format(time.RFC3339Nano))
	if err != nil {
		return Task{}, fmt.Errorf("add task: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Task{}, fmt.Errorf("task id: %w", err)
	}
	return Task{ID: id, SessionID: sessionID, Body: body, Status: status, CreatedAt: now}, nil
}

func (s *Store) UpdateTask(ctx context.Context, taskID int64, status domain.TaskStatus) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET status = ? WHERE id = ?`, status, taskID)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	return nil
}

func (s *Store) ListTasks(ctx context.Context, sessionID int64) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_id, body, status, created_at FROM tasks WHERE session_id = ? ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var items []Task
	for rows.Next() {
		var item Task
		var created string
		if err := rows.Scan(&item.ID, &item.SessionID, &item.Body, &item.Status, &created); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetApproval(ctx context.Context, approvalID int64) (Approval, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, session_id, tool, command_text, status, created_at FROM approvals WHERE id = ?`, approvalID)
	var item Approval
	var created string
	if err := row.Scan(&item.ID, &item.SessionID, &item.Tool, &item.Command, &item.Status, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Approval{}, fmt.Errorf("approval %d not found", approvalID)
		}
		return Approval{}, fmt.Errorf("get approval: %w", err)
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return item, nil
}
