package service

import (
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
)

// validateHighRiskChunkPolicy keeps incomplete research drafts possible while
// preventing advice-bearing chunks from becoming active without enough context
// to judge their applicability, provenance requirements, and freshness.
func validateHighRiskChunkPolicy(chunk memory.Chunk) error {
	if chunk.State != memory.ChunkStateActive || !requiresHighRiskAdvicePolicy(chunk.Risk) {
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
		return fmt.Errorf("%w: active high-risk chunk requires %s", memory.ErrInvalidRecord, strings.Join(missing, ", "))
	}
	return nil
}

func requiresHighRiskAdvicePolicy(risks []memory.RiskClass) bool {
	for _, risk := range risks {
		switch risk {
		case memory.RiskClassMedical, memory.RiskClassLegal, memory.RiskClassFinancial,
			memory.RiskClassPhysicalSafety:
			return true
		}
	}
	return false
}
