package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/fsutil"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/tools/chattool"
)

// prepareChatWorktree runs before a child is persisted or allowed to execute.
// Shared children inherit the parent's checkout. Isolated children start at its
// exact HEAD, without copying uncommitted files or changing the parent's branch.
// Worktrees are retained on completion and archive; deletion requires cleanup.
func (s *Session) prepareChatWorktree(ctx context.Context, session domain.Session, parent domain.Chat, child *domain.Chat, isolation string) error {
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()
	switch strings.TrimSpace(isolation) {
	case "":
		child.Worktree = parent.Worktree
		if child.Worktree != nil {
			if s.creatingWorktrees == nil {
				s.creatingWorktrees = make(map[id.ID]string)
			}
			s.creatingWorktrees[child.ID] = child.Worktree.Path
			if _, err := os.Stat(child.WorkingDirectory(session)); err != nil {
				return fmt.Errorf("parent worktree is unavailable; restore it before starting children: %w", err)
			}
		}
		return nil
	case "worktree":
	default:
		return fmt.Errorf("isolation must be worktree when specified")
	}
	root := strings.TrimSpace(s.configSnapshot().WorktreeRoot)
	if root == "" {
		return fmt.Errorf("worktree storage is not configured")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	cwd := parent.WorkingDirectory(session)
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
		}
		return strings.TrimSpace(string(output)), nil
	}
	base, err := git("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return fmt.Errorf("worktree isolation requires a git checkout with a commit: %w", err)
	}
	prefix, err := git("rev-parse", "--show-prefix")
	if err != nil {
		return err
	}
	gitDir, err := git("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create worktree storage: %w", err)
	}
	if s.creatingWorktrees == nil {
		s.creatingWorktrees = make(map[id.ID]string)
	}
	path := filepath.Join(root, string(child.ID))
	s.creatingWorktrees[child.ID] = path
	branch := "koder/" + string(child.ID)
	record := WorktreeRecord{
		Version:      1,
		ChatWorktree: domain.ChatWorktree{Path: path, WorkingDirectory: filepath.Join(path, prefix), SourceDirectory: cwd, Branch: branch, BaseCommit: base, GitDir: gitDir},
		ChatID:       string(child.ID),
		SessionID:    string(session.ID),
		ParentChatID: string(parent.ID),
		ParentPath:   cwd,
		State:        "creating",
	}
	if err := s.saveWorktree(record); err != nil {
		return err
	}
	if _, err := git("worktree", "add", "-b", branch, path, base); err != nil {
		record.State = "failed"
		record.Error = err.Error()
		if saveErr := s.saveWorktree(record); saveErr != nil {
			return fmt.Errorf("create worktree: %v; record failure: %w", err, saveErr)
		}
		return fmt.Errorf("create isolated checkout: %w", err)
	}
	if info, err := os.Stat(record.WorkingDirectory); err != nil || !info.IsDir() {
		record.State = "failed"
		record.Error = "the parent working directory does not exist in the selected commit"
		if saveErr := s.saveWorktree(record); saveErr != nil {
			return saveErr
		}
		return fmt.Errorf("%s; retained checkout: %s", record.Error, path)
	}
	record.State = "ready"
	if err := s.saveWorktree(record); err != nil {
		return fmt.Errorf("worktree created at %s but recording completion failed: %w", path, err)
	}
	child.Worktree = &record.ChatWorktree
	return nil
}

// WorktreeRecord survives failed creation and chat deletion. It is written
// before git modifies the repository, so interrupted work remains discoverable.
type WorktreeRecord struct {
	Version int `json:"version"`
	domain.ChatWorktree
	ChatID       string `json:"chat_id"`
	SessionID    string `json:"session_id"`
	ParentChatID string `json:"parent_chat_id"`
	ParentPath   string `json:"parent_path"`
	State        string `json:"state"`
	Error        string `json:"error,omitempty"`
}

func (s *Session) saveWorktree(record WorktreeRecord) error {
	root := s.configSnapshot().WorktreeRoot
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, record.ChatID+".json")
	return fsutil.WriteFileAtomic(path, data, 0o600)
}

func worktreeGit(ctx context.Context, record WorktreeRecord, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--git-dir", record.GitDir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// worktreeRecords includes incomplete creations and retained or removed trees.
func (s *Session) worktreeRecords() ([]WorktreeRecord, error) {
	root := s.configSnapshot().WorktreeRoot
	if root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []WorktreeRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		var record WorktreeRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("read worktree %s: %w", entry.Name(), err)
		}
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		if record.Version != 1 || record.ChatID == "" || record.ChatID != filepath.Base(record.ChatID) || record.ChatID == "." || record.ChatID == ".." ||
			entry.Name() != record.ChatID+".json" || filepath.Clean(record.Path) != filepath.Join(rootAbs, record.ChatID) ||
			!filepath.IsAbs(record.GitDir) || record.Branch != "koder/"+record.ChatID {
			return nil, fmt.Errorf("invalid ownership record: %s", entry.Name())
		}
		if record.SessionID == string(s.Snapshot().Session.ID) {
			records = append(records, record)
		}
	}
	return records, nil
}

func (s *Session) inspectWorktree(ctx context.Context, record WorktreeRecord) chattool.WorktreeStatus {
	status := chattool.WorktreeStatus{Worktree: record.ChatWorktree, ChatID: record.ChatID, State: record.State, Error: record.Error}
	if _, err := os.Stat(record.Path); err != nil {
		if os.IsNotExist(err) && (record.State == "removed" || record.State == "removing") {
			status.State = "removed"
		} else {
			status.State = "missing"
			if record.State == "failed" {
				status.State = "failed"
			}
			if record.State == "creating" {
				status.State = "interrupted"
			}
			status.Error = strings.TrimSpace(record.Error + "; " + fmt.Sprintf("checkout unavailable: %v; retained branch: %s", err, record.Branch))
		}
		return status
	}
	common, err := exec.CommandContext(ctx, "git", "-C", record.Path, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil || filepath.Clean(strings.TrimSpace(string(common))) != filepath.Clean(record.GitDir) {
		status.State = "failed"
		status.Error = "checkout does not match its recorded repository"
		return status
	}
	branch, err := exec.CommandContext(ctx, "git", "-C", record.Path, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(branch)) != record.Branch {
		status.State = "failed"
		status.Error = "checkout no longer uses managed branch " + record.Branch
		return status
	}
	cmd := exec.CommandContext(ctx, "git", "-C", record.Path, "status", "--porcelain", "--untracked-files=normal", "--ignored=matching")
	out, err := cmd.CombinedOutput()
	if err != nil {
		status.Error = string(out)
		status.State = "failed"
		return status
	}
	status.Dirty = len(strings.TrimSpace(string(out))) != 0
	head, err := exec.CommandContext(ctx, "git", "-C", record.Path, "rev-parse", "HEAD").Output()
	if err != nil {
		status.State = "failed"
		status.Error = err.Error()
		return status
	}
	parent, err := exec.CommandContext(ctx, "git", "-C", record.ParentPath, "rev-parse", "HEAD").Output()
	if err != nil {
		status.State = "failed"
		status.Error = "parent checkout unavailable: " + err.Error()
		return status
	}
	_, err = worktreeGit(ctx, record, "merge-base", "--is-ancestor", strings.TrimSpace(string(head)), strings.TrimSpace(string(parent)))
	status.Unmerged = err != nil
	status.Error = ""
	status.State = "ready"
	if status.Dirty || status.Unmerged {
		status.State = "retained"
	}
	return status
}

// ManageWorktrees never discards files or unique commits. Only archived,
// unused, clean, integrated checkouts may be removed; branches remain available.
func (c chatControl) ManageWorktrees(ctx context.Context, sessionID, ownerChatID id.ID, operation, chatID string) ([]chattool.WorktreeStatus, error) {
	s := c.session
	if err := s.requireSession(sessionID); err != nil {
		return nil, err
	}
	if operation != "list" && operation != "cleanup" {
		return nil, fmt.Errorf("worktree_action must be list or cleanup")
	}
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()
	records, err := s.worktreeRecords()
	if err != nil {
		return nil, err
	}
	statuses := make([]chattool.WorktreeStatus, 0, len(records))
	for _, record := range records {
		if chatID != "" && record.ChatID != chatID {
			continue
		}
		status := s.inspectWorktree(ctx, record)
		if s.creatingWorktrees[id.ID(record.ChatID)] != "" {
			status.State = "creating"
		}
		if operation == "cleanup" {
			if s.creatingWorktrees[id.ID(record.ChatID)] != "" {
				return nil, fmt.Errorf("worktree creation is still in progress")
			}
			if _, ok := chatByID(s.Snapshot().Chats, ownerChatID); !ok {
				return nil, fmt.Errorf("requesting chat does not belong to this session")
			}
			for _, path := range s.creatingWorktrees {
				if path == record.Path {
					return nil, fmt.Errorf("a chat is still being attached to this worktree")
				}
			}
			if _, statErr := os.Stat(record.Path); os.IsNotExist(statErr) && (record.State == "creating" || record.State == "failed") {
				registrations, err := worktreeGit(ctx, record, "worktree", "list", "--porcelain")
				if err != nil {
					return nil, err
				}
				if !strings.Contains(registrations, "worktree "+record.Path+"\n") {
					record.State = "removed"
					record.Error = ""
					if err := s.saveWorktree(record); err != nil {
						return nil, err
					}
					status.State = "removed"
					status.Error = ""
				}
			}
			if status.State != "removed" {
				if status.Error != "" {
					return nil, fmt.Errorf("cannot clean worktree: %s", status.Error)
				}
				if status.Dirty || status.Unmerged {
					return nil, fmt.Errorf("worktree has dirty/ignored files or commits not integrated into its parent; retain %s on branch %s", record.Path, record.Branch)
				}
				for _, dependent := range records {
					relative, err := filepath.Rel(record.Path, dependent.ParentPath)
					if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
						dependent.ChatID != record.ChatID && dependent.State != "removed" && dependent.State != "removing" {
						return nil, fmt.Errorf("worktree %s still depends on this checkout; integrate and clean up children first", dependent.ChatID)
					}
				}
				for _, chat := range s.Snapshot().Chats {
					if chat.Worktree == nil || chat.Worktree.Path != record.Path {
						continue
					}
					state, err := s.ChatStatus(ctx, chat.ID)
					if err != nil {
						return nil, err
					}
					if !chat.Archived || state.Busy || state.QueuedInputs > 0 || state.PendingApprovals > 0 {
						return nil, fmt.Errorf("archive all chats using this worktree before cleanup; chat %s still uses it", chat.ID)
					}
				}
				if s.configSnapshot().ExitReportsPending != nil {
					for _, chat := range s.Snapshot().Chats {
						if chat.Worktree != nil && chat.Worktree.Path == record.Path {
							pending := s.configSnapshot().ExitReportsPending(sessionID, chat.ID)
							if pending == nil {
								continue
							}
							select {
							case <-pending:
							default:
								return nil, fmt.Errorf("worktree still has running exec sessions")
							}
						}
					}
				}
				if inUse := s.configSnapshot().WorktreeInUse; inUse != nil {
					if err := inUse(record.Path); err != nil {
						return nil, err
					}
				}
				record.State = "removing"
				if err := s.saveWorktree(record); err != nil {
					return nil, err
				}
				if _, err := worktreeGit(ctx, record, "worktree", "remove", record.Path); err != nil {
					record.State = "retained"
					record.Error = err.Error()
					_ = s.saveWorktree(record)
					return nil, err
				}
				record.State = "removed"
				record.Error = ""
				if err := s.saveWorktree(record); err != nil {
					return nil, err
				}
				status.State = "removed"
			}
		}
		statuses = append(statuses, status)
	}
	if operation == "cleanup" && len(statuses) == 0 {
		return nil, fmt.Errorf("managed worktree %s not found", chatID)
	}
	return statuses, nil
}

// restoreWorktreeLocked requires worktreeMu and restores only a checkout explicitly removed by Koder. A
// missing checkout is never silently recreated: it may have contained edits.
func (s *Session) restoreWorktreeLocked(ctx context.Context, chat domain.Chat) error {
	if chat.Worktree == nil {
		return nil
	}
	if _, err := os.Stat(chat.Worktree.Path); err == nil {
		return nil
	}
	records, err := s.worktreeRecords()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Path != chat.Worktree.Path {
			continue
		}
		if record.State != "removed" && record.State != "removing" {
			return fmt.Errorf("worktree %s is missing; inspect retained branch %s before recovery", record.Path, record.Branch)
		}
		if _, err := worktreeGit(ctx, record, "worktree", "add", record.Path, record.Branch); err != nil {
			return err
		}
		record.State = "ready"
		return s.saveWorktree(record)
	}
	return fmt.Errorf("worktree ownership record is missing for %s", chat.Worktree.Path)
}

func (s *Session) finishWorktreeCreation(chatID id.ID) {
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()
	delete(s.creatingWorktrees, chatID)
}
