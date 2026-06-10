package planning

import "testing"

func TestParseTodoKeyRequiresScopedKey(t *testing.T) {
	key, err := ParseTodoKey("M129T003")
	if err != nil {
		t.Fatal(err)
	}
	if key != "M129T003" {
		t.Fatalf("task key = %q", key)
	}
	for _, raw := range []string{"M129", "T003", "M129T", "M129T003x"} {
		if _, err := ParseTodoKey(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

func TestNormalizeTodosKeysScopesLegacyTaskKeys(t *testing.T) {
	items, changed := NormalizeTodosKeys([]TodoItem{
		{Key: "T001", MilestoneRef: "old"},
		{MilestoneRef: "M002"},
	}, map[string]string{"old": "M001", "M002": "M002"})
	if !changed {
		t.Fatal("expected normalization to report changed keys")
	}
	if got := TodoKey(items[0]); got != "M001T001" {
		t.Fatalf("legacy task key = %q", got)
	}
	if got := TodoKey(items[1]); got != "M002T001" {
		t.Fatalf("new task key = %q", got)
	}
}
