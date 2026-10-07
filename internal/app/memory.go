package app

import (
	"context"
	"errors"
	"strings"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/memory"
)

// MemoryState is the memory a session can see: global memory, and the
// project memory of the session's project when it has one.
type MemoryState struct {
	ProjectRoot string          `json:"project_root,omitempty"`
	Global      []memory.Memory `json:"global"`
	Project     []memory.Memory `json:"project"`
	GlobalDir   string          `json:"global_dir"`
	ProjectDir  string          `json:"project_dir,omitempty"`
}

// MemoryEdit creates or updates one memory from the settings page.
type MemoryEdit struct {
	SessionID   id.ID        `json:"session_id"`
	Scope       memory.Scope `json:"scope"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Content     string       `json:"content"`
	Create      bool         `json:"create"`
}

// Memories lists the memory a session can see.
func (c *Controller) Memories(ctx context.Context, sessionID id.ID) (MemoryState, error) {
	store, root, err := c.memoryFor(ctx, sessionID)
	if err != nil {
		return MemoryState{}, err
	}
	state := MemoryState{ProjectRoot: root}
	if state.Global, err = store.List(memory.ScopeGlobal, root); err != nil {
		return MemoryState{}, err
	}
	if state.Project, err = store.List(memory.ScopeProject, root); err != nil {
		return MemoryState{}, err
	}
	state.GlobalDir, _ = store.Dir(memory.ScopeGlobal, root)
	if root != "" {
		state.ProjectDir, _ = store.Dir(memory.ScopeProject, root)
	}
	return state, nil
}

// SaveMemory creates or updates a memory and returns the updated list.
func (c *Controller) SaveMemory(ctx context.Context, edit MemoryEdit) (MemoryState, error) {
	store, root, err := c.memoryFor(ctx, edit.SessionID)
	if err != nil {
		return MemoryState{}, err
	}
	entry := memory.Memory{Scope: edit.Scope, Name: strings.TrimSpace(edit.Name), Description: strings.TrimSpace(edit.Description), Content: edit.Content}
	if edit.Create {
		_, err = store.Create(entry, root)
	} else {
		_, err = store.Update(entry, root)
	}
	if err != nil {
		return MemoryState{}, err
	}
	return c.Memories(ctx, edit.SessionID)
}

// DeleteMemory removes a memory and returns the updated list.
func (c *Controller) DeleteMemory(ctx context.Context, sessionID id.ID, scope memory.Scope, name string) (MemoryState, error) {
	store, root, err := c.memoryFor(ctx, sessionID)
	if err != nil {
		return MemoryState{}, err
	}
	if err := store.Delete(scope, root, name); err != nil {
		return MemoryState{}, err
	}
	return c.Memories(ctx, sessionID)
}

// memoryFor returns the memory store and the project root of a session;
// without a session only global memory is reachable.
func (c *Controller) memoryFor(ctx context.Context, sessionID id.ID) (*memory.Store, string, error) {
	if c == nil || c.agent == nil || c.agent.Memory() == nil {
		return nil, "", errors.New("memory is unavailable")
	}
	if sessionID == "" {
		return c.agent.Memory(), "", nil
	}
	owner, err := c.agent.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, "", err
	}
	return c.agent.Memory(), strings.TrimSpace(owner.Snapshot().Session.ProjectRoot), nil
}
