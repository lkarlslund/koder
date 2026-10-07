// Package memorytool lets the model read and keep notes across chats. The
// memory index is already in the chat's instructions; this tool reads a
// memory in full and creates, updates or deletes memories.
package memorytool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/tools"
)

const parameters = `{"type":"object","properties":{` +
	`"action":{"type":"string","enum":["list","get","create","update","delete"],"description":"list shows the current index; get reads a memory in full; create, update and delete change memories."},` +
	`"scope":{"type":"string","enum":["global","project"],"description":"global: the user, their machine and preferences that apply in every project. project: this project only. Required for create; for get, update and delete it may be omitted when the name is unique."},` +
	`"name":{"type":"string","description":"Short kebab-case name, e.g. prefers-terse-answers"},` +
	`"description":{"type":"string","description":"One specific line saying what the memory is; it is shown in every chat's memory index. Required for create."},` +
	`"content":{"type":"string","description":"The memory itself. For a rule or preference, say why and when it applies. Required for create."}` +
	`},"required":["action"],"additionalProperties":false}`

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Memory",
		Description: "Read and keep notes that carry over to later chats.",
		Usage: "The memory index is in your instructions. Use get to read a memory before relying on its details. " +
			"Create a memory when you learn something that will matter in later chats: the user's preferences, corrections and approaches they confirmed, facts about their machine or setup, or project decisions and context that the code does not show. " +
			"Use scope=global for the user and their machine, scope=project for this project. " +
			"Update a memory rather than creating a near-duplicate, and delete one that turns out wrong or stale. " +
			"Do not save what the code, git history or AGENTS.md already records, task progress, guesses, or credentials.",
		Parameters:  parameters,
		ExposeToLLM: true,
	})
}

type tool struct{}

func (tool) ID() tools.ID             { return tools.Memory }
func (tool) BypassesPermission() bool { return true }

func (tool) Preview(req tools.Request) string {
	return strings.TrimSpace(strings.Join([]string{req.Args["action"], req.Args["scope"], req.Args["name"]}, " "))
}

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(args))
	for _, key := range []string{"action", "scope", "name", "description"} {
		if value := strings.TrimSpace(args[key]); value != "" {
			out[key] = value
		}
	}
	if content := strings.TrimSpace(args["content"]); content != "" {
		out["content"] = content
	}
	switch out["action"] {
	case "list":
	case "get", "delete":
		if out["name"] == "" {
			return nil, fmt.Errorf("name is required for %s", out["action"])
		}
	case "create":
		for _, key := range []string{"scope", "name", "description", "content"} {
			if out[key] == "" {
				return nil, fmt.Errorf("%s is required for create", key)
			}
		}
	case "update":
		if out["name"] == "" || (out["description"] == "" && out["content"] == "") {
			return nil, errors.New("update needs name and a new description, content or both")
		}
	default:
		return nil, errors.New("action must be list, get, create, update or delete")
	}
	if scope := out["scope"]; scope != "" && scope != string(memory.ScopeGlobal) && scope != string(memory.ScopeProject) {
		return nil, errors.New("scope must be global or project")
	}
	return out, nil
}

func (tool) Call(_ context.Context, opts tools.Options) (tools.Result, error) {
	store := opts.Runtime.Memory
	if store == nil {
		return tools.Result{}, errors.New("memory is unavailable")
	}
	args, root := opts.Request.Args, opts.Runtime.Workdir
	scope := memory.Scope(args["scope"])
	switch args["action"] {
	case "list":
		return result(store.Instructions(root)), nil
	case "create":
		created, err := store.Create(memory.Memory{Scope: scope, Name: args["name"], Description: args["description"], Content: args["content"]}, root)
		if err != nil {
			return tools.Result{}, err
		}
		return result(fmt.Sprintf("Created %s memory %s: %s", created.Scope, created.Name, created.Description)), nil
	}
	existing, err := find(store, scope, root, args["name"])
	if err != nil {
		return tools.Result{}, err
	}
	switch args["action"] {
	case "get":
		return result(fmt.Sprintf("%s memory %s: %s\nUpdated %s\n\n%s", existing.Scope, existing.Name, existing.Description, existing.UpdatedAt.Format("2006-01-02"), existing.Content)), nil
	case "update":
		if description := args["description"]; description != "" {
			existing.Description = description
		}
		if content := args["content"]; content != "" {
			existing.Content = content
		}
		updated, err := store.Update(existing, root)
		if err != nil {
			return tools.Result{}, err
		}
		return result(fmt.Sprintf("Updated %s memory %s: %s", updated.Scope, updated.Name, updated.Description)), nil
	default:
		if err := store.Delete(existing.Scope, root, existing.Name); err != nil {
			return tools.Result{}, err
		}
		return result(fmt.Sprintf("Deleted %s memory %s", existing.Scope, existing.Name)), nil
	}
}

// find reads a memory by name, looking in both scopes when none is given;
// a name used in both needs a scope.
func find(store *memory.Store, scope memory.Scope, root, name string) (memory.Memory, error) {
	if scope != "" {
		return store.Get(scope, root, name)
	}
	var found []memory.Memory
	for _, candidate := range []memory.Scope{memory.ScopeProject, memory.ScopeGlobal} {
		if candidate == memory.ScopeProject && strings.TrimSpace(root) == "" {
			continue
		}
		got, err := store.Get(candidate, root, name)
		if errors.Is(err, memory.ErrNotFound) {
			continue
		}
		if err != nil {
			return memory.Memory{}, err
		}
		found = append(found, got)
	}
	switch len(found) {
	case 0:
		return memory.Memory{}, fmt.Errorf("%w: %q", memory.ErrNotFound, name)
	case 1:
		return found[0], nil
	default:
		return memory.Memory{}, fmt.Errorf("both global and project memory have %q; give a scope", name)
	}
}

func result(text string) tools.Result {
	return tools.Result{Output: text}
}
