package memory

import (
	"fmt"
	"strings"
)

// Instructions renders the memory index for a chat's instructions: each
// memory's name and description, per scope, with how full the scope is.
// The full text is read with the memory tool.
func (s *Store) Instructions(projectRoot string) string {
	var b strings.Builder
	b.WriteString("Memory: notes kept across chats with the memory tool. Read a memory in full with action=get before relying on its details; it may be out of date, so check facts that may have changed.\n")
	s.writeScope(&b, ScopeGlobal, projectRoot, "Global memory (the user, their machine, preferences that apply everywhere)")
	if strings.TrimSpace(projectRoot) != "" {
		s.writeScope(&b, ScopeProject, projectRoot, "Project memory ("+projectRoot+")")
	}
	return strings.TrimSpace(b.String())
}

func (s *Store) writeScope(b *strings.Builder, scope Scope, projectRoot, title string) {
	memories, err := s.List(scope, projectRoot)
	if err != nil {
		fmt.Fprintf(b, "\n%s: unavailable: %v\n", title, err)
		return
	}
	fmt.Fprintf(b, "\n%s, %d of %d:\n", title, len(memories), MaxMemories)
	if len(memories) == 0 {
		b.WriteString("(none yet)\n")
	}
	for _, memory := range memories {
		fmt.Fprintf(b, "- %s: %s\n", memory.Name, memory.Description)
	}
}
