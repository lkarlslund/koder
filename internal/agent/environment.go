package agent

import (
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
)

func sessionProjectRoot(session domain.Session) string {
	return strings.TrimSpace(session.ProjectRoot)
}
