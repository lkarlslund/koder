package knowledge

import (
	"context"
	"regexp"
	"slices"
	"strings"
)

// ClassificationField is one named text field considered before knowledge is persisted.
type ClassificationField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ClassificationInput contains text and already-known risk metadata for one candidate write.
type ClassificationInput struct {
	Fields []ClassificationField `json:"fields"`
	Risk   []RiskClass           `json:"risk,omitempty"`
}

// ClassificationFinding locates sensitive material without copying its value into results or logs.
// Start and End are byte offsets within Field and are both zero for metadata-only findings.
type ClassificationFinding struct {
	Kind  FindingKind `json:"kind"`
	Field string      `json:"field,omitempty"`
	Start int         `json:"start,omitempty"`
	End   int         `json:"end,omitempty"`
	Rule  string      `json:"rule"`
}

// ClassificationResult describes whether a candidate can proceed, requires review, or must be rejected.
type ClassificationResult struct {
	Decision ClassificationDecision  `json:"decision"`
	Findings []ClassificationFinding `json:"findings,omitempty"`
}

// Classifier inspects candidate knowledge before any canonical record or derived index is written.
type Classifier interface {
	Classify(context.Context, ClassificationInput) (ClassificationResult, error)
}

type classificationRule struct {
	name     string
	kind     FindingKind
	decision ClassificationDecision
	pattern  *regexp.Regexp
}

var classificationRules = []classificationRule{
	{
		name:     "private_key_header",
		kind:     FindingKindPrivateKey,
		decision: ClassificationDecisionReject,
		pattern:  regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`),
	},
	{
		name:     "credential_assignment",
		kind:     FindingKindCredential,
		decision: ClassificationDecisionReject,
		pattern:  regexp.MustCompile(`(?i)\b(?:password|passwd|api[_-]?key|client[_-]?secret|secret)\s*[:=]\s*["']?[^\s"',;]{8,}`),
	},
	{
		name:     "bearer_token",
		kind:     FindingKindAuthToken,
		decision: ClassificationDecisionReject,
		pattern:  regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{16,}`),
	},
	{
		name:     "password_in_url",
		kind:     FindingKindCredential,
		decision: ClassificationDecisionReject,
		pattern:  regexp.MustCompile(`://[^/@\s:]+:[^/@\s]+@`),
	},
	{
		name:     "email_address",
		kind:     FindingKindContact,
		decision: ClassificationDecisionReview,
		pattern:  regexp.MustCompile("(?i)\\b[a-z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+\\b"),
	},
	{
		name:     "international_phone",
		kind:     FindingKindContact,
		decision: ClassificationDecisionReview,
		pattern:  regexp.MustCompile(`\+[1-9][0-9 ()-]{7,}[0-9]`),
	},
	{
		name:     "labelled_personal_id",
		kind:     FindingKindPersonalIdentifier,
		decision: ClassificationDecisionReview,
		pattern:  regexp.MustCompile(`(?i)\b(?:cpr|ssn|national[_ -]?id)\s*[:=]\s*[0-9][0-9 -]{5,}[0-9]`),
	},
	{
		name:     "labelled_coordinates",
		kind:     FindingKindPreciseLocation,
		decision: ClassificationDecisionReview,
		pattern:  regexp.MustCompile(`(?i)\b(?:gps|coordinates?|lat(?:itude)?)\s*[:=]\s*-?[0-9]{1,3}\.[0-9]{3,}\s*[,;/ ]\s*-?[0-9]{1,3}\.[0-9]{3,}`),
	},
}

// RuleClassifier provides a high-precision, local baseline. It deliberately leaves names
// and ambiguous prose to richer classifiers instead of guessing and creating noisy findings.
type RuleClassifier struct{}

// Classify implements Classifier. Returned findings never contain the matched secret text.
func (RuleClassifier) Classify(ctx context.Context, input ClassificationInput) (ClassificationResult, error) {
	result := ClassificationResult{Decision: ClassificationDecisionAllow}
	for _, field := range input.Fields {
		if err := ctx.Err(); err != nil {
			return ClassificationResult{}, err
		}
		if strings.TrimSpace(field.Name) == "" {
			return ClassificationResult{}, invalid("classification.fields.name", "is required")
		}
		for _, rule := range classificationRules {
			for _, match := range rule.pattern.FindAllStringIndex(field.Value, -1) {
				result.Findings = append(result.Findings, ClassificationFinding{
					Kind: rule.kind, Field: field.Name, Start: match[0], End: match[1], Rule: rule.name,
				})
				result.Decision = strongerDecision(result.Decision, rule.decision)
			}
		}
	}

	for _, risk := range input.Risk {
		if risk == RiskClassUnspecified || !risk.IsARiskClass() {
			return ClassificationResult{}, invalid("classification.risk", "contains an unknown risk class")
		}
		finding, ok := findingForRisk(risk)
		if !ok {
			continue
		}
		result.Findings = append(result.Findings, finding)
		decision := ClassificationDecisionReview
		if risk == RiskClassProhibitedSecret {
			decision = ClassificationDecisionReject
		}
		result.Decision = strongerDecision(result.Decision, decision)
	}

	slices.SortStableFunc(result.Findings, func(a, b ClassificationFinding) int {
		if order := strings.Compare(a.Field, b.Field); order != 0 {
			return order
		}
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		if a.End != b.End {
			return a.End - b.End
		}
		return strings.Compare(a.Rule, b.Rule)
	})
	return result, nil
}

func strongerDecision(current, candidate ClassificationDecision) ClassificationDecision {
	if candidate > current {
		return candidate
	}
	return current
}

func findingForRisk(risk RiskClass) (ClassificationFinding, bool) {
	switch risk {
	case RiskClassPersonalSensitive:
		return ClassificationFinding{Kind: FindingKindPersonalIdentifier, Rule: "risk_personal_sensitive"}, true
	case RiskClassMedical:
		return ClassificationFinding{Kind: FindingKindMedical, Rule: "risk_medical"}, true
	case RiskClassLegal:
		return ClassificationFinding{Kind: FindingKindLegal, Rule: "risk_legal"}, true
	case RiskClassFinancial:
		return ClassificationFinding{Kind: FindingKindFinancial, Rule: "risk_financial"}, true
	case RiskClassPhysicalSafety:
		return ClassificationFinding{Kind: FindingKindPhysicalSafety, Rule: "risk_physical_safety"}, true
	case RiskClassSecuritySensitive:
		return ClassificationFinding{Kind: FindingKindSecuritySensitive, Rule: "risk_security_sensitive"}, true
	case RiskClassProhibitedSecret:
		return ClassificationFinding{Kind: FindingKindCredential, Rule: "risk_prohibited_secret"}, true
	default:
		return ClassificationFinding{}, false
	}
}
