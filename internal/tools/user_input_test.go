package tools

import "testing"

func TestParseUserInputQuestions(t *testing.T) {
	raw := `[{"id":"choice","header":"Choose","question":"Which one?","options":[{"label":"A","description":"First"},{"label":"B","description":"Second"}]}]`
	questions, err := ParseUserInputQuestions(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 1 || questions[0].ID != "choice" || len(questions[0].Options) != 2 {
		t.Fatalf("unexpected questions: %#v", questions)
	}
}

func TestParseUserInputQuestionsRejectsInvalidBatches(t *testing.T) {
	tests := []string{
		`[]`,
		`[{"id":"same","header":"One","question":"One?","options":[{"label":"A","description":"First"},{"label":"B","description":"Second"}]},{"id":"same","header":"Two","question":"Two?","options":[{"label":"A","description":"First"},{"label":"B","description":"Second"}]}]`,
		`[{"id":"choice","header":"Choose","question":"Which?","options":[{"label":"A","description":"First"}]}]`,
		`[{"id":"choice","header":"Choose","question":"Which?","options":[{"label":"A","description":"First"},{"label":"A","description":"Again"}]}]`,
	}
	for _, raw := range tests {
		if _, err := ParseUserInputQuestions(raw); err == nil {
			t.Fatalf("expected validation error for %s", raw)
		}
	}
}
