package planning

import "testing"

func TestParseTaskKeyRequiresScopedKey(t *testing.T) {
	key, err := ParseTaskKey("M129T003")
	if err != nil {
		t.Fatal(err)
	}
	if key != "M129T003" {
		t.Fatalf("task key = %q", key)
	}
	for _, raw := range []string{"M129", "T003", "M129T", "M129T003x"} {
		if _, err := ParseTaskKey(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

func TestNormalizeTaskKeysScopesLegacyTaskKeys(t *testing.T) {
	items, changed := NormalizeTaskKeys([]Task{
		{Key: "T001", MilestoneKey: "old"},
		{MilestoneKey: "M002"},
	}, map[string]string{"old": "M001", "M002": "M002"})
	if !changed {
		t.Fatal("expected normalization to report changed keys")
	}
	if got := TaskKey(items[0]); got != "M001T001" {
		t.Fatalf("legacy task key = %q", got)
	}
	if got := TaskKey(items[1]); got != "M002T001" {
		t.Fatalf("new task key = %q", got)
	}
}
