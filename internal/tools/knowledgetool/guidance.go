package knowledgetool

import (
	"slices"
	"strings"

	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
)

func toolGuidance(offer knowledgeService.ToolOffer) string {
	parts := []string{
		"Secrets: never persist passwords, tokens, private keys, session cookies, authorization headers, recovery codes, or equivalent credentials in any Knowledge field.",
	}
	if hasAnyAction(offer, "search", "get", "neighbors", "history") {
		parts = append(parts,
			"Retrieval: search existing Knowledge before web research when a durable fact, prior workaround, personal preference, project convention, or environment-specific lesson may apply. Treat stale, disputed, scope-mismatched, and unverified results as leads rather than current truth; fetch full content only when needed.")
	}
	if hasAnyAction(offer, "chunk_create", "chunk_update", "entry_create", "entry_update") {
		parts = append(parts,
			"Durable learning: persist only a reusable result, correction, preference, decision, warning, or procedure—not routine narration, scratch work, an unfinished guess, or a transcript copy. Record the narrowest correct scope and selector, applicability, confidence, concise reason, and inspectable evidence. Write after the result is established, and keep uncertain candidates as drafts.")
	}
	if hasAnyAction(offer, "entry_update", "entry_supersede", "link") {
		parts = append(parts,
			"Contradictions and corrections: inspect the current entry, neighbors, and history first. Update wording when the same claim remains valid; supersede a materially replaced claim; preserve simultaneous conflicting claims with a contradicts link and explicit scope/evidence instead of silently overwriting or duplicating them.")
	}
	if hasAnyAction(offer, "verify") {
		parts = append(parts,
			"Verification: use verified or partially_verified only with existing inspectable evidence; use disputed when evidence conflicts, and return to unverified when support no longer holds. Popularity or repeated use is not verification.")
	}
	if hasAnyAction(offer, "chunk_create", "chunk_update", "entry_create", "entry_update", "verify") {
		parts = append(parts,
			"High-risk knowledge: for medical, legal, financial, physical-safety, or security-sensitive material, require fresh authoritative sources, locale/domain and applicability, review/validity dates, explicit uncertainty, and a warning not to substitute stored guidance for current professional or primary-source advice.",
			"Personal knowledge: distinguish explicit user statements from observed facts and inferred conclusions with personal_origin. Do not convert casual conversation into a profile; keep sensitive or uncertain inferences draft/review-only and prefer a direct user statement as evidence.")
	}
	if hasAnyAction(offer, "package_preview", "package_stage", "package_activate", "package_export") {
		parts = append(parts,
			"Packages: use package_preview before package_stage, inspect conflicts, dependencies, classification, and signature state, then choose replace, merge, or keep_both explicitly when conflicts exist. Activate only the intended actor-owned stage after review; package_export writes a portable archive to a new workspace path and never overwrites a file.")
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
