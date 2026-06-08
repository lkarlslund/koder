package agent

import (
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/environment"
	"github.com/lkarlslund/koder/internal/id"
)

func (e *Engine) environmentPrompt(session domain.Session) string {
	return environment.Prompt(sessionProjectRoot(session))
}

func sessionProjectRoot(session domain.Session) string {
	return strings.TrimSpace(session.ProjectRoot)
}

func (e *Engine) sessionEnvironmentPrompt(session domain.Session) string {
	e.envMu.Lock()
	defer e.envMu.Unlock()
	if e.envPrompts == nil {
		e.envPrompts = map[id.ID]string{}
	}
	if text := e.envPrompts[session.ID]; text != "" {
		return text
	}
	text := e.environmentPrompt(session)
	e.envPrompts[session.ID] = text
	return text
}
