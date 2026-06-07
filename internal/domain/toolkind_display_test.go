package domain

import "testing"

func TestToolKindDisplayNameRejectsInvalidValue(t *testing.T) {
	if got := ToolKind("").DisplayName(); got != "" {
		t.Fatalf("expected invalid tool kind display name to be empty, got %q", got)
	}
}
