package knowledgetool

import (
	"slices"
	"strings"

	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
)

func toolGuidance(offer knowledgeService.ToolOffer) string {
	parts := []string{}
	if hasAnyAction(offer, "recall") {
		parts = append(parts,
			"Recall before repeating research when durable memory may contain a relevant preference, decision, workaround, project convention, or environment fact. Treat stale, disputed, scope-mismatched, and unverified results as leads rather than current truth.")
	}
	if hasAnyAction(offer, "remember") {
		parts = append(parts,
			"Remember only an established reusable result or an explicit user request—not routine progress, guesses, scratch work, or a transcript copy. Never persist passwords, tokens, keys, cookies, recovery codes, or equivalent credentials. Mark only direct facts or preferences about the user as personal, and do not silently replace a conflicting memory.")
	}
	return strings.Join(parts, " ")
}

func hasAnyAction(offer knowledgeService.ToolOffer, actions ...string) bool {
	for _, action := range actions {
		if slices.Contains(offer.Actions, action) {
			return true
		}
	}
	return false
}
