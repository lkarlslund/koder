package service

import (
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
)

// validateHighRiskChunkPolicy keeps incomplete research drafts possible while
// preventing advice-bearing chunks from becoming active without enough context
// to judge their applicability, provenance requirements, and freshness.
func validateHighRiskChunkPolicy(chunk knowledge.Chunk) error {
	if chunk.State != knowledge.ChunkStateActive || !requiresHighRiskAdvicePolicy(chunk.Risk) {
		return nil
	}
	missing := make([]string, 0, 4)
	if strings.TrimSpace(chunk.Locale) == "" {
		missing = append(missing, "locale")
	}
	if strings.TrimSpace(chunk.Domain) == "" {
		missing = append(missing, "domain")
	}
	if strings.TrimSpace(chunk.SourcePolicy) == "" {
		missing = append(missing, "source_policy")
	}
	if chunk.ReviewAfter.IsZero() {
		missing = append(missing, "review_after")
	}
	if len(missing) != 0 {
		return fmt.Errorf("%w: active high-risk chunk requires %s", knowledge.ErrInvalidRecord, strings.Join(missing, ", "))
	}
	return nil
}

func requiresHighRiskAdvicePolicy(risks []knowledge.RiskClass) bool {
	for _, risk := range risks {
		switch risk {
		case knowledge.RiskClassMedical, knowledge.RiskClassLegal, knowledge.RiskClassFinancial,
			knowledge.RiskClassPhysicalSafety:
			return true
		}
	}
	return false
}
