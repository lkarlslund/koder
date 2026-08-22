package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type classificationFixture struct {
	Name         string                 `json:"name"`
	Fields       []ClassificationField  `json:"fields"`
	Risk         []RiskClass            `json:"risk,omitempty"`
	Decision     ClassificationDecision `json:"decision"`
	FindingKinds []FindingKind          `json:"finding_kinds"`
}

func TestRuleClassifierFixtures(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "protocol", "knowledge", "v1", "testdata", "classification_cases.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read classification fixtures: %v", err)
	}
	var fixtures []classificationFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("unmarshal classification fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("classification fixture set is empty")
	}
	classifier := RuleClassifier{}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			result, err := classifier.Classify(context.Background(), ClassificationInput{
				Fields: fixture.Fields,
				Risk:   fixture.Risk,
			})
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			if result.Decision != fixture.Decision {
				t.Errorf("Classify() decision = %s, want %s", result.Decision, fixture.Decision)
			}
			kinds := make([]FindingKind, len(result.Findings))
			for i, finding := range result.Findings {
				kinds[i] = finding.Kind
				if finding.Rule == "" {
					t.Errorf("finding %d has no rule identity", i)
				}
			}
			if !reflect.DeepEqual(kinds, fixture.FindingKinds) {
				t.Errorf("Classify() kinds = %v, want %v", kinds, fixture.FindingKinds)
			}
		})
	}
}

func TestRuleClassifierDoesNotReturnMatchedSecret(t *testing.T) {
	t.Parallel()
	const secret = "synthetic-secret-for-tests"
	result, err := (RuleClassifier{}).Classify(context.Background(), ClassificationInput{
		Fields: []ClassificationField{{Name: "body", Value: "password=" + secret}},
	})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatalf("classification result leaked matched secret: %s", data)
	}
}

func TestRuleClassifierRejectsUnnamedFields(t *testing.T) {
	t.Parallel()
	_, err := (RuleClassifier{}).Classify(context.Background(), ClassificationInput{
		Fields: []ClassificationField{{Value: "ordinary"}},
	})
	if !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("Classify() error = %v, want ErrInvalidRecord", err)
	}
}

func TestRuleClassifierHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (RuleClassifier{}).Classify(ctx, ClassificationInput{
		Fields: []ClassificationField{{Name: "body", Value: "ordinary"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Classify() error = %v, want context.Canceled", err)
	}
}

func TestRuleClassifierClassifiesEveryRisk(t *testing.T) {
	t.Parallel()
	tests := []struct {
		risk     RiskClass
		kind     FindingKind
		decision ClassificationDecision
	}{
		{risk: RiskClassPersonalSensitive, kind: FindingKindPersonalIdentifier, decision: ClassificationDecisionReview},
		{risk: RiskClassMedical, kind: FindingKindMedical, decision: ClassificationDecisionReview},
		{risk: RiskClassLegal, kind: FindingKindLegal, decision: ClassificationDecisionReview},
		{risk: RiskClassFinancial, kind: FindingKindFinancial, decision: ClassificationDecisionReview},
		{risk: RiskClassPhysicalSafety, kind: FindingKindPhysicalSafety, decision: ClassificationDecisionReview},
		{risk: RiskClassSecuritySensitive, kind: FindingKindSecuritySensitive, decision: ClassificationDecisionReview},
		{risk: RiskClassProhibitedSecret, kind: FindingKindCredential, decision: ClassificationDecisionReject},
	}
	for _, tt := range tests {
		t.Run(tt.risk.String(), func(t *testing.T) {
			t.Parallel()
			result, err := (RuleClassifier{}).Classify(context.Background(), ClassificationInput{
				Fields: []ClassificationField{{Name: "body", Value: "classified by metadata"}},
				Risk:   []RiskClass{tt.risk},
			})
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			if result.Decision != tt.decision || len(result.Findings) != 1 || result.Findings[0].Kind != tt.kind {
				t.Fatalf("Classify() = %#v, want decision %s and kind %s", result, tt.decision, tt.kind)
			}
		})
	}
}

func TestRuleClassifierRejectsUnknownRisk(t *testing.T) {
	t.Parallel()
	for _, risk := range []RiskClass{RiskClassUnspecified, RiskClass(255)} {
		_, err := (RuleClassifier{}).Classify(context.Background(), ClassificationInput{
			Fields: []ClassificationField{{Name: "body", Value: "ordinary"}},
			Risk:   []RiskClass{risk},
		})
		if !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("Classify(risk=%d) error = %v, want ErrInvalidRecord", risk, err)
		}
	}
}
